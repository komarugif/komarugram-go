// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"log"
	"slices"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// Local Premium has one list of pinned chats, longer than Telegram allows. Its
// first chats, as many as the account's limit, are pinned on Telegram like any
// others, so that the other devices see them; those after them are shown here
// alone. A new pin pushes the last of the first ones off Telegram, not off the
// list, and when a place frees, the next chat takes it.
//
// s.chats keeps what Telegram says, with or without Local Premium: the setting
// off, the list is simply Telegram's, and nothing is lost when it is on again.

// localPinsKey is where the account's cache keeps localPins.
const localPinsKey = "local-premium/pins"

// localPins is immutable once in localCollections: a change makes a new one.
type localPins struct {
	// Order is the list as this device shows it.
	Order []int64
	// Synced is what Telegram had pinned when last compared with Order. A chat
	// missing from Telegram is told from one never there by this: unpinned on
	// another device, it leaves Order too.
	Synced []int64
}

// mergePins takes into order what changed on Telegram since base, to server:
// what was unpinned elsewhere goes, what was pinned elsewhere comes first, and
// a new order made elsewhere is taken. What this device changed and did not
// tell Telegram yet stands, unless Telegram's order changed meanwhile.
func mergePins(order, base, server []int64) []int64 {
	out := slices.DeleteFunc(slices.Clone(order), func(id int64) bool {
		return slices.Contains(base, id) && !slices.Contains(server, id)
	})
	var added []int64
	news := false
	for _, id := range server {
		if !slices.Contains(base, id) {
			news = true
			if !slices.Contains(out, id) {
				added = append(added, id)
			}
		}
	}
	both := func(a, b []int64) []int64 {
		return slices.DeleteFunc(slices.Clone(a), func(id int64) bool { return !slices.Contains(b, id) })
	}
	if !news && slices.Equal(both(base, server), both(server, base)) {
		return out
	}
	out = append(added, out...)
	// The chats on Telegram take the places they have among the local ones, in
	// Telegram's order.
	theirs := both(server, out)
	next := 0
	for i, id := range out {
		if slices.Contains(server, id) {
			out[i] = theirs[next]
			next++
		}
	}
	return out
}

// pinPlan is what makes Telegram's pins agree with the list.
type pinPlan struct {
	// unpin and pin are told in this order: pin goes last to first, as each
	// new pin comes first on Telegram.
	unpin, pin []int64
	// reorder, when set, is the order to tell after them.
	reorder []int64
}

func (p pinPlan) empty() bool { return len(p.unpin) == 0 && len(p.pin) == 0 && p.reorder == nil }

// planPins is what limit, the account's real limit of pins, asks of server for
// order. The chats can says no to stay local, and take no place. Chats beyond
// the first ones already on Telegram (a Premium that ended leaves more than the
// limit) are pushed off it only when a new one needs the room.
func planPins(order, server []int64, limit int, can func(int64) bool) pinPlan {
	var first []int64
	for _, id := range order {
		if len(first) == limit {
			break
		}
		if can(id) {
			first = append(first, id)
		}
	}
	var p pinPlan
	for _, id := range first {
		if !slices.Contains(server, id) {
			p.pin = append(p.pin, id)
		}
	}
	for _, id := range server {
		if !slices.Contains(order, id) {
			p.unpin = append(p.unpin, id)
		}
	}
	if len(p.pin) > 0 && len(server)-len(p.unpin)+len(p.pin) > limit {
		for _, id := range server {
			if slices.Contains(order, id) && !slices.Contains(first, id) {
				p.unpin = append(p.unpin, id)
			}
		}
	}
	kept := slices.DeleteFunc(slices.Clone(server), func(id int64) bool { return slices.Contains(p.unpin, id) })
	then := append(slices.Clone(p.pin), kept...)
	want := slices.DeleteFunc(slices.Clone(order), func(id int64) bool { return !slices.Contains(then, id) })
	if !slices.Equal(then, want) {
		p.reorder = want
	}
	return p
}

// restoreLocalPins reads the list a former run kept. One that cannot be read
// costs the local pins, not the account.
func (s *Store) restoreLocalPins(ctx context.Context, l *localCollections) {
	var pins *localPins
	if _, err := s.Cache().Get(ctx, localPinsKey, &pins); err != nil {
		log.Printf("tgstore: local pins: %v", err)
		return
	}
	l.pins = pins
}

// saveLocalPins keeps the list as it is now; saveMu keeps an older one from
// being written last.
func (s *Store) saveLocalPins(ctx context.Context) error {
	l := &s.collections
	l.saveMu.Lock()
	defer l.saveMu.Unlock()
	l.mu.Lock()
	pins := l.pins
	l.mu.Unlock()
	return s.Cache().Put(ctx, localPinsKey, pins)
}

