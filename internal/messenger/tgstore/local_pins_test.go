// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

func TestMergePins(t *testing.T) {
	for _, c := range []struct {
		name                string
		order, base, server []int64
		want                []int64
	}{
		{"nothing changed", []int64{1, 2, 9}, []int64{1, 2}, []int64{1, 2}, []int64{1, 2, 9}},
		{"unpinned elsewhere", []int64{1, 2, 9}, []int64{1, 2}, []int64{1}, []int64{1, 9}},
		{"pinned elsewhere", []int64{1, 2, 9}, []int64{1, 2}, []int64{3, 1, 2}, []int64{3, 1, 2, 9}},
		{"pinned elsewhere was local", []int64{1, 2, 9}, []int64{1, 2}, []int64{9, 1, 2}, []int64{9, 1, 2}},
		{"reordered elsewhere", []int64{1, 9, 2, 8}, []int64{1, 2}, []int64{2, 1}, []int64{2, 9, 1, 8}},
		{"pushed off here, not told yet", []int64{7, 1, 2}, []int64{1, 2}, []int64{1, 2}, []int64{7, 1, 2}},
		{"unpinned here, not told yet", []int64{2}, []int64{1, 2}, []int64{1, 2}, []int64{2}},
		{"nothing kept of Telegram's yet", []int64{9, 1}, nil, []int64{1, 2}, []int64{1, 9, 2}},
	} {
		if got := mergePins(c.order, c.base, c.server); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPlanPins(t *testing.T) {
	all := func(int64) bool { return true }
	for _, c := range []struct {
		name          string
		order, server []int64
		limit         int
		can           func(int64) bool
		want          pinPlan
	}{
		{"agree", []int64{1, 2}, []int64{1, 2}, 5, all, pinPlan{}},
		{"the local ones ask nothing", []int64{1, 2, 3}, []int64{1, 2}, 2, all, pinPlan{}},
		{"new first pushes the last", []int64{3, 1, 2}, []int64{1, 2}, 2, all, pinPlan{unpin: []int64{2}, pin: []int64{3}}},
		{"unpinned brings the next", []int64{2, 3}, []int64{1, 2}, 2, all, pinPlan{unpin: []int64{1}, pin: []int64{3}, reorder: []int64{2, 3}}},
		{"order", []int64{2, 1}, []int64{1, 2}, 2, all, pinPlan{reorder: []int64{2, 1}}},
		{"more than the limit stays while nothing needs room", []int64{1, 2, 3}, []int64{1, 2, 3}, 2, all, pinPlan{}},
		{"more than the limit goes when something does", []int64{4, 1, 2, 3}, []int64{1, 2, 3}, 2, all, pinPlan{unpin: []int64{2, 3}, pin: []int64{4}}},
		{"one that cannot be pinned takes no place", []int64{9, 1, 2}, []int64{1}, 2, func(id int64) bool { return id != 9 }, pinPlan{pin: []int64{2}, reorder: []int64{1, 2}}},
	} {
		got := planPins(c.order, c.server, c.limit, c.can)
		if !slices.Equal(got.unpin, c.want.unpin) || !slices.Equal(got.pin, c.want.pin) || !slices.Equal(got.reorder, c.want.reorder) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// pinServer is Telegram's side of the pins: it keeps them by its rules.
type pinServer struct {
	mu    sync.Mutex
	pins  []int64
	limit int
	calls []string
	fail  error
}

func (p *pinServer) client() *tg.Client {
	id := func(peer tg.InputDialogPeerClass) int64 {
		return peer.(*tg.InputDialogPeer).Peer.(*tg.InputPeerUser).UserID
	}
	return composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.fail != nil {
			return nil, p.fail
		}
		switch r := in.(type) {
		case *tg.MessagesToggleDialogPinRequest:
			chat := id(r.Peer)
			p.calls = append(p.calls, fmt.Sprintf("pin %d %v", chat, r.Pinned))
			if r.Pinned {
				if len(p.pins) >= p.limit {
					return nil, tgerr.New(400, "PINNED_DIALOGS_TOO_MUCH")
				}
				p.pins = append([]int64{chat}, p.pins...)
			} else {
				p.pins = slices.DeleteFunc(p.pins, func(other int64) bool { return other == chat })
			}
			return &tg.BoolTrue{}, nil
		case *tg.MessagesReorderPinnedDialogsRequest:
			var order []int64
			for _, peer := range r.Order {
				order = append(order, id(peer))
			}
			p.calls = append(p.calls, fmt.Sprintf("order %v", order))
			p.pins = order
			return &tg.BoolTrue{}, nil
		case *tg.MessagesGetPinnedDialogsRequest:
			res := &tg.MessagesPeerDialogs{}
			for _, chat := range p.pins {
				d := tg.Dialog{Peer: &tg.PeerUser{UserID: chat}}
				d.SetFlags()
				d.Pinned = true
				res.Dialogs = append(res.Dialogs, &d)
			}
			return res, nil
		}
		return nil, fmt.Errorf("unexpected %T", in)
	})
}

func (p *pinServer) took() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.calls
	p.calls = nil
	return out
}

func (p *pinServer) pinned() []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.pins)
}

