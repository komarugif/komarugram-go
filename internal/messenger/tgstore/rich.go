// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

const (
	// richDepth bounds how deep blocks, and text in them, are read, as
	// Telegram Desktop bounds blocks (kMaxParsedBlockDepth); what is deeper
	// is left out. Telegram itself allows 16 levels.
	richDepth = 64
	// richBlocks bounds the blocks of a page, nested ones included, as
	// Telegram Desktop bounds what it prepares; Telegram allows 500.
	richBlocks = 4096
	// richButtons is the most buttons a row has, as in Telegram Desktop.
	richButtons = 8
	// richSpan bounds a table cell's colspan and rowspan.
	richSpan = 1024
)

// richMode is what a text keeps: everything, or no links (a code block's),
// or no links but dates (a button's label).
type richMode uint8

const (
	richAll richMode = iota
	richNoLinks
	richNoLinksButDates
)

// richConverter turns Telegram's rich message into the model's, finding its
// media among the photos and documents the message carries.
type richConverter struct {
	photos    map[int64]*tg.Photo
	documents map[int64]*tg.Document
	blocks    int
}

// convertRich converts a rich message.
func convertRich(r tg.RichMessage) *model.RichPage {
	c := richConverter{photos: map[int64]*tg.Photo{}, documents: map[int64]*tg.Document{}}
	for _, p := range r.Photos {
		if p, ok := p.(*tg.Photo); ok {
			c.photos[p.ID] = p
		}
	}
	for _, d := range r.Documents {
		if d, ok := d.(*tg.Document); ok {
			c.documents[d.ID] = d
		}
	}
	return &model.RichPage{RTL: r.Rtl, Part: r.Part, Blocks: c.convertBlocks(r.Blocks, 0)}
}

// richRefs keeps in refs where the photos and documents of a rich message
// download from, by the IDs of their media and of their sizes. It returns
// the thumbnails whose bytes came inline.
func richRefs(r tg.RichMessage, refs map[string]fileLocation) []*model.MessageMedia {
	var inlines []*model.MessageMedia
	for _, p := range r.Photos {
		if p, ok := p.(*tg.Photo); ok {
			meta, thumb := photoMedia(p)
			addMediaRefs(refs, meta, fileLocation{ID: p.ID, Hash: p.AccessHash, Reference: p.FileReference, DC: p.DCID, Photo: true, Thumb: thumb})
		}
	}
	for _, d := range r.Documents {
		if d, ok := d.(*tg.Document); ok {
			_, meta, ref := documentMedia(d)
			if inline := addMediaRefs(refs, meta, *ref); inline != nil {
				inlines = append(inlines, inline)
			}
		}
	}
	return inlines
}

func (c *richConverter) convertBlocks(blocks []tg.PageBlockClass, depth int) []model.RichBlock {
	var out []model.RichBlock
	for _, b := range blocks {
		out = c.appendBlock(out, b, depth)
	}
	return out
}

// textBlock is a block of kind with text t, which gives the block the first
// anchor it has.
func textBlock(kind string, t model.RichText) model.RichBlock {
	b := model.RichBlock{Kind: kind, Text: t}
	adoptAnchor(&b.Anchor, &b.Text)
	return b
}

// adoptAnchor makes the first anchor of t, when it is at its start, the
// block's; one inside the text stays there, where a link goes to its line.
func adoptAnchor(anchor *string, t *model.RichText) {
	if *anchor == "" && len(t.Anchors) > 0 && t.AnchorOffset(0) <= 0 {
		*anchor = t.Anchors[0]
		t.Anchors = t.Anchors[1:]
		if len(t.AnchorAt) > 0 {
			t.AnchorAt = t.AnchorAt[1:]
		}
		if len(t.Anchors) == 0 {
			t.Anchors, t.AnchorAt = nil, nil
		}
	}
}

func heading(level int, t model.RichText) model.RichBlock {
	b := textBlock(model.RichHeading, t)
	b.Level = level
	return b
}

