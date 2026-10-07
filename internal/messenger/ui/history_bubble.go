// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"strconv"
	"strings"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/gesture"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"golang.org/x/exp/shiny/materialdesign/icons"

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
	iconKindChannel = wdk.RequireIconWidget(icons.ActionAnnouncement)
	iconKindBot     = wdk.RequireIconWidget(icons.ActionAndroid)
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

// joinable reports whether b, the message after a, continues a's group.
func joinable(a, b model.Message) bool {
	return a.Kind != model.MessageService && b.Kind != model.MessageService &&
		a.Outgoing == b.Outgoing && a.SenderID == b.SenderID && a.SenderName == b.SenderName &&
		sameDay(a.Date, b.Date) && b.Date.Sub(a.Date) < joinGap
}

// messageJoins tells for each message of msgs, in order, how it joins its
// neighbours.
func messageJoins(msgs []model.Message) []bubbleJoin {
	joins := make([]bubbleJoin, len(msgs))
	for i := 1; i < len(msgs); i++ {
		if joinable(msgs[i-1], msgs[i]) {
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
	// Where the article is in the history, as the last frame put it.
	r.viewKnown = p.rowTopKnown
	if r.viewKnown {
		r.viewTop = p.rowTop + r.bodyTop + gtx.Dp(bubblePadTop) + r.articleAbove
	}
	if m.ReplyToMessageID != 0 && r.reply.Clicked(gtx) {
		p.jumpPending = m.ReplyToMessageID
		p.invalidate()
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
	dims := layout.Inset{Top: top, Bottom: bottom, Left: 16, Right: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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
					gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(660))
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
	x := gtx.Dp(16)
	if m.Outgoing {
		x = max(x, gtx.Constraints.Max.X-gtx.Dp(20)-r.bodySize.X)
	}
	r.avatarPoint = image.Pt(x, r.bodyTop+r.bodySize.Y-gtx.Dp(34))
	return dims
}

// bubblePadTop is the space over what a bubble holds.
const bubblePadTop unit.Dp = 8

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
				return layout.Inset{Top: bubblePadTop, Bottom: 7, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, p.bubbleContent(gtx, r, m, join, l, animate)...)
				})
			}
			if !p.showsComments(m) {
				return content(gtx)
			}
			// The comments bar spans the bubble under what it holds.
			dims := content(gtx)
			width := max(dims.Size.X, min(gtx.Constraints.Max.X, commentsNatural(gtx, m, l)))
			bar := gtx.Dp(commentsBarHeight)
			size := image.Pt(width, dims.Size.Y+bar)
			defer shape.rrect(size).Push(gtx.Ops).Pop()
			offset(gtx, image.Pt(0, dims.Size.Y), func(gtx layout.Context) layout.Dimensions {
				return p.commentsBar(gtx, r, m, width, l)
			})
			return layout.Dimensions{Size: size}
		}),
	)
}

