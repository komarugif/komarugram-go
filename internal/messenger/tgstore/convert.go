// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"komarugram/internal/messenger/model"
	"sort"
	"strings"
	"time"

	"github.com/gotd/td/telegram/thumbnail"
	"github.com/gotd/td/tg"
)

// fileLocation stays in the account cache, never in presentation state.
type fileLocation struct {
	WebURL                     string
	WebHash                    int64
	WebNoProxy                 bool
	GiftChat                   int64
	Gift                       bool
	GiftOffset                 string
	WallpaperID, WallpaperHash int64
	ID, Hash                   int64
	Reference                  []byte
	DC                         int
	Photo                      bool
	Thumb                      string
	Peer                       *peerRecord
	// Big asks for the large picture of a peer's photo, not the small one.
	Big bool `json:",omitempty"`
	// ProfileMessage is the service message that tells a group's profile
	// photo, by which its file reference is renewed.
	ProfileMessage int `json:",omitempty"`
}

func (l fileLocation) input() tg.InputFileLocationClass {
	if l.Peer != nil {
		return &tg.InputPeerPhotoFileLocation{Peer: l.Peer.input(), PhotoID: l.ID, Big: l.Big}
	}
	if l.Photo {
		return &tg.InputPhotoFileLocation{ID: l.ID, AccessHash: l.Hash, FileReference: l.Reference, ThumbSize: l.Thumb}
	}
	return &tg.InputDocumentFileLocation{ID: l.ID, AccessHash: l.Hash, FileReference: l.Reference, ThumbSize: l.Thumb}
}
func convertMessage(account string, m tg.MessageClass, names map[int64]string) (model.Message, *fileLocation) {
	out := model.Message{Key: model.MessageKey{AccountID: account, MessageID: model.MessageID(m.GetID())}}
	var loc *fileLocation
	switch m := m.(type) {
	case *tg.Message:
		out.Key.ChatID = peerID(m.PeerID)
		out.Text = m.Message
		out.Date = time.Unix(int64(m.Date), 0)
		out.Outgoing = m.Out
		out.MediaUnread = m.MediaUnread
		out.GroupedID = m.GroupedID
		out.NoForwards = m.Noforwards
		out.Post = m.Post
		if m.EditDate != 0 {
			out.EditedAt = time.Unix(int64(m.EditDate), 0)
		}
		if m.FromID != nil {
			out.SenderID = peerID(m.FromID)
			out.SenderName = names[out.SenderID]
		}
		if r, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			out.ReplyToMessageID = model.MessageID(r.ReplyToMsgID)
			out.ForumTopic = r.ForumTopic
			if top, ok := r.GetReplyToTopID(); ok {
				out.ReplyToTopID = model.MessageID(top)
			}
		}
		if f, ok := m.GetFwdFrom(); ok {
			if f.FromID != nil {
				out.ForwardFromID = peerID(f.FromID)
				out.ForwardName = names[out.ForwardFromID]
			}
			if f.FromName != "" {
				out.ForwardName = f.FromName
			}
			out.ForwardDate = time.Unix(int64(f.Date), 0)
		}
		out.Views, _ = m.GetViews()
		out.PostAuthor, _ = m.GetPostAuthor()
		if r, ok := m.GetReplies(); ok && r.Comments {
			out.Comments, out.CommentsOpen = r.Replies, true
			for _, p := range r.RecentRepliers {
				out.Commenters = append(out.Commenters, peerID(p))
			}
		}
		if reactions, ok := m.GetReactions(); ok {
			out.Reactions = convertReactions(reactions)
			out.ReactionsListed = reactions.CanSeeList && len(out.Reactions) > 0
		}
		for _, e := range m.Entities {
			entity := model.Entity{Offset: e.GetOffset(), Length: e.GetLength()}
			switch e := e.(type) {
			case *tg.MessageEntityBold:
				entity.Kind = "bold"
			case *tg.MessageEntityItalic:
				entity.Kind = "italic"
			case *tg.MessageEntityUnderline:
				entity.Kind = "underline"
			case *tg.MessageEntityStrike:
				entity.Kind = "strike"
			case *tg.MessageEntityCode:
				entity.Kind = "code"
			case *tg.MessageEntityPre:
				entity.Kind = "pre"
				entity.Language = e.Language
			case *tg.MessageEntityBlockquote:
				entity.Kind = "quote"
				entity.Collapsed = e.Collapsed
			case *tg.MessageEntitySpoiler:
				entity.Kind = "spoiler"
			case *tg.MessageEntityURL:
				entity.Kind = "url"
			case *tg.MessageEntityTextURL:
				entity.Kind = "url"
				entity.URL = e.URL
			case *tg.MessageEntityMention:
				entity.Kind = "mention"
			case *tg.MessageEntityMentionName:
				entity.Kind = "url"
				entity.URL = fmt.Sprintf("tg://user?id=%d", e.UserID)
			case *tg.MessageEntityCustomEmoji:
				entity.Kind = "emoji"
				entity.DocumentID = e.DocumentID
			case *tg.MessageEntityHashtag:
				entity.Kind = "hashtag"
			case *tg.MessageEntityCashtag:
				entity.Kind = "cashtag"
			case *tg.MessageEntityBotCommand:
				entity.Kind = "bot_command"
			case *tg.MessageEntityEmail:
				entity.Kind = "email"
			case *tg.MessageEntityPhone:
				entity.Kind = "phone"
			case *tg.MessageEntityBankCard:
				entity.Kind = "bank_card"
			case *tg.MessageEntityFormattedDate:
				entity.Kind = "date"
				entity.Date = int64(e.Date)
				entity.DateFormat = dateFlags(e.Relative, e.ShortTime, e.LongTime, e.ShortDate, e.LongDate, e.DayOfWeek)
			default:
				entity.Kind = "unsupported"
			}
			out.Entities = append(out.Entities, entity)
		}
		if rich, ok := m.GetRichMessage(); ok {
			// Telegram sends a rich message's text empty: it shows the
			// article's summary, as Telegram Desktop does.
			out.Rich = convertRich(rich)
			summary := out.Rich.Summary()
			out.Text, out.Entities = summary.Text, summary.Entities
		}
		switch media := m.Media.(type) {
		case *tg.MessageMediaPhoto:
			if p, ok := media.Photo.(*tg.Photo); ok {
				meta, thumb := photoMedia(p)
				out.Kind = model.MessagePhoto
				out.Media = meta
				loc = &fileLocation{ID: p.ID, Hash: p.AccessHash, Reference: p.FileReference, DC: p.DCID, Photo: true, Thumb: thumb}
			}
		case *tg.MessageMediaWebPage:
			if page, ok := media.Webpage.(*tg.WebPage); ok {
				out.WebPage = &model.WebPreview{URL: page.URL, DisplayURL: page.DisplayURL, Site: page.SiteName, Title: page.Title, Description: page.Description}
				_, out.WebPage.InstantView = page.GetCachedPage()
				if photo, ok := page.Photo.(*tg.Photo); ok {
					meta, thumb := photoMedia(photo)
					out.WebPage.Photo = meta
					loc = &fileLocation{ID: photo.ID, Hash: photo.AccessHash, Reference: photo.FileReference, DC: photo.DCID, Photo: true, Thumb: thumb}
				}
				// A page's video, as Telegram keeps the video of a YouTube
				// page, plays as a video message: it is what downloads,
				// its own thumbnail standing for the photo.
				if doc, ok := page.Document.(*tg.Document); ok {
					if kind, meta, ref := documentMedia(doc); kind == model.MessageVideo || kind == model.MessageGIF {
						out.WebPage.Video, out.WebPage.VideoKind = meta, kind
						loc = ref
					}
				}
			}
		case *tg.MessageMediaToDo:
			out.Kind = model.MessageText
			out.Text = media.Todo.Title.Text
			out.Entities = nil
			done := map[int]bool{}
			for _, item := range media.Completions {
				done[item.ID] = true
			}
			for _, item := range media.Todo.List {
				mark := "☐ "
				if done[item.ID] {
					mark = "☑ "
				}
				out.Text += "\n" + mark + item.Title.Text
			}
		case *tg.MessageMediaPoll:
			out.Kind = model.MessagePoll
			out.Poll = convertPoll(media)
		case *tg.MessageMediaDocument:
			if d, ok := media.Document.(*tg.Document); ok {
				out.Kind, out.Media, loc = documentMedia(d)
			}
		}
		switch markup := m.ReplyMarkup.(type) {
		case *tg.ReplyInlineMarkup:
			out.Buttons = convertInlineKeyboard(markup.Rows)
		case *tg.ReplyKeyboardMarkup:
			out.Keyboard = &model.ReplyKeyboard{
				Rows:      convertReplyKeyboard(markup.Rows),
				SingleUse: markup.SingleUse, Persistent: markup.Persistent, Placeholder: markup.Placeholder,
			}
		case *tg.ReplyKeyboardHide:
			out.KeyboardHide = true
		}
	case *tg.MessageService:
		out.Key.ChatID = peerID(m.PeerID)
		out.Date = time.Unix(int64(m.Date), 0)
		out.Kind = model.MessageService
		out.Outgoing = m.Out
		out.Post = m.Post
		if m.FromID != nil {
			out.SenderID = peerID(m.FromID)
			out.SenderName = names[out.SenderID]
		}
		if r, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			out.ReplyToMessageID = model.MessageID(r.ReplyToMsgID)
		}
		if reactions, ok := m.GetReactions(); ok {
			out.Reactions = convertReactions(reactions)
		}
		out.Service = serviceAction(m.Action, names)
	}
	b, _ := json.Marshal(out)
	h := fnv.New64a()
	h.Write(b)
	out.ContentRevision = h.Sum64() & ((1 << 63) - 1)
	return out, loc
}