func (c *richConverter) appendBlock(out []model.RichBlock, block tg.PageBlockClass, depth int) []model.RichBlock {
	if depth >= richDepth || c.blocks >= richBlocks {
		return out
	}
	c.blocks++
	depth++
	text := func(t tg.RichTextClass) model.RichText { return c.text(t, richAll, depth) }
	switch b := block.(type) {
	case *tg.PageBlockTitle:
		out = append(out, heading(1, text(b.Text)))
	case *tg.PageBlockSubtitle:
		out = append(out, heading(2, text(b.Text)))
	case *tg.PageBlockHeader:
		out = append(out, heading(3, text(b.Text)))
	case *tg.PageBlockSubheader:
		out = append(out, heading(4, text(b.Text)))
	case *tg.PageBlockKicker:
		out = append(out, heading(5, text(b.Text)))
	case *tg.PageBlockHeading1:
		out = append(out, heading(1, text(b.Text)))
	case *tg.PageBlockHeading2:
		out = append(out, heading(2, text(b.Text)))
	case *tg.PageBlockHeading3:
		out = append(out, heading(3, text(b.Text)))
	case *tg.PageBlockHeading4:
		out = append(out, heading(4, text(b.Text)))
	case *tg.PageBlockHeading5:
		out = append(out, heading(5, text(b.Text)))
	case *tg.PageBlockHeading6:
		out = append(out, heading(6, text(b.Text)))
	case *tg.PageBlockAuthorDate:
		r := textBlock(model.RichAuthorDate, text(b.Author))
		r.Date = unixTime(b.PublishedDate)
		out = append(out, r)
	case *tg.PageBlockParagraph:
		out = append(out, textBlock(model.RichParagraph, text(b.Text)))
	case *tg.PageBlockThinking:
		out = append(out, textBlock(model.RichThinking, text(b.Text)))
	case *tg.PageBlockFooter:
		out = append(out, textBlock(model.RichFooter, text(b.Text)))
	case *tg.PageBlockPreformatted:
		r := textBlock(model.RichCode, c.text(b.Text, richNoLinks, depth))
		r.Language = strings.TrimSpace(b.Language)
		out = append(out, r)
	case *tg.PageBlockDivider:
		out = append(out, model.RichBlock{Kind: model.RichDivider})
	case *tg.PageBlockAnchor:
		if name := model.AnchorName(b.Name); name != "" {
			out = append(out, model.RichBlock{Kind: model.RichAnchor, Anchor: name})
		}
	case *tg.PageBlockList:
		r := model.RichBlock{Kind: model.RichList}
		for _, item := range b.Items {
			var i model.RichListItem
			switch item := item.(type) {
			case *tg.PageListItemText:
				i.Checkbox, i.Checked = item.Checkbox, item.Checked
				i.Text = text(item.Text)
				adoptAnchor(&i.Anchor, &i.Text)
			case *tg.PageListItemBlocks:
				i.Checkbox, i.Checked = item.Checkbox, item.Checked
				i.Blocks = c.convertBlocks(item.Blocks, depth)
				adoptParagraph(&i)
			}
			r.Items = append(r.Items, i)
		}
		out = append(out, r)
	case *tg.PageBlockOrderedList:
		r := model.RichBlock{Kind: model.RichList, Ordered: true, Reversed: b.Reversed}
		if start, ok := b.GetStart(); ok {
			r.Start = &start
		}
		r.Type, _ = b.GetType()
		for _, item := range b.Items {
			var i model.RichListItem
			switch item := item.(type) {
			case *tg.PageListOrderedItemText:
				i.Checkbox, i.Checked = item.Checkbox, item.Checked
				i.Num, _ = item.GetNum()
				i.Type, _ = item.GetType()
				if v, ok := item.GetValue(); ok {
					i.Value = &v
				}
				i.Text = text(item.Text)
				adoptAnchor(&i.Anchor, &i.Text)
			case *tg.PageListOrderedItemBlocks:
				i.Checkbox, i.Checked = item.Checkbox, item.Checked
				i.Num, _ = item.GetNum()
				i.Type, _ = item.GetType()
				if v, ok := item.GetValue(); ok {
					i.Value = &v
				}
				i.Blocks = c.convertBlocks(item.Blocks, depth)
				adoptParagraph(&i)
			}
			r.Items = append(r.Items, i)
		}
		out = append(out, r)
	case *tg.PageBlockBlockquote:
		r := textBlock(model.RichQuote, text(b.Text))
		r.Collapsed = b.Collapsed
		r.Caption = text(b.Caption)
		adoptAnchor(&r.Anchor, &r.Caption)
		out = append(out, r)
	case *tg.PageBlockBlockquoteBlocks:
		r := model.RichBlock{Kind: model.RichQuote, Blocks: c.convertBlocks(b.Blocks, depth), Caption: text(b.Caption)}
		adoptAnchor(&r.Anchor, &r.Caption)
		out = append(out, r)
	case *tg.PageBlockPullquote:
		r := textBlock(model.RichQuote, text(b.Text))
		r.Pullquote = true
		r.Caption = text(b.Caption)
		adoptAnchor(&r.Anchor, &r.Caption)
		out = append(out, r)
	case *tg.PageBlockPhoto:
		out = append(out, c.mediaBlock(b.Caption, depth, c.photo(b.PhotoID, b.Spoiler)))
	case *tg.PageBlockVideo:
		m := c.document(b.VideoID)
		m.Spoiler, m.Autoplay, m.Loop = b.Spoiler, b.Autoplay, b.Loop
		out = append(out, c.mediaBlock(b.Caption, depth, m))
	case *tg.PageBlockAudio:
		out = append(out, c.mediaBlock(b.Caption, depth, c.document(b.AudioID)))
	case *tg.PageBlockDocument:
		out = append(out, c.mediaBlock(b.Caption, depth, c.document(b.DocumentID)))
	case *tg.PageBlockCollage:
		out = append(out, c.mediaBlock(b.Caption, depth, c.grouped(b.Items)...))
	case *tg.PageBlockSlideshow:
		r := c.mediaBlock(b.Caption, depth, c.grouped(b.Items)...)
		r.Slideshow = true
		out = append(out, r)
	case *tg.PageBlockCover:
		out = c.appendBlock(out, b.Cover, depth)
	case *tg.PageBlockEmbed:
		// The HTML of an embed is left out: this client shows what it
		// links to, not a page from a stranger.
		r := model.RichBlock{Kind: model.RichEmbed, Caption: c.caption(b.Caption, depth)}
		r.URL, _ = b.GetURL()
		r.Width, _ = b.GetW()
		r.Height, _ = b.GetH()
		adoptAnchor(&r.Anchor, &r.Caption)
		out = append(out, r)
	case *tg.PageBlockEmbedPost:
		r := model.RichBlock{Kind: model.RichEmbedPost, URL: b.URL, Author: b.Author, Date: unixTime(b.Date), Blocks: c.convertBlocks(b.Blocks, depth), Caption: c.caption(b.Caption, depth)}
		adoptAnchor(&r.Anchor, &r.Caption)
		out = append(out, r)
	case *tg.PageBlockChannel:
		r := model.RichBlock{Kind: model.RichChannel}
		switch ch := b.Channel.(type) {
		case *tg.Channel:
			r.Channel, r.Title, r.Username = peerID(&tg.PeerChannel{ChannelID: ch.ID}), ch.Title, ch.Username
		case *tg.ChannelForbidden:
			r.Channel, r.Title = peerID(&tg.PeerChannel{ChannelID: ch.ID}), ch.Title
		case *tg.Chat:
			r.Channel, r.Title = peerID(&tg.PeerChat{ChatID: ch.ID}), ch.Title
		case *tg.ChatForbidden:
			r.Channel, r.Title = peerID(&tg.PeerChat{ChatID: ch.ID}), ch.Title
		}
		out = append(out, r)
	case *tg.PageBlockMath:
		out = append(out, model.RichBlock{Kind: model.RichMath, Formula: formulaSource(b.Source)})
	case *tg.PageBlockTable:
		r := textBlock(model.RichTable, text(b.Title))
		r.Bordered, r.Striped, r.Compact = b.Bordered, b.Striped, b.Compact
		for _, row := range b.Rows {
			var cells []model.RichTableCell
			for _, cell := range row.Cells {
				rc := model.RichTableCell{Header: cell.Header}
				if t, ok := cell.GetText(); ok {
					rc.Text = text(t)
					rc.Text.Anchors, rc.Text.AnchorAt = nil, nil
				}
				if n, ok := cell.GetColspan(); ok && n > 1 {
					rc.Colspan = min(n, richSpan)
				}
				if n, ok := cell.GetRowspan(); ok && n > 1 {
					rc.Rowspan = min(n, richSpan)
				}
				switch {
				case cell.AlignCenter:
					rc.Align = "center"
				case cell.AlignRight:
					rc.Align = "right"
				}
				switch {
				case cell.ValignMiddle:
					rc.VAlign = "middle"
				case cell.ValignBottom:
					rc.VAlign = "bottom"
				}
				cells = append(cells, rc)
			}
			r.Rows = append(r.Rows, model.RichTableRow{Cells: cells})
		}
		out = append(out, r)
	case *tg.PageBlockDetails:
		r := textBlock(model.RichDetails, text(b.Title))
		r.Open = b.Open
		r.Blocks = c.convertBlocks(b.Blocks, depth)
		out = append(out, r)
	case *tg.PageBlockRelatedArticles:
		r := textBlock(model.RichRelated, text(b.Title))
		for _, a := range b.Articles {
			article := model.RichArticle{URL: a.URL}
			title, _ := a.GetTitle()
			description, _ := a.GetDescription()
			author, _ := a.GetAuthor()
			date, _ := a.GetPublishedDate()
			article.Title, article.Description, article.Author = strings.TrimSpace(title), strings.TrimSpace(description), strings.TrimSpace(author)
			article.Date = unixTime(date)
			r.Related = append(r.Related, article)
		}
		out = append(out, r)
	case *tg.PageBlockMap:
		r := model.RichBlock{Kind: model.RichMap, Zoom: b.Zoom, Width: b.W, Height: b.H, Caption: c.caption(b.Caption, depth)}
		// A map without a size is drawn at Telegram Desktop's.
		if r.Width <= 0 {
			r.Width = 400
		}
		if r.Height <= 0 {
			r.Height = 200
		}
		if geo, ok := b.Geo.(*tg.GeoPoint); ok {
			r.Latitude, r.Longitude = geo.Lat, geo.Long
		}
		adoptAnchor(&r.Anchor, &r.Caption)
		out = append(out, r)
	case *tg.PageBlockButtonRow:
		r := model.RichBlock{Kind: model.RichButtons}
		switch {
		case b.AlignLeft:
			r.Align = "left"
		case b.AlignCenter:
			r.Align = "center"
		case b.AlignRight:
			r.Align = "right"
		}
		for _, button := range b.Buttons[:min(len(b.Buttons), richButtons)] {
			label := c.text(button.Text, richNoLinksButDates, depth)
			label.Anchors, label.AnchorAt = nil, nil
			style, _ := button.GetStyle()
			r.Buttons = append(r.Buttons, model.RichButton{Text: label, Button: inlineButton(label.Text, button.Type), Style: richButtonStyle(style)})
		}
		if len(r.Buttons) > 0 {
			out = append(out, r)
		}
	default:
		out = append(out, model.RichBlock{Kind: model.RichUnsupported})
	}
	return out
}

