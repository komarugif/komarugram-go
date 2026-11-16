// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"sync"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"

	"github.com/gotd/td/tg"
)

var _ model.SavedMusicStore = (*Store)(nil)

// savedMusic is the documents of the songs on the account's profile, as
// account.getSavedMusicIds told them, asked for once a window first wants
// to know, and changed as songs are saved.
type savedMusic struct {
	mu    sync.Mutex
	ids   map[int64]bool
	asked bool
}

// SavedMusic implements model.SavedMusicStore. It is asked in frames: the
// first time asks Telegram for the list in the background.
func (s *Store) SavedMusic(m model.Message) bool {
	if m.Media == nil {
		return false
	}
	s.history.mu.Lock()
	ref, ok := s.history.refs[m.Media.ID]
	api := s.history.api
	s.history.mu.Unlock()
	music := &s.music
	music.mu.Lock()
	defer music.mu.Unlock()
	if !music.asked && api != nil {
		music.asked = true
		go s.loadSavedMusic(api)
	}
	return ok && music.ids[ref.ID]
}

// loadSavedMusic asks for the list of the songs on the profile.
func (s *Store) loadSavedMusic(api *tg.Client) {
	defer crash.Recover("saved music", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := api.AccountGetSavedMusicIDs(ctx, 0)
	music := &s.music
	music.mu.Lock()
	defer music.mu.Unlock()
	list, ok := res.(*tg.AccountSavedMusicIDs)
	if err != nil || !ok {
		// Asked again the next time it is wanted.
		music.asked = false
		return
	}
	ids := make(map[int64]bool, len(list.IDs))
	for _, id := range list.IDs {
		ids[id] = true
	}
	// What was saved while the list came stays.
	for id := range music.ids {
		ids[id] = true
	}
	music.ids = ids
}

// SaveMusic implements model.SavedMusicStore with account.saveMusic, as
// Telegram Desktop's Data::SavedMusic::save: the song goes first on the
// profile's playlist.
func (s *Store) SaveMusic(ctx context.Context, m model.Message) error {
	if m.Media == nil || m.Kind != model.MessageMusic {
		return errors.New("no song to save")
	}
	ref, err := s.pickerDocument(ctx, m, func(api *tg.Client, doc *tg.InputDocument) error {
		_, err := api.AccountSaveMusic(ctx, &tg.AccountSaveMusicRequest{ID: doc})
		return err
	})
	if err != nil {
		return err
	}
	music := &s.music
	music.mu.Lock()
	if music.ids == nil {
		music.ids = map[int64]bool{}
	}
	music.ids[ref.ID] = true
	music.mu.Unlock()
	return nil
}

// RemoveMusic implements model.SavedMusicStore with account.saveMusic and
// unsave, as Telegram Desktop's Data::SavedMusic::remove.
func (s *Store) RemoveMusic(ctx context.Context, m model.Message) error {
	if m.Media == nil {
		return errors.New("no song to remove")
	}
	ref, err := s.pickerDocument(ctx, m, func(api *tg.Client, doc *tg.InputDocument) error {
		_, err := api.AccountSaveMusic(ctx, &tg.AccountSaveMusicRequest{ID: doc, Unsave: true})
		return err
	})
	if err != nil {
		return err
	}
	music := &s.music
	music.mu.Lock()
	delete(music.ids, ref.ID)
	music.mu.Unlock()
	return nil
}

// MoveMusic implements model.SavedMusicStore with account.saveMusic and
// after_id, as Telegram Desktop's Data::SavedMusic::reorder: a song saved
// again moves, and after_id says where to.
func (s *Store) MoveMusic(ctx context.Context, m model.Message, after *model.Message) error {
	if m.Media == nil {
		return errors.New("no song to move")
	}
	var afterDoc *tg.InputDocument
	if after != nil && after.Media != nil {
		s.history.mu.Lock()
		ref, ok := s.history.refs[after.Media.ID]
		s.history.mu.Unlock()
		if !ok || ref.ID == 0 {
			return errors.New("media reference unavailable")
		}
		afterDoc = &tg.InputDocument{ID: ref.ID, AccessHash: ref.Hash, FileReference: ref.Reference}
	}
	_, err := s.pickerDocument(ctx, m, func(api *tg.Client, doc *tg.InputDocument) error {
		req := &tg.AccountSaveMusicRequest{ID: doc}
		if afterDoc != nil {
			req.SetAfterID(afterDoc)
		}
		_, err := api.AccountSaveMusic(ctx, req)
		return err
	})
	return err
}
