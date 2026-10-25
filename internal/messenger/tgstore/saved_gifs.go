// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"

	"komarugram/internal/messenger/model"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

var (
	_ model.SavedGIFStore      = (*Store)(nil)
	_ model.RecentStickerStore = (*Store)(nil)
)

// SavedGIF implements model.SavedGIFStore, by the GIF tab's list kept in
// memory: it is asked in frames, and the tab has the list once opened.
func (s *Store) SavedGIF(id string) bool {
	p := &s.picker
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, item := range p.pages[model.PickerGIF].Items {
		if item.ID == id {
			return true
		}
	}
	return false
}

// SaveGIF implements model.SavedGIFStore with messages.saveGif, as Telegram
// Desktop's Api::ToggleSavedGif: the list kept changes at once, the saved
// GIF first, and is asked for again the next time the tab opens.
func (s *Store) SaveGIF(ctx context.Context, m model.Message, unsave bool) error {
	if m.Media == nil {
		return errors.New("no GIF to save")
	}
	ref, err := s.pickerDocument(ctx, m, func(api *tg.Client, doc *tg.InputDocument) error {
		_, err := api.MessagesSaveGif(ctx, &tg.MessagesSaveGifRequest{ID: doc, Unsave: unsave})
		return err
	})
	if err != nil {
		return err
	}
	id := m.Media.ID
	if !unsave {
		// What renews the reference of a saved GIF shown in the picker,
		// which has no message of it.
		s.markSavedGIF(ctx, m.Media, ref)
	}
	// Loaded from the cache when this window has not opened the tab.
	s.keptCatalogue(ctx, model.PickerGIF)
	p := &s.picker
	p.mu.Lock()
	p.init()
	page := p.pages[model.PickerGIF]
	items := model.WithoutItem(page.Items, id)
	var full error
	if !unsave {
		item := model.PickerItem{ID: id, DocumentID: ref.ID, Media: model.Message{Kind: m.Kind, Media: m.Media}}
		items = append([]model.PickerItem{item}, items...)
		if limit := model.SavedGIFLimit(s.Premium()); limit > 0 && len(items) > limit {
			items, full = items[:limit], model.ErrSavedGIFLimit
		}
	}
	page.Items, page.Stale = items, false
	p.pages[model.PickerGIF] = page
	saved := pickerSaved{Page: page, Hashes: p.hashes[model.PickerGIF]}
	// Telegram's list, with its hash, replaces this one when next asked.
	delete(p.at, model.PickerGIF)
	p.mu.Unlock()
	if cache := s.Cache(); cache != nil {
		_ = cache.Put(ctx, pickerKey(model.PickerGIF), saved)
	}
	return full
}

// RemoveRecentSticker implements model.RecentStickerStore with
// messages.saveRecentSticker, as Telegram Desktop's
// Api::ToggleRecentSticker: off Telegram's recent stickers, and off those
// the client keeps chosen here.
func (s *Store) RemoveRecentSticker(ctx context.Context, item model.PickerItem) error {
	if item.Media.Media == nil {
		return errors.New("no sticker to remove")
	}
	if _, err := s.pickerDocument(ctx, item.Media, func(api *tg.Client, doc *tg.InputDocument) error {
		_, err := api.MessagesSaveRecentSticker(ctx, &tg.MessagesSaveRecentStickerRequest{ID: doc, Unsave: true})
		return err
	}); err != nil {
		return err
	}
	tab := model.PickerStickers
	s.keptCatalogue(ctx, tab)
	p := &s.picker
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	page := p.pages[tab]
	page.Recent = model.WithoutItem(page.Recent, item.ID)
	p.pages[tab] = page
	saved := pickerSaved{Page: page, Hashes: p.hashes[tab]}
	delete(p.at, tab)
	cache := s.Cache()
	if cache == nil {
		return nil
	}
	_ = cache.Put(ctx, pickerKey(tab), saved)
	// Under p.mu, as RememberPicker changes them.
	key := fmt.Sprintf("picker/recent/%d", tab)
	var local []model.PickerItem
	if _, err := cache.Get(ctx, key, &local); err != nil {
		return err
	}
	if kept := model.WithoutItem(local, item.ID); len(kept) != len(local) {
		return cache.Put(ctx, key, kept)
	}
	return nil
}

// pickerDocument calls call with the document of the media m shows, and
// again with its reference renewed when Telegram says it expired. It
// returns where the media is.
func (s *Store) pickerDocument(ctx context.Context, m model.Message, call func(*tg.Client, *tg.InputDocument) error) (fileLocation, error) {
	s.history.mu.Lock()
	api := s.history.api
	s.history.mu.Unlock()
	if api == nil {
		return fileLocation{}, errors.New("unavailable offline")
	}
	locate := func() (fileLocation, error) {
		s.history.mu.Lock()
		ref, ok := s.history.refs[m.Media.ID]
		s.history.mu.Unlock()
		if !ok {
			if cache := s.Cache(); cache != nil {
				ok, _ = cache.Get(ctx, "ref/"+m.Media.ID, &ref)
			}
		}
		// A GIF Telegram's search found on the web is no document.
		if !ok || ref.ID == 0 || ref.WebURL != "" || ref.Photo || ref.Peer != nil {
			return ref, errors.New("media reference unavailable")
		}
		return ref, nil
	}
	ref, err := locate()
	if err != nil {
		return ref, err
	}
	doc := func(ref fileLocation) *tg.InputDocument {
		return &tg.InputDocument{ID: ref.ID, AccessHash: ref.Hash, FileReference: ref.Reference}
	}
	err = call(api, doc(ref))
	if tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_EMPTY", "FILE_REFERENCE_INVALID") {
		if err = s.refreshReference(ctx, m); err != nil {
			return ref, err
		}
		if ref, err = locate(); err != nil {
			return ref, err
		}
		err = call(api, doc(ref))
	}
	return ref, err
}

// markSavedGIF has the references of media, at ref, renewed by the saved
// GIFs when no message has it, as those the picker fetched are.
func (s *Store) markSavedGIF(ctx context.Context, media *model.MessageMedia, ref fileLocation) {
	refs := map[string]fileLocation{}
	ref.SavedGIF = true
	refs[media.ID] = ref
	s.history.mu.Lock()
	if thumb := media.Thumbnail; thumb != nil {
		if r, ok := s.history.refs[thumb.ID]; ok && r.ID == ref.ID {
			r.SavedGIF = true
			refs[thumb.ID] = r
		}
	}
	for id, r := range refs {
		s.history.refs[id] = r
	}
	cache := s.history.cache
	s.history.mu.Unlock()
	if cache != nil {
		values := make(map[string]any, len(refs))
		for id, r := range refs {
			values["ref/"+id] = r
		}
		_ = cache.PutAll(ctx, values)
	}
}
