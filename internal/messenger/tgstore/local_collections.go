// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"log"
	"slices"
	"sync"

	"komarugram/internal/messenger/model"
)

// Local collections extend the server's lists while Local Premium is enabled:
// what fits in the account's real limits is on Telegram as always, and what
// Telegram would push out stays here. See local_pins.go for the pins.
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

	// gifs are the saved GIFs Telegram no longer has.
	gifs []model.PickerItem

	// storage and work are the channel the lists are kept in: storage_channel.go.
	storage storageState
	work    storageWork
}

type localGIFs struct{ Items []model.PickerItem }

const localGIFsKey = "local-premium/gifs"

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
	if _, err := s.Cache().Get(ctx, localGIFsKey, &gifs); err != nil {
		log.Printf("tgstore: local GIFs: %v", err)
	}
	l.gifs = gifs.Items
	s.restoreStorage(ctx, l)
	l.loaded = true
	return nil
}

// withLocalCollections is chats with the list of pins of this device. It is
// asked in frames: the slice is the same until chats or the pins change, as
// chats is never changed in place.
func (s *Store) withLocalCollections(chats []model.Chat) []model.Chat {
	if !s.local().Pins {
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
	if !s.local().GIFs {
		return page
	}
	l := &s.collections
	l.mu.Lock()
	defer l.mu.Unlock()
	page.Items = model.PreferSaved(page.Items, l.gifs)
	return page
}

// localGIF reports whether Local Premium keeps the GIF id.
func (s *Store) localGIF(id string) bool {
	if !s.local().GIFs {
		return false
	}
	l := &s.collections
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.ContainsFunc(l.gifs, func(item model.PickerItem) bool { return item.ID == id })
}

// changeLocalGIFs drops the GIF id from those kept here, and adds pushed, what
// Telegram's list has no room for any more, to their top.
func (s *Store) changeLocalGIFs(ctx context.Context, id string, pushed []model.PickerItem) error {
	l := &s.collections
	l.mu.Lock()
	items := model.PreferSaved(pushed, model.WithoutItem(l.gifs, id))
	same := len(items) == len(l.gifs)
	l.gifs = items
	l.mu.Unlock()
	if same && len(pushed) == 0 {
		return nil
	}
	err := s.Cache().Put(ctx, localGIFsKey, localGIFs{items})
	s.markStorageChanged(true, false)
	s.changed()
	return err
}
