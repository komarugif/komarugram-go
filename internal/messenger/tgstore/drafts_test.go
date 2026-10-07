// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// draftStore is a store with the chat of a bot that has one message.
func draftStore(t *testing.T) (*Store, int64) {
	t.Helper()
	s := testStore(t)
	bot := int64(77)
	s.history.peers[bot] = peerRecord{Kind: "user", ID: bot, Hash: 1, Name: "Бот"}
	s.history.histories[bot] = &model.History{Revision: 1, Messages: []model.Message{{Key: model.MessageKey{AccountID: "a", ChatID: bot, MessageID: 1}, Text: "до"}}}
	return s, bot
}

// typing applies a typing update of the bot in its chat.
func typing(t *testing.T, s *Store, bot int64, action tg.SendMessageActionClass) {
	t.Helper()
	if err := s.Handle(context.Background(), &tg.UpdateShort{Update: &tg.UpdateUserTyping{UserID: bot, Action: action}}); err != nil {
		t.Fatal(err)
	}
}

// drafts are the drafts the history of chat ends with.
func drafts(t *testing.T, s *Store, chat int64) []model.Message {
	t.Helper()
	h, _ := s.HistorySince(chat, 0)
	var out []model.Message
	for i, m := range h.Messages {
		if m.Streaming {
			if m.Key.MessageID >= 0 || i < len(h.Messages)-1 && !h.Messages[i+1].Streaming {
				t.Fatalf("a draft is message %d, before a message", m.Key.MessageID)
			}
			out = append(out, m)
		}
	}
	return out
}

func textDraft(id int64, text string, canStop, keep bool) *tg.SendMessageTextDraftAction {
	return &tg.SendMessageTextDraftAction{RandomID: id, Text: tg.TextWithEntities{Text: text}, CanStop: canStop, KeepOnStop: keep}
}

// A bot's draft shows at the end of its chat, and grows with its updates;
// the bot's next draft replaces it, and the message it becomes takes its
// place.
func TestStreamedDraftShowsUntilItsMessage(t *testing.T) {
	s, bot := draftStore(t)
	typing(t, s, bot, textDraft(1, "При", true, false))
	d := drafts(t, s, bot)
	if len(d) != 1 || d[0].Text != "При" || !d[0].Stoppable || d[0].SenderID != bot {
		t.Fatalf("drafts %+v", d)
	}
	revision := d[0].ContentRevision
	typing(t, s, bot, textDraft(1, "Привет", true, false))
	if d = drafts(t, s, bot); len(d) != 1 || d[0].Text != "Привет" || d[0].ContentRevision == revision {
		t.Fatalf("grown, drafts %+v", d)
	}
	typing(t, s, bot, textDraft(2, "Второй", false, false))
	if d = drafts(t, s, bot); len(d) != 1 || d[0].Text != "Второй" || d[0].Stoppable {
		t.Fatalf("replaced, drafts %+v", d)
	}
	// The message comes without a sender, as a private chat's may.
	message := &tg.Message{ID: 2, PeerID: &tg.PeerUser{UserID: bot}, Message: "Второй и всё", Date: int(time.Now().Unix())}
	if err := s.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: message}}}); err != nil {
		t.Fatal(err)
	}
	if d = drafts(t, s, bot); len(d) != 0 {
		t.Fatalf("after its message, drafts %+v", d)
	}
}

// A draft the bot stops goes, unless it keeps on stop; what comes for it
// after is dropped. A draft without news goes after half a minute.
func TestStreamedDraftStops(t *testing.T) {
	s, bot := draftStore(t)
	typing(t, s, bot, textDraft(1, "раз", true, false))
	typing(t, s, bot, &tg.SendMessageStopDraftAction{RandomID: 1})
	if d := drafts(t, s, bot); len(d) != 0 {
		t.Fatalf("stopped, drafts %+v", d)
	}
	typing(t, s, bot, textDraft(1, "раз два", true, false))
	if d := drafts(t, s, bot); len(d) != 0 {
		t.Fatalf("news of a stopped draft shows: %+v", d)
	}
	typing(t, s, bot, textDraft(3, "останется", true, true))
	typing(t, s, bot, &tg.SendMessageStopDraftAction{RandomID: 3})
	d := drafts(t, s, bot)
	if len(d) != 1 || d[0].Stoppable {
		t.Fatalf("one that keeps on stop: %+v", d)
	}
	s.history.mu.Lock()
	s.history.drafts.byID[3].updated = time.Now().Add(-draftTimeout)
	s.history.mu.Unlock()
	s.expireDrafts()
	if d := drafts(t, s, bot); len(d) != 0 {
		t.Fatalf("without news, drafts %+v", d)
	}
}

// A rich draft is an article, its text the article's summary.
func TestStreamedRichDraft(t *testing.T) {
	s, bot := draftStore(t)
	rich := tg.RichMessage{Blocks: []tg.PageBlockClass{&tg.PageBlockParagraph{Text: &tg.TextPlain{Text: "Абзац статьи"}}}}
	typing(t, s, bot, &tg.SendMessageRichMessageDraftAction{RandomID: 5, RichMessage: rich})
	d := drafts(t, s, bot)
	if len(d) != 1 || d[0].Rich == nil || len(d[0].Rich.Blocks) != 1 || d[0].Text != "Абзац статьи" {
		t.Fatalf("drafts %+v", d)
	}
}
