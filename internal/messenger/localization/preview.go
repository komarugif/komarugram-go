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
	sender, media, text := c.PreviewLine(p, chat, channel)
	switch {
	case media == "":
		return sender, text
	case text == "":
		return sender, media
	}
	return sender, media + ", " + text
}

// PreviewLine is Preview in parts: who wrote the message; the interface's
// words for what it is or carries, which Telegram Desktop's list draws in
// the color of links, a service message's all of them; and the message's
// own text, which p.Entities format. With thumbnails a message with text
// leaves out the words of its media, which they show, and an album shows
// its caption alone, or what it holds when it has none, or several.
func (c Catalog) PreviewLine(p model.Preview, chat string, channel bool) (sender, media, text string) {
	sender = p.Sender
	if p.Outgoing {
		sender = c.T("history.you")
	}
	if p.Service != nil {
		if media = c.ServicePreview(*p.Service, chat, channel); media == "" {
			media = c.T("preview.service")
		}
		return "", media, ""
	}
	if a := p.Album; a != nil {
		if a.Captions == 1 && p.Text != "" {
			return sender, "", p.Text
		}
		return sender, c.albumWords(*a), ""
	}
	if p.Text != "" && len(p.Thumbs) > 0 {
		return sender, "", p.Text
	}
	if p.Title != "" {
		// A poll's question or a checklist's title, after its icon.
		return sender, p.Title, p.Text
	}
	media = p.Emoji
	if p.Media != "" {
		// A sticker's emoji before the word, as the official clients show
		// it, whatever a pack's lng_in_dlg_sticker_emoji puts first.
		media = strings.TrimSpace(p.Emoji + " " + c.T(p.Media))
	}
	return sender, media, p.Text
}

// albumWords say what an album holds, as Telegram Desktop's
// ComputeAlbumCountsString does.
func (c Catalog) albumWords(a model.PreviewAlbum) string {
	switch {
	case a.Photos > 0 && a.Videos > 0:
		return c.Count("preview.media_count", a.Photos+a.Videos, nil)
	case a.Photos > 1:
		return c.Count("preview.photos", a.Photos, nil)
	case a.Photos == 1:
		return c.T("preview.photo")
	case a.Videos > 1:
		return c.Count("preview.videos", a.Videos, nil)
	case a.Videos == 1:
		return c.T("preview.video")
	case a.Audios > 1:
		return c.Count("preview.audios", a.Audios, nil)
	case a.Audios == 1:
		return c.T("preview.audio")
	case a.Files > 1:
		return c.Count("preview.files", a.Files, nil)
	case a.Files == 1:
		return c.T("preview.file")
	}
	return c.T("preview.album")
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
