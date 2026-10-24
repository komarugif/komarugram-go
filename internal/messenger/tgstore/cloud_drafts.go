// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// A message the account began in a chat and did not send is a draft
// Telegram keeps for all of its devices: the dialogs bring each chat's,
// updateDraftMessage its changes, and messages.saveDraft keeps one. A list
// shows it in place of the last message, as Telegram Desktop's does, and
// orders the chat by when it was saved. Only a chat's own draft is kept
// here, not a thread's.

// draftOf is Telegram's draft as the model's; nil for none.
func draftOf(d tg.DraftMessageClass) *model.Draft {
	draft, ok := d.(*tg.DraftMessage)
	if !ok {
		return nil
	}
	out := &model.Draft{Text: draft.Message, Entities: convertEntities(draft.Entities), Date: time.Unix(int64(draft.Date), 0)}
	if rich, ok := draft.GetRichMessage(); ok && strings.TrimSpace(out.Text) == "" {
		summary := convertRich(rich).Summary()
		out.Text, out.Entities = summary.Text, summary.Entities
	}
	if r, ok := draft.ReplyTo.(*tg.InputReplyToMessage); ok {
		out.ReplyTo = model.MessageID(r.ReplyToMsgID)
	}
	if strings.TrimSpace(out.Text) == "" && out.ReplyTo == 0 {
		return nil
	}
	return out
}

// applyDraft applies an updateDraftMessage: the draft of a chat changed on
// one of the account's devices, this one too.
func (s *Store) applyDraft(u *tg.UpdateDraftMessage) {
	if u.TopMsgID != 0 || u.SavedPeerID != nil {
		return
	}
	s.setDraft(peerID(u.Peer), draftOf(u.Draft))
}

// setDraft keeps d as chat's draft, and reports whether that changed it.
func (s *Store) setDraft(chat int64, d *model.Draft) bool {
	s.mu.Lock()
	i := slices.IndexFunc(s.chats, func(c model.Chat) bool { return c.ID == chat })
	if i < 0 || s.chats[i].Draft.Equal(d) && (d == nil || s.chats[i].Draft.Date.Equal(d.Date)) {
		s.mu.Unlock()
		return false
	}
	chats := slices.Clone(s.chats)
	chats[i].Draft = d
	model.SortChats(chats)
	s.chats = chats
	s.mu.Unlock()
	s.changed()
	return true
}

// draft is chat's draft, nil for none.
func (s *Store) draft(chat int64) *model.Draft {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.chats {
		if c.ID == chat {
			return c.Draft
		}
	}
	return nil
}

// SaveDraft implements model.DraftStore with messages.saveDraft: d, or no
// draft for nil, becomes chat's on all of the account's devices. It asks
// nothing of Telegram when chat has that draft already.
func (s *Store) SaveDraft(chat int64, d *model.Draft) {
	if isThread(chat) {
		return
	}
	if d != nil && strings.TrimSpace(d.Text) == "" && d.ReplyTo == 0 {
		d = nil
	}
	if s.draft(chat).Equal(d) {
		return
	}
	if d != nil {
		saved := *d
		saved.Date = time.Now()
		d = &saved
	}
	c := s.history
	c.mu.Lock()
	peer := c.peers[chat]
	c.mu.Unlock()
	if peer.ID == 0 || !s.setDraft(chat, d) {
		return
	}
	req := &tg.MessagesSaveDraftRequest{Peer: peer.input()}
	if d != nil {
		req.Message, req.Entities = d.Text, inputEntities(d.Entities)
		if d.ReplyTo != 0 {
			req.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: int(d.ReplyTo)})
		}
	}
	s.goSend("draft", func(ctx context.Context, api *tg.Client) error {
		_, err := api.MessagesSaveDraft(ctx, req)
		return err
	})
	go func() {
		defer crash.Recover("draft persist", nil)
		_ = s.persistDialogs(context.Background())
	}()
}

// hasDraft reports whether chat has a draft: a message sent to it clears it
// (clear_draft), as Telegram Desktop's do. A thread's, which is not kept, is
// Telegram's to clear.
func (s *Store) hasDraft(chat int64) bool {
	return !isThread(chat) && s.draft(chat) != nil
}
