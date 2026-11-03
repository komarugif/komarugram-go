// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/pool"
	"github.com/gotd/td/rpc"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"komarugram/internal/messenger/model"
)

func waitOutgoing(t *testing.T, s *Store, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.outbox.mu.Lock()
		got := len(s.outbox.entries)
		s.outbox.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("outbox did not reach expected size", want)
}

func TestOutgoingSurvivesRestartAndOriginalRemoval(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "attachment.txt")
	if err := os.WriteFile(path, []byte("private contents"), 0600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(dir, "history")
	s := New(nil)
	if err := s.Configure(ctx, "a", database, nil); err != nil {
		t.Fatal(err)
	}
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.histories[5] = &model.History{}
	msg := model.OutgoingMessage{RandomID: 91, Text: "caption", ReplyTo: 7, Files: &model.OutgoingFiles{Paths: []string{path}}}
	if err := s.QueueMessage(ctx, 5, msg); err != nil {
		t.Fatal(err)
	}
	before := s.History(5).Messages
	if len(before) != 1 || !before[0].Sending || before[0].Kind != model.MessageFile || before[0].Media.FileName != "attachment.txt" {
		t.Fatalf("pending file: %+v", before)
	}
	if msg.Files.Paths[0] != path {
		t.Fatal("enqueue modified the caller's draft")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s = New(nil)
	if err := s.Configure(ctx, "a", database, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.history.histories[5] = &model.History{}
	after := s.History(5).Messages
	if len(after) != 1 || after[0].Key != before[0].Key || !after[0].Sending {
		t.Fatalf("restored file: %+v", after)
	}
	if b, err := s.Media(ctx, after[0]); err != nil || string(b) != "private contents" {
		t.Fatalf("staged file %q: %v", b, err)
	}
	if got := s.Chats(); len(got) != 1 || got[0].LastSendState() != model.SendSending {
		t.Fatalf("restored dialog: %+v", got)
	}
	server := new(filesServer)
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		if req, ok := in.(*tg.MessagesSendMediaRequest); ok {
			if req.RandomID != 91 || req.ReplyTo.(*tg.InputReplyToMessage).ReplyToMsgID != 7 {
				return nil, fmt.Errorf("request lost its identity or reply: %+v", req)
			}
		}
		return server.handle(in)
	})
	s.startOutgoing()
	waitOutgoing(t, s, 0)
	if len(server.singles) != 1 {
		t.Fatalf("sent %d files", len(server.singles))
	}
	if rows, err := s.Cache().Outgoing(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("completed request retained: %d, %v", len(rows), err)
	}
}

func TestOutgoingRetriesTransportWithSameIdentity(t *testing.T) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	var attempts int
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesSendMessageRequest)
		if !ok {
			return nil, fmt.Errorf("unexpected %T", in)
		}
		attempts++
		if r.RandomID != 123 {
			return nil, errors.New("retry changed identity")
		}
		if attempts < 3 {
			return nil, io.ErrUnexpectedEOF
		}
		return &tg.UpdateShortSentMessage{ID: 9, Date: 10}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Send(ctx, 5, model.OutgoingMessage{RandomID: 123, Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("%d attempts", attempts)
	}
}

func TestOutgoingFailedMessageCanRetryOrCancel(t *testing.T) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.histories[5] = &model.History{}
	var fail atomic.Bool
	fail.Store(true)
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		if _, ok := in.(*tg.MessagesSendMessageRequest); !ok {
			return nil, fmt.Errorf("unexpected %T", in)
		}
		if fail.Load() {
			return nil, tgerr.New(400, "CHAT_WRITE_FORBIDDEN")
		}
		return &tg.UpdateShortSentMessage{ID: 9, Date: 10}, nil
	})
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 123, Text: "hi"}); err == nil {
		t.Fatal("permanent error lost")
	}
	pending := s.History(5).Messages
	if len(pending) != 1 || model.MessageSendState(pending[0], model.KindUser, 0) != model.SendFailed {
		t.Fatalf("failed message: %+v", pending)
	}
	fail.Store(false)
	s.RetryOutgoing(pending[0].Key)
	waitOutgoing(t, s, 0)
	fail.Store(true)
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 124, Text: "again"}); err == nil {
		t.Fatal("permanent error lost")
	}
	all := s.History(5).Messages
	s.CancelOutgoing(all[len(all)-1].Key)
	waitOutgoing(t, s, 0)
	if len(s.History(5).Messages) != 1 {
		t.Fatal("cancel did not remove failed message")
	}
}

func TestOutgoingPartialBatchSkipsAcceptedFile(t *testing.T) {
	s, server := filesStore(t)
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "one.txt"), filepath.Join(dir, "two.txt")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("file"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var sent []int64
	failed := false
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		if r, ok := in.(*tg.MessagesSendMediaRequest); ok {
			sent = append(sent, r.RandomID)
			if r.RandomID == 501 && !failed {
				failed = true
				return nil, io.EOF
			}
			return &tg.UpdateShortSentMessage{ID: int(r.RandomID), Date: 10}, nil
		}
		return server.handle(in)
	})
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 500, Files: &model.OutgoingFiles{Paths: paths}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sent, []int64{500, 501, 501}) {
		t.Fatalf("retried accepted file: %v", sent)
	}
}

