// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/gotd/td/telegram/thumbnail"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// What a chat list shows of a last message, as Telegram Desktop's
// (Dialogs::Ui::MessageView, HistoryItem::toPreview): the text on one line
// with some of its formatting, and small pictures of the media before it.

// previewThumbPx is the side, in pixels, the smallest size of a photo a
// preview's thumbnail downloads covers: the list's square at three pixels
// to a dp.
const previewThumbPx = 48

// Telegram's service notifications (777000) and the chat of verification
// codes (489000) send codes to sign in with, which Telegram Desktop's list
// hides under a spoiler: a list shown to someone else does not give them
// away.
const (
	serviceNotifications = 777000
	verificationCodes    = 489000
)

// spoilerLoginCode puts the first code of minLength to 8 digits, which
// dashes may part, of text under a spoiler, as Telegram Desktop's
// SpoilerLoginCode does: a code not inside a word, and not where an entity
// begins before it and goes on into it.
func spoilerLoginCode(text string, entities []model.Entity, minLength int) []model.Entity {
	word := func(r rune) bool {
		return r == '_' || r == '-' || r == '#' || unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	isDigit := func(b byte) bool { return b >= '0' && b <= '9' }
	for i := 0; i < len(text); i++ {
		if !isDigit(text[i]) {
			continue
		}
		if before, _ := utf8.DecodeLastRuneInString(text[:i]); i > 0 && word(before) {
			continue
		}
		end := i
		for end < len(text) && (isDigit(text[end]) || text[end] == '-') {
			end++
		}
		code := strings.TrimRight(text[i:end], "-")
		after, _ := utf8.DecodeRuneInString(text[i+len(code):])
		if len(code) < minLength || len(code) > 8 || i+len(code) < len(text) && (unicode.IsLetter(after) || after == '_') {
			i = end
			continue
		}
		// Digits and dashes are one UTF-16 unit each.
		offset := utf16Units(text[:i])
		for _, e := range entities {
			if e.Offset < offset && e.Offset+e.Length > offset {
				return entities
			}
		}
		spoiler := model.Entity{Kind: "spoiler", Offset: offset, Length: len(code)}
		at, _ := slices.BinarySearchFunc(entities, offset, func(e model.Entity, offset int) int { return e.Offset - offset })
		return slices.Insert(slices.Clone(entities), at, spoiler)
	}
	return entities
}

func utf16Units(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// previewThumb is the small picture of what m carries, as Telegram
// Desktop's list draws before the text: of a photo, a video, a GIF, a
// video message, a file with a thumbnail, a link's photo or video.
func previewThumb(m model.Message) (model.PreviewThumb, bool) {
	t := model.PreviewThumb{Message: m.Key.MessageID, Spoiler: m.MediaSpoiler}
	var media *model.MessageMedia
	switch m.Kind {
	case model.MessagePhoto:
		if m.Media != nil {
			media = m.Media.Variant(previewThumbPx, previewThumbPx)
			t.Blurred = m.Media.Preview
		}
	case model.MessageVideo, model.MessageRound, model.MessageGIF, model.MessageFile:
		if m.Media != nil {
			media = m.Media.Thumbnail
		}
		t.Video, t.Round = m.Kind == model.MessageVideo || m.Kind == model.MessageRound, m.Kind == model.MessageRound
	case model.MessageText:
		if p := m.WebPage; p != nil && (p.Type == "photo" || p.Type == "video" || p.Type == "document" || p.Video != nil) {
			switch {
			case p.Video != nil:
				media, t.Video = p.Video.Thumbnail, p.VideoKind == model.MessageVideo
			case p.Photo != nil:
				media, t.Blurred = p.Photo.Variant(previewThumbPx, previewThumbPx), p.Photo.Preview
			}
		}
	}
	if media == nil {
		return model.PreviewThumb{}, false
	}
	if t.Blurred == nil {
		t.Blurred = media.Preview
	}
	if strings.HasSuffix(media.ID, "/inline") {
		// Only its inline picture came: there is nothing to download.
		if t.Blurred == nil {
			return model.PreviewThumb{}, false
		}
		t.Spoiler = true
	}
	t.ID = media.ID
	// Drawn in the picture's place, which downloads only without it; kept
	// stripped, as Telegram sends it, for the dialogs the cache keeps.
	t.Blurred = stripThumb(t.Blurred)
	return t, true
}

// previewBlurredMost is the size of the most of a blurred picture a preview
// keeps that is not a stripped one: Telegram's cached sizes are full JPEGs.
const previewBlurredMost = 2 << 10

// strippedJPEG is what Telegram's stripped thumbnails are expanded with
// around their bytes, its two of the size zero.
var strippedJPEG, _ = thumbnail.Expand([]byte{1, 0, 0})

// stripThumb is jpeg, a stripped thumbnail expanded, stripped again: the
// bytes Telegram sent, three bytes and the scan, without the six hundred
// of the header every one of them shares. Another JPEG stays as it is, or
// is left out over previewBlurredMost.
func stripThumb(jpeg []byte) []byte {
	const height, width = 164, 166 // where Expand writes the size
	head, foot := strippedJPEG[:len(strippedJPEG)-2], strippedJPEG[len(strippedJPEG)-2:]
	if len(jpeg) < len(strippedJPEG) || !bytes.HasSuffix(jpeg, foot) ||
		!bytes.Equal(jpeg[:height], head[:height]) || jpeg[height+1] != head[height+1] || !bytes.Equal(jpeg[width+1:len(head)], head[width+1:]) {
		if len(jpeg) > previewBlurredMost {
			return nil
		}
		return jpeg
	}
	out := make([]byte, 0, 3+len(jpeg)-len(strippedJPEG))
	out = append(out, 1, jpeg[height], jpeg[width])
	return append(out, jpeg[len(head):len(jpeg)-len(foot)]...)
}

// albumCounts counts m in an album as Telegram Desktop's does.
func albumCounts(a *model.PreviewAlbum, m model.Message) {
	switch m.Kind {
	case model.MessagePhoto:
		a.Photos++
	case model.MessageVideo, model.MessageGIF, model.MessageRound:
		a.Videos++
	case model.MessageMusic, model.MessageVoice:
		a.Audios++
	default:
		a.Files++
	}
}

// joinAlbum is p, the preview of an album, with m, a message of it, new or
// edited: a new one is counted, and its thumbnail joins the first three.
func joinAlbum(p model.Preview, m model.Message, group bool) model.Preview {
	item := messagePreview(m, group)
	a := *p.Album
	thumbs := slices.Clone(p.Thumbs)
	switch {
	case m.Key.MessageID > a.Last:
		albumCounts(&a, m)
		a.Last = m.Key.MessageID
		if len(thumbs) < 3 {
			thumbs = append(thumbs, item.Thumbs...)
		}
		if item.Text != "" {
			a.Captions++
			if a.Captions == 1 {
				p.Text, p.Entities, a.Caption = item.Text, item.Entities, m.Key.MessageID
			}
		}
		p.ID, p.Sent, p.Forwarded = item.ID, item.Sent, item.Forwarded
	case m.Key.MessageID == a.Caption:
		p.Text, p.Entities = item.Text, item.Entities
		if item.Text == "" {
			a.Captions, a.Caption = max(a.Captions-1, 0), 0
		}
	}
	// An edit may change the media.
	for i, t := range thumbs {
		if t.Message == m.Key.MessageID && len(item.Thumbs) > 0 {
			thumbs[i] = item.Thumbs[0]
		}
	}
	p.Thumbs, p.Album = thumbs, &a
	return p
}

// lastAlbum is the last message of msgs not kept deleted, with the
// messages of its album before it: what the chat's preview is of. It is
// nil without one, and a copy of msgs.
func lastAlbum(msgs []model.Message) []model.Message {
	end := len(msgs)
	for end > 0 && msgs[end-1].Deleted {
		end--
	}
	if end == 0 {
		return nil
	}
	start := end - 1
	if group := msgs[start].GroupedID; group != 0 {
		for start > 0 && !msgs[start-1].Deleted && msgs[start-1].GroupedID == group {
			start--
		}
	}
	return slices.Clone(msgs[start:end])
}

// albumPreview is the preview of items, a message or an album's messages
// in their order.
func albumPreview(items []model.Message, group bool) model.Preview {
	p := messagePreview(items[0], group)
	for _, m := range items[1:] {
		if p.Album == nil || m.GroupedID != p.Album.ID {
			p = messagePreview(m, group)
			continue
		}
		p = joinAlbum(p, m, group)
	}
	return p
}

// updatePreview shows m, a message that came or was edited, as the chat's
// last one: the next message of the album the preview is of joins it, and
// an older message edited leaves it as it is.
func updatePreview(chat *model.Chat, m model.Message) {
	last := chat.Last
	if a := last.Album; a != nil && m.GroupedID == a.ID && m.Kind != model.MessageService {
		chat.Last = joinAlbum(last, m, chat.Kind == model.KindGroup)
		if m.Key.MessageID == chat.Last.ID {
			chat.LastTime = m.Date
		}
		return
	}
	if last.ID != 0 && m.Key.MessageID < last.ID {
		return
	}
	setPreview(chat, m)
}

// keepAlbums gives chats, as a page of dialogs tells them, what the client
// saw of the albums their last messages end, from kept: the page has the
// last message of each alone.
func keepAlbums(chats, kept []model.Chat) []model.Chat {
	index := make(map[int64]int, len(kept))
	for i, c := range kept {
		if c.Last.Album != nil {
			index[c.ID] = i
		}
	}
	if len(index) == 0 {
		return chats
	}
	for i, c := range chats {
		j, ok := index[c.ID]
		if !ok || c.Last.Album == nil {
			continue
		}
		k := kept[j].Last
		if k.ID == c.Last.ID && k.Album.ID == c.Last.Album.ID {
			chats[i].Last.Album, chats[i].Last.Thumbs = k.Album, k.Thumbs
			chats[i].Last.Text, chats[i].Last.Entities = k.Text, k.Entities
		}
	}
	return chats
}

// previewContent gives p the text of m, its formatting, and the thumbnail
// and album of its media.
func previewContent(p *model.Preview, m model.Message) {
	p.Text, p.Entities = model.PreviewText(m.Text, m.Entities)
	if !m.Outgoing {
		switch m.Key.ChatID {
		case serviceNotifications:
			p.Entities = spoilerLoginCode(p.Text, p.Entities, 5)
		case verificationCodes:
			p.Entities = spoilerLoginCode(p.Text, p.Entities, 4)
		}
	}
	if t, ok := previewThumb(m); ok {
		p.Thumbs = []model.PreviewThumb{t}
	}
	if m.GroupedID != 0 {
		a := &model.PreviewAlbum{ID: m.GroupedID, Last: m.Key.MessageID}
		albumCounts(a, m)
		if p.Text != "" {
			a.Captions, a.Caption = 1, m.Key.MessageID
		}
		p.Album = a
	}
}

// previewContent gives p, the preview of msg, the last message of a dialog,
// what a list draws of it beyond its words, and keeps in l where its
// thumbnail downloads from, for one without an inline picture: the message
// is in no history.
func (l *list) previewContent(p *model.Preview, msg *tg.Message) {
	m, loc := convertMessage("", msg, nil)
	p.Forwarded = !m.ForwardDate.IsZero()
	previewContent(p, m)
	if len(p.Thumbs) == 0 || p.Thumbs[0].Spoiler || p.Thumbs[0].Blurred != nil || loc == nil {
		return
	}
	media := m.Media
	if media == nil && m.WebPage != nil {
		media = m.WebPage.Photo
		if m.WebPage.Video != nil {
			media = m.WebPage.Video
		}
	}
	if media == nil {
		return
	}
	refs := map[string]fileLocation{}
	addMediaRefs(refs, media, *loc)
	id := p.Thumbs[0].ID
	if ref, ok := refs[id]; ok {
		if l.refs == nil {
			l.refs = map[string]fileLocation{}
		}
		l.refs[id] = ref
	}
}

// keepPreviewRefs keeps refs, where the thumbnails of dialogs' last
// messages download from, those not kept already in this run with the same
// file reference written to the cache in one go.
func (s *Store) keepPreviewRefs(ctx context.Context, refs map[string]fileLocation) error {
	if len(refs) == 0 {
		return nil
	}
	c := s.history
	fresh := map[string]any{}
	c.mu.Lock()
	cache := c.cache
	for id, ref := range refs {
		if old, ok := c.refs[id]; ok && bytes.Equal(old.Reference, ref.Reference) {
			continue
		}
		c.refs[id] = ref
		fresh["ref/"+id] = ref
	}
	c.mu.Unlock()
	if cache == nil || len(fresh) == 0 {
		return nil
	}
	return cache.PutAll(ctx, fresh)
}
