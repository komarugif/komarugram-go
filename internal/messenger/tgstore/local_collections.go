// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"sync"

	"komarugram/internal/messenger/model"
)

// Local collections overlay the server's lists while Local Premium is enabled.
// The actual Premium entitlement and the server's limits stay independent; the
// pins extend Telegram's, as local_pins.go tells.
type localCollections struct {
	mu     sync.Mutex
	loaded bool

	pins   *localPins
	saveMu sync.Mutex
	// ready tells that s.chats has been read from Telegram.
	ready          bool
	running, dirty bool
	attempts       int
	refused        map[int64]bool
	viewFrom, view []model.Chat
	viewPins       *localPins

	gifs       []model.PickerItem
	hiddenGIFs map[string]bool
}

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
	l.refused = map[int64]bool{}
	s.restoreLocalPins(ctx, l)
	var gifs localGIFs
	if _, err := s.Cache().Get(ctx, "local-premium/gifs", &gifs); err != nil {
		return err
	}
	l.gifs, l.hiddenGIFs = gifs.Items, gifs.Hidden
	l.loaded = true
	return nil
}

// withLocalCollections is chats with the list of pins of this device. It is
// asked in frames: the slice is the same until chats or the pins change, as
// chats is never changed in place.
func (s *Store) withLocalCollections(chats []model.Chat) []model.Chat {
	if !s.localPremium.Load() {
		return chats
	}
	l := &s.collections
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pins == nil {
		return chats
	}
	if l.viewPins != l.pins || !sameSlice(l.viewFrom, chats) {
		l.viewFrom, l.viewPins = chats, l.pins
		l.view = model.WithPinOrder(chats, l.pins.Order)
	}
	return l.view
}

// sameSlice reports whether a and b are one slice, not merely equal.
func sameSlice(a, b []model.Chat) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
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