func (s *Store) pinLocalChat(ctx context.Context, chat int64, pin bool) error {
	return s.changeLocalPins(ctx, func(chats []model.Chat, order []int64) ([]int64, error) {
		if !slices.ContainsFunc(chats, func(c model.Chat) bool { return c.ID == chat }) {
			return nil, errNotConnected
		}
		order = slices.DeleteFunc(order, func(id int64) bool { return id == chat })
		if pin {
			order = append([]int64{chat}, order...)
		}
		return order, nil
	})
}

func (s *Store) reorderLocalPins(ctx context.Context, want []int64) error {
	return s.changeLocalPins(ctx, func(chats []model.Chat, order []int64) ([]int64, error) {
		if !model.SamePinned(model.WithPinOrder(chats, order), want) {
			return nil, model.ErrPinnedChanged
		}
		// Those not in the list until the dialogs are read are kept.
		rest := slices.DeleteFunc(order, func(id int64) bool {
			return slices.Contains(want, id) || slices.ContainsFunc(chats, func(c model.Chat) bool { return c.ID == id })
		})
		return append(slices.Clone(want), rest...), nil
	})
}

// changeLocalPins changes the list on this device at once, offline too;
// Telegram is told in the background.
func (s *Store) changeLocalPins(ctx context.Context, change func(chats []model.Chat, order []int64) ([]int64, error)) error {
	s.mu.RLock()
	chats := s.chats
	s.mu.RUnlock()
	l := &s.collections
	l.mu.Lock()
	was := l.pins
	now := &localPins{}
	if was != nil {
		now.Order, now.Synced = slices.Clone(was.Order), was.Synced
	} else {
		now.Order = model.PinnedOrder(chats)
		now.Synced = slices.Clone(now.Order)
	}
	var err error
	if now.Order, err = change(chats, now.Order); err != nil {
		l.mu.Unlock()
		return err
	}
	l.pins = now
	l.mu.Unlock()
	if err := s.saveLocalPins(ctx); err != nil {
		l.mu.Lock()
		if l.pins == now {
			l.pins = was
		}
		l.mu.Unlock()
		return err
	}
	s.changed()
	s.syncPins()
	return nil
}

// dialogsRead tells that s.chats has what Telegram has pinned: until now, it
// is what the cache kept, and comparing with it would tell nothing.
func (s *Store) dialogsRead() {
	l := &s.collections
	l.mu.Lock()
	l.ready = true
	l.mu.Unlock()
	s.syncPins()
}

// syncPins has the list and Telegram's pins made to agree, in the background,
// after either changed. One runs at a time, and goes again if asked meanwhile.
func (s *Store) syncPins() {
	if !s.localPremium.Load() {
		return
	}
	l := &s.collections
	l.mu.Lock()
	if !l.ready {
		l.mu.Unlock()
		return
	}
	l.dirty = true
	start := !l.running
	l.running = true
	l.mu.Unlock()
	if !start {
		return
	}
	c := s.history
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		l.mu.Lock()
		l.running = false
		l.mu.Unlock()
		return
	}
	defer c.mu.Unlock()
	ctx := c.ctx
	c.wg.Go(func() {
		defer crash.Recover("pinned chats", func(*crash.Panic) {
			l.mu.Lock()
			l.running = false
			l.mu.Unlock()
		})
		s.runPinSync(ctx)
	})
}

func (s *Store) runPinSync(ctx context.Context) {
	l := &s.collections
	for {
		l.mu.Lock()
		if !l.dirty {
			l.running = false
			l.mu.Unlock()
			return
		}
		l.dirty = false
		l.mu.Unlock()
		did, err := s.pushPins(ctx)
		l.mu.Lock()
		if err == nil {
			l.attempts = 0
			// The list is compared again with what came meanwhile.
			l.dirty = l.dirty || did
		} else {
			l.attempts++
		}
		attempts := l.attempts
		l.mu.Unlock()
		if err != nil {
			log.Printf("tgstore: pinned chats: %v", err)
			if delay, retry := outgoingRetry(err, attempts); retry {
				time.AfterFunc(delay, s.syncPins)
			}
		}
	}
}

