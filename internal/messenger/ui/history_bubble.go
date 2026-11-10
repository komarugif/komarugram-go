// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"strconv"
	"strings"
	"sync"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"golang.org/x/exp/shiny/materialdesign/icons"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

var (
	iconViews    = wdk.RequireIconWidget(icons.ActionVisibility)
	iconComments = wdk.RequireIconWidget(icons.CommunicationChatBubbleOutline)
	iconFileRow  = wdk.RequireIconWidget(icons.EditorInsertDriveFile)
	iconPlayFile = wdk.RequireIconWidget(icons.AVPlayArrow)
	// iconStop stops a draft a bot streams.
	iconStop      = wdk.RequireIconWidget(icons.AVStop)
	iconPauseFile = wdk.RequireIconWidget(icons.AVPause)
	// iconAudiotrack marks music in the box for sending files.
	iconAudiotrack = wdk.RequireIconWidget(icons.ImageAudiotrack)
	iconInfo       = wdk.RequireIconWidget(icons.ActionInfoOutline)
	// The marks of a chat's kind before its title in the chat list.
	iconKindGroup   = wdk.RequireIconWidget(icons.SocialPeople)
	iconKindChannel = wdk.RequireIconWidget(icons.CommunicationRSSFeed)
	iconKindBot     = wdk.RequireIconWidget(icons.NotificationADB)
)

const (
	// bubbleRadiusJoined rounds a bubble's corners where it touches
	// another bubble of the same sender, as materialgram draws them; the
	// others are as round as the look asks (bubbleRadiusOf).
	bubbleRadiusJoined = unit.Dp(6)
	// joinGap is the time within which messages of one sender join.
	joinGap = 15 * time.Minute
)

// bubbleJoin tells whether a message joins the one above or below it into
// one group: a sender's messages in a row show its name once, at the top,
// and its avatar once, at the bottom.
type bubbleJoin uint8

const (
	joinAbove bubbleJoin = 1 << iota
	joinBelow
)

// joinable reports whether b, the message after a, continues a's group. In
// Saved Messages, whose messages tell no sender, the group is of whom they
// were forwarded from, whose avatar each group shows, as Telegram
// Desktop's.
func joinable(a, b model.Message, saved bool) bool {
	if saved && (a.ForwardFromID != b.ForwardFromID || a.ForwardName != b.ForwardName) {
		return false
	}
	return a.Kind != model.MessageService && b.Kind != model.MessageService &&
		a.Outgoing == b.Outgoing && a.SenderID == b.SenderID && a.SenderName == b.SenderName &&
		sameDay(a.Date, b.Date) && b.Date.Sub(a.Date) < joinGap
}

// messageJoins tells for each message of msgs, in order, how it joins its
// neighbours; saved for those of Saved Messages.
func messageJoins(msgs []model.Message, saved bool) []bubbleJoin {
	joins := make([]bubbleJoin, len(msgs))
	for i := 1; i < len(msgs); i++ {
		if joinable(msgs[i-1], msgs[i], saved) {
			joins[i-1] |= joinBelow
			joins[i] |= joinAbove
		}
	}
	return joins
}

// bubbleShape is the radius of each corner of a bubble.
type bubbleShape struct{ nw, ne, se, sw int }

// shapeOf is the shape of a bubble that joins its neighbours so: the
// corners on its sender's side that touch them are less round.
func shapeOf(gtx layout.Context, join bubbleJoin, outgoing bool) bubbleShape {
	r := bubbleRadiusOf(gtx)
	small := min(gtx.Dp(bubbleRadiusJoined), r)
	s := bubbleShape{r, r, r, r}
	if outgoing {
		if join&joinAbove != 0 {
			s.ne = small
		}
		if join&joinBelow != 0 {
			s.se = small
		}
	} else {
		if join&joinAbove != 0 {
			s.nw = small
		}
		if join&joinBelow != 0 {
			s.sw = small
		}
	}
	return s
}

func (s bubbleShape) rrect(size image.Point) clip.RRect {
	return clip.RRect{Rect: image.Rectangle{Max: size}, NW: s.nw, NE: s.ne, SE: s.se, SW: s.sw}
}

// senderColor is the color of a sender's name: the color of its avatar,
// darker on a light theme and lighter on a dark one, so that it reads on a
// bubble.
func senderColor(gtx layout.Context, id int64) token.MatColor {
	c := avatarColors[avatarColorIndex(id)]
	sc := scheme(gtx)
	if token.IsDarkColorSet(sc.Surface) {
		return token.LerpColor(c, white, .25)
	}
	return token.LerpColor(c, token.NewMatColorFromHexRGB(0), .28)
}

// datePill draws a date over the history, tinted as materialgram's are.
func datePill(gtx layout.Context, txt string) layout.Dimensions {
	sc := scheme(gtx)
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			fillRounded(gtx, sc.SecondaryContainer.Color, gtx.Constraints.Min, gtx.Constraints.Min.Y/2)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 5, Bottom: 5, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, txt, token.TypestyleLabelLargeEmphasized, sc.SecondaryContainer.OnColor, 1)
			})
		}),
	)
}

// row draws a message of the history: the date over it when a day starts,
// then its bubble, which join fits to its neighbours.
func (p *chatPage) row(gtx layout.Context, m model.Message, date bool, join bubbleJoin, l localization.Catalog, animate bool) layout.Dimensions {
	if p.appearance != nil {
		gtx = p.appearance.messageContext(gtx, m.Outgoing)
	}
	r := p.rows[m.Key.MessageID]
	if r == nil || r.revision != m.ContentRevision {
		if p.activeText == r {
			p.activeText = nil
		}
		r = newMessageRow(m, l, gtx.Now)
		for _, row := range m.Buttons {
			r.buttons = append(r.buttons, make([]surface, len(row)))
		}
		p.rows[m.Key.MessageID] = r
	}
	r.refreshDates(gtx, m, l)
	p.refreshStamps(gtx, r, m, l)
	r.capture = rowCapture{on: p.wantsProfile(r, m), chips: r.capture.chips[:0], parts: r.capture.parts[:0]}
	// Where the article is in the history, as the last frame put it.
	r.viewKnown = p.rowTopKnown
	if r.viewKnown {
		r.viewTop = p.rowTop + r.bodyTop + gtx.Dp(bubblePadTop) + r.articleAbove
	}
	if r.avatar.Clicked(gtx) && p.senderOpens(m) {
		p.openSender(p.avatarSender(m))
	}
	if r.forward.Clicked(gtx) && p.forwardOpens(m) {
		p.openSender(forwardSender(m))
	}
	if m.ReplyToMessageID != 0 && r.reply.Clicked(gtx) {
		p.jumpPending = m.ReplyToMessageID
		p.invalidate()
	}
	if story := m.ReplyToStory; story != nil && m.ReplyToMessageID == 0 && r.reply.Clicked(gtx) && p.openStoryOf != nil {
		p.openStoryOf(*story)
	}
	if m.Service != nil && m.Service.Kind == model.ServiceStoryMention && r.reply.Clicked(gtx) && m.StoryShown() && p.openStory != nil {
		p.openStory(m)
	}
	for {
		e, ok := r.quick.Update(gtx.Source)
		if !ok {
			break
		}
		if e.Kind == gesture.KindClick && e.NumClicks == 2 {
			p.quickReact(m)
		}
	}
	if m.Service != nil && m.Service.Kind == model.ServiceHidden && !date {
		// Telegram Desktop shows nothing for it, as a group's migration.
		r.bodySize = image.Point{}
		return layout.Dimensions{}
	}
	top, bottom := unit.Dp(4), unit.Dp(4)
	if join&joinAbove != 0 {
		top = 1
	}
	if join&joinBelow != 0 {
		bottom = 1
	}
	dateHeight := 0
	record := op.Record(gtx.Ops)
	dims := layout.Inset{Top: top, Bottom: bottom, Left: rowInsetLeft, Right: rowInsetRight}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !date {
					return layout.Dimensions{}
				}
				dims := layout.Inset{Top: 10, Bottom: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return datePill(gtx, m.Date.Local().Format("02.01.2006"))
					})
				})
				dateHeight = dims.Size.Y
				return dims
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				align := layout.W
				if m.Outgoing {
					align = layout.E
				}
				if m.ServicePill() {
					dims := layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return p.servicePill(gtx, r, m, l)
					})
					r.bodySize = dims.Size
					return dims
				}
				return align.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(bubbleMaxWidth))
					gtx.Constraints.Min = image.Point{}
					bodyDims := p.withSenderAvatar(gtx, m, func(gtx layout.Context) layout.Dimensions {
						if p.unwrapped(m) {
							return p.unwrappedBody(gtx, r, m, join, l, animate)
						}
						if len(m.Buttons) > 0 {
							return p.withKeyboard(gtx, r, m, join, l, func(gtx layout.Context) layout.Dimensions {
								return p.bubble(gtx, r, m, join, l, animate)
							})
						}
						return p.bubble(gtx, r, m, join, l, animate)
					})
					r.bodySize = bodyDims.Size
					return bodyDims
				})
			}),
		)
	})
	call := record.Stop()
	if p.selection.selected[m.Key.MessageID] && !p.snapshotting {
		fillRect(gtx, scheme(gtx).Primary.Color.SetOpacity(.13), dims.Size)
	} else if p.highlight == m.Key.MessageID && gtx.Now.Before(p.highlightUntil) && !p.snapshotting {
		// A message a search went to, which fades out.
		left := float32(p.highlightUntil.Sub(gtx.Now)) / float32(highlightTime)
		fillRect(gtx, scheme(gtx).Primary.Color.SetOpacity(token.OpacityLevel(.2*min(1, 2*left))), dims.Size)
		gtx.Execute(op.InvalidateCmd{})
	}
	call.Add(gtx.Ops)
	r.bodyTop = gtx.Dp(top) + dateHeight
	x := gtx.Dp(rowInsetLeft)
	if m.Outgoing {
		x = max(x, gtx.Constraints.Max.X-gtx.Dp(rowInsetRight)-r.bodySize.X)
	}
	r.bodyLeft = x
	// The avatar is at the start of an incoming message, at the end of a
	// sent one.
	if m.Outgoing {
		x += max(0, r.bodySize.X-gtx.Dp(34))
	}
	r.avatarPoint = image.Pt(x, r.bodyTop+r.bodySize.Y-gtx.Dp(34))
	if r.capture.on {
		r.capture.rest = dims.Size.Y - r.bodySize.Y
	}
	return dims
}