// photoMedia describes the largest size of a photo and keeps the others as
// variants, so that a tile or a thumbnail downloads only what it shows.
// It returns the type of the largest size, which its file location names.
func photoMedia(p *tg.Photo) (*model.MessageMedia, string) {
	meta := &model.MessageMedia{MIMEType: "image/jpeg"}
	var sizes []model.MessageMedia
	var cached []byte
	for _, s := range p.Sizes {
		var w, h, size int
		switch s := s.(type) {
		case *tg.PhotoStrippedSize:
			meta.Preview, _ = thumbnail.Expand(s.Bytes)
			continue
		case *tg.PhotoCachedSize:
			// Its bytes arrive inline and are never downloaded.
			cached = s.Bytes
			continue
		case *tg.PhotoSize:
			w, h, size = s.W, s.H, s.Size
		case *tg.PhotoSizeProgressive:
			w, h = s.W, s.H
			for _, n := range s.Sizes {
				size = max(size, n)
			}
		default:
			continue
		}
		if w > 0 && h > 0 {
			sizes = append(sizes, model.MessageMedia{ID: fmt.Sprintf("photo/%d/%s", p.ID, s.GetType()), MIMEType: "image/jpeg", Width: w, Height: h, Size: int64(size)})
		}
	}
	if meta.Preview == nil {
		meta.Preview = cached
	}
	if len(sizes) == 0 {
		meta.ID = fmt.Sprintf("photo/%d/", p.ID)
		return meta, ""
	}
	sort.SliceStable(sizes, func(i, j int) bool { return sizes[i].Width*sizes[i].Height < sizes[j].Width*sizes[j].Height })
	largest := sizes[len(sizes)-1]
	meta.ID, meta.Width, meta.Height, meta.Size = largest.ID, largest.Width, largest.Height, largest.Size
	meta.Variants = sizes[:len(sizes)-1]
	return meta, largest.ID[strings.LastIndexByte(largest.ID, '/')+1:]
}

