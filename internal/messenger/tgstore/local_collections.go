// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"slices"
	"sync"

	"komarugram/internal/messenger/model"
)

// Local collections overlay the server's lists while Local Premium is enabled.
// The actual Premium entitlement and the server's limits stay independent.
type localCollections struct {
	mu         sync.Mutex
	loaded     bool
	pins       *localPins
	gifs       []model.PickerItem
	hiddenGIFs map[string]bool
}
type localPins struct{ Order []int64 }
type localGIFs struct {
	Items  []model.PickerItem
	Hidden map[string]bool
}

func (s *Store) restoreLocalCollections(ctx context.Context) error {
	l := &s.collections
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loaded {
		return nil
	}
	cache := s.Cache()
	if _, err := cache.Get(ctx, "local-premium/pins", &l.pins); err != nil {
		return err
	}
	var gifs localGIFs
	if _, err := cache.Get(ctx, "local-premium/gifs", &gifs); err != nil {
		return err
	}
	l.gifs, l.hiddenGIFs = gifs.Items, gifs.Hidden
	l.loaded = true
	return nil
}

func (s *Store) withLocalCollections(chats []model.Chat) []model.Chat {
	if !s.localPremium.Load() {
		return chats
	}
	l := &s.collections
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pins != nil {
		return model.WithPinOrder(chats, l.pins.Order)
	}
	return chats
}

func (s *Store) pinLocalChat(ctx context.Context, chat int64, pin bool) error {
	return s.changeLocalPins(ctx, func(chats []model.Chat) ([]int64, error) {
		if !slices.ContainsFunc(chats, func(c model.Chat) bool { return c.ID == chat }) {
			return nil, errNotConnected
		}
		return model.PinnedOrder(model.WithPinned(chats, chat, pin)), nil
	})
}

func (s *Store) reorderLocalPins(ctx context.Context, order []int64) error {
	return s.changeLocalPins(ctx, func(chats []model.Chat) ([]int64, error) {
		if !model.SamePinned(chats, order) {
			return nil, model.ErrPinnedChanged
		}
		return order, nil
	})
}

func (s *Store) changeLocalPins(ctx context.Context, change func([]model.Chat) ([]int64, error)) error {
	s.mu.RLock()
	chats := s.chats
	s.mu.RUnlock()
	l := &s.collections
	err := func() error {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.pins != nil {
			chats = model.WithPinOrder(chats, l.pins.Order)
		}
		order, err := change(chats)
		if err != nil {
			return err
		}
		value := &localPins{Order: slices.Clone(order)}
		if err := s.Cache().Put(ctx, "local-premium/pins", value); err != nil {
			return err
		}
		l.pins = value
		return nil
	}()
	if err == nil {
		s.changed()
	}
	return err
}

func (s *Store) withLocalGIFs(page model.PickerPage) model.PickerPage {
	if !s.localPremium.Load() {
		return page
	}
	l := &s.collections
	l.mu.Lock()
	defer l.mu.Unlock()
	var server []model.PickerItem
	for _, item := range page.Items {
		if !l.hiddenGIFs[item.ID] {
			server = append(server, item)
		}
	}
	page.Items = model.PreferSaved(l.gifs, server)
	return page
}

func (s *Store) saveLocalGIF(ctx context.Context, m model.Message, unsave bool) error {
	l := &s.collections
	err := func() error {
		l.mu.Lock()
		defer l.mu.Unlock()
		items := model.WithoutItem(l.gifs, m.Media.ID)
		hidden := make(map[string]bool, len(l.hiddenGIFs)+1)
		for id, on := range l.hiddenGIFs {
			hidden[id] = on
		}
		if unsave {
			hidden[m.Media.ID] = true
		} else {
			delete(hidden, m.Media.ID)
			items = append([]model.PickerItem{model.PickerItemOf(m)}, items...)
		}
		if err := s.Cache().Put(ctx, "local-premium/gifs", localGIFs{items, hidden}); err != nil {
			return err
		}
		l.gifs, l.hiddenGIFs = items, hidden
		return nil
	}()
	if err == nil {
		s.changed()
	}
	return err
}