// bubbleContent is what a bubble holds, top to bottom: the sender, where a
// forward came from, the message replied to, media, the text, buttons,
// reactions and the line of its time.
func (p *chatPage) bubbleContent(gtx layout.Context, r *messageRow, m model.Message, join bubbleJoin, l localization.Catalog, animate bool) []layout.FlexChild {
	sc := scheme(gtx)
	var children []layout.FlexChild
	if m.SenderName != "" && !m.Outgoing && join&joinAbove == 0 {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, m.SenderName, token.TypestyleLabelLargeEmphasized, senderColor(gtx, m.SenderID), 1)
		}), vspace(3))
	}
	if !m.ForwardDate.IsZero() {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			text := l.T("history.forward")
			if m.ForwardName != "" {
				text = l.Format("history.forwarded_from", map[string]string{"name": m.ForwardName, "user": m.ForwardName})
			}
			return label(gtx, text, token.TypestyleLabelMedium, sc.Primary.Color, 1)
		}), vspace(4))
	}
	// In a thread, as the comments to a post, what replies to its root
	// quotes nothing, as in Telegram Desktop: all of it does.
	if m.ReplyToMessageID != 0 && m.ReplyToMessageID != p.threadRoot {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.replyQuote(gtx, r, m, l)
		}), vspace(6))
	}
	if len(m.Attachments) > 1 {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.albumLayout(gtx, r, m, l, animate) }), vspace(6))
	} else if m.Media != nil {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.mediaLayout(gtx, r, m, l, animate) }), vspace(6))
	}
	if m.Poll != nil {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return pollLayout(gtx, m.Poll, l) }))
	}
	if r.article != nil {
		// What is over the article is measured, for links to its anchors.
		above := children
		children = []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			dims := layout.Flex{Axis: layout.Vertical}.Layout(gtx, above...)
			r.articleAbove = dims.Size.Y
			return dims
		}), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.articleLayout(gtx, r, m, l, animate) })}
		if r.article.part && p.openArticle != nil {
			children = append(children, vspace(6), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.showMore(gtx, r, m, l) }))
		}
	} else if len(m.Attachments) > 1 {
		seen := map[string]bool{}
		for _, member := range m.Attachments {
			if member.Text != "" && !seen[member.Text] {
				seen[member.Text] = true
				member := member
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return p.richText(gtx, albumRow(gtx, r, member, l), l, animate)
				}), vspace(4))
			}
		}
	} else if m.Text != "" {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.richText(gtx, r, l, animate) }))
	} else if m.Media == nil && m.Poll == nil {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if m.Kind == model.MessageService {
				return label(gtx, p.serviceText(m, l), token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
			}
			text := l.T("history.empty_message")
			if m.Rich != nil {
				if kind := m.Rich.Fallback(); kind != "" {
					text = l.T("rich." + kind)
				}
			}
			return label(gtx, text, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}))
	}
	if m.WebPage != nil && m.Rich == nil {
		children = append(children, vspace(6), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.webPreview(gtx, r, m, l, animate) }))
	}
	if len(m.Reactions) > 0 {
		children = append(children, vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.reactions(gtx, r, m, animate)
		}))
	}
	children = append(children, vspace(4), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if m.Streaming {
				// A draft a bot streams turns a ring before its time while
				// the bot writes it.
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.writing.sized(gtx, l, 12) }),
					layout.Rigid(layout.Spacer{Width: 6}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return messageFooter(gtx, m, l, false) }))
			}
			return messageFooter(gtx, m, l, !p.showsComments(m))
		})
	}))
	return children
}

// messageFooter is the line at the bottom of a bubble: comments and views
// of a post, its author, whether it was edited, and its time. The comments
// are left to the comments bar when it is not counted here.
func messageFooter(gtx layout.Context, m model.Message, l localization.Catalog, countComments bool) layout.Dimensions {
	return messageFooterIn(gtx, m, l, countComments, scheme(gtx).SurfaceVariant.OnColor)
}

// messageFooterIn is messageFooter in col.
func messageFooterIn(gtx layout.Context, m model.Message, l localization.Catalog, countComments bool, col token.MatColor) layout.Dimensions {
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
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, items...)
}