func TestOutgoingIdentitySkipsZero(t *testing.T) {
	for _, base := range []int64{-2, -1, 1, 1<<63 - 1} {
		seen := map[int64]bool{}
		for i := range 10 {
			id := outgoingRandomID(base, i)
			if id == 0 || seen[id] {
				t.Fatalf("base %d, index %d: duplicate/zero %d", base, i, id)
			}
			seen[id] = true
		}
	}
}

func TestOutgoingRetriesClosedTelegramConnection(t *testing.T) {
	for _, err := range []error{net.ErrClosed, pool.ErrConnDead, rpc.ErrEngineClosed} {
		if delay, retry := outgoingRetry(fmt.Errorf("send: %w", err), 1); !retry || delay <= 0 {
			t.Errorf("connection loss was permanent: %v", err)
		}
	}
}

func TestOutgoingForwardSurvivesRestartAndRetry(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history")
	s := New(nil)
	t.Cleanup(func() { s.Close() })
	if err := s.Configure(ctx, "a", path, nil); err != nil {
		t.Fatal(err)
	}
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55, Name: "Friend"}
	s.history.peers[6] = peerRecord{Kind: "user", ID: 6, Hash: 66}
	s.history.histories[5] = &model.History{Messages: []model.Message{{Key: model.MessageKey{AccountID: "a", ChatID: 5, MessageID: 10}, Text: "forward me", SenderName: "Friend"}}}
	if err := s.ForwardMessages(ctx, 5, []model.MessageID{10}, 6); err != nil {
		t.Fatal(err)
	}
	s.outbox.mu.Lock()
	identity := s.outbox.entries[0].Request.RandomID
	s.outbox.mu.Unlock()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = New(nil)
	if err := s.Configure(ctx, "a", path, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.history.histories[6] = &model.History{}
	if got := s.History(6).Messages; len(got) != 1 || !got[0].Sending || got[0].Text != "forward me" {
		t.Fatalf("forward not restored: %+v", got)
	}
	calls := 0
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesForwardMessagesRequest)
		if !ok {
			return nil, fmt.Errorf("unexpected %T", in)
		}
		if len(r.RandomID) != 1 || r.RandomID[0] != identity || len(r.ID) != 1 || r.ID[0] != 10 {
			return nil, fmt.Errorf("forward identity changed: %+v", r)
		}
		calls++
		if calls == 1 {
			return nil, io.EOF
		}
		return &tg.Updates{}, nil
	})
	s.startOutgoing()
	waitOutgoing(t, s, 0)
	if calls != 2 {
		t.Fatalf("forward had %d attempts", calls)
	}
}

func TestOutgoingDeliveryPreservesNewerDraft(t *testing.T) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5}
	s.chats = []model.Chat{{ID: 5, Draft: &model.Draft{Text: "old", Date: time.Unix(1, 0)}}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	e, err := s.enqueueOutgoing(ctx, 5, model.OutgoingMessage{RandomID: 123, Text: "old"})
	if err != nil {
		t.Fatal(err)
	}
	s.setDraft(5, &model.Draft{Text: "new draft", Date: time.Unix(2, 0)})
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesSendMessageRequest)
		if !ok {
			return nil, fmt.Errorf("unexpected %T", in)
		}
		if r.ClearDraft {
			return nil, errors.New("delivery would erase a newer draft")
		}
		return &tg.UpdateShortSentMessage{ID: 9, Date: 10}, nil
	})
	s.startOutgoing()
	if err := s.waitOutgoingResult(ctx, e); err != nil {
		t.Fatal(err)
	}
	if draft := s.draft(5); draft == nil || draft.Text != "new draft" {
		t.Fatal("newer draft was cleared", draft)
	}
}

// Waiting for the network or Telegram spends no attempts; a server error and an
// attempt out of time stop in the end.
func TestOutgoingRetryLimits(t *testing.T) {
	const many = 100
	for _, err := range []error{errNotConnected, io.ErrUnexpectedEOF, tgerr.New(420, "FLOOD_WAIT_3")} {
		if _, retry := outgoingRetry(fmt.Errorf("send: %w", err), many); !retry {
			t.Errorf("%v gave up", err)
		}
	}
	for _, err := range []error{tgerr.New(500, "INTERNAL"), context.DeadlineExceeded} {
		if _, retry := outgoingRetry(err, 4); !retry {
			t.Errorf("%v gave up early", err)
		}
		if _, retry := outgoingRetry(err, 5); retry {
			t.Errorf("%v goes on for ever", err)
		}
	}
	if _, retry := outgoingRetry(tgerr.New(400, "PEER_ID_INVALID"), 0); retry {
		t.Error("a refusal is asked again")
	}
}