// pushPins takes Telegram's changes into the list, and tells Telegram the
// list's first chats. It reports whether it told anything.
func (s *Store) pushPins(ctx context.Context) (bool, error) {
	if !s.localPremium.Load() {
		return false, nil
	}
	s.mu.RLock()
	chats := s.chats
	s.mu.RUnlock()
	server := model.PinnedOrder(chats)
	shown := func(id int64) bool {
		return slices.ContainsFunc(chats, func(c model.Chat) bool { return c.ID == id })
	}
	l := &s.collections
	l.mu.Lock()
	was := l.pins
	now := &localPins{Synced: server}
	if was != nil {
		// A chat gone from the list (deleted, left, archived) is unpinned.
		now.Order = slices.DeleteFunc(mergePins(was.Order, was.Synced, server), func(id int64) bool { return !shown(id) })
	} else {
		now.Order = slices.Clone(server)
	}
	changed := was == nil || !slices.Equal(was.Order, now.Order) || !slices.Equal(was.Synced, now.Synced)
	if changed {
		l.pins = now
	}
	refused := l.refused
	l.mu.Unlock()
	if changed {
		if err := s.saveLocalPins(ctx); err != nil {
			return false, err
		}
		s.changed()
	}

	c := s.history
	c.mu.Lock()
	api := c.api
	can := make(map[int64]tg.InputDialogPeerClass, len(now.Order))
	for _, id := range append(slices.Clone(now.Order), server...) {
		if peer := c.peers[id]; peer.ID != 0 {
			can[id] = peer.dialogPeer()
		}
	}
	c.mu.Unlock()
	if api == nil {
		// Load comes when connected, and sends this here again.
		return false, nil
	}
	plan := planPins(now.Order, server, s.Premium().Limit("dialogs_pinned_limit"), func(id int64) bool {
		return can[id] != nil && !refused[id]
	})
	// A chat on Telegram with no known peer can be neither unpinned nor named
	// in an order, which would unpin it.
	plan.unpin = slices.DeleteFunc(plan.unpin, func(id int64) bool { return can[id] == nil })
	if slices.ContainsFunc(server, func(id int64) bool { return can[id] == nil }) {
		plan.reorder = nil
	}
	if plan.empty() {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	synced := slices.Clone(server)
	setSynced := func(ids []int64) error {
		synced = ids
		l.mu.Lock()
		next := *l.pins
		next.Synced = ids
		l.pins = &next
		l.mu.Unlock()
		return s.saveLocalPins(ctx)
	}
	for _, id := range plan.unpin {
		// Kept before Telegram is told: were the client to stop between the
		// two, the chat is on Telegram and in the list, and is seen again. The
		// other way round it would look unpinned elsewhere, and leave the list.
		before := synced
		err := setSynced(slices.DeleteFunc(slices.Clone(synced), func(other int64) bool { return other == id }))
		if err != nil {
			return false, err
		}
		if _, err = api.MessagesToggleDialogPin(ctx, &tg.MessagesToggleDialogPinRequest{Peer: can[id]}); err != nil {
			_ = setSynced(before)
			return false, err
		}
		s.publish(func() { s.chats = model.WithPinned(s.chats, id, false) })
	}
	for _, id := range slices.Backward(plan.pin) {
		_, err := api.MessagesToggleDialogPin(ctx, &tg.MessagesToggleDialogPinRequest{Pinned: true, Peer: can[id]})
		switch {
		case tgerr.Is(err, "PINNED_DIALOGS_TOO_MUCH"):
			// Telegram has more than was known here. It is read again, and the
			// list waits for the next change: asking again now gets the same.
			s.readPinned(ctx)
			return false, nil
		case tgerr.Is(err, "PEER_ID_INVALID", "CHANNEL_PRIVATE", "CHANNEL_INVALID", "USER_BANNED_IN_CHANNEL"):
			// This one stays local; the others go on until the next run.
			log.Printf("tgstore: chat %d stays pinned locally: %v", id, err)
			l.mu.Lock()
			l.refused[id] = true
			l.mu.Unlock()
			return true, nil
		case err != nil:
			return false, err
		}
		if err = setSynced(append([]int64{id}, synced...)); err != nil {
			return false, err
		}
		s.publish(func() { s.chats = model.WithPinned(s.chats, id, true) })
	}
	if plan.reorder != nil {
		peers := make([]tg.InputDialogPeerClass, 0, len(plan.reorder))
		for _, id := range plan.reorder {
			peers = append(peers, can[id])
		}
		if _, err := api.MessagesReorderPinnedDialogs(ctx, &tg.MessagesReorderPinnedDialogsRequest{Force: true, Order: peers}); err != nil {
			return false, err
		}
		if err := setSynced(plan.reorder); err != nil {
			return false, err
		}
		s.publish(func() { s.chats = model.WithPinOrder(s.chats, plan.reorder) })
	}
	_ = s.persistDialogs(ctx)
	return true, nil
}