// localPinStore has chats 1 to 9, the given ones pinned on Telegram, the limit
// of an account without Premium, and Local Premium on.
func localPinStore(t *testing.T, pinned ...int64) (*Store, *pinServer) {
	t.Helper()
	s := testStore(t)
	for id := int64(1); id <= 9; id++ {
		s.chats = append(s.chats, model.Chat{ID: id, LastTime: time.Unix(1000-id, 0)})
		s.history.peers[id] = peerRecord{Kind: "user", ID: id, Hash: id * 11}
	}
	s.chats = model.WithPinOrder(s.chats, pinned)
	server := &pinServer{pins: slices.Clone(pinned), limit: 5}
	s.history.api = server.client()
	s.SetLocalPremium(model.AllLocalPremium)
	s.dialogsRead()
	settled(t, s)
	return s, server
}

// settled waits for the background sync to end.
func settled(t *testing.T, s *Store) {
	t.Helper()
	l := &s.collections
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		l.mu.Lock()
		busy := l.running || l.dirty
		l.mu.Unlock()
		if !busy {
			return
		}
	}
	t.Fatal("the pins never settled")
}

func checkPins(t *testing.T, s *Store, server *pinServer, shown, onServer []int64) {
	t.Helper()
	settled(t, s)
	if got := model.PinnedOrder(s.Chats()); !slices.Equal(got, shown) {
		t.Errorf("shown %v, want %v", got, shown)
	}
	if got := server.pinned(); !slices.Equal(got, onServer) {
		t.Errorf("on Telegram %v, want %v", got, onServer)
	}
}

// A pin within the limit is a pin on Telegram, as without Local Premium.
func TestLocalPinWithinLimitReachesTelegram(t *testing.T) {
	s, server := localPinStore(t, 1, 2)
	ctx := context.Background()
	if err := s.PinChat(ctx, 3, true); err != nil {
		t.Fatal(err)
	}
	checkPins(t, s, server, []int64{3, 1, 2}, []int64{3, 1, 2})
	if err := s.PinChat(ctx, 1, false); err != nil {
		t.Fatal(err)
	}
	checkPins(t, s, server, []int64{3, 2}, []int64{3, 2})
	if err := s.ReorderPinned(ctx, []int64{2, 3}); err != nil {
		t.Fatal(err)
	}
	checkPins(t, s, server, []int64{2, 3}, []int64{2, 3})
}