// A row of the history is rowInsetLeft and rowInsetRight in from its
// edges; a bubble is at most bubbleMaxWidth wide, a service message's
// plate serviceMaxWidth.
const (
	rowInsetLeft    unit.Dp = 16
	rowInsetRight   unit.Dp = 20
	bubbleMaxWidth  unit.Dp = 660
	serviceMaxWidth unit.Dp = 480
)

// rowsWidth is the width of the history past which no row is laid out
// wider: its rows are as high in any history wider than it, so the heights
// of rows are kept under the history's width up to it (layoutWidth).
func rowsWidth(m unit.Metric) int {
	return m.Dp(rowInsetLeft) + m.Dp(rowInsetRight) + max(m.Dp(bubbleMaxWidth), m.Dp(serviceMaxWidth))
}

// layoutWidth is the width the heights of rows in a history width px wide
// are kept under: rows are as high in any history wider than rowsWidth.
func layoutWidth(m unit.Metric, width int) int {
	return min(width, rowsWidth(m))
}

// bubblePadTop is the space over what a bubble holds, bubblePadSide
// beside it and bubblePadBottom under it.
const (
	bubblePadTop    unit.Dp = 8
	bubblePadSide   unit.Dp = 12
	bubblePadBottom unit.Dp = 7
)

// mediaInset is the space between the edges of a bubble and a photo, a
// video or an album of them in it: the gap between the pictures of an
// album, as Telegram for Android leaves it. Over media at the top of a
// bubble it is the space over it too.
const mediaInset = albumGap

// bubble draws a message's bubble and what it holds.
func (p *chatPage) bubble(gtx layout.Context, r *messageRow, m model.Message, join bubbleJoin, l localization.Catalog, animate bool) layout.Dimensions {
	shape := shapeOf(gtx, join, m.Outgoing)
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			size := gtx.Constraints.Min
			col := scheme(gtx).Surface.Color
			if m.Outgoing {
				col = scheme(gtx).PrimaryContainer.Color
			}
			paint.FillShape(gtx.Ops, col.AsNRGBA(), shape.rrect(size).Op(gtx.Ops))
			if p.appearance != nil {
				p.appearance.Bubble(gtx, m.Outgoing, animate, shape)
			}
			// A double click on the bubble, where nothing over it takes
			// clicks, reacts to it.
			area := shape.rrect(size).Push(gtx.Ops)
			r.quick.Add(gtx.Ops)
			area.Pop()
			return layout.Dimensions{Size: size}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			content := func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: bubblePadTop, Bottom: bubblePadBottom, Left: bubblePadSide, Right: bubblePadSide}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return p.bubbleBody(gtx, r, m, join, l, animate)
				})
			}
			if !p.showsComments(m) {
				return content(gtx)
			}
			// The comments bar spans the bubble under what it holds.
			dims := content(gtx)
			natural := commentsNatural(gtx, m, l)
			width := max(dims.Size.X, min(gtx.Constraints.Max.X, natural))
			bar := gtx.Dp(commentsBarHeight)
			if r.capture.on {
				r.capture.bar, r.capture.barNatural = bar, natural
			}
			size := image.Pt(width, dims.Size.Y+bar)
			defer shape.rrect(size).Push(gtx.Ops).Pop()
			offset(gtx, image.Pt(0, dims.Size.Y), func(gtx layout.Context) layout.Dimensions {
				return p.commentsBar(gtx, r, m, width, l)
			})
			return layout.Dimensions{Size: size}
		}),
	)
}

// bubblePartKind is what a part of a bubble is, for bubbleBody to place it.
type bubblePartKind uint8

const (
	// partWidget is drawn by its widget, as it would be alone.
	partWidget bubblePartKind = iota
	// partMedia is a photo, a video, an album or a file, drawn by its
	// widget; a text under one narrower than it wraps to keep the bubble
	// narrow (captionWidth).
	partMedia
	// partSpace is a gap of its space.
	partSpace
	// partText is the message's text, whose last line the time may go on.
	partText
	// partArticle is a rich message's article; what is over it is measured,
	// for links to its anchors.
	partArticle
	// partReply is the quote of the message replied to, which takes two
	// lines where one would make the bubble much wider than the rest.
	partReply
	// partReactions are the reactions, wrapped to keep the bubble narrow,
	// with the time on their last row when it fits there.
	partReactions
)

// bubblePart is one of what a bubble holds, top to bottom.
type bubblePart struct {
	kind   bubblePartKind
	space  unit.Dp
	widget layout.Widget
	// edge is set for media that come mediaInset from the bubble's edges,
	// not as far as the rest of what it holds: a photo, a video or a GIF,
	// or an album of them.
	edge bool
	// row is the row of a caption of an album's member, whose text the
	// part is: the message's own row for any other.
	row *messageRow
}

// laidPart is a part of a bubble laid out, to be drawn where it goes: x
// from the left of the rest of what the bubble holds.
type laidPart struct {
	call op.CallOp
	size image.Point
	x    int
}

// edgeMedia tells whether media of kind come to the edges of a bubble, as
// the tiles of an album do: a picture, not a file, a sticker or a round
// video.
func edgeMedia(kind model.MessageKind) bool {
	switch kind {
	case model.MessagePhoto, model.MessageVideo, model.MessageGIF:
		return true
	}
	return false
}

func bubbleWidget(w layout.Widget) bubblePart { return bubblePart{widget: w} }
func bubbleGap(dp unit.Dp) bubblePart         { return bubblePart{kind: partSpace, space: dp} }

