// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// verifiedChat is a chat of messages 1..last served as Telegram serves
// them, some deleted or edited at will, and the requests for its history:
// asked, the pages asked for as the history needs them; checked, the
// ranges checked in the background (history_check.go).
type verifiedChat struct {
	t       *testing.T
	s       *Store
	chat    int64
	peer    tg.PeerClass
	last    int
	mu      sync.Mutex
	deleted map[int]bool
	edited  map[int]int  // edit dates
	hidden  map[int]bool // edits hidden (edit_hide)
	refuse  error        // what the checks are answered with
	asked   atomic.Int32
	checked atomic.Int32
	// same counts the answers messagesNotModified.
	same atomic.Int32
}

func newVerifiedChat(t *testing.T, s *Store, peer tg.PeerClass, record peerRecord) *verifiedChat {
	return newVerifiedChatTo(t, s, peer, record, 1000)
}

// newVerifiedChatTo is newVerifiedChat of a chat of messages 1..last. The
// chat is shown, as the checks need; they run soon after the scroll rests.
func newVerifiedChatTo(t *testing.T, s *Store, peer tg.PeerClass, record peerRecord, last int) *verifiedChat {
	v := &verifiedChat{t: t, s: s, chat: peerID(peer), peer: peer, last: last, deleted: map[int]bool{}, edited: map[int]int{}, hidden: map[int]bool{}}
	s.history.peers[v.chat] = record
	s.history.top[v.chat] = last
	s.WatchChat(v, v.chat)
	v.setRest(30 * time.Millisecond)
	s.history.api = tg.NewClient(telegram.InvokeFunc(v.invoke))
	return v
}

// fold is the hash messages.getHistory takes of msgs, newest first: an
// edit date counts hidden or not, as Telegram's does.
func fold(msgs []tg.MessageClass) int64 {
	var h uint64
	add := func(v int64) {
		h ^= h >> 21
		h ^= h << 35
		h ^= h >> 4
		h += uint64(v)
	}
	for _, r := range msgs {
		m := r.(*tg.Message)
		add(int64(m.ID))
		if e, ok := m.GetEditDate(); ok {
			add(int64(e))
		} else {
			add(int64(m.Date))
		}
	}
	return int64(h)
}

// invoke answers messages.getHistory as Telegram does: the window that
// starts add_offset messages from offset_id in the newest-first list, cut
// at the newest message, then only the messages between min_id and max_id;
// messagesNotModified when they fold to the hash asked with.
func (v *verifiedChat) invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	req, ok := in.(*tg.MessagesGetHistoryRequest)
	if !ok {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if req.MinID != 0 {
		v.checked.Add(1)
		if v.refuse != nil {
			return v.refuse
		}
	} else {
		v.asked.Add(1)
	}
	var newest []int
	for id := v.last; id >= 1; id-- {
		if !v.deleted[id] {
			newest = append(newest, id)
		}
	}
	at := 0
	if req.OffsetID != 0 {
		at = len(newest)
		for i, id := range newest {
			if id < req.OffsetID {
				at = i
				break
			}
		}
	}
	from := at + req.AddOffset
	to := min(from+req.Limit, len(newest))
	from = max(from, 0)
	var msgs []tg.MessageClass
	for _, id := range newest[from:max(from, to)] {
		if req.MinID != 0 && id <= req.MinID || req.MaxID != 0 && id >= req.MaxID {
			continue
		}
		m := &tg.Message{ID: id, PeerID: v.peer, Message: "m", Date: 10 + id}
		if e, ok := v.edited[id]; ok {
			m.Message = "edited"
			m.SetEditDate(e)
			m.EditHide = v.hidden[id]
		}
		msgs = append(msgs, m)
	}
	box := out.(*tg.MessagesMessagesBox)
	if req.Hash != 0 && req.Hash == fold(msgs) {
		v.same.Add(1)
		box.Messages = &tg.MessagesMessagesNotModified{Count: len(msgs)}
		return nil
	}
	box.Messages = &tg.MessagesMessages{Messages: msgs, Users: []tg.UserClass{&tg.User{ID: 5, AccessHash: 1}}}
	return nil
}