// Past the limit the newest stay on Telegram: the oldest leaves it, not the
// list. Off and on, Local Premium shows Telegram's and its own again.
func TestLocalPinPastLimitPushesTheLastOff(t *testing.T) {
	s, server := localPinStore(t, 1, 2, 3, 4, 5)
	ctx := context.Background()
	if err := s.PinChat(ctx, 6, true); err != nil {
		t.Fatal(err)
	}
	checkPins(t, s, server, []int64{6, 1, 2, 3, 4, 5}, []int64{6, 1, 2, 3, 4})
	if got := server.took(); !slices.Equal(got, []string{"pin 5 false", "pin 6 true"}) {
		t.Fatalf("asked %v", got)
	}
	s.SetLocalPremium(model.LocalPremium{})
	if got := model.PinnedOrder(s.Chats()); !slices.Equal(got, []int64{6, 1, 2, 3, 4}) {
		t.Fatalf("without Local Premium %v", got)
	}
	s.SetLocalPremium(model.AllLocalPremium)
	checkPins(t, s, server, []int64{6, 1, 2, 3, 4, 5}, []int64{6, 1, 2, 3, 4})
	if got := server.took(); len(got) != 0 {
		t.Fatalf("nothing changed, and asked %v", got)
	}
}

// Unpinned on this device, a chat on Telegram leaves it, and the next takes its
// place; unpinned among the local ones, Telegram hears nothing.
func TestLocalUnpinBringsTheNext(t *testing.T) {
	s, server := localPinStore(t, 1, 2, 3, 4, 5)
	ctx := context.Background()
	for _, id := range []int64{7, 6} {
		if err := s.PinChat(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}
	checkPins(t, s, server, []int64{6, 7, 1, 2, 3, 4, 5}, []int64{6, 7, 1, 2, 3})
	server.took()
	if err := s.PinChat(ctx, 5, false); err != nil {
		t.Fatal(err)
	}
	checkPins(t, s, server, []int64{6, 7, 1, 2, 3, 4}, []int64{6, 7, 1, 2, 3})
	if got := server.took(); len(got) != 0 {
		t.Fatalf("a local pin asked %v", got)
	}
	if err := s.PinChat(ctx, 6, false); err != nil {
		t.Fatal(err)
	}
	checkPins(t, s, server, []int64{7, 1, 2, 3, 4}, []int64{7, 1, 2, 3, 4})
}

// An order among the local ones is told to nobody; one that brings a local chat
// among the first changes which are on Telegram.
func TestLocalReorder(t *testing.T) {
	s, server := localPinStore(t, 1, 2, 3, 4, 5)
	ctx := context.Background()
	for _, id := range []int64{7, 6} {
		if err := s.PinChat(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}
	settled(t, s)
	server.took()
	if err := s.ReorderPinned(ctx, []int64{6, 7, 1, 2, 3, 5, 4}); err != nil {
		t.Fatal(err)
	}
	checkPins(t, s, server, []int64{6, 7, 1, 2, 3, 5, 4}, []int64{6, 7, 1, 2, 3})
	if got := server.took(); len(got) != 0 {
		t.Fatalf("an order of local pins asked %v", got)
	}
	if err := s.ReorderPinned(ctx, []int64{4, 6, 7, 1, 2, 3, 5}); err != nil {
		t.Fatal(err)
	}
	checkPins(t, s, server, []int64{4, 6, 7, 1, 2, 3, 5}, []int64{4, 6, 7, 1, 2})
	if err := s.ReorderPinned(ctx, []int64{4, 6}); !errors.Is(err, model.ErrPinnedChanged) {
		t.Fatalf("an order without all the pins: %v", err)
	}
}

// What another device does comes into the list, and the first ones stay on
// Telegram; the update of a chat this device pushed off does not unpin it.
func TestLocalPinsTakeUpdates(t *testing.T) {
	s, server := localPinStore(t, 1, 2, 3, 4, 5)
	ctx := context.Background()
	if err := s.PinChat(ctx, 6, true); err != nil {
		t.Fatal(err)
	}
	settled(t, s)
	update := func(id int64, pin bool) {
		t.Helper()
		peer := &tg.DialogPeer{Peer: &tg.PeerUser{UserID: id}}
		if err := s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateDialogPinned{Pinned: pin, Peer: peer}}); err != nil {
			t.Fatal(err)
		}
	}
	// This device's own, come back: nothing moves.
	update(5, false)
	update(4, true)
	checkPins(t, s, server, []int64{6, 1, 2, 3, 4, 5}, []int64{6, 1, 2, 3, 4})

	// Unpinned elsewhere: gone here too, and the next goes up.
	server.mu.Lock()
	server.pins = []int64{6, 2, 3, 4}
	server.mu.Unlock()
	update(1, false)
	checkPins(t, s, server, []int64{6, 2, 3, 4, 5}, []int64{6, 2, 3, 4, 5})

	// Pinned elsewhere, once it had room: first here too.
	server.mu.Lock()
	server.pins = []int64{8, 6, 2, 3, 4}
	server.mu.Unlock()
	update(5, false)
	update(8, true)
	checkPins(t, s, server, []int64{8, 6, 2, 3, 4}, []int64{8, 6, 2, 3, 4})
}

