// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/historycache"
	"komarugram/internal/messenger/historylog"
	"komarugram/internal/messenger/model"
)

// Keep the complete TL response. Besides the display order it carries the
// account's tag switch, title entities, peer access hashes and ordered pins.
const dialogFiltersKey = "dialog-filters-v1"

type keptDialogFilters struct {
	Self int64 // Resolves inputPeerSelf even before the profile is loaded.
	TL   []byte
}

func cachedDialogFilters(ctx context.Context, cache *historycache.Cache) (int64, tg.MessagesDialogFilters, error) {
	var kept keptDialogFilters
	found, err := cache.Get(ctx, dialogFiltersKey, &kept)
	if !found {
		return 0, tg.MessagesDialogFilters{}, err
	}
	var filters tg.MessagesDialogFilters
	if err == nil {
		err = filters.Decode(&bin.Buffer{Buf: kept.TL})
	}
	if err != nil {
		// A damaged or obsolete folder snapshot can be fetched again.
		historylog.Printf("cached dialog filters: %v", err)
		return 0, tg.MessagesDialogFilters{}, nil
	}
	return kept.Self, filters, nil
}

func (s *Store) loadDialogFilters(ctx context.Context, api *tg.Client, self int64) error {
	// A notification can arrive while the initial fetch is in flight. Only
	// one fetch may replace the account's snapshot at a time.
	s.filterLoad.Lock()
	defer s.filterLoad.Unlock()
	filters, err := api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return err
	}
	return s.storeDialogFilters(ctx, self, filters)
}

func (s *Store) storeDialogFilters(ctx context.Context, self int64, filters *tg.MessagesDialogFilters) error {
	cache := s.Cache()
	var b bin.Buffer
	// TL encoding sets presence flags on the values it visits. A reorder
	// reuses the old filters, so encode under the same lock as their readers.
	err := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if cache != nil {
			if err := filters.Encode(&b); err != nil {
				return err
			}
		}
		s.filterSelf, s.filters = self, *filters
		s.folders = folderList(self, filters.Filters, s.entries)
		return nil
	}()
	if err != nil {
		return err
	}
	s.changed()
	if cache != nil {
		return cache.Put(ctx, dialogFiltersKey, keptDialogFilters{Self: self, TL: b.Buf})
	}
	return nil
}

func dialogFolder(self int64, raw tg.DialogFilterClass, entries map[int64]entry) (model.Folder, bool) {
	var folder model.Folder
	var title tg.TextWithEntities
	var pinned, included, excluded []tg.InputPeerClass
	var color int
	var colored bool
	switch f := raw.(type) {
	case *tg.DialogFilterDefault:
		return folder, true
	case *tg.DialogFilter:
		folder.ID, title, folder.Emoticon, folder.TitleNoAnimate = int64(f.ID), f.Title, f.Emoticon, f.TitleNoanimate
		color, colored = f.GetColor()
		folder.FolderTypes = model.FolderTypes{
			Contacts: f.Contacts, NonContacts: f.NonContacts,
			Groups: f.Groups, Channels: f.Broadcasts, Bots: f.Bots,
		}
		folder.ExcludeMuted, folder.ExcludeRead, folder.ExcludeArchived = f.ExcludeMuted, f.ExcludeRead, f.ExcludeArchived
		pinned, included, excluded = f.PinnedPeers, f.IncludePeers, f.ExcludePeers
	case *tg.DialogFilterChatlist:
		folder.ID, title, folder.Emoticon, folder.TitleNoAnimate = int64(f.ID), f.Title, f.Emoticon, f.TitleNoanimate
		folder.Shared, folder.HasMyInvites = true, f.HasMyInvites
		color, colored = f.GetColor()
		pinned, included = f.PinnedPeers, f.IncludePeers
	default:
		return folder, false
	}
	folder.Title, folder.TitleEntities = title.Text, convertEntities(title.Entities)
	if colored {
		folder.Color = &color
	}
	ids := func(peers []tg.InputPeerClass) []int64 {
		var ids []int64
		for _, p := range peers {
			ids = append(ids, inputPeerID(p, self))
		}
		return ids
	}
	folder.Pinned, folder.Included, folder.Excluded = ids(pinned), ids(included), ids(excluded)
	return folder.WithDialogFlags(func(id int64) (bool, bool) {
		e := entries[id]
		return e.contact, e.unreadMark
	}), true
}

