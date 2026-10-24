// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// serveHistory answers messages.getHistory as Telegram does, over the chat's
// messages first..last: the window that starts add_offset messages from the
// place of offset_id in the newest-first list.
func serveHistory(first, last int) func(context.Context, bin.Encoder, bin.Decoder) error {
	return func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesGetHistoryRequest)
		if !ok {
			return nil
		}
		var newest []int // ids, newest first
		for id := last; id >= first; id-- {
			newest = append(newest, id)
		}
		at := 0 // the place of offset_id: after the messages newer than it
		if req.OffsetID != 0 {
			at = len(newest)
			for i, id := range newest {
				if id < req.OffsetID {
					at = i
					break
				}
			}
		}
		from := max(at+req.AddOffset, 0)
		to := min(from+req.Limit, len(newest))
		var msgs []tg.MessageClass
		if from < to {
			for _, id := range newest[from:to] {
				msgs = append(msgs, &tg.Message{ID: id, PeerID: &tg.PeerUser{UserID: 5}, Message: "m", Date: 10 + id})
			}
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: msgs, Users: []tg.UserClass{&tg.User{ID: 5, AccessHash: 1}}}
		return nil
	}
}

func waitHistory(t *testing.T, s *Store, chat int64) model.History {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		h := s.History(chat)
		if !h.LoadingOlder && !h.LoadingNewer && len(h.Messages) > 0 {
			return h
		}
		if time.Now().After(deadline) {
			t.Fatalf("the history did not load: %+v", h)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRevealFirstAndLastOpenTheEnds(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	s.history.peers[chat] = peerRecord{ID: 5, Hash: 1, Kind: "user"}
	// A private chat's ids do not start at 1.
	s.history.top[chat] = 500 // what the dialog list says
	s.history.api = tg.NewClient(telegram.InvokeFunc(serveHistory(101, 500)))

	s.OpenChat(chat)
	h := waitHistory(t, s, chat)
	if last := h.Messages[len(h.Messages)-1].Key.MessageID; last != 500 || !h.HasOlder {
		t.Fatalf("the chat did not open at its end: %+v", h.Messages[len(h.Messages)-1])
	}

	if !s.RevealFirst(chat) {
		t.Fatal("no reveal")
	}
	s.OpenChat(chat)
	h = waitHistory(t, s, chat)
	if first := h.Messages[0].Key.MessageID; first != 101 || h.HasOlder || !h.HasNewer {
		t.Fatalf("the first message is %d, HasOlder %v, HasNewer %v", first, h.HasOlder, h.HasNewer)
	}
	if v, _ := s.Viewport(chat); v.AtEnd {
		t.Fatalf("the viewport is at the end: %+v", v)
	}

	if !s.RevealLast(chat) {
		t.Fatal("no reveal")
	}
	s.OpenChat(chat)
	h = waitHistory(t, s, chat)
	if last := h.Messages[len(h.Messages)-1].Key.MessageID; last != 500 || h.HasNewer || !h.HasOlder {
		t.Fatalf("the last message is %d, HasOlder %v, HasNewer %v", last, h.HasOlder, h.HasNewer)
	}
	if v, _ := s.Viewport(chat); !v.AtEnd {
		t.Fatalf("the viewport is not at the end: %+v", v)
	}
}

// repliesOf answers messages.getReplies as Telegram does, over the topic's
// replies first..last: see serveHistory.
func repliesOf(topic, first, last int) func(*tg.MessagesGetRepliesRequest) []tg.MessageClass {
	return func(req *tg.MessagesGetRepliesRequest) []tg.MessageClass {
		var newest []int
		for id := last; id >= first; id-- {
			newest = append(newest, id)
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
		from := max(at+req.AddOffset, 0)
		to := min(from+req.Limit, len(newest))
		var msgs []tg.MessageClass
		if from < to {
			for _, id := range newest[from:to] {
				msgs = append(msgs, topicMessage(id, topic, false, fmt.Sprint("m", id)))
			}
		}
		return msgs
	}
}

func waitThread(t *testing.T, s *Store, id int64, cond func(model.History) bool) model.History {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		h := s.History(id)
		if !h.LoadingOlder && !h.LoadingNewer && cond(h) {
			return h
		}
		if time.Now().After(deadline) {
			t.Fatalf("the thread did not load: %d messages, %+v", len(h.Messages), h)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A topic's history is not kept, so a jump to its start replaces it with the
// oldest page, which pages down to the newest, and a jump to the end brings
// the newest back.
func TestRevealTopicEnds(t *testing.T) {
	s, server, chat := forumStore(t)
	server.replyPage = repliesOf(7, 8, 400)
	s.OpenForum(chat)
	thread := openedTopic(t, s, chat, 7)
	ids := func(h model.History) (first, last model.MessageID) {
		return h.Messages[0].Key.MessageID, h.Messages[len(h.Messages)-1].Key.MessageID
	}
	if first, last := ids(s.History(thread.ID)); first != 351 || last != 400 || !s.History(thread.ID).HasOlder {
		t.Fatalf("the topic opened at %d..%d", first, last)
	}

	if !s.RevealFirst(thread.ID) {
		t.Fatal("no jump to the start")
	}
	if h := s.History(thread.ID); len(h.Messages) != 0 || !h.LoadingOlder || h.HasNewer {
		t.Fatalf("the old page was kept while the first comes: %+v", h)
	}
	h := waitThread(t, s, thread.ID, func(h model.History) bool { return len(h.Messages) > 0 })
	if first, last := ids(h); first != 8 || last != 57 || h.HasOlder || !h.HasNewer || h.ThreadRoot != 7 {
		t.Fatalf("the start is %d..%d, HasOlder %v, HasNewer %v, root %d", first, last, h.HasOlder, h.HasNewer, h.ThreadRoot)
	}

	// Paged down to the end.
	for i := 0; i < 20 && s.History(thread.ID).HasNewer; i++ {
		s.LoadNewer(thread.ID)
		waitThread(t, s, thread.ID, func(model.History) bool { return true })
	}
	h = s.History(thread.ID)
	if first, last := ids(h); first != 8 || last != 400 || h.HasNewer || len(h.Messages) != 393 {
		t.Fatalf("paged down to %d..%d of %d, HasNewer %v", first, last, len(h.Messages), h.HasNewer)
	}

	// Back to the newest, from the start.
	s.RevealFirst(thread.ID)
	waitThread(t, s, thread.ID, func(h model.History) bool { return len(h.Messages) > 0 })
	if !s.RevealLast(thread.ID) {
		t.Fatal("no jump to the end")
	}
	h = waitThread(t, s, thread.ID, func(h model.History) bool { return len(h.Messages) > 0 })
	if first, last := ids(h); first != 351 || last != 400 || h.HasNewer || !h.HasOlder {
		t.Fatalf("the end is %d..%d, HasOlder %v, HasNewer %v", first, last, h.HasOlder, h.HasNewer)
	}
}

// A chat the account has no dialog of, as a channel opened from a search,
// opened at a message in its past has newer messages to load: its newest
// message is learnt, which no dialog tells. It had none, and stopped there.
func TestRevealWithoutDialogHasNewer(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	s.history.peers[chat] = peerRecord{ID: 5, Hash: 1, Kind: "user"}
	s.history.api = tg.NewClient(telegram.InvokeFunc(serveHistory(101, 500)))
	s.Reveal(chat, 200)
	s.OpenChat(chat)
	h := waitHistory(t, s, chat)
	if last := h.Messages[len(h.Messages)-1].Key.MessageID; last >= 500 || !h.HasNewer {
		t.Fatalf("opened at 200: the last message %d, HasNewer %v", last, h.HasNewer)
	}
	s.Reveal(chat, 495)
	s.OpenChat(chat)
	h = waitHistory(t, s, chat)
	if last := h.Messages[len(h.Messages)-1].Key.MessageID; last != 500 || h.HasNewer {
		t.Fatalf("opened at 495: the last message %d, HasNewer %v", last, h.HasNewer)
	}
}

// A chat not opened here before, with messages not read, opens at the first
// of them: opened at its end it was read whole, and the account's other
// devices lost where it stopped reading. Without unread messages, or once it
// was opened here, it opens where it was.
func TestUnreadChatOpensAtFirstUnread(t *testing.T) {
	s := testStore(t)
	chat := peerID(&tg.PeerUser{UserID: 5})
	s.history.peers[chat] = peerRecord{ID: 5, Hash: 1, Kind: "user"}
	s.history.top[chat] = 500
	s.history.api = tg.NewClient(telegram.InvokeFunc(serveHistory(101, 500)))
	s.chats = []model.Chat{{ID: chat, Unread: 20, ReadInbox: 480}}

	// forget leaves the chat as one not opened here.
	forget := func() {
		c := s.history
		c.mu.Lock()
		c.epochs[chat]++
		delete(c.histories, chat)
		delete(c.views, chat)
		c.mu.Unlock()
	}
	s.OpenChat(chat)
	h := waitHistory(t, s, chat)
	v, ok := s.Viewport(chat)
	if !ok || v.AtEnd || v.AnchorMessageID != 481 {
		t.Fatalf("the chat opened at %+v (%t), want its first unread message, 481", v, ok)
	}
	if !slices.ContainsFunc(h.Messages, func(m model.Message) bool { return m.Key.MessageID == 481 }) {
		t.Fatalf("the history %d..%d lacks the first unread message", h.Messages[0].Key.MessageID, h.Messages[len(h.Messages)-1].Key.MessageID)
	}

	// Dialogs kept before they told what was read: Telegram is asked.
	forget()
	s.chats = []model.Chat{{ID: chat, Unread: 30}}
	history := serveHistory(101, 500)
	s.history.api = tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		if _, ok := in.(*tg.MessagesGetPeerDialogsRequest); ok {
			*out.(*tg.MessagesPeerDialogs) = tg.MessagesPeerDialogs{Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerUser{UserID: 5}, ReadInboxMaxID: 470, UnreadCount: 30}}, State: tg.UpdatesState{}}
			return nil
		}
		return history(ctx, in, out)
	}))
	s.OpenChat(chat)
	waitHistory(t, s, chat)
	if v, _ := s.Viewport(chat); v.AnchorMessageID != 471 || v.AtEnd {
		t.Fatalf("a chat whose dialog lacked what was read opened at %+v, want 471", v)
	}
	if s.Chats()[0].ReadInbox != 470 {
		t.Fatalf("what was read is not kept: %+v", s.Chats()[0])
	}

	// Read to its end, it opens at its end.
	forget()
	s.chats = []model.Chat{{ID: chat, ReadInbox: 500}}
	s.OpenChat(chat)
	waitHistory(t, s, chat)
	if v, ok := s.Viewport(chat); ok && !v.AtEnd {
		t.Fatalf("a chat read whole opened at %+v", v)
	}
}