// bubbleContent is what a bubble holds, top to bottom: the sender, where a
// forward came from, the message replied to, media, the text, buttons and
// reactions; bubbleBody adds the line of its time.
func (p *chatPage) bubbleContent(gtx layout.Context, r *messageRow, m model.Message, join bubbleJoin, l localization.Catalog, animate bool) []bubblePart {
	// The closures below, kept in what this returns, read the message from
	// the row: captured, m itself would be moved to the heap, 480 bytes for
	// each message drawn in each frame.
	r.shown = m
	msg := &r.shown
	sc := scheme(gtx)
	// The row keeps what it held last, for this frame to fill again: grown
	// from nothing, the list was made anew in each frame.
	parts := r.parts[:0]
	defer func() { r.parts = parts[:0] }()
	// As Telegram Desktop's Message::hasFromName: a chat with one person,
	// a bot or Saved Messages names no sender over a message, whatever
	// Telegram told of it; where a forward came from says the rest.
	private := p.kind == model.KindUser || p.kind == model.KindBot || p.kind == model.KindSaved
	if msg.SenderName != "" && !msg.Outgoing && !private && join&joinAbove == 0 {
		parts = append(parts, bubbleWidget(func(gtx layout.Context) layout.Dimensions {
			return p.layoutSenderName(gtx, msg)
		}), bubbleGap(3))
	}
	if msg.StoryOf != nil && !msg.StoryOf.Mention {
		// Whose story it is, as Telegram Desktop heads one: it drops the
		// forward's own.
		parts = append(parts, bubbleWidget(func(gtx layout.Context) layout.Dimensions {
			text := l.Format("history.story_from", map[string]string{"user": msg.StoryOf.From})
			return label(gtx, text, token.TypestyleLabelMedium, sc.Primary.Color, 1)
		}), bubbleGap(4))
	} else if !msg.ForwardDate.IsZero() {
		parts = append(parts, bubbleWidget(func(gtx layout.Context) layout.Dimensions {
			return p.forwardLine(gtx, r, *msg, l)
		}), bubbleGap(4))
	}
	// In a thread, as the comments to a post, what replies to its root
	// quotes nothing, as in Telegram Desktop: all of it does.
	if msg.ReplyToMessageID != 0 && msg.ReplyToMessageID != p.threadRoot || msg.ReplyToStory != nil {
		parts = append(parts, bubblePart{kind: partReply}, bubbleGap(6))
	}
	if len(msg.Attachments) > 1 {
		parts = append(parts, bubblePart{kind: partMedia, edge: tiledAlbum(msg.Attachments), widget: func(gtx layout.Context) layout.Dimensions { return p.albumLayout(gtx, r, *msg, l, animate) }}, bubbleGap(6))
	} else if msg.Media != nil {
		parts = append(parts, bubblePart{kind: partMedia, edge: edgeMedia(msg.Kind), widget: func(gtx layout.Context) layout.Dimensions { return p.mediaLayout(gtx, r, *msg, l, animate) }}, bubbleGap(6))
	}
	if msg.Poll != nil {
		parts = append(parts, bubbleWidget(func(gtx layout.Context) layout.Dimensions { return p.pollLayout(gtx, msg, l) }))
	}
	if msg.Todo != nil {
		parts = append(parts, bubbleWidget(func(gtx layout.Context) layout.Dimensions { return p.todoLayout(gtx, msg, l) }))
	}
	if r.article != nil {
		parts = append(parts, bubblePart{kind: partArticle, widget: func(gtx layout.Context) layout.Dimensions { return p.articleLayout(gtx, r, *msg, l, animate) }})
		if r.article.part && p.openArticle != nil {
			parts = append(parts, bubbleGap(6), bubbleWidget(func(gtx layout.Context) layout.Dimensions { return p.showMore(gtx, r, *msg, l) }))
		}
	} else if len(msg.Attachments) > 1 {
		seen := map[string]bool{}
		for _, member := range msg.Attachments {
			if member.Text != "" && !seen[member.Text] {
				seen[member.Text] = true
				member := member
				// A caption of the album, wrapped as narrow as the
				// album lets it be, as the message's own (captionWidth).
				child := albumRow(gtx, r, member, l)
				parts = append(parts, bubblePart{kind: partText, row: child, widget: func(gtx layout.Context) layout.Dimensions {
					// The caption's profile is the row's.
					child.capture = rowCapture{on: r.capture.on}
					p.refreshStamps(gtx, child, member, l)
					return p.richText(gtx, child, l, animate)
				}}, bubbleGap(4))
			}
		}
	} else if msg.Text != "" {
		parts = append(parts, bubblePart{kind: partText, widget: func(gtx layout.Context) layout.Dimensions { return p.richText(gtx, r, l, animate) }})
		// The time may go on its last line; not while a bot streams it.
		r.textLast = !msg.Streaming
	} else if msg.Media == nil && msg.Poll == nil && msg.Todo == nil {
		parts = append(parts, bubbleWidget(func(gtx layout.Context) layout.Dimensions {
			if msg.Kind == model.MessageService {
				return label(gtx, p.serviceText(*msg, l), token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
			}
			text := l.T("history.empty_message")
			if story := msg.StoryOf; story != nil {
				// Gone, or not told yet: the store asks for it.
				text = l.T("history.story")
				if story.Expired {
					text = l.T("history.story_expired")
				}
			}
			if msg.Rich != nil {
				if kind := msg.Rich.Fallback(); kind != "" {
					text = l.T("rich." + kind)
				}
			}
			return label(gtx, text, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}))
	}
	if msg.WebPage != nil && msg.Rich == nil {
		r.textLast = false
		parts = append(parts, bubbleGap(6), bubbleWidget(func(gtx layout.Context) layout.Dimensions { return p.webPreview(gtx, r, *msg, l, animate) }))
	}
	if len(msg.Reactions) > 0 {
		r.textLast = false
		parts = append(parts, bubbleGap(8), bubblePart{kind: partReactions})
	}
	return parts
}

// captionFit is the width a caption under media wraps at, as captionWidth
// found it, for the text, the width and the scale it was found for.
type captionFit struct {
	revision uint64
	most     int
	least    int
	metric   unit.Metric
	width    int
	// height is how high the text is at width: a text laid out higher, as
	// in another font, is measured again.
	height int
}

// check forgets the width when the text, laid out at it, is not as high as
// it was measured: it is found again in the next frame.
func (c *captionFit) check(height int) {
	if c.width > 0 && height != c.height {
		*c = captionFit{}
	}
}

// captionWidth is the width the text of r, under media least wide, wraps
// at: the narrowest, but not narrower than the media, that keeps it on as
// many lines as the whole width does, so that a caption of two lines under
// a narrow video leaves no half of the bubble empty beside the video; or,
// where a line more saves a good deal of the width, as for the reactions
// (reactionFit), the narrowest on a line more, but not narrower than
// minCaptionWidth. A text of quotes or code keeps the whole width. It is
// measured once, with the layout of the text, and kept in the row.
func (p *chatPage) captionWidth(gtx layout.Context, r *messageRow, l localization.Catalog, animate bool, least int) int {
	most := gtx.Constraints.Max.X
	c := &r.caption
	if c.width > 0 && c.revision == r.revision && c.most == most && c.least == least && c.metric == gtx.Metric {
		return c.width
	}
	r.prepareTextBlocks()
	for _, b := range r.textBlocks {
		// A quote that does not fold is measured as quoteBlock lays it
		// out; code, or a quote with its button, keeps the whole width.
		if run := r.runs[b.first]; run.Block != 0 && (!run.Quote || run.Collapsed) {
			return most
		}
	}
	line := 0
	height := func(w int) int {
		mgtx := gtx
		mgtx.Constraints = layout.Constraints{Max: image.Pt(w, gtx.Constraints.Max.Y)}
		keep := len(r.text.fragments)
		macro := op.Record(gtx.Ops)
		h := 0
		for i := range r.textBlocks {
			b := &r.textBlocks[i]
			if b.end-b.first == 1 && r.runs[b.first].Block == 0 && r.runs[b.first].Text == "\n" && (b.trimStart || b.trimEnd) {
				continue
			}
			if h > 0 {
				h += gtx.Dp(6)
			}
			if r.runs[b.first].Block == 0 {
				h += p.textFlow(mgtx, r, b, image.Pt(0, h), animate).Size.Y
				continue
			}
			// A quote, as quoteBlock: padded, and its whole text.
			pad, top := min(gtx.Dp(10), w/2), gtx.Dp(6)
			qgtx := mgtx
			qgtx.Constraints.Max.X = max(1, w-2*pad)
			b.maxLines = 0
			h += p.textFlow(qgtx, r, b, image.Pt(pad, h+top), animate).Size.Y + 2*top
		}
		macro.Stop()
		for _, f := range r.text.fragments[keep:] {
			line = max(line, f.Line.Dy())
		}
		r.text.fragments = r.text.fragments[:keep]
		return h
	}
	width := captionFitWidth(gtx.Metric, most, least, func() int { return line }, height)
	*c = captionFit{revision: r.revision, most: most, least: least, metric: gtx.Metric, width: width, height: height(width)}
	return width
}

// captionFitWidth is the width a caption wraps at under media least wide,
// in a bubble most wide (captionWidth): height tells how high the text is
// at a width, and line how high its highest line laid out so far is. The
// history's row profiles (row_predict.go) find it at another width with it.
func captionFitWidth(m unit.Metric, most, least int, line func() int, height func(int) int) int {
	// narrowest is the narrowest width from floor on, to a few pixels,
	// where the text is no higher than limit.
	narrowest := func(floor, limit int) int {
		lo, hi := min(floor, most), most
		if height(lo) <= limit {
			return lo
		}
		for hi-lo > m.Dp(4) {
			mid := (lo + hi) / 2
			if height(mid) <= limit {
				hi = mid
			} else {
				lo = mid
			}
		}
		return hi
	}
	full := height(most)
	width := narrowest(least, full)
	if more := narrowest(max(least, m.Dp(minCaptionWidth)), full+line()); more <= width-max(m.Dp(64), width/4) {
		width = more
	}
	return width
}

// minCaptionWidth is the narrowest a caption takes a line more to be.
const minCaptionWidth unit.Dp = 200

// footerGap is the least space between the end of a text's last line and
// the time beside it, Telegram Desktop's msgDateSpace less msgDateDelta.x.
const footerGap unit.Dp = 10

// footerSpace is the space over the time on a line of its own.
const footerSpace unit.Dp = 4

// bubbleBody lays out what a bubble holds, and its time at the bottom
// right: on the text's last line, or the last row of the reactions, when
// it fits there with footerGap before it, the bubble widening for it as far
// as it may, as Telegram Desktop and Telegram for Android place it (lib_ui's
// skip block, the reactions' InlineList::updateSkipBlock); on a line of its
// own otherwise. What the time says, the seconds and the edited mark
// included, sets its width.
//
// Photos and videos come mediaInset from the bubble's edges (edgeMedia),
// at its top too when nothing is over them, their corners there as round
// as the bubble's less the inset.
//
// What would make the bubble much wider than the rest of what it holds
// wraps to keep it narrow: a caption under narrower media as narrow as it
// keeps its lines, the reactions onto more rows, the quote of the message
// replied to onto two lines.
func (p *chatPage) bubbleBody(gtx layout.Context, r *messageRow, m model.Message, join bubbleJoin, l localization.Catalog, animate bool) layout.Dimensions {
	r.textLast = false
	parts := p.bubbleContent(gtx, r, m, join, l, animate)
	most := gtx.Constraints.Max.X
	cgtx := gtx
	cgtx.Constraints.Min = image.Point{}
	// What takes its own width is laid out first; a caption, the reply and
	// the reactions then fit around it.
	laid := r.laid[:0]
	defer func() { r.laid = laid[:0] }()
	base, reply, reactions := 0, -1, -1
	media := false
	for _, part := range parts {
		media = media || part.kind == partMedia
	}
	// out is how far media go past the side of the rest; lift, how far
	// media at the top go over it.
	out := gtx.Dp(bubblePadSide) - gtx.Dp(mediaInset)
	lift := 0
	r.corners = tileCorners{}
	for _, part := range parts {
		if part.kind == partSpace {
			continue
		}
		if part.edge {
			lift = gtx.Dp(bubblePadTop) - gtx.Dp(mediaInset)
			shape := shapeOf(gtx, join, m.Outgoing)
			inset := gtx.Dp(mediaInset)
			r.corners = tileCorners{nw: max(1, shape.nw-inset), ne: max(1, shape.ne-inset)}
		}
		break
	}
	// Media that end the bubble carry its time on a plate in their corner,
	// as Telegram for Android puts it, where it fits there: the tile tells
	// (timePlate).
	r.time = timePlate{}
	last := -1
	for i, part := range parts {
		if part.kind != partSpace {
			last = i
		}
	}
	if last >= 0 && parts[last].edge && !m.Streaming {
		macro := op.Record(gtx.Ops)
		size := p.mediaFooter(cgtx, r, l).Size
		r.time = timePlate{call: macro.Stop(), size: size}
		// Where the time goes on them, the media end the bubble: their
		// bottom corners are round as its, but over the comments bar.
		if !p.showsComments(m) {
			shape := shapeOf(gtx, join, m.Outgoing)
			inset := gtx.Dp(mediaInset)
			r.corners.se, r.corners.sw = max(1, shape.se-inset), max(1, shape.sw-inset)
		}
	}
	egtx := cgtx
	egtx.Constraints.Max.X += 2 * out
	for i, part := range parts {
		var lp laidPart
		switch {
		case part.kind == partSpace:
			lp.size.Y = gtx.Dp(part.space)
		case part.kind == partReply:
			reply = i
		case part.kind == partReactions:
			reactions = i
		case part.kind == partText && media:
			// Laid out next, around the media.
		case part.edge:
			macro := op.Record(gtx.Ops)
			lp.size = part.widget(egtx).Size
			lp.call = macro.Stop()
			lp.x = -out
			base = max(base, lp.size.X-2*out)
		default:
			macro := op.Record(gtx.Ops)
			lp.size = part.widget(cgtx).Size
			lp.call = macro.Stop()
			base = max(base, lp.size.X)
		}
		laid = append(laid, lp)
	}
	// The captions, the message's own or its album's.
	captions := base
	for i, part := range parts {
		if part.kind != partText || !media {
			continue
		}
		tr := r
		if part.row != nil {
			tr = part.row
		}
		tgtx := cgtx
		tgtx.Constraints.Max.X = p.captionWidth(cgtx, tr, l, animate, captions)
		macro := op.Record(gtx.Ops)
		laid[i].size = part.widget(tgtx).Size
		laid[i].call = macro.Stop()
		tr.caption.check(laid[i].size.Y)
		base = max(base, laid[i].size.X)
	}
	onMedia := r.time.drawn
	macro := op.Record(gtx.Ops)
	footer := p.bubbleFooter(cgtx, r, l)
	foot := macro.Stop()
	if !onMedia {
		base = max(base, footer.Size.X)
	}
	right, bottom, onText := r.lastLine()
	onText = onText && r.textLast
	if onText {
		if end := right + gtx.Dp(footerGap) + footer.Size.X; end <= most {
			base = max(base, end)
		} else {
			onText = false
		}
	}
	var fit reactionFit
	if reactions >= 0 {
		chips := p.measureReactions(cgtx, r, m, animate)
		fit = newReactionFit(gtx, r, chips, base, most, footer.Size)
	}
	if reply >= 0 {
		// The reply wraps to the width the reactions keep the bubble at,
		// when they keep it wider than the rest.
		within := base
		if reactions >= 0 {
			_, within, _ = fit.at(fit.choose())
		}
		macro := op.Record(gtx.Ops)
		laid[reply].size = p.replyQuote(cgtx, r, m, l, within).Size
		laid[reply].call = macro.Stop()
		base = max(base, laid[reply].size.X)
	}
	onChips := false
	if reactions >= 0 {
		fit.base = base
		w := fit.choose()
		_, _, onChips = fit.at(w)
		macro := op.Record(gtx.Ops)
		laid[reactions].size = p.placeReactions(cgtx, r, r.chips, w).Size
		laid[reactions].call = macro.Stop()
	}
	y, width, textTop, chipsTop, mediaBottom := -lift, 0, 0, 0, 0
	for i, lp := range laid {
		if i == last {
			mediaBottom = y + lp.size.Y
		}
		switch parts[i].kind {
		case partSpace:
			y += lp.size.Y
			continue
		case partText:
			textTop = y
		case partArticle:
			r.articleAbove = y
		case partReactions:
			chipsTop = y
		}
		stack := op.Offset(image.Pt(lp.x, y)).Push(gtx.Ops)
		lp.call.Add(gtx.Ops)
		stack.Pop()
		y += lp.size.Y
		width = max(width, lp.size.X+2*lp.x)
	}
	if r.capture.on {
		c := &r.capture
		c.bubble, c.footer, c.onText, c.plate, c.textLast = true, footer.Size, onText, r.time.size, r.textLast
		if reactions >= 0 {
			c.chips, c.chipRow = append(c.chips[:0], fit.widths...), fit.row
		}
		c.parts = c.parts[:0]
		for i, part := range parts {
			c.parts = append(c.parts, p.partProfile(r, part, laid[i].size, most))
		}
	}
	if onMedia {
		// The media end the bubble, mediaInset from its bottom as from its
		// sides, the time on them.
		return layout.Dimensions{Size: image.Pt(width, mediaBottom-(gtx.Dp(bubblePadBottom)-gtx.Dp(mediaInset)))}
	}
	width = max(width, footer.Size.X)
	at := image.Pt(0, y+gtx.Dp(footerSpace))
	height := at.Y + footer.Size.Y
	switch {
	case onText:
		// The text is the last of what the bubble holds.
		width = max(width, right+gtx.Dp(footerGap)+footer.Size.X)
		at.Y = textTop + bottom - footer.Size.Y
		height = y
	case onChips:
		// The reactions are: the time is in the middle of their last row.
		row := r.lastChips
		width = max(width, row.Max.X+gtx.Dp(footerGap)+footer.Size.X)
		at.Y = chipsTop + row.Min.Y + (row.Dy()-footer.Size.Y)/2
		height = y
	}
	at.X = width - footer.Size.X
	stack := op.Offset(at).Push(gtx.Ops)
	foot.Add(gtx.Ops)
	stack.Pop()
	return layout.Dimensions{Size: image.Pt(width, height)}
}

// timePlate is the time of a message on a plate, for the tile of media
// that end its bubble to draw in its corner: recorded by the bubble, and
// drawn, when the tile has room for it, by mediaTile, which sets drawn.
type timePlate struct {
	call  op.CallOp
	size  image.Point
	drawn bool
}

// mediaFooter is the line of the time of the message r shows on a plate,
// as on media: the footer's, in the colors of the plates over pictures.
func (p *chatPage) mediaFooter(gtx layout.Context, r *messageRow, l localization.Catalog) layout.Dimensions {
	msg := &r.shown
	sc := scheme(gtx)
	macro := op.Record(gtx.Ops)
	dims := layout.Inset{Top: 2, Bottom: 2, Left: 7, Right: 7}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return messageFooterIn(gtx, *msg, l, !p.showsComments(*msg), p.sendState(gtx, *msg), sc.InverseSurface.OnColor)
	})
	call := macro.Stop()
	fillRounded(gtx, sc.InverseSurface.Color.SetOpacity(0.55), dims.Size, dims.Size.Y/2)
	call.Add(gtx.Ops)
	return dims
}

