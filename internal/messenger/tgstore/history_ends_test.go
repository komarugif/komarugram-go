// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
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