// A chat left at its end that has unread messages now opens at the first of
// them, as one not opened here does: its history loaded again, or the one
// kept. The last message of the chat, all the cache has, is not shown at
// the end meanwhile, to jump from to the first unread when the page comes.
func TestUnreadChatLeftAtEndOpensAtFirstUnread(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	chat := peerID(&tg.PeerUser{UserID: 5})
	s.history.peers[chat] = peerRecord{ID: 5, Hash: 1, Kind: "user"}
	s.history.top[chat] = 500
	gate := make(chan struct{})
	// Opened on a failure too: the store waits for its pages to close.
	release := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release)
	history := serveHistory(101, 500)
	s.history.api = tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		<-gate
		return history(ctx, in, out)
	}))
	s.chats = []model.Chat{{ID: chat, Unread: 20, ReadInbox: 480}}
	if err := s.history.cache.Put(ctx, fmt.Sprintf("viewport/%d", chat), model.Viewport{ChatID: chat, AnchorMessageID: 450, AtEnd: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ingest(ctx, []tg.MessageClass{&tg.Message{ID: 500, PeerID: &tg.PeerUser{UserID: 5}, Message: "last", Date: 510}}, false, 0); err != nil {
		t.Fatal(err)
	}

	s.OpenChat(chat)
	deadline := time.Now().Add(3 * time.Second)
	for s.History(chat).Revision == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the cache was not read")
		}
		time.Sleep(time.Millisecond)
	}
	if h := s.History(chat); len(h.Messages) > 0 {
		t.Fatalf("shown at the end before the first unread came: %d messages, the last %d", len(h.Messages), h.Messages[len(h.Messages)-1].Key.MessageID)
	}
	release()
	waitHistory(t, s, chat)
	if v, _ := s.Viewport(chat); v.AtEnd || v.AnchorMessageID != 481 {
		t.Fatalf("a chat left at its end opened at %+v, want 481", v)
	}

	// Kept, and left at its end again while more came.
	atEnd := func() {
		s.history.mu.Lock()
		s.history.views[chat] = model.Viewport{ChatID: chat, AnchorMessageID: 470, AtEnd: true}
		s.history.mu.Unlock()
	}
	atEnd()
	s.chats = []model.Chat{{ID: chat, Unread: 5, ReadInbox: 495}}
	s.OpenUnread(chat)
	if v, _ := s.Viewport(chat); v.AtEnd || v.AnchorMessageID != 496 || s.History(chat).LoadingOlder {
		t.Fatalf("the kept history opened at %+v, want 496 without loading again", v)
	}
	// Elsewhere it stays.
	s.chats = []model.Chat{{ID: chat, Unread: 3, ReadInbox: 497}}
	s.OpenUnread(chat)
	if v, _ := s.Viewport(chat); v.AnchorMessageID != 496 {
		t.Fatalf("a history left in its middle moved to %+v", v)
	}
	// The first unread beyond what is kept: loaded again around it.
	atEnd()
	s.chats = []model.Chat{{ID: chat, Unread: 300, ReadInbox: 200}}
	s.OpenUnread(chat)
	s.OpenChat(chat)
	h := waitHistory(t, s, chat)
	if v, _ := s.Viewport(chat); v.AnchorMessageID != 201 || !slices.ContainsFunc(h.Messages, func(m model.Message) bool { return m.Key.MessageID == 201 }) {
		t.Fatalf("opened at %+v, the history %d..%d", v, h.Messages[0].Key.MessageID, h.Messages[len(h.Messages)-1].Key.MessageID)
	}
}