// adoptParagraph makes an item's only paragraph its text, as Telegram
// Desktop does: an item has text or blocks, not both.
func adoptParagraph(i *model.RichListItem) {
	if len(i.Blocks) == 1 && i.Blocks[0].Kind == model.RichParagraph {
		i.Text, i.Anchor = i.Blocks[0].Text, i.Blocks[0].Anchor
		i.Blocks = nil
	}
}

func (c *richConverter) mediaBlock(caption tg.PageCaption, depth int, media ...model.RichMedia) model.RichBlock {
	r := model.RichBlock{Kind: model.RichMediaBlock, Media: media, Caption: c.caption(caption, depth)}
	adoptAnchor(&r.Anchor, &r.Caption)
	return r
}

// grouped is the photos and videos of a collage or a slideshow.
func (c *richConverter) grouped(items []tg.PageBlockClass) []model.RichMedia {
	var out []model.RichMedia
	for _, item := range items {
		switch item := item.(type) {
		case *tg.PageBlockPhoto:
			out = append(out, c.photo(item.PhotoID, item.Spoiler))
		case *tg.PageBlockVideo:
			m := c.document(item.VideoID)
			m.Spoiler, m.Autoplay, m.Loop = item.Spoiler, item.Autoplay, item.Loop
			out = append(out, m)
		}
	}
	return out
}