func documentMedia(d *tg.Document) (model.MessageKind, *model.MessageMedia, *fileLocation) {
	k := model.MessageFile
	m := &model.MessageMedia{ID: fmt.Sprintf("document/%d", d.ID), MIMEType: d.MimeType, Size: d.Size}
	m.Thumbnail = photoThumbnail(d.Thumbs, fmt.Sprintf("document/%d/thumb", d.ID))
	if m.Thumbnail != nil {
		m.Preview = m.Thumbnail.Preview
	}
	sticker, animated := false, false
	for _, a := range d.Attributes {
		switch a := a.(type) {
		case *tg.DocumentAttributeFilename:
			m.FileName = a.FileName
		case *tg.DocumentAttributeImageSize:
			m.Width, m.Height = a.W, a.H
		case *tg.DocumentAttributeVideo:
			k = model.MessageVideo
			m.Width, m.Height = a.W, a.H
			m.Duration = time.Duration(a.Duration * float64(time.Second))
		case *tg.DocumentAttributeAudio:
			if a.Voice {
				k = model.MessageVoice
				m.Waveform = a.Waveform
			} else {
				k = model.MessageMusic
			}
			m.Title, m.Performer = a.Title, a.Performer
			m.Duration = time.Duration(a.Duration) * time.Second
		case *tg.DocumentAttributeAnimated:
			animated = true
		case *tg.DocumentAttributeSticker:
			sticker = true
			m.StickerSet = stickerSetRef(a.Stickerset)
		case *tg.DocumentAttributeCustomEmoji:
			sticker = true
			m.StickerSet = stickerSetRef(a.Stickerset)
		}
	}
	if animated || d.MimeType == "image/gif" {
		k = model.MessageGIF
	}
	if sticker {
		k = model.MessageSticker
	}
	return k, m, &fileLocation{ID: d.ID, Hash: d.AccessHash, Reference: d.FileReference, DC: d.DCID}
}