// filtersForEdit reads the current server order before creating a folder or
// applying an order from a window. The caller holds filterLoad through the write.
func (s *Store) filtersForEdit(ctx context.Context) (*tg.Client, int64, *tg.MessagesDialogFilters, error) {
	s.mu.RLock()
	self := s.filterSelf
	s.mu.RUnlock()
	api := s.api()
	if api == nil || self == 0 {
		return nil, 0, nil, errNotConnected
	}
	current, err := api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	return api, self, current, s.storeDialogFilters(ctx, self, current)
}

func (s *Store) SuggestedFolders(ctx context.Context) ([]model.Folder, error) {
	api := s.api()
	if api == nil {
		return nil, errNotConnected
	}
	suggestions, err := api.MessagesGetSuggestedDialogFilters(ctx)
	if err != nil {
		return nil, err
	}
	var out []model.Folder
	for _, suggestion := range suggestions {
		if f, ok := dialogFolder(0, suggestion.Filter, nil); ok && !f.Shared {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *Store) SaveFolder(ctx context.Context, previous *model.Folder, draft model.Folder) error {
	if previous == nil || previous.Title != draft.Title {
		draft.Title = strings.TrimSpace(draft.Title)
	}
	s.filterLoad.Lock()
	defer s.filterLoad.Unlock()
	api, self, current, err := s.filtersForEdit(ctx)
	if err != nil {
		return err
	}
	if done, err := model.CheckFolderSave(s.Folders(), previous, draft, s.Premium()); done || err != nil {
		return err
	}
	var original tg.DialogFilterClass
	index := slices.IndexFunc(current.Filters, func(raw tg.DialogFilterClass) bool {
		f, ok := raw.(interface{ GetID() int })
		return ok && int64(f.GetID()) == draft.ID
	})
	if index >= 0 {
		original = current.Filters[index]
	}
	raw, err := s.editedFilter(self, original, draft)
	if err != nil {
		return err
	}
	ok, err := api.MessagesUpdateDialogFilter(ctx, &tg.MessagesUpdateDialogFilterRequest{ID: int(draft.ID), Filter: raw})
	switch {
	case tgerr.Is(err, "DIALOG_FILTERS_TOO_MUCH"):
		return model.ErrFolderLimit
	case tgerr.Is(err, "FILTER_INCLUDE_TOO_MUCH", "FILTER_EXCLUDE_TOO_MUCH"):
		return model.ErrFolderPeerLimit
	case err != nil:
		return err
	case !ok:
		return errors.New("folder update refused")
	}
	current.Filters = slices.Clone(current.Filters)
	if index < 0 {
		current.Filters = append(current.Filters, raw)
	} else {
		current.Filters[index] = raw
	}
	return s.keepFolderChange(ctx, self, current)
}

// Reuse Telegram's exact peers, even when a dialog was never loaded. Resolve
// only newly selected chats against the account's peer records.
func (s *Store) editedFilter(self int64, original tg.DialogFilterClass, draft model.Folder) (tg.DialogFilterClass, error) {
	var title tg.TextWithEntities
	known := map[int64]tg.InputPeerClass{}
	remember := func(groups ...[]tg.InputPeerClass) {
		for _, group := range groups {
			for _, peer := range group {
				known[inputPeerID(peer, self)] = peer
			}
		}
	}
	switch f := original.(type) {
	case *tg.DialogFilter:
		title = f.Title
		remember(f.PinnedPeers, f.IncludePeers, f.ExcludePeers)
	case *tg.DialogFilterChatlist:
		title = f.Title
		remember(f.PinnedPeers, f.IncludePeers)
	}
	if title.Text != draft.Title {
		title = tg.TextWithEntities{Text: draft.Title}
	}
	resolve := func(ids []int64) ([]tg.InputPeerClass, error) {
		var peers []tg.InputPeerClass
		for _, id := range ids {
			peer := known[id]
			if peer == nil {
				record, ok := s.history.peers[id]
				if !ok {
					return nil, errors.New("chat no longer available")
				}
				peer = record.input()
			}
			peers = append(peers, peer)
		}
		return peers, nil
	}
	s.history.mu.Lock()
	defer s.history.mu.Unlock()
	pins, err := resolve(draft.Pinned)
	if err != nil {
		return nil, err
	}
	includes, err := resolve(draft.Included)
	if err != nil {
		return nil, err
	}
	if src, ok := original.(*tg.DialogFilterChatlist); ok {
		f := *src
		f.Title, f.Emoticon, f.PinnedPeers, f.IncludePeers = title, draft.Emoticon, pins, includes
		if f.Emoticon == "" {
			f.Flags.Unset(25)
		}
		return &f, nil
	}
	excludes, err := resolve(draft.Excluded)
	if err != nil {
		return nil, err
	}
	f := &tg.DialogFilter{ID: int(draft.ID), Title: title, Emoticon: draft.Emoticon, PinnedPeers: pins, IncludePeers: includes, ExcludePeers: excludes,
		Contacts: draft.Contacts, NonContacts: draft.NonContacts, Groups: draft.Groups, Broadcasts: draft.Channels, Bots: draft.Bots,
		ExcludeMuted: draft.ExcludeMuted, ExcludeRead: draft.ExcludeRead, ExcludeArchived: draft.ExcludeArchived}
	if src, ok := original.(*tg.DialogFilter); ok {
		f.TitleNoanimate = src.TitleNoanimate
		if color, ok := src.GetColor(); ok {
			f.SetColor(color)
		}
	}
	return f, nil
}

// Removing the filter with no replacement leaves every dialog and membership intact.
func (s *Store) DeleteFolder(ctx context.Context, folder model.Folder) error {
	if folder.ID == 0 {
		return model.ErrFolderConflict
	}
	s.filterLoad.Lock()
	defer s.filterLoad.Unlock()
	api, self, current, err := s.filtersForEdit(ctx)
	if err != nil {
		return err
	}
	for i, raw := range current.Filters {
		f, ok := dialogFolder(self, raw, nil)
		if !ok || f.ID != folder.ID {
			continue
		}
		if !f.SameRules(folder) || f.HasMyInvites != folder.HasMyInvites {
			return model.ErrFolderConflict
		}
		ok, err = api.MessagesUpdateDialogFilter(ctx, &tg.MessagesUpdateDialogFilterRequest{ID: int(folder.ID)})
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("folder deletion refused")
		}
		current.Filters = slices.Delete(slices.Clone(current.Filters), i, i+1)
		return s.keepFolderChange(ctx, self, current)
	}
	return nil
}

func (s *Store) ReorderFolders(ctx context.Context, previous, next []int64) error {
	s.filterLoad.Lock()
	defer s.filterLoad.Unlock()
	api, self, current, err := s.filtersForEdit(ctx)
	if err != nil {
		return err
	}
	if _, err := model.ReorderedFolders(s.Folders(), previous, next, s.Premium().Active); err != nil {
		return err
	}
	order := make([]int, len(next))
	for i, id := range next {
		order[i] = int(id)
	}
	ok, err := api.MessagesUpdateDialogFiltersOrder(ctx, order)
	if err != nil {
		return err
	}
	if !ok {
		return model.ErrFolderOrder
	}
	byID := map[int64]tg.DialogFilterClass{0: &tg.DialogFilterDefault{}}
	for _, raw := range current.Filters {
		switch f := raw.(type) {
		case *tg.DialogFilter:
			byID[int64(f.ID)] = f
		case *tg.DialogFilterChatlist:
			byID[int64(f.ID)] = f
		}
	}
	current.Filters = make([]tg.DialogFilterClass, len(next))
	for i, id := range next {
		current.Filters[i] = byID[id]
	}
	return s.keepFolderChange(ctx, self, current)
}

// Telegram has acknowledged the change. Report a cache failure separately so
// the editor does not invite the user to repeat a successful creation.
func (s *Store) keepFolderChange(ctx context.Context, self int64, filters *tg.MessagesDialogFilters) error {
	if err := s.storeDialogFilters(ctx, self, filters); err != nil {
		return errors.Join(model.ErrFolderCache, err)
	}
	return nil
}
