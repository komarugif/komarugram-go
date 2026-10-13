// SPDX-License-Identifier: Unlicense OR MIT

package localization

import (
	"strings"

	"komarugram/internal/messenger/model"
)

// ChatTitle is a chat's name: Saved Messages' and a deleted account's are
// the interface's.
func (c Catalog) ChatTitle(chat model.Chat) string {
	switch {
	case chat.Kind == model.KindSaved:
		return c.T("nav.saved")
	case chat.Title == "" && (chat.Kind == model.KindUser || chat.Kind == model.KindBot):
		return c.T("chat.deleted_account")
	}
	return chat.Title
}

// Preview words the last message of a chat or topic, as its list shows it:
// who wrote it, empty but in groups, and the line itself. chat names the
// chat and channel is set in one, for a service message.
func (c Catalog) Preview(p model.Preview, chat string, channel bool) (sender, text string) {
	sender = p.Sender
	if p.Outgoing {
		sender = c.T("history.you")
	}
	if p.Service != nil {
		if text = c.ServicePreview(*p.Service, chat, channel); text == "" {
			text = c.T("preview.service")
		}
		return "", text
	}
	media := p.Emoji
	switch {
	case p.Media == "preview.sticker" && p.Emoji != "":
		media = c.Format("preview.sticker_emoji", map[string]string{"emoji": p.Emoji})
	case p.Media != "":
		media = strings.TrimSpace(p.Emoji + " " + c.T(p.Media))
	}
	switch {
	case media == "":
		return sender, p.Text
	case p.Text == "":
		return sender, media
	}
	return sender, media + ", " + p.Text
}

// ServicePreview words a service message as a chat list shows it: a pin
// names the message pinned only when it is loaded, which the list's is
// not.
func (c Catalog) ServicePreview(m model.Message, chat string, channel bool) string {
	if m.Service != nil && m.Service.Kind == model.ServicePin {
		return c.PinnedSummary(m, chat)
	}
	return c.Service(m, ServiceContext{Chat: chat, Channel: channel})
}
