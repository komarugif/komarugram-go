// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

// An edit sends the formatting with the text, and the message Telegram
// answers with is in the history and in the cache.
func TestEditMessageSendsEntitiesAndKeepsTheEdit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	chat := int64(5)
	s.history.peers[chat] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.histories[chat] = &model.History{}
	m := &tg.Message{ID: 17, Out: true, PeerID: &tg.PeerUser{UserID: 5}, Message: "before", Date: 10}
	if _, e := s.ingest(ctx, []tg.MessageClass{m}, false, 0); e != nil {
		t.Fatal(e)
	}
	var got *tg.MessagesEditMessageRequest
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesEditMessageRequest)
		if !ok {
			return nil, fmt.Errorf("unexpected %T", in)
		}
		got = r
		edited := *m
		edited.Message, edited.Entities, edited.EditDate = r.Message, r.Entities, 11
		return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateEditMessage{Message: &edited}}}, nil
	})
	entities := []model.Entity{
		{Kind: model.EntityItalic, Offset: 6, Length: 4},
		{Kind: model.EntityBold, Offset: 0, Length: 5},
		{Kind: model.EntityURL, Offset: 6, Length: 4, URL: "https://telegram.org"},
		{Kind: model.EntityEmoji, Offset: 11, Length: 2, DocumentID: 77},
	}
	key := model.MessageKey{AccountID: "a", ChatID: chat, MessageID: 17}
	if err := s.EditMessage(ctx, key, "after with 🙂", entities); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != 17 || got.Message != "after with 🙂" || len(got.Entities) != 4 {
		t.Fatalf("request %+v", got)
	}
	if b, ok := got.Entities[0].(*tg.MessageEntityBold); !ok || b.Offset != 0 || b.Length != 5 {
		t.Fatalf("entities not in order: %#v", got.Entities)
	}
	if u, ok := got.Entities[2].(*tg.MessageEntityTextURL); !ok || u.URL != "https://telegram.org" {
		t.Fatalf("link lost: %#v", got.Entities)
	}
	h := s.History(chat)
	if len(h.Messages) != 1 || h.Messages[0].Text != "after with 🙂" || h.Messages[0].EditedAt.IsZero() || len(h.Messages[0].Entities) != 4 {
		t.Fatalf("history %+v", h.Messages)
	}
	cached, e := s.Cache().Around(ctx, chat, 0, 10)
	if e != nil || len(cached) != 1 || cached[0].Text != "after with 🙂" {
		t.Fatal("cache kept the old text", cached, e)
	}
}

func TestEditMessageErrors(t *testing.T) {
	for name, c := range map[string]struct {
		tg   string
		want error
	}{
		"not modified": {"MESSAGE_NOT_MODIFIED", model.ErrMessageNotModified},
		"too old":      {"MESSAGE_EDIT_TIME_EXPIRED", model.ErrEditForbidden},
		"gone":         {"MESSAGE_ID_INVALID", model.ErrEditForbidden},
		"no rights":    {"CHAT_ADMIN_REQUIRED", model.ErrEditForbidden},
		"empty":        {"MESSAGE_EMPTY", model.ErrMessageEmpty},
	} {
		s := testStore(t)
		s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
		s.history.api = composerAPI(func(bin.Encoder) (bin.Encoder, error) { return nil, tgerr.New(400, c.tg) })
		err := s.EditMessage(context.Background(), model.MessageKey{ChatID: 5, MessageID: 1}, "x", nil)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestEditTimeLimitFromConfig(t *testing.T) {
	s := testStore(t)
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		if _, ok := in.(*tg.HelpGetConfigRequest); !ok {
			return nil, fmt.Errorf("unexpected %T", in)
		}
		return &tg.Config{EditTimeLimit: 3600}, nil
	})
	if got := s.EditTimeLimit(); got != model.DefaultEditTimeLimit {
		t.Fatal("before the config:", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.EditTimeLimit() != time.Hour {
		if time.Now().After(deadline) {
			t.Fatal("config's limit not read")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
