// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// archiveChat is a chat of messages 1..650 that tells its count to a search
// for "-", as Telegram does.
func archiveChat(t *testing.T) (*Store, *verifiedChat, *atomic.Int32) {
	t.Helper()
	s := testStore(t)
	v := newVerifiedChatTo(t, s, &tg.PeerUser{UserID: 5}, peerRecord{ID: 5, Hash: 1, Kind: "user"}, 650)
	var searched atomic.Int32
	s.history.api = tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		if req, ok := in.(*tg.MessagesSearchRequest); ok {
			if req.Q != "-" {
				t.Errorf("the count was asked with %q", req.Q)
			}
			searched.Add(1)
			out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessagesSlice{Count: 650}
			return nil
		}
		return v.invoke(ctx, in, out)
	}))
	return s, v, &searched
}

// Saving a chat's messages puts all of them in the cache, with their span,
// by pages that go through the history's pacer, and changes neither the
// history on screen (nothing is opened) nor the room it reserves: the
// reserve of the rows after a message already seen is the same, and the
// measurer is brought no row of the run saved to lay out. A second run
// asks for the newest page alone, and jumps over what the cache holds
// whole.
func TestArchiveChatSavesAllMessagesToTheCache(t *testing.T) {
	s, v, searched := archiveChat(t)
	var slept atomic.Int32
	s.history.pace.sleep = func(context.Context, time.Duration) error { slept.Add(1); return nil }
	if n, err := s.MessageTotal(context.Background(), v.chat); err != nil || n != 650 || searched.Load() != 1 {
		t.Fatalf("total %d, %v, after %d searches", n, err, searched.Load())
	}
	// The chat was opened at its newest 100 messages.
	var seen []model.Message
	for id := 551; id <= 650; id++ {
		seen = append(seen, model.Message{Key: model.MessageKey{AccountID: "a", ChatID: v.chat, MessageID: model.MessageID(id)}, Text: "m"})
	}
	if err := s.history.cache.SaveMessages(context.Background(), seen); err != nil {
		t.Fatal(err)
	}
	if _, err := s.history.cache.AddSpan(context.Background(), v.chat, 551, 650); err != nil {
		t.Fatal(err)
	}
	reserve := func() int {
		rows, err := s.history.cache.Reserve(context.Background(), v.chat, 551, model.RenderEnvironment{}, 1, 20000)
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}
	before := reserve()
	var last atomic.Value
	if err := s.ArchiveChat(context.Background(), v.chat, func(p model.ArchiveProgress) { last.Store(p) }); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CachedMessages(context.Background(), v.chat); err != nil || n != 650 {
		t.Fatalf("%d cached, %v", n, err)
	}
	span, ok, _, err := s.history.cache.SpanOf(context.Background(), v.chat, 0)
	if err != nil || !ok || span.Low != 1 || span.High != 650 {
		t.Fatalf("span %+v %v %v, want 1..650", span, ok, err)
	}
	// The rows the user saw are the measurer's as before; of the run saved
	// it is brought the row it stands on alone.
	if n := len(s.CachedFrom(v.chat, 600, 64)); n != 51 {
		t.Fatalf("the measurer reads %d messages from 600, want 51", n)
	}
	if n := len(s.CachedFrom(v.chat, 400, 64)); n != 1 {
		t.Fatalf("the measurer reads %d messages from 400, in the run saved, want 1", n)
	}
	if n := reserve(); n != before {
		t.Fatalf("the room reserved after message 551 went from %d rows to %d", before, n)
	}
	if p := last.Load().(model.ArchiveProgress); p.Cached != 650 || p.Total != 650 || p.Share() != 1 {
		t.Fatalf("last progress %+v", p)
	}
	if n := v.asked.Load(); n != 7 {
		t.Fatalf("%d pages asked, want 7: 650 messages in pages of 100, the newest of them asked for whatever the span holds", n)
	}
	if n := s.history.pace.used(); n != 7 {
		t.Fatalf("%d pages went through the pacer, want 7", n)
	}
	if h := s.history.histories[v.chat]; h != nil {
		t.Fatalf("the saving made a history of %d messages", len(h.Messages))
	}
	asked := v.asked.Load()
	if err := s.ArchiveChat(context.Background(), v.chat, func(model.ArchiveProgress) {}); err != nil {
		t.Fatal(err)
	}
	if n := v.asked.Load() - asked; n != 1 {
		t.Fatalf("a second run asked %d pages, want the newest alone", n)
	}
}