// setRest sets how long the scroll rests before a check.
func (v *verifiedChat) setRest(d time.Duration) {
	k := &v.s.history.checks
	k.mu.Lock()
	k.rest = d
	k.mu.Unlock()
}

func (v *verifiedChat) rest() time.Duration {
	k := &v.s.history.checks
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.restFor()
}

// settle waits for the checks queued to be done, and tells how many asked
// Telegram since the last time.
func (v *verifiedChat) settle() int {
	v.t.Helper()
	k := &v.s.history.checks
	time.Sleep(2 * v.rest())
	deadline := time.Now().Add(5 * time.Second)
	for {
		k.mu.Lock()
		idle := len(k.queue) == 0 && !k.running
		k.mu.Unlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			v.t.Fatal("the checks did not end")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return int(v.checked.Swap(0))
}

// expectVerified tells that the next page older comes from the cache,
// which vouches for it: neither asked of Telegram nor checked.
func (v *verifiedChat) expectVerified(what string) {
	v.t.Helper()
	v.settle()
	if n := v.older(); n != 0 {
		v.t.Fatalf("%s: %d pages asked of Telegram", what, n)
	}
	if n := v.settle(); n != 0 {
		v.t.Fatalf("%s: %d ranges checked", what, n)
	}
}

// expectChecked tells that the next page older is the cache's, which
// cannot vouch for it: shown at once, not asked of Telegram, and checked
// once the scroll rests.
func (v *verifiedChat) expectChecked(what string) {
	v.t.Helper()
	v.settle()
	if n := v.older(); n != 0 {
		v.t.Fatalf("%s: %d pages asked of Telegram, want none: the cache's is shown", what, n)
	}
	if n := v.settle(); n != 1 {
		v.t.Fatalf("%s: %d ranges checked, want 1", what, n)
	}
}

// open opens the chat again at its end, as the button to the end does.
func (v *verifiedChat) open() {
	v.s.reveal(v.chat, 0, true)
	v.s.OpenChat(v.chat)
	waitHistory(v.t, v.s, v.chat)
}

// older loads the page before the history and tells how many pages that
// asked of Telegram.
func (v *verifiedChat) older() int {
	v.t.Helper()
	before := v.asked.Load()
	first := v.s.History(v.chat).Messages[0].Key.MessageID
	// The view asks for older messages when it shows the first it has.
	v.s.SaveView(model.Viewport{ChatID: v.chat, AnchorMessageID: first}, nil)
	v.s.LoadOlder(v.chat)
	h := waitHistory(v.t, v.s, v.chat)
	if h.Err != nil || h.Messages[0].Key.MessageID >= first {
		v.t.Fatalf("nothing older than %d came: %v", first, h.Err)
	}
	return int(v.asked.Load() - before)
}

// read reads the chat's newest 400 messages from Telegram: a page at its
// end and four before it.
func (v *verifiedChat) read() {
	v.t.Helper()
	v.s.OpenChat(v.chat)
	waitHistory(v.t, v.s, v.chat)
	for range 4 {
		v.older()
	}
}

// The pages scrolled through again that the cache has whole, read from
// Telegram in this chain, are not asked of it: most pages of a scroll were
// the cache's, and each was read again only to be compared with it. Where
// the verified part ends, Telegram is asked again.
func TestVerifiedPagesComeFromTheCache(t *testing.T) {
	s := testStore(t)
	v := newVerifiedChat(t, s, &tg.PeerUser{UserID: 5}, peerRecord{ID: 5, Hash: 1, Kind: "user"})
	v.read() // 601..1000
	v.open() // asks for the newest page; the cache shows 801..1000
	v.settle()
	for i := range 2 {
		if n := v.older(); n != 0 {
			t.Fatalf("page %d of the verified part: %d pages asked of Telegram", i+1, n)
		}
	}
	if n := v.settle(); n != 0 {
		t.Fatalf("the verified part: %d ranges checked", n)
	}
	if n := v.older(); n != 1 {
		t.Fatalf("past the verified part: %d pages asked of Telegram, want 1", n)
	}
}

// openStoreAt opens a store on the cache at path, as the client does when
// it starts.
func openStoreAt(t *testing.T, path string) *Store {
	t.Helper()
	s := New(nil)
	s.history.pageNormal, s.history.pageActive = 80, 80
	if e := s.Configure(context.Background(), "a", path, nil); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// The common box's updates are asked for again from the state kept, so
// what it verified stays so after a restart; not once its difference was
// too long, nor without a state to ask it from.
func TestCommonChainOutlastsARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	user := peerRecord{ID: 5, Hash: 1, Kind: "user"}
	first := openStoreAt(t, path)
	newVerifiedChat(t, first, &tg.PeerUser{UserID: 5}, user).read()
	first.Close()

	s := openStoreAt(t, path)
	v := newVerifiedChat(t, s, &tg.PeerUser{UserID: 5}, user)
	v.open()
	v.expectVerified("after a restart")

	s.endLive(0) // OnTooLong
	v.open()
	v.expectChecked("after a difference too long")

	v.read()
	s.endChainsWithoutState(42) // no updates state kept for the account
	v.open()
	v.expectChecked("without an updates state")
}

// A channel's updates skip what came before its pts was raised to its
// dialog's, as after every start and reconnection, so what it verified
// stops being so there, on OnChannelTooLong, and in another run; a
// channel the account is not in, which gets no updates, never is.
func TestChannelChainsEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	peer := &tg.PeerChannel{ChannelID: 77}
	channel := peerRecord{ID: 77, Hash: 1, Kind: "channel"}
	s := openStoreAt(t, path)
	v := newVerifiedChat(t, s, peer, channel)
	v.read()
	v.open()
	v.expectVerified("verified in this chain")

	if e := s.keepChannelPts(context.Background(), 42, map[int64]int{77: 10}); e != nil {
		t.Fatal(e)
	}
	v.open()
	v.expectChecked("after its pts was raised")

	v.read()
	s.Close()
	s = openStoreAt(t, path)
	v = newVerifiedChat(t, s, peer, channel)
	v.open()
	v.expectChecked("in another run")

	v.read()
	s.endLive(v.chat) // OnChannelTooLong
	v.open()
	v.expectChecked("after its difference was too long")

	left := channel
	left.Rights.Left = true
	s.history.peers[v.chat] = left
	v.read()
	v.open()
	v.expectChecked("a channel the account is not in")
}

// A chat read down to its first message is shown down to it from the cache
// after a restart too: the last page, shorter than the others, reaches the
// start, as its verified range running down to 1 tells. The first message
// itself is known only once such a page was read in the run, and that page
// was asked of Telegram again after each restart.
func TestVerifiedStartAfterARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	user := peerRecord{ID: 5, Hash: 1, Kind: "user"}
	first := openStoreAt(t, path)
	v := newVerifiedChatTo(t, first, &tg.PeerUser{UserID: 5}, user, 990)
	v.s.OpenChat(v.chat)
	waitHistory(t, first, v.chat)
	for first.History(v.chat).HasOlder {
		v.older()
	}
	first.Close()

	s := openStoreAt(t, path)
	v = newVerifiedChatTo(t, s, &tg.PeerUser{UserID: 5}, user, 990)
	v.open()
	asked := 0
	for s.History(v.chat).HasOlder {
		asked += v.older()
	}
	h := s.History(v.chat)
	if asked != 0 || h.Messages[0].Key.MessageID != 1 {
		t.Fatalf("scrolled to the start after a restart: %d pages asked of Telegram, the history from %d", asked, h.Messages[0].Key.MessageID)
	}
}