// Offline the list changes all the same, and Telegram is told after the
// connection, from where the dialogs tell what it has.
func TestLocalPinsWaitForTheConnection(t *testing.T) {
	s, server := localPinStore(t, 1, 2, 3, 4, 5)
	ctx := context.Background()
	s.history.mu.Lock()
	api := s.history.api
	s.history.api = nil
	s.history.mu.Unlock()
	if err := s.PinChat(ctx, 6, true); err != nil {
		t.Fatal(err)
	}
	checkPins(t, s, server, []int64{6, 1, 2, 3, 4, 5}, []int64{1, 2, 3, 4, 5})
	s.history.mu.Lock()
	s.history.api = api
	s.history.mu.Unlock()
	s.dialogsRead()
	checkPins(t, s, server, []int64{6, 1, 2, 3, 4, 5}, []int64{6, 1, 2, 3, 4})
}

// A list that cannot be sent keeps what it has, and Telegram's limit being
// lower than known does not make the client ask for ever.
func TestLocalPinsSurviveRefusals(t *testing.T) {
	s, server := localPinStore(t, 1, 2, 3, 4, 5)
	ctx := context.Background()
	server.mu.Lock()
	server.fail = tgerr.New(400, "SOMETHING_ELSE")
	server.mu.Unlock()
	if err := s.PinChat(ctx, 6, true); err != nil {
		t.Fatal(err)
	}
	checkPins(t, s, server, []int64{6, 1, 2, 3, 4, 5}, []int64{1, 2, 3, 4, 5})
	server.mu.Lock()
	server.fail = nil
	server.limit = 4
	server.mu.Unlock()
	s.syncPins()
	settled(t, s)
	if got := server.took(); !slices.Equal(got, []string{"pin 5 false", "pin 6 true"}) {
		t.Fatalf("asked %v", got)
	}
	if got := model.PinnedOrder(s.Chats()); !slices.Contains(got, 5) || !slices.Contains(got, 6) || len(got) != 6 {
		t.Fatalf("shown %v after Telegram refused", got)
	}
}