// bubbleFooter is the line of the time of the message r shows, which it
// reads from the row, as bubbleContent does.
func (p *chatPage) bubbleFooter(gtx layout.Context, r *messageRow, l localization.Catalog) layout.Dimensions {
	msg := &r.shown
	if msg.Streaming {
		// A draft a bot streams turns a ring before its time while the
		// bot writes it.
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.writing.sized(gtx, l, 12) }),
			layout.Rigid(layout.Spacer{Width: 6}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return messageFooter(gtx, *msg, l, false, model.SendNone) }))
	}
	return messageFooter(gtx, *msg, l, !p.showsComments(*msg), p.sendState(gtx, *msg))
}

// messageFooter is the line at the bottom of a bubble: comments and views
// of a post, its author, whether it was edited, its time, and the mark of
// state, of a message the account sent. The comments are left to the
// comments bar when it is not counted here.
func messageFooter(gtx layout.Context, m model.Message, l localization.Catalog, countComments bool, state model.SendState) layout.Dimensions {
	return messageFooterIn(gtx, m, l, countComments, state, scheme(gtx).SurfaceVariant.OnColor)
}

// messageFooterIn is messageFooter in col.
func messageFooterIn(gtx layout.Context, m model.Message, l localization.Catalog, countComments bool, state model.SendState, col token.MatColor) layout.Dimensions {
	var items []layout.FlexChild
	text := func(s string) {
		items = append(items, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, s, token.TypestyleLabelSmall, col, 1)
		}))
	}
	counter := func(icon wdk.IconWidget, n int) {
		items = append(items, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(gtx.Dp(14), gtx.Dp(14)), func(gtx layout.Context) layout.Dimensions { return icon(gtx, col) })
		}), layout.Rigid(layout.Spacer{Width: 3}.Layout))
		text(shortCount(n))
		items = append(items, layout.Rigid(layout.Spacer{Width: 8}.Layout))
	}
	if m.Comments > 0 && countComments {
		counter(iconComments, m.Comments)
	}
	if m.Views > 0 {
		counter(iconViews, m.Views)
	}
	text(footerText(gtx, m, l))
	if state != model.SendNone {
		items = append(items, layout.Rigid(layout.Spacer{Width: 3}.Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sendStateMark(gtx, state, sendStateMarkSize, col)
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, items...)
}

// footerText is the line of a message's time: who signed it, whether it
// was edited or deleted, with the look's marks, and the time.
func footerText(gtx layout.Context, m model.Message, l localization.Catalog) string {
	look := lookOf(gtx)
	key := footerKey{date: m.Date.Unix(), format: timeFormat(gtx), author: m.PostAuthor}
	_, key.zone = m.Date.Local().Zone()
	if !m.EditedAt.IsZero() {
		key.edited = look.EditedMark
		if key.edited == "" {
			key.edited = l.T("history.edited")
		}
	}
	if m.Deleted {
		key.deleted = look.DeletedMark
		if key.deleted == "" {
			key.deleted = l.T("history.deleted_mark")
		}
	}
	footers.mu.Lock()
	defer footers.mu.Unlock()
	if text, ok := footers.texts[key]; ok {
		return text
	}
	if footers.texts == nil || len(footers.texts) >= footersKept {
		footers.texts = make(map[footerKey]string)
	}
	text := writeFooter(m, key)
	footers.texts[key] = text
	return text
}

// footerKey is what the line of a message's time is made of.
type footerKey struct {
	date            int64
	zone            int
	format, author  string
	edited, deleted string
}

// footersKept is how many lines of time footers keeps before it starts
// afresh: those of the messages of a few screens.
const footersKept = 1024

// footers keeps the lines of time written, which every message shown asked
// for in every frame, each a slice and two strings made anew. Windows ask
// from their own goroutines.
var footers struct {
	mu    sync.Mutex
	texts map[footerKey]string
}

// writeFooter writes the line of time of m that key describes.
func writeFooter(m model.Message, key footerKey) string {
	var parts []string
	if key.author != "" {
		parts = append(parts, key.author)
	}
	if key.edited != "" {
		parts = append(parts, key.edited)
	}
	if key.deleted != "" {
		// Kept after Telegram deleted it, with AyuGram's mark.
		parts = append(parts, key.deleted)
	}
	parts = append(parts, m.Date.Local().Format(key.format))
	return strings.Join(parts, " · ")
}

// shortCount writes a count as Telegram does: 1.2K, 3.4M.
func shortCount(n int) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + "M"
	case n >= 10_000:
		return strconv.Itoa(n/1000) + "K"
	case n >= 1000:
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "K"
	}
	return strconv.Itoa(n)
}