// A chat whose newest message was deleted while the client was closed, the
// newest its dialogs still tell, ends at the newest there is once a page
// reaches it: waiting for the one gone, its history never ended, and read
// to its end it never read the chat. A message that comes while the page
// is asked for stays the newest.
func TestGoneTopEndsTheHistory(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	s.history.peers[chat] = peerRecord{ID: 5, Hash: 1, Kind: "user"}
	s.history.top[chat] = 505
	s.history.api = tg.NewClient(telegram.InvokeFunc(serveHistory(101, 500)))
	s.OpenChat(chat)
	h := waitHistory(t, s, chat)
	if h.HasNewer || s.history.top[chat] != 500 {
		t.Fatalf("opened at its end: HasNewer %v, the newest %d, want 500", h.HasNewer, s.history.top[chat])
	}

	// Opened in its past, and read down to its end.
	s.history.top[chat] = 505
	s.reveal(chat, 300, false)
	s.OpenChat(chat)
	waitHistory(t, s, chat)
	for i := 0; i < 10 && s.History(chat).HasNewer; i++ {
		s.LoadNewer(chat)
		waitHistory(t, s, chat)
	}
	if h := s.History(chat); h.HasNewer || h.Messages[len(h.Messages)-1].Key.MessageID != 500 {
		t.Fatalf("read down: HasNewer %v, the last %d", h.HasNewer, h.Messages[len(h.Messages)-1].Key.MessageID)
	}

	// The page was asked for before 506 came.
	s.history.top[chat] = 505
	s.endTop(chat, nil, 1, 500, 80, 504)
	if s.history.top[chat] != 505 {
		t.Fatalf("a message newer than the page asked for was dropped: the newest %d", s.history.top[chat])
	}
}
