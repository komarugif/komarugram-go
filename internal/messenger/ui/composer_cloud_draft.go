// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"slices"
	"strings"
	"time"

	"komarugram/internal/messenger/model"
)

// The field of a chat begins with the chat's draft, which Telegram keeps
// for all of the account's devices, and what it holds when the chat is
// left becomes the draft, as Telegram Desktop saves it: the list shows it,
// and the other devices have it.

// cloudDraft is chat's draft as the store has it, nil for none.
func (c *messageComposer) cloudDraft(chat int64) *model.Draft {
	store, ok := c.source.(model.Store)
	if !ok {
		return nil
	}
	for _, ch := range store.Chats() {
		if ch.ID == chat {
			return ch.Draft
		}
	}
	return nil
}

// takeDraft puts chat's draft in its field, when the field is empty and
// the draft is not the one taken already: cleared here, a draft is not
// put back.
func (c *messageComposer) takeDraft(chat int64) {
	cloud := c.cloudDraft(chat)
	if cloud == nil {
		return
	}
	d := c.draft(chat)
	if d.editing != nil || d.sending || strings.TrimSpace(d.editor.Text()) != "" || cloud.Date.Equal(d.cloud) {
		return
	}
	d.cloud = cloud.Date
	d.editor.SetText(cloud.Text)
	// As the field had it already: putting it there is not typing.
	d.text, d.entities = cloud.Text, slices.Clone(cloud.Entities)
}

// keepDraft makes what chat's field holds its draft, and no text none; the
// store asks nothing of Telegram when that is the draft it has. A message
// being edited is not a draft.
func (c *messageComposer) keepDraft(chat int64) {
	store, ok := c.source.(model.DraftStore)
	d := c.drafts[chat]
	if !ok || chat == 0 || d == nil || d.editing != nil || d.sending {
		return
	}
	var draft *model.Draft
	if text := d.editor.Text(); strings.TrimSpace(text) != "" {
		draft = &model.Draft{Text: text, Entities: slices.Clone(d.entities)}
		if d.reply != nil {
			draft.ReplyTo = d.reply.Key.MessageID
		}
	}
	store.SaveDraft(chat, draft)
	if draft != nil {
		// Taken as it is, it is not put back in the field again.
		d.cloud = time.Time{}
		if kept := c.cloudDraft(chat); kept != nil {
			d.cloud = kept.Date
		}
	}
}