// reactions draws the reactions to m as chips that wrap onto more lines, in
// Telegram Desktop's order; the account's own are in the primary color. A
// click on a chip chooses the reaction or takes it back, as in Telegram
// Desktop; the paid one is left to clients that pay.
func (p *chatPage) reactions(gtx layout.Context, r *messageRow, m model.Message, animate bool) layout.Dimensions {
	chips := p.measureReactions(gtx, r, m, animate)
	return p.placeReactions(gtx, r, chips, gtx.Constraints.Max.X)
}

// reactionChip is the chip of a reaction laid out, before it is placed.
type reactionChip struct {
	call                op.CallOp
	size                image.Point
	background, content token.MatColor
	// still is set for a chip that takes no clicks: the paid reaction's,
	// or any where the account cannot react.
	still bool
}

// measureReactions lays out the chips of the reactions to m, which the row
// keeps, and chooses or takes back the reaction of one clicked.
func (p *chatPage) measureReactions(gtx layout.Context, r *messageRow, m model.Message, animate bool) []reactionChip {
	var rank func(model.Reaction) (int, bool)
	if orderer, ok := p.source.(model.ReactionOrderer); ok {
		rank = orderer.ReactionRank
	}
	r.shownReactions = model.ShownReactions(r.shownReactions, m.Reactions, rank)
	reactions := r.shownReactions
	sc := scheme(gtx)
	for len(r.reactions) < len(reactions) {
		r.reactions = append(r.reactions, surface{})
	}
	reactor, canReact := p.source.(model.Reactor)
	canReact = canReact && p.canReact(m)
	chips := r.chips[:0]
	for i, reaction := range reactions {
		if r.reactions[i].Clicked(gtx) && canReact && !reaction.Paid {
			reactor.ToggleReaction(m, reaction, p.reportMedia)
		}
		background, content := reactionChipColors(sc, reaction.Chosen, m.Outgoing)
		macro := op.Record(gtx.Ops)
		cgtx := gtx
		cgtx.Constraints = layout.Constraints{Max: image.Pt(gtx.Constraints.Max.X, gtx.Dp(40))}
		dims := layout.Inset{Top: 4, Bottom: 4, Left: 8, Right: 10}.Layout(cgtx, func(gtx layout.Context) layout.Dimensions {
			// An emoji of the fonts sits on the count's baseline, which puts
			// its middle at the digits', as in Telegram Desktop; a picture
			// is centered on them.
			align := layout.Baseline
			if reaction.DocumentID != 0 {
				align = layout.Middle
			}
			return layout.Flex{Alignment: align}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return p.reactionIcon(gtx, reaction, animate)
				}),
				layout.Rigid(layout.Spacer{Width: 5}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, shortCount(reaction.Count), token.TypestyleLabelLargeEmphasized, content, 1)
				}),
			)
		})
		chips = append(chips, reactionChip{call: macro.Stop(), size: dims.Size, background: background, content: content, still: !canReact || reaction.Paid})
	}
	r.chips = chips
	return chips
}