// The list and what Telegram had when last compared outlive the client: a chat
// pushed off Telegram does not look unpinned elsewhere when it starts again.
func TestLocalPinsSurviveRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "h")
	s := New(nil)
	if err := s.Configure(ctx, "a", path, nil); err != nil {
		t.Fatal(err)
	}
	for id := int64(1); id <= 9; id++ {
		s.chats = append(s.chats, model.Chat{ID: id, LastTime: time.Unix(1000-id, 0)})
		s.history.peers[id] = peerRecord{Kind: "user", ID: id, Hash: id * 11}
	}
	s.chats = model.WithPinOrder(s.chats, []int64{1, 2, 3, 4, 5})
	server := &pinServer{pins: []int64{1, 2, 3, 4, 5}, limit: 5}
	s.history.api = server.client()
	s.SetLocalPremium(model.AllLocalPremium)
	s.dialogsRead()
	settled(t, s)
	if err := s.PinChat(ctx, 6, true); err != nil {
		t.Fatal(err)
	}
	settled(t, s)
	if err := s.persistDialogs(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = New(nil)
	if err := s.Configure(ctx, "a", path, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.history.api = server.client()
	s.SetLocalPremium(model.AllLocalPremium)
	want := []int64{6, 1, 2, 3, 4, 5}
	if got := model.PinnedOrder(s.Chats()); !slices.Equal(got, want) {
		t.Fatalf("shown %v after the restart, before the dialogs are read", got)
	}
	s.dialogsRead()
	checkPins(t, s, server, want, []int64{6, 1, 2, 3, 4})
}

// A local chat gone from the list (deleted, left, archived) leaves the pins,
// not to come back when a later chat of the same id does.
func TestLocalPinOfAChatGone(t *testing.T) {
	s, server := localPinStore(t, 1, 2, 3, 4, 5)
	if err := s.PinChat(context.Background(), 6, true); err != nil {
		t.Fatal(err)
	}
	settled(t, s)
	s.publish(func() {
		s.chats = slices.DeleteFunc(slices.Clone(s.chats), func(c model.Chat) bool { return c.ID == 5 })
	})
	s.syncPins()
	checkPins(t, s, server, []int64{6, 1, 2, 3, 4}, []int64{6, 1, 2, 3, 4})
	if slices.Contains(s.collections.pins.Order, 5) {
		t.Fatalf("kept %v", s.collections.pins.Order)
	}
}

// Chats is asked in every frame: with local pins and stickers it must not copy
// and sort the whole list each time.
func TestChatsIsTheSameSliceUntilChanged(t *testing.T) {
	s, _ := localPinStore(t, 1, 2)
	s.history.mu.Lock()
	s.history.localStickers = []model.Message{{Key: model.MessageKey{ChatID: 3, MessageID: -5}, Date: time.Unix(5000, 0), LocalPremium: true}}
	s.history.mu.Unlock()
	a, b := s.Chats(), s.Chats()
	if &a[0] != &b[0] {
		t.Fatal("Chats made a new list without a change")
	}
	if allocs := testing.AllocsPerRun(10, func() { s.Chats() }); allocs > 5 {
		t.Fatalf("%v allocations in a frame", allocs)
	}
	if err := s.PinChat(context.Background(), 4, true); err != nil {
		t.Fatal(err)
	}
	settled(t, s)
	if c := s.Chats(); !slices.Equal(model.PinnedOrder(c), []int64{4, 1, 2}) {
		t.Fatalf("a pin did not change the list: %v", model.PinnedOrder(c))
	}
	s.history.mu.Lock()
	s.history.localStickers = nil
	s.history.mu.Unlock()
	if c, ok := pinChatOf(s.Chats(), 3); !ok || c.Last.Sent {
		t.Fatalf("chat 3: %+v", c)
	}
}

func pinChatOf(chats []model.Chat, id int64) (model.Chat, bool) {
	for _, c := range chats {
		if c.ID == id {
			return c, true
		}
	}
	return model.Chat{}, false
}

// BenchmarkChats is what a frame pays for the list of a large account.
func BenchmarkChats(b *testing.B) {
	s := New(nil)
	if err := s.Configure(context.Background(), "a", filepath.Join(b.TempDir(), "h"), nil); err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	for id := int64(1); id <= 5000; id++ {
		s.chats = append(s.chats, model.Chat{ID: id, LastTime: time.Unix(100000-id, 0)})
	}
	s.SetLocalPremium(model.AllLocalPremium)
	s.collections.pins = &localPins{Order: []int64{1, 2, 3, 4, 5, 6, 7}}
	s.history.localStickers = []model.Message{{Key: model.MessageKey{ChatID: 3, MessageID: -5}, Date: time.Unix(200000, 0), LocalPremium: true}}
	b.ReportAllocs()
	for b.Loop() {
		s.Chats()
	}
}