// A panic in the worker's own loop must not leave the queue without a worker.
func TestOutgoingWorkerRestartsAfterPanic(t *testing.T) {
	var calls atomic.Int32
	calls.Store(-1000)
	s := New(func() {
		// The first after queuing is the end of the delivery, in the worker.
		if calls.Add(1) == 2 {
			panic("injected")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Configure(ctx, "a", filepath.Join(t.TempDir(), "h"), nil); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetLocalPremium(model.AllLocalPremium)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5}
	s.history.histories[5] = &model.History{}
	sticker := func(id int64) model.OutgoingMessage {
		m := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "document/77", Premium: true, PremiumKnown: true}}
		return model.OutgoingMessage{RandomID: id, Item: &model.PickerItem{ID: m.Media.ID, Media: m}}
	}
	calls.Store(0)
	if err := s.Send(ctx, 5, sticker(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(ctx, 5, sticker(2)); err != nil {
		t.Fatalf("after the panic: %v", err)
	}
}

// A sticker that stays on this device does not wait behind a message that needs
// the connection.
func TestOutgoingLocalStickerPassesOffline(t *testing.T) {
	s := testStore(t)
	s.SetLocalPremium(model.AllLocalPremium)
	for _, id := range []int64{5, 6} {
		s.history.peers[id] = peerRecord{Kind: "user", ID: id}
		s.history.histories[id] = &model.History{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.QueueMessage(ctx, 5, model.OutgoingMessage{RandomID: 1, Text: "waits"}); err != nil {
		t.Fatal(err)
	}
	m := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "document/77", Premium: true, PremiumKnown: true}}
	if err := s.Send(ctx, 6, model.OutgoingMessage{RandomID: 2, Item: &model.PickerItem{ID: m.Media.ID, Media: m}}); err != nil {
		t.Fatal(err)
	}
}

// One outgoing message that cannot be read does not keep the account closed.
func TestOutgoingUnreadableRowOpensAccount(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "h")
	s := New(nil)
	if err := s.Configure(ctx, "a", path, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Cache().PutOutgoing(ctx, 1, "not an entry", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Cache().Put(ctx, "local-premium/stickers", "not a list"); err != nil {
		t.Fatal(err)
	}
	if err := s.Cache().Put(ctx, localPinsKey, "not pins"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = New(nil)
	defer s.Close()
	if err := s.Configure(ctx, "a", path, nil); err != nil {
		t.Fatal(err)
	}
}

// A message that failed stays where it was sent, under what came before and over
// what came after; one on its way comes last. Retried, it is on its way again.
func TestFailedMessageKeepsItsPlace(t *testing.T) {
	at := func(sec int64) time.Time { return time.Unix(sec, 0) }
	key := func(id int) model.MessageKey { return model.MessageKey{ChatID: 5, MessageID: model.MessageID(id)} }
	c := newConversation()
	h := &model.History{Messages: []model.Message{{Key: key(10), Date: at(100)}, {Key: key(12), Date: at(300)}}}
	c.histories[5] = h
	failed := &pendingMessage{chat: 5, msg: model.Message{Key: key(-1), Date: at(200), Sending: true, SendError: "CHAT_WRITE_FORBIDDEN"}}
	sending := &pendingMessage{chat: 5, msg: model.Message{Key: key(-2), Date: at(150), Sending: true}}
	c.pending.list = []*pendingMessage{failed, sending}
	ids := func() []model.MessageID {
		var out []model.MessageID
		for _, m := range c.shownMessages(5, h) {
			out = append(out, m.Key.MessageID)
		}
		return out
	}
	if got := ids(); !slices.Equal(got, []model.MessageID{10, -1, 12, -2}) {
		t.Fatalf("shown %v", got)
	}
	// Older than the part loaded, it waits for that part.
	h.HasOlder = true
	h.Messages = h.Messages[1:]
	if got := ids(); !slices.Equal(got, []model.MessageID{12, -2}) {
		t.Fatalf("older than what is loaded: %v", got)
	}
	failed.msg.Date = at(400)
	if got := ids(); !slices.Equal(got, []model.MessageID{12, -1, -2}) {
		t.Fatalf("after what is loaded, at the end of the history: %v", got)
	}
	failed.msg.Date = at(200)
	h.HasOlder, h.HasNewer = false, true
	h.Messages = []model.Message{{Key: key(10), Date: at(100)}}
	if got := ids(); !slices.Equal(got, []model.MessageID{10, -2}) {
		t.Fatalf("newer than what is loaded: %v", got)
	}
	h.HasNewer = false
	failed.msg.SendError = ""
	if got := ids(); !slices.Equal(got, []model.MessageID{10, -1, -2}) {
		t.Fatalf("retried: %v", got)
	}
}