// reactionGap is the space between reaction chips, across and down.
const reactionGap unit.Dp = 6

// placeReactions draws chips in rows width wide, a chip that would go
// past the end starting the next, and keeps the last row in the row.
func (p *chatPage) placeReactions(gtx layout.Context, r *messageRow, chips []reactionChip, width int) layout.Dimensions {
	gap := gtx.Dp(reactionGap)
	x, y, lineHeight, widest := 0, 0, 0, 0
	for i, c := range chips {
		if x > 0 && x+c.size.X > width {
			x, y = 0, y+lineHeight+gap
			lineHeight = 0
		}
		chip := &r.reactions[i]
		offset(gtx, image.Pt(x, y), func(gtx layout.Context) layout.Dimensions {
			if c.still {
				fillRounded(gtx, c.background, c.size, c.size.Y/2)
				c.call.Add(gtx.Ops)
				return layout.Dimensions{Size: c.size}
			}
			style := surfaceStyle{radius: c.size.Y / 2, background: c.background, content: c.content}
			return chip.Layout(gtx, c.size, style, func(gtx layout.Context) layout.Dimensions {
				c.call.Add(gtx.Ops)
				return layout.Dimensions{Size: c.size}
			})
		})
		x += c.size.X
		widest = max(widest, x)
		lineHeight = max(lineHeight, c.size.Y)
		r.lastChips = image.Rect(0, y, x, y+lineHeight)
		x += gap
	}
	return layout.Dimensions{Size: image.Pt(widest, y+lineHeight)}
}

// reactionRows wraps chips of widths into rows w wide, gap apart, as
// placeReactions does, and tells how many rows they take, how wide the
// widest is and where the last ends.
func reactionRows(widths []int, gap, w int) (rows, widest, last int) {
	x := 0
	rows = 1
	for _, cw := range widths {
		if x > 0 && x+cw > w {
			rows++
			x = 0
		}
		x += cw
		widest = max(widest, x)
		last = x
		x += gap
	}
	return rows, widest, last
}

// firstRow is how wide the first row of chips of widths is, wrapped at w,
// gap apart.
func firstRow(widths []int, gap, w int) int {
	x := 0
	for _, cw := range widths {
		if x > 0 && x+cw > w {
			break
		}
		x += cw + gap
	}
	return max(0, x-gap)
}

// reactionFit chooses the width a bubble's reactions wrap at, from the
// widths of their chips, the width of the rest of what the bubble holds and
// the time's. Telegram Desktop wraps them at the widest a bubble may be;
// here they wrap where the bubble is narrowest without growing taller, so
// that the rows come out even, and, where the chips alone would make it
// much wider than the rest, onto a row more.
type reactionFit struct {
	widths []int
	// gap is between the chips, row the height of a row of them.
	gap, row int
	// base is how wide the rest of the bubble is, most the widest it may be.
	base, most int
	foot       image.Point
	// footGap is the space before the time beside the chips; footSpace, over
	// one on a line of its own.
	footGap, footSpace int
	// worth is the least width a row more must save.
	worth int
}

// minChipsPerRow is the fewest chips a row has on average where the
// reactions take a row more to keep a bubble narrow.
const minChipsPerRow = 2

func newReactionFit(gtx layout.Context, r *messageRow, chips []reactionChip, base, most int, foot image.Point) reactionFit {
	widths := r.chipWidths[:0]
	row := 0
	for _, c := range chips {
		widths = append(widths, c.size.X)
		row = max(row, c.size.Y)
	}
	r.chipWidths = widths
	return reactionFit{widths: widths, gap: gtx.Dp(reactionGap), row: row, base: base, most: most, foot: foot,
		footGap: gtx.Dp(footerGap), footSpace: gtx.Dp(footerSpace), worth: gtx.Dp(64)}
}

// at tells how high the reactions and the time under them are when the
// chips wrap at w, how wide the bubble is then, and whether the time is on
// the chips' last row: where it fits within w, or the rest of the bubble.
// It goes on a line of its own otherwise, which choose weighs as a row of
// chips more: a long signature of a post's author beside the chips made
// the bubble as wide as they and it, half of it empty over them.
func (f *reactionFit) at(w int) (height, width int, inline bool) {
	rows, widest, last := reactionRows(f.widths, f.gap, w)
	height = rows*f.row + (rows-1)*f.gap
	width = max(f.base, widest)
	if end := last + f.footGap + f.foot.X; end <= max(w, f.base) && end <= f.most {
		return height, max(width, end), true
	}
	return height + f.footSpace + f.foot.Y, max(width, f.foot.X), false
}

// choose is the width the chips wrap at. A row ends where a chip, or the
// time after the last, ends, so only the widths of runs of chips, with the
// time or without it, are tried. Of the ways no higher than the fewest
// rows take, and of those a row higher, each of those about as narrow as
// the narrowest of its kind, with the time on the chips' row rather than
// alone on a line, then the lowest: a line or a row more is taken for the
// width it saves, not for a few px. The higher is taken where it saves a
// fifth of the width, at least worth; worth alone where the reactions then
// keep to the rest of the bubble.
func (f *reactionFit) choose() int {
	least := 0
	for _, w := range f.widths {
		least = max(least, w)
	}
	if len(f.widths) == 0 || least >= f.most {
		return f.most
	}
	type pick struct {
		w, height, width int
		// inline is set where the time is on the chips' last row, and
		// settled where that row is no wider than the first.
		inline, settled bool
	}
	tallest, _, _ := f.at(f.most)
	fewest, _, _ := reactionRows(f.widths, f.gap, f.most)
	// each calls visit with every width to try, and how high and wide the
	// bubble is at it, and whether it is no higher than the fewest rows,
	// or a row higher.
	each := func(visit func(p pick, even, taller bool)) {
		try := func(w int) {
			if w < least || w > f.most {
				return
			}
			height, width, inline := f.at(w)
			// A row more of chips holds a couple each: one alone on its
			// row looks scattered, and saves little. The time on a line of
			// its own scatters none.
			rows, _, last := reactionRows(f.widths, f.gap, w)
			few := rows > fewest && len(f.widths) < minChipsPerRow*rows
			settled := last <= firstRow(f.widths, f.gap, w)
			visit(pick{w, height, width, inline, settled}, height <= tallest, height <= tallest+f.row+f.gap && !few)
		}
		try(f.most)
		try(f.base)
		for i := range f.widths {
			run := 0
			for j := i; j < len(f.widths) && run <= f.most; j++ {
				if j > i {
					run += f.gap
				}
				run += f.widths[j]
				try(run)
				try(run + f.footGap + f.foot.X)
			}
		}
	}
	narrowEven, narrowTaller := -1, -1
	each(func(p pick, even, taller bool) {
		if even && (narrowEven < 0 || p.width < narrowEven) {
			narrowEven = p.width
		}
		if taller && (narrowTaller < 0 || p.width < narrowTaller) {
			narrowTaller = p.width
		}
	})
	// better is whether q beats p: the time beside the chips, not alone on
	// a line under them, then a lower bubble, then a narrower one, then
	// the last row no wider than the first, then rows as even as they can
	// be: the chips wrapped as narrow as they go on as many rows, where the
	// rest of the bubble is wider than they.
	better := func(p, q pick) bool {
		switch {
		case p.w < 0:
			return true
		case q.inline != p.inline:
			return q.inline
		case q.height != p.height:
			return q.height < p.height
		case q.width != p.width:
			return q.width < p.width
		case q.settled != p.settled:
			return q.settled
		}
		return q.w < p.w
	}
	slack := f.worth / 2
	even, taller := pick{w: -1}, pick{w: -1}
	each(func(q pick, isEven, isTaller bool) {
		if isEven && q.width <= narrowEven+slack && better(even, q) {
			even = q
		}
		if isTaller && q.width <= narrowTaller+slack && better(taller, q) {
			taller = q
		}
	})
	// Where the reactions then keep to the rest of the bubble, worth alone
	// does.
	saves := max(f.worth, even.width/5)
	if taller.width <= f.base {
		saves = f.worth
	}
	if taller.w >= 0 && taller.width <= even.width-saves {
		return taller.w
	}
	return even.w
}