func (c *richConverter) photo(id int64, spoiler bool) model.RichMedia {
	m := model.RichMedia{Kind: model.MessagePhoto, Spoiler: spoiler}
	if p := c.photos[id]; p != nil {
		m.Media, _ = photoMedia(p)
	}
	return m
}

func (c *richConverter) document(id int64) model.RichMedia {
	m := model.RichMedia{Kind: model.MessageFile}
	if d := c.documents[id]; d != nil {
		m.Kind, m.Media, _ = documentMedia(d)
	}
	return m
}

// caption is a caption's text and, on a line of its own, its credit.
func (c *richConverter) caption(caption tg.PageCaption, depth int) model.RichText {
	out := c.text(caption.Text, richAll, depth)
	credit := c.text(caption.Credit, richAll, depth)
	if credit.Text != "" {
		if out.Text != "" {
			out.Text += "\n"
		}
		out.Append(credit)
	} else {
		out.Append(model.RichText{Anchors: credit.Anchors})
	}
	return out
}

// text converts rich text into text with entities.
func (c *richConverter) text(t tg.RichTextClass, mode richMode, depth int) model.RichText {
	var out model.RichText
	c.appendText(&out, t, mode, depth)
	return out
}

func (c *richConverter) appendText(out *model.RichText, t tg.RichTextClass, mode richMode, depth int) {
	if depth >= richDepth {
		return
	}
	depth++
	// wrap adds inner, as an entity e over what it adds.
	wrap := func(inner tg.RichTextClass, e model.Entity) {
		from := model.UTF16Len(out.Text)
		c.appendText(out, inner, mode, depth)
		out.Mark(from, e)
	}
	// link adds inner, or target when inner is empty, as an entity e over
	// it unless the mode drops links.
	link := func(inner tg.RichTextClass, target string, e model.Entity) {
		from := model.UTF16Len(out.Text)
		c.appendText(out, inner, mode, depth)
		if model.UTF16Len(out.Text) == from {
			out.Text += target
		}
		if mode == richAll {
			out.Mark(from, e)
		}
	}
	// tag adds inner, as an entity of kind unless the mode drops links.
	tag := func(inner tg.RichTextClass, kind string) {
		if mode == richAll {
			wrap(inner, model.Entity{Kind: kind})
		} else {
			c.appendText(out, inner, mode, depth)
		}
	}
	switch t := t.(type) {
	case *tg.TextPlain:
		out.Text += t.Text
	case *tg.TextConcat:
		for _, part := range t.Texts {
			c.appendText(out, part, mode, depth)
		}
	case *tg.TextBold:
		wrap(t.Text, model.Entity{Kind: "bold"})
	case *tg.TextItalic:
		wrap(t.Text, model.Entity{Kind: "italic"})
	case *tg.TextUnderline:
		wrap(t.Text, model.Entity{Kind: "underline"})
	case *tg.TextStrike:
		wrap(t.Text, model.Entity{Kind: "strike"})
	case *tg.TextFixed:
		wrap(t.Text, model.Entity{Kind: "code"})
	case *tg.TextSubscript:
		wrap(t.Text, model.Entity{Kind: "sub"})
	case *tg.TextSuperscript:
		wrap(t.Text, model.Entity{Kind: "sup"})
	case *tg.TextMarked:
		wrap(t.Text, model.Entity{Kind: "marked"})
	case *tg.TextSpoiler:
		wrap(t.Text, model.Entity{Kind: "spoiler"})
	case *tg.TextURL:
		link(t.Text, t.URL, model.Entity{Kind: "url", URL: t.URL})
	case *tg.TextEmail:
		link(t.Text, t.Email, model.Entity{Kind: "url", URL: "mailto:" + t.Email})
	case *tg.TextPhone:
		link(t.Text, t.Phone, model.Entity{Kind: "url", URL: "tel:" + t.Phone})
	case *tg.TextMentionName:
		link(t.Text, "", model.Entity{Kind: "url", URL: "tg://user?id=" + strconv.FormatInt(t.UserID, 10)})
	case *tg.TextMention:
		tag(t.Text, "mention")
	case *tg.TextHashtag:
		tag(t.Text, "hashtag")
	case *tg.TextBotCommand:
		tag(t.Text, "bot_command")
	case *tg.TextCashtag:
		tag(t.Text, "cashtag")
	case *tg.TextAutoURL:
		tag(t.Text, "url")
	case *tg.TextAutoEmail:
		tag(t.Text, "email")
	case *tg.TextAutoPhone:
		tag(t.Text, "phone")
	case *tg.TextBankCard:
		tag(t.Text, "bank_card")
	case *tg.TextDate:
		if mode == richNoLinks {
			c.appendText(out, t.Text, mode, depth)
			break
		}
		wrap(t.Text, model.Entity{Kind: "date", Date: int64(t.Date), DateFormat: dateFlags(t.Relative, t.ShortTime, t.LongTime, t.ShortDate, t.LongDate, t.DayOfWeek)})
	case *tg.TextImage:
		// Telegram Desktop shows no inline images in rich messages either.
		out.Text += "[image]"
	case *tg.TextMath:
		from := model.UTF16Len(out.Text)
		out.Text += formulaSource(t.Source)
		out.Mark(from, model.Entity{Kind: "math"})
	case *tg.TextCustomEmoji:
		from := model.UTF16Len(out.Text)
		alt := t.Alt
		if alt == "" {
			alt = "￼"
		}
		out.Text += alt
		out.Mark(from, model.Entity{Kind: "emoji", DocumentID: t.DocumentID})
	case *tg.TextAnchor:
		if name := model.AnchorName(t.Name); name != "" {
			out.AddAnchor(name)
		}
		c.appendText(out, t.Text, mode, depth)
	case *tg.TextDiff:
		// The text as it is now; what it was is for showing an edit.
		c.appendText(out, t.Text, mode, depth)
	case *tg.TextButton:
		label := c.text(t.Text, richNoLinksButDates, depth).Trimmed()
		if label.Text == "" {
			break
		}
		button := inlineButton(label.Text, t.Type)
		from := model.UTF16Len(out.Text)
		out.Append(model.RichText{Text: label.Text, Entities: label.Entities})
		out.Mark(from, model.Entity{Kind: "button", Button: &button})
	}
}