func stickerSetRef(set tg.InputStickerSetClass) *model.StickerSetRef {
	switch set := set.(type) {
	case *tg.InputStickerSetID:
		return &model.StickerSetRef{Type: "id", ID: set.ID, AccessHash: set.AccessHash}
	case *tg.InputStickerSetShortName:
		return &model.StickerSetRef{Type: "short_name", ShortName: set.ShortName}
	case *tg.InputStickerSetDice:
		return &model.StickerSetRef{Type: "dice", Emoticon: set.Emoticon}
	case *tg.InputStickerSetPremiumGifts:
		return &model.StickerSetRef{Type: "premium_gifts"}
	case *tg.InputStickerSetEmojiDefaultStatuses:
		return &model.StickerSetRef{Type: "emoji_default_statuses"}
	case *tg.InputStickerSetEmojiDefaultTopicIcons:
		return &model.StickerSetRef{Type: "emoji_default_topic_icons"}
	case *tg.InputStickerSetEmojiChannelDefaultStatuses:
		return &model.StickerSetRef{Type: "emoji_channel_default_statuses"}
	default:
		return nil
	}
}

func photoThumbnail(sizes []tg.PhotoSizeClass, id string) *model.MessageMedia {
	var preview []byte
	var best *model.MessageMedia
	var kind string
	for _, s := range sizes {
		var w, h, n int
		switch s := s.(type) {
		case *tg.PhotoStrippedSize:
			preview, _ = thumbnail.Expand(s.Bytes)
		case *tg.PhotoCachedSize:
			preview = s.Bytes
			w, h, n = s.W, s.H, len(s.Bytes)
		case *tg.PhotoSize:
			w, h, n = s.W, s.H, s.Size
		case *tg.PhotoSizeProgressive:
			w, h = s.W, s.H
			for _, size := range s.Sizes {
				n = max(n, size)
			}
		}
		if w > 0 && h > 0 && (best == nil || w*h > best.Width*best.Height) {
			kind = s.GetType()
			best = &model.MessageMedia{ID: id + "/" + kind, MIMEType: "image/jpeg", Width: w, Height: h, Size: int64(n)}
		}
	}
	if best == nil && len(preview) > 0 {
		best = &model.MessageMedia{ID: id + "/inline", MIMEType: "image/jpeg"}
	}
	if best != nil {
		best.Preview = preview
	}
	return best
}

func convertPoll(media *tg.MessageMediaPoll) *model.Poll {
	p := &model.Poll{Question: media.Poll.Question.Text, Total: media.Results.TotalVoters, Closed: media.Poll.Closed, Quiz: media.Poll.Quiz, Multiple: media.Poll.MultipleChoice}
	for _, raw := range media.Poll.Answers {
		a, ok := raw.(*tg.PollAnswer)
		if !ok {
			continue
		}
		answer := model.PollAnswer{Text: a.GetText().Text}
		for _, r := range media.Results.Results {
			if string(r.Option) == string(a.GetOption()) {
				answer.Voters = r.Voters
				answer.Chosen = r.Chosen
				answer.Correct = r.Correct
			}
		}
		p.Answers = append(p.Answers, answer)
	}
	return p
}

// convertInlineKeyboard turns the rows of the keyboard under a bot's message
// into the model's; what this client does not do stays a disabled "action".
func convertInlineKeyboard(rows []tg.KeyboardInlineButtonRow) [][]model.MessageButton {
	var out [][]model.MessageButton
	for _, r := range rows {
		var row []model.MessageButton
		for _, b := range r.Buttons {
			row = append(row, inlineButton(b.Text, b.Type))
		}
		out = append(out, row)
	}
	return out
}

// inlineButton is a button of a bot's message, or of a rich message, with
// text and type t; what this client does not do stays a disabled "action".
func inlineButton(text string, t tg.InlineButtonTypeClass) model.MessageButton {
	btn := model.MessageButton{Text: text, Kind: "action"}
	switch t := t.(type) {
	case *tg.InlineButtonTypeURL:
		btn.Kind, btn.URL = "url", t.URL
	case *tg.InlineButtonTypeCallback:
		// One that wants the password is left disabled.
		if !t.RequiresPassword {
			btn.Kind, btn.Data = "callback", t.Data
		}
	case *tg.InlineButtonTypeCopy:
		btn.Kind, btn.Copy = "copy", t.CopyText
	case *tg.InlineButtonTypeWebView:
		btn.Kind, btn.URL = "webview", t.URL
	}
	return btn
}

// convertReplyKeyboard turns the rows of a bot's reply keyboard into the
// model's. A plain button sends its own text; what this client does not do
// stays a disabled "action".
func convertReplyKeyboard(rows []tg.KeyboardButtonRow) [][]model.MessageButton {
	var out [][]model.MessageButton
	for _, r := range rows {
		var row []model.MessageButton
		for _, b := range r.Buttons {
			btn := model.MessageButton{Text: b.Text, Kind: "action"}
			switch t := b.Type.(type) {
			case *tg.ButtonTypeDefault:
				btn.Kind = "text"
			case *tg.ButtonTypeSimpleWebView:
				btn.Kind, btn.URL = "simple_webview", t.URL
			}
			row = append(row, btn)
		}
		out = append(out, row)
	}
	return out
}