// reactionChipColors are the background and the content of a reaction's
// chip in a bubble of sc, as Telegram Desktop's: the bubble's accent,
// faint, under the accent's text (kInNonChosenOpacity,
// kOutNonChosenOpacity), and solid for the account's own. The bubble's
// colors are a chat theme's where it has one, which the app's containers
// are not: chips of those were darker than a dark theme's bubble.
func reactionChipColors(sc *token.Scheme, chosen, outgoing bool) (background, content token.MatColor) {
	if chosen {
		return sc.Primary.Color, sc.Primary.OnColor
	}
	faint := token.OpacityLevel(.12)
	if outgoing {
		faint = .18
	}
	return sc.Primary.Color.SetOpacity(faint), sc.Primary.Color
}

// reactionIcon draws a reaction's emoji, custom emoji or the star of paid
// reactions.
func (p *chatPage) reactionIcon(gtx layout.Context, reaction model.Reaction, animate bool) layout.Dimensions {
	size := gtx.Dp(18)
	if reaction.DocumentID != 0 && p.media != nil {
		if frame := customEmoji(p.media, p.images, reaction.DocumentID, size, animate, scheme(gtx).Surface.OnColor.AsNRGBA()); frame != nil {
			return drawImage(gtx, p.images, frame, image.Pt(size, size))
		}
		return layout.Dimensions{Size: image.Pt(size, size)}
	}
	emoji := reaction.Emoji
	if reaction.Paid {
		emoji = "⭐"
	}
	return label(gtx, emoji, token.TypestyleBodyLarge, scheme(gtx).Surface.OnColor, 1)
}

// replyQuote draws the message a message replies to, as a quote with a bar
// in its sender's color; a click shows it. Its text takes one line, cut
// where it ends, as in Telegram Desktop; two, where one would make the
// bubble much wider than the rest of what it holds, within wide.
func (p *chatPage) replyQuote(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog, within int) layout.Dimensions {
	if m.ReplyToStory != nil && m.ReplyToMessageID == 0 {
		return p.storyQuote(gtx, r, *m.ReplyToStory, l)
	}
	replied, state, known := p.referenced(m.Key.ChatID, m.ReplyToMessageID)
	name, text := l.T("history.reply_missing"), ""
	var runs []model.TextRun
	switch {
	case known && state == model.LookupLoading:
		text = l.T("service.loading")
	case known && state == model.LookupGone:
		text = l.T("service.deleted_message")
	}
	color := scheme(gtx).Primary.Color
	if state == model.LookupFound {
		if replied.SenderName != "" {
			name = replied.SenderName
		} else if replied.Post && p.title != "" {
			// A channel post, as the post at the top of its comments.
			name = p.title
		} else if replied.Outgoing {
			name = l.T("history.you")
		}
		if !replied.Outgoing {
			color = senderColor(gtx, replied.SenderID)
		}
		runs = quoteRuns(replied, l)
	}
	inset := layout.Inset{Top: 5, Bottom: 5, Left: 11, Right: 10}
	least := gtx.Dp(120)
	lines := 1
	if len(runs) > 0 {
		var w int
		var named, line, lineHeight int
		if lines, w, named, line, lineHeight = p.replyLines(gtx, name, runs, inset, least, within); lines > 1 {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, w)
		}
		if r.capture.on {
			// As wide as they are, which a narrower bubble cuts.
			_ = named
			natural := gtx
			natural.Constraints = layout.Constraints{Max: image.Pt(1<<20, gtx.Constraints.Max.Y)}
			macro := op.Record(gtx.Ops)
			named = label(natural, name, token.TypestyleLabelLargeEmphasized, color, 1).Size.X
			line = p.quoteLines(natural, runs, scheme(gtx).Surface.OnColor, 1).Size.X
			macro.Stop()
			r.capture.reply = replyProfile{named: named, line: line, lineHeight: lineHeight, lines: lines, pad: gtx.Dp(inset.Left + inset.Right), least: least}
		}
	}
	macro := op.Record(gtx.Ops)
	dims := inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, name, token.TypestyleLabelLargeEmphasized, color, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if runs != nil {
					return p.quoteLines(gtx, runs, scheme(gtx).Surface.OnColor, lines)
				}
				if text == "" {
					return layout.Dimensions{}
				}
				return label(gtx, text, token.TypestyleBodySmall, scheme(gtx).Surface.OnColor, 1)
			}),
		)
	})
	call := macro.Stop()
	size := image.Pt(max(dims.Size.X, least), dims.Size.Y)
	if r.capture.on {
		r.capture.reply.size = size
	}
	radius := gtx.Dp(6)
	style := surfaceStyle{radius: radius, background: color.SetOpacity(.12), content: color}
	return r.reply.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
		fillRect(gtx, color, image.Pt(gtx.Dp(3), size.Y))
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	})
}

// replyLines is how many lines the text of a quote takes, and how wide the
// quote is then: two when one, as wide as it is, would make the bubble
// wider than within by a good deal, and the two, about half as wide, save
// it. The name stays on its line whole.
//
// named and line are how wide the name and one line of the text are,
// lineHeight how high the line is.
func (p *chatPage) replyLines(gtx layout.Context, name string, runs []model.TextRun, inset layout.Inset, least, within int) (lines, width, named, line, lineHeight int) {
	pad := gtx.Dp(inset.Left + inset.Right)
	macro := op.Record(gtx.Ops)
	measure := gtx
	measure.Constraints = layout.Constraints{Max: image.Pt(max(0, gtx.Constraints.Max.X-pad), gtx.Constraints.Max.Y)}
	named = label(measure, name, token.TypestyleLabelLargeEmphasized, scheme(gtx).Primary.Color, 1).Size.X
	one := p.quoteLines(measure, runs, scheme(gtx).Surface.OnColor, 1).Size
	macro.Stop()
	line, lineHeight = one.X, one.Y
	lines, width = replyLineCount(gtx.Metric, named, line, pad, least, within)
	return lines, width, named, line, lineHeight
}

// replyLineCount is replyLines' choice for a quote whose name is named wide
// and text, on one line, line wide, pad its padding, least the narrowest
// it is: one line, or two where they save a good deal of the width within
// which one would go past.
func replyLineCount(m unit.Metric, named, line, pad, least, within int) (lines, width int) {
	one := max(named, line) + pad
	if one <= within {
		return 1, one
	}
	// Half the line, and room for a word that does not fit at the end of
	// the first.
	two := max(within, named+pad, least, line/2+m.Dp(24)+pad)
	// A line of small text is a fair price for an eighth of the width.
	if two > one-max(m.Dp(48), one/8) {
		return 1, one
	}
	return 2, two
}