// footerText is the line of a message's time: who signed it, whether it
// was edited or deleted, with the look's marks, and the time.
func footerText(gtx layout.Context, m model.Message, l localization.Catalog) string {
	var parts []string
	if m.PostAuthor != "" {
		parts = append(parts, m.PostAuthor)
	}
	look := lookOf(gtx)
	if !m.EditedAt.IsZero() {
		mark := look.EditedMark
		if mark == "" {
			mark = l.T("history.edited")
		}
		parts = append(parts, mark)
	}
	if m.Deleted {
		// Kept after Telegram deleted it, with AyuGram's mark.
		mark := look.DeletedMark
		if mark == "" {
			mark = l.T("history.deleted_mark")
		}
		parts = append(parts, mark)
	}
	parts = append(parts, m.Date.Local().Format(timeFormat(gtx)))
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
	var rank func(model.Reaction) (int, bool)
	if orderer, ok := p.source.(model.ReactionOrderer); ok {
		rank = orderer.ReactionRank
	}
	r.shownReactions = model.ShownReactions(r.shownReactions, m.Reactions, rank)
	reactions := r.shownReactions
	sc := scheme(gtx)
	maxWidth := gtx.Constraints.Max.X
	gap := gtx.Dp(6)
	x, y, lineHeight, width := 0, 0, 0, 0
	for len(r.reactions) < len(reactions) {
		r.reactions = append(r.reactions, surface{})
	}
	reactor, canReact := p.source.(model.Reactor)
	canReact = canReact && p.canReact(m)
	for i, reaction := range reactions {
		chip := &r.reactions[i]
		if chip.Clicked(gtx) && canReact && !reaction.Paid {
			reactor.ToggleReaction(m, reaction, p.reportMedia)
		}
		background, content := sc.SecondaryContainer.Color, sc.SecondaryContainer.OnColor
		if reaction.Chosen {
			background, content = sc.Primary.Color, sc.Primary.OnColor
		}
		macro := op.Record(gtx.Ops)
		cgtx := gtx
		cgtx.Constraints = layout.Constraints{Max: image.Pt(maxWidth, gtx.Dp(40))}
		dims := layout.Inset{Top: 4, Bottom: 4, Left: 8, Right: 10}.Layout(cgtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return p.reactionIcon(gtx, reaction, animate)
				}),
				layout.Rigid(layout.Spacer{Width: 5}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, shortCount(reaction.Count), token.TypestyleLabelLargeEmphasized, content, 1)
				}),
			)
		})
		call := macro.Stop()
		if x > 0 && x+dims.Size.X > maxWidth {
			x, y = 0, y+lineHeight+gap
			lineHeight = 0
		}
		offset(gtx, image.Pt(x, y), func(gtx layout.Context) layout.Dimensions {
			if !canReact || reaction.Paid {
				fillRounded(gtx, background, dims.Size, dims.Size.Y/2)
				call.Add(gtx.Ops)
				return dims
			}
			style := surfaceStyle{radius: dims.Size.Y / 2, background: background, content: content}
			return chip.Layout(gtx, dims.Size, style, func(gtx layout.Context) layout.Dimensions {
				call.Add(gtx.Ops)
				return dims
			})
		})
		x += dims.Size.X + gap
		width = max(width, x-gap)
		lineHeight = max(lineHeight, dims.Size.Y)
	}
	return layout.Dimensions{Size: image.Pt(width, y+lineHeight)}
}

// reactionIcon draws a reaction's emoji, custom emoji or the star of paid
// reactions.
func (p *chatPage) reactionIcon(gtx layout.Context, reaction model.Reaction, animate bool) layout.Dimensions {
	size := gtx.Dp(18)
	if reaction.DocumentID != 0 && p.media != nil {
		msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: fmt.Sprintf("emoji/%d", reaction.DocumentID), MIMEType: "application/x-custom-emoji"}}
		if frame, _ := p.media.Frame(msg, animate); frame != nil && p.images != nil {
			return exact(gtx, image.Pt(size, size), func(gtx layout.Context) layout.Dimensions {
				return widget.Image{Src: p.images.Op(frame), Fit: widget.Contain}.Layout(gtx)
			})
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
// in its sender's color; a click shows it.
func (p *chatPage) replyQuote(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog) layout.Dimensions {
	replied, state, known := p.referenced(m.Key.ChatID, m.ReplyToMessageID)
	name, text := l.T("history.reply_missing"), ""
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
		text = foundText(replied, l)
		if replied.SenderName != "" {
			text = strings.TrimPrefix(text, replied.SenderName+": ")
		}
	}
	macro := op.Record(gtx.Ops)
	dims := layout.Inset{Top: 5, Bottom: 5, Left: 11, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, name, token.TypestyleLabelLargeEmphasized, color, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if text == "" {
					return layout.Dimensions{}
				}
				return label(gtx, text, token.TypestyleBodySmall, scheme(gtx).Surface.OnColor, 1)
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

// referenced is a message another one names, as the one it replies to:
// the loaded one, or else one the store looks up, which may be on its way
// or gone. known is false when the store cannot look it up.
func (p *chatPage) referenced(chat int64, id model.MessageID) (m model.Message, state model.LookupState, known bool) {
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