// dateFlags is how a date of a message or of a rich text is written, by
// the flags Telegram sets on it.
func dateFlags(relative, shortTime, longTime, shortDate, longDate, dayOfWeek bool) model.DateFormat {
	var f model.DateFormat
	for _, flag := range []struct {
		set bool
		f   model.DateFormat
	}{{relative, model.DateRelative}, {shortTime, model.DateShortTime}, {longTime, model.DateLongTime}, {shortDate, model.DateShortDate}, {longDate, model.DateLongDate}, {dayOfWeek, model.DateDayOfWeek}} {
		if flag.set {
			f |= flag.f
		}
	}
	return f
}

// formulaSource is a formula's LaTeX, trimmed, without the dollars it may
// be enclosed in.
func formulaSource(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '$' && s[len(s)-1] == '$' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

func richButtonStyle(s tg.RichButtonStyle) string {
	switch {
	case s.BgPrimary:
		return "primary"
	case s.BgDanger:
		return "danger"
	case s.BgSuccess:
		return "success"
	case s.Link:
		return "link"
	}
	return ""
}

func unixTime(t int) time.Time {
	if t == 0 {
		return time.Time{}
	}
	return time.Unix(int64(t), 0)
}

// RichMessage implements model.RichStore with messages.getRichMessage, which
// only reads: it loads the whole of a rich message Telegram sent cut short.
// The whole article is kept apart from the history, which keeps the part,
// and is what comes back offline.
func (s *Store) RichMessage(ctx context.Context, key model.MessageKey) (model.RichPage, error) {
	c := s.history
	c.mu.Lock()
	api, cache := c.api, c.cache
	chat, _ := c.threadChat(key.ChatID)
	peer := c.peers[chat]
	c.mu.Unlock()
	cacheKey := fmt.Sprintf("rich/%d/%d", key.ChatID, key.MessageID)
	page, err := s.loadRichMessage(ctx, api, peer, key)
	if err == nil {
		if cache != nil {
			_ = cache.Put(ctx, cacheKey, page)
		}
		return page, nil
	}
	if cache != nil {
		var kept model.RichPage
		if ok, e := cache.Get(ctx, cacheKey, &kept); e == nil && ok {
			return kept, nil
		}
	}
	return model.RichPage{}, err
}

func (s *Store) loadRichMessage(ctx context.Context, api *tg.Client, peer peerRecord, key model.MessageKey) (model.RichPage, error) {
	if api == nil || peer.ID == 0 {
		return model.RichPage{}, errNotConnected
	}
	res, err := api.MessagesGetRichMessage(ctx, &tg.MessagesGetRichMessageRequest{Peer: peer.input(), ID: int(key.MessageID)})
	if err != nil {
		return model.RichPage{}, err
	}
	mod, ok := res.AsModified()
	if !ok {
		return model.RichPage{}, errMessageGone
	}
	s.rememberPeers(mod.GetUsers(), mod.GetChats())
	// The messages are converted only for their pages and the media in
	// them; the history keeps the part it has.
	c := s.history
	c.apply.Lock()
	msgs, err := s.convert(ctx, mod.GetMessages(), false, ^uint64(0))
	c.apply.Unlock()
	if err != nil {
		return model.RichPage{}, err
	}
	for _, m := range msgs {
		if m.Key.MessageID == key.MessageID && m.Rich != nil {
			return *m.Rich, nil
		}
	}
	return model.RichPage{}, errMessageGone
}

// InstantView implements model.InstantViewStore: the Instant View of the
// page at url, through messages.getWebPage, as Telegram Desktop loads it
// (Iv::Instance::show), its media downloadable as a message's. The cache
// keeps it, and gives it back when Telegram cannot.
func (s *Store) InstantView(ctx context.Context, url string) (model.RichPage, error) {
	c := s.history
	c.mu.Lock()
	api, cache := c.api, c.cache
	c.mu.Unlock()
	cacheKey := "iv/" + url
	page, err := s.loadInstantView(ctx, api, url)
	if err == nil {
		if cache != nil {
			_ = cache.Put(ctx, cacheKey, page)
		}
		return page, nil
	}
	if cache != nil {
		var kept model.RichPage
		if ok, e := cache.Get(ctx, cacheKey, &kept); e == nil && ok {
			return kept, nil
		}
	}
	return model.RichPage{}, err
}

func (s *Store) loadInstantView(ctx context.Context, api *tg.Client, url string) (model.RichPage, error) {
	if api == nil {
		return model.RichPage{}, errNotConnected
	}
	res, err := api.MessagesGetWebPage(ctx, &tg.MessagesGetWebPageRequest{URL: url})
	if err != nil {
		return model.RichPage{}, err
	}
	s.rememberPeers(res.Users, res.Chats)
	web, ok := res.Webpage.(*tg.WebPage)
	if !ok {
		return model.RichPage{}, errMessageGone
	}
	cached, ok := web.GetCachedPage()
	if !ok {
		return model.RichPage{}, errMessageGone
	}
	// The page has what a rich message has: its blocks, and the photos and
	// documents they show.
	rich := tg.RichMessage{Rtl: cached.Rtl, Part: cached.Part, Blocks: cached.Blocks, Photos: cached.Photos, Documents: cached.Documents}
	refs := map[string]fileLocation{}
	richRefs(rich, refs)
	c := s.history
	c.mu.Lock()
	for id, ref := range refs {
		c.refs[id] = ref
	}
	c.mu.Unlock()
	return *convertRich(rich), nil
}