// A saving that is cancelled keeps what it saved, and goes on from it.
func TestArchiveChatCancelsAndGoesOn(t *testing.T) {
	s, v, _ := archiveChat(t)
	s.history.pace.sleep = func(context.Context, time.Duration) error { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pages := 0
	err := s.ArchiveChat(ctx, v.chat, func(p model.ArchiveProgress) {
		if p.Cached > 0 {
			if pages++; pages == 3 {
				cancel()
			}
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled saving ended with %v", err)
	}
	kept, _ := s.CachedMessages(context.Background(), v.chat)
	if kept < 200 || kept >= 650 {
		t.Fatalf("%d messages kept, want a part", kept)
	}
	before := v.asked.Load()
	if err := s.ArchiveChat(context.Background(), v.chat, func(model.ArchiveProgress) {}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CachedMessages(context.Background(), v.chat); n != 650 {
		t.Fatalf("%d cached after the second run", n)
	}
	if n := v.asked.Load() - before; n > 5 {
		t.Fatalf("the second run asked %d pages, it should not ask for what was kept", n)
	}
}

// What a saving put in the cache is the history's offline: scrolled up
// from the chat's end, without Telegram, it comes whole to the first
// message.
func TestArchivedChatScrollsOffline(t *testing.T) {
	s, v, _ := archiveChat(t)
	s.history.pace.sleep = func(context.Context, time.Duration) error { return nil }
	if err := s.ArchiveChat(context.Background(), v.chat, func(model.ArchiveProgress) {}); err != nil {
		t.Fatal(err)
	}
	s.history.mu.Lock()
	s.history.api = nil
	s.history.mu.Unlock()
	s.reveal(v.chat, 0, true)
	s.OpenChat(v.chat)
	h := waitHistory(t, s, v.chat)
	for i := 0; i < 30 && h.Messages[0].Key.MessageID > 1; i++ {
		s.SaveView(model.Viewport{ChatID: v.chat, AnchorMessageID: h.Messages[0].Key.MessageID}, nil)
		s.LoadOlder(v.chat)
		h = waitHistory(t, s, v.chat)
	}
	// The history holds a window of 600 messages, the newest let go of as
	// the older come: whole, from the first on.
	if h.Messages[0].Key.MessageID != 1 || len(h.Messages) != historyWindow || h.Err != nil {
		t.Fatalf("offline, the history comes from %d with %d messages (%v), want 1 and %d", h.Messages[0].Key.MessageID, len(h.Messages), h.Err, historyWindow)
	}
	for i, m := range h.Messages {
		if int(m.Key.MessageID) != i+1 {
			t.Fatalf("message %d of the window is %d, want %d: it has a gap", i, m.Key.MessageID, i+1)
		}
	}
}

// The media a saving brings are located in the cache, not in memory: a
// chat of a million messages would fill it. They are found there when asked
// for.
func TestArchiveChatKeepsMediaLocationsOutOfMemory(t *testing.T) {
	s, v, _ := archiveChat(t)
	s.history.pace.sleep = func(context.Context, time.Duration) error { return nil }
	inner := s.history.api
	_ = inner
	s.history.api = tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		if req, ok := in.(*tg.MessagesSearchRequest); ok {
			_ = req
			out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessagesSlice{Count: 650}
			return nil
		}
		if err := v.invoke(ctx, in, out); err != nil {
			return err
		}
		if page, ok := out.(*tg.MessagesMessagesBox).Messages.(*tg.MessagesMessages); ok {
			for _, m := range page.Messages {
				m := m.(*tg.Message)
				m.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: int64(m.ID), AccessHash: 2, FileReference: []byte{1}, Date: 1, DCID: 2,
					Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", W: 100, H: 100, Size: 1000}}}}
			}
		}
		return nil
	}))
	before := len(s.history.refs)
	if err := s.ArchiveChat(context.Background(), v.chat, func(model.ArchiveProgress) {}); err != nil {
		t.Fatal(err)
	}
	if n := len(s.history.refs); n != before {
		t.Fatalf("%d locations of media in memory after the saving, %d before", n, before)
	}
	keys, err := s.history.cache.KeysWithPrefix(context.Background(), "ref/")
	if err != nil || len(keys) < 650 {
		t.Fatalf("%d locations in the cache (%v), want one for each of 650 photos", len(keys), err)
	}
}