// storyQuote draws the story a message replies to, as Telegram Desktop's
// reply to one: whose it is and the word; a click opens it.
func (p *chatPage) storyQuote(gtx layout.Context, r *messageRow, story model.StoryRef, l localization.Catalog) layout.Dimensions {
	color := senderColor(gtx, story.Peer)
	name := story.From
	if name == "" {
		name = l.T("history.story")
	}
	macro := op.Record(gtx.Ops)
	dims := layout.Inset{Top: 5, Bottom: 5, Left: 11, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, name, token.TypestyleLabelLargeEmphasized, color, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("history.story"), token.TypestyleBodySmall, scheme(gtx).Surface.OnColor, 1)
			}),
		)
	})
	call := macro.Stop()
	size := image.Pt(max(dims.Size.X, gtx.Dp(120)), dims.Size.Y)
	radius := gtx.Dp(6)
	style := surfaceStyle{radius: radius, background: color.SetOpacity(.12), content: color}
	return r.reply.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
		fillRect(gtx, color, image.Pt(gtx.Dp(3), size.Y))
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	})
}

// quoteRuns are the line a quote of m shows: what media it has and its
// text, on one line, with its custom emoji, which Telegram Desktop's
// quotes draw as they are: as plain text they would be their fallback
// emoji.
func quoteRuns(m model.Message, l localization.Catalog) []model.TextRun {
	var emoji []model.Entity
	for _, e := range m.Entities {
		if e.Kind == model.EntityEmoji {
			emoji = append(emoji, e)
		}
	}
	var runs []model.TextRun
	for _, r := range model.TextRuns(m.Text, emoji) {
		runs = append(runs, model.TextRun{Text: strings.ReplaceAll(r.Text, "\n", " "), Emoji: r.Emoji})
	}
	if len(runs) > 0 {
		runs[0].Text = strings.TrimLeft(runs[0].Text, " ")
	}
	if media := foundText(model.Message{Kind: m.Kind, Media: m.Media, StoryOf: m.StoryOf}, l); media != "" {
		if len(runs) > 0 {
			media += ", "
		}
		runs = append([]model.TextRun{{Text: media}}, runs...)
	}
	if len(runs) == 0 && m.Todo != nil {
		return []model.TextRun{{Text: m.Todo.Title}}
	}
	if len(runs) == 0 {
		return []model.TextRun{}
	}
	return runs
}

// quoteLine draws runs, a quote's line, in color.
func (p *chatPage) quoteLine(gtx layout.Context, runs []model.TextRun, color token.MatColor) layout.Dimensions {
	return p.quoteLines(gtx, runs, color, 1)
}

// quoteLines is quoteLine on up to lines lines, the text one paragraph.
func (p *chatPage) quoteLines(gtx layout.Context, runs []model.TextRun, color token.MatColor, lines int) layout.Dimensions {
	if len(runs) == 0 {
		return layout.Dimensions{}
	}
	return richLabel{runs: runs, style: token.TypestyleBodySmall, color: color, maxLines: lines, flat: true, media: p.media, images: p.images, animate: p.animate}.Layout(gtx)
}

// referenced is a message another one names, as the one it replies to:
// the loaded one, or else one the store looks up, which may be on its way
// or gone. known is false when the store cannot look it up.
func (p *chatPage) referenced(chat int64, id model.MessageID) (m model.Message, state model.LookupState, known bool) {
	if p.measuring {
		// Laid out off screen: nothing is looked up on Telegram for it.
		return p.measuredReference(id)
	}
	if m, ok := p.messageByID(id); ok {
		return m, model.LookupFound, true
	}
	if s, ok := p.source.(model.MessageLookup); ok && chat != 0 {
		m, state = s.LookupMessage(chat, id)
		return m, state, true
	}
	return model.Message{}, model.LookupGone, false
}

// messageByID is the loaded message with the id, a part of an album too.
func (p *chatPage) messageByID(id model.MessageID) (model.Message, bool) {
	for _, m := range p.messages {
		if m.Key.MessageID == id {
			return m, true
		}
		for _, part := range m.Attachments {
			if part.Key.MessageID == id {
				return part, true
			}
		}
	}
	return model.Message{}, false
}

// jumpTo shows the message with the id: scrolled to when it is loaded,
// loaded around otherwise.
func (p *chatPage) jumpTo(id model.MessageID) {
	if _, ok := p.messageByID(id); ok {
		p.restore(id, 0)
		p.list.Position.BeforeEnd = true
		p.invalidate()
		return
	}
	if r, ok := p.source.(model.MessageRevealer); ok {
		r.Reveal(p.chat, id)
		p.forget()
		p.invalidate()
	}
}

// forwardLine is the line of whom m, a forward, came from. Before the name
// is their avatar, as big as a custom emoji in the text, when Telegram told who
// they are, but in Saved Messages, which draws it beside the bubble, as
// Telegram Desktop's; a click on it opens their chat, as a group member's
// avatar does.
func (p *chatPage) forwardLine(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	text := l.T("history.forward")
	if m.ForwardName != "" {
		text = l.Format("history.forwarded_from", map[string]string{"name": m.ForwardName, "user": m.ForwardName})
	}
	line := func(gtx layout.Context) layout.Dimensions {
		if m.ForwardFromID == 0 || p.kind == model.KindSaved || p.avatar == nil {
			return label(gtx, text, token.TypestyleLabelMedium, sc.Primary.Color, 1)
		}
		// As big as a custom emoji in the message's text: one of this
		// small line's size was much smaller than those beside it.
		ty := wdk.GetMaterialTheme(gtx).Typescale[token.TypestyleBodyLarge]
		side := emojiBox(gtx, ty.Size).Size.X
		kind := model.KindUser
		if model.IsChannelID(m.ForwardFromID) {
			kind = model.KindChannel
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return p.avatar(gtx, m.ForwardFromID, kind, m.ForwardName, unit.Dp(float32(side)/gtx.Metric.PxPerDp))
			}),
			layout.Rigid(layout.Spacer{Width: 4}.Layout),
			// Rigid, not Flexed: a Flexed child is given the whole rest of
			// the width as its minimum, and the bubble grew to its widest
			// with it. Rigid still ends a long name with an ellipsis.
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, text, token.TypestyleLabelMedium, sc.Primary.Color, 1)
			}),
		)
	}
	if !p.forwardOpens(m) || p.selectionCount() > 0 {
		return line(gtx)
	}
	return r.forward.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		return line(gtx)
	})
}

// layoutSenderName draws the name of msg's sender over the message, in the
// sender's color, with all the marks beside it a chat's name has in the
// list: Telegram Desktop shows only the emoji status or the Premium star.
func (p *chatPage) layoutSenderName(gtx layout.Context, msg *model.Message) layout.Dimensions {
	name := func(gtx layout.Context) layout.Dimensions {
		return label(gtx, msg.SenderName, token.TypestyleLabelLargeEmphasized, senderColor(gtx, msg.SenderID), 1)
	}
	if p.senderBadges == nil {
		return name(gtx)
	}
	before, after := p.senderBadges(msg.SenderID, msg.SenderName)
	return withBadges(gtx, before, name, after)
}

// openStatusSet shows the set the emoji status emoji, clicked beside a
// sender's name, belongs to, once the emoji tells it.
func (p *chatPage) openStatusSet(emoji int64) {
	source, ok := p.source.(customEmojiSource)
	if !ok || emoji == 0 {
		return
	}
	ch := make(chan emojiSetResult, 1)
	p.statusSet = ch
	chat, invalidate := p.chat, p.invalidate
	go func() {
		defer crash.Recover("emoji set of a status", func(*crash.Panic) { ch <- emojiSetResult{chat: chat} })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var ref *model.StickerSetRef
		if m, err := source.CustomEmoji(ctx, emoji); err == nil && m.Media != nil {
			ref = m.Media.StickerSet
		}
		ch <- emojiSetResult{chat: chat, ref: ref}
		invalidate()
	}()
}

// updateStatusSet opens the set openStatusSet found, if the page is still
// of the chat it was asked in.
func (p *chatPage) updateStatusSet() {
	select {
	case r := <-p.statusSet:
		p.statusSet = nil
		if r.chat == p.chat && r.ref != nil {
			p.stickers.open(p, *r.ref)
		}
	default:
	}
}
