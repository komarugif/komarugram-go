// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"time"

	"gio-mw/token"
	"gio-mw/widget/button"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// The comments bar at the bottom of a channel post, as materialgram draws
// it: a line across the bubble, the userpics of the last commenters, the
// count and an arrow; a click opens the discussion.
const (
	commentsBarHeight = unit.Dp(44)
	commenterSize     = unit.Dp(24)
	// commenterStep is how far each userpic sits from the one before, which
	// it overlaps.
	commenterStep = unit.Dp(16)
	commentersMax = 3
)

// showsComments reports whether m's bubble ends with the comments bar: a
// channel post with a discussion, in a chat that can open it, and not the
// post at the top of its own comments.
func (p *chatPage) showsComments(m model.Message) bool {
	return m.CommentsOpen && m.Post && p.openComments != nil && !p.thread
}

// commentsBar draws the bar at the bottom of a bubble of width.
func (p *chatPage) commentsBar(gtx layout.Context, r *messageRow, m model.Message, width int, l localization.Catalog) layout.Dimensions {
	if r.comments.Clicked(gtx) {
		p.openComments(m)
	}
	sc := scheme(gtx)
	size := image.Pt(width, gtx.Dp(commentsBarHeight))
	fillRect(gtx, sc.OutlineVariant, image.Pt(width, gtx.Dp(1)))
	text := l.T("comments.none")
	if m.Comments > 0 {
		text = l.Count("comments.open", m.Comments, nil)
	}
	style := surfaceStyle{content: sc.Primary.Color, button: text}
	return r.comments.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		x := gtx.Dp(12)
		commenters := m.Commenters[:min(len(m.Commenters), commentersMax)]
		if len(commenters) > 0 && p.avatar != nil {
			pic := gtx.Dp(commenterSize)
			ring := gtx.Dp(2)
			y := (size.Y - pic) / 2
			// The first is drawn last, over the others, as in Telegram.
			for i := len(commenters) - 1; i >= 0; i-- {
				at := image.Pt(x+i*gtx.Dp(commenterStep), y)
				offset(gtx, at.Sub(image.Pt(ring, ring)), func(gtx layout.Context) layout.Dimensions {
					d := pic + 2*ring
					bubble := sc.Surface.Color
					if m.Outgoing {
						bubble = sc.PrimaryContainer.Color
					}
					paint.FillShape(gtx.Ops, bubble.AsNRGBA(), clip.Ellipse{Max: image.Pt(d, d)}.Op(gtx.Ops))
					return layout.Dimensions{Size: image.Pt(d, d)}
				})
				offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
					return p.avatar(gtx, commenters[i], model.KindUser, "", commenterSize)
				})
			}
			x += (len(commenters)-1)*gtx.Dp(commenterStep) + pic + gtx.Dp(10)
		}
		arrow := gtx.Dp(20)
		textGtx := gtx
		textGtx.Constraints = layout.Constraints{Max: image.Pt(max(0, size.X-x-arrow-gtx.Dp(12)), size.Y)}
		offset(textGtx, image.Pt(x, 0), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = size.Y
			return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{}
				return label(gtx, text, token.TypestyleLabelLargeEmphasized, sc.Primary.Color, 1)
			})
		})
		offset(gtx, image.Pt(size.X-arrow-gtx.Dp(10), (size.Y-arrow)/2), func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(arrow, arrow), func(gtx layout.Context) layout.Dimensions {
				return iconChevron(gtx, sc.Primary.Color)
			})
		})
		return layout.Dimensions{Size: size}
	})
}

// commentsNatural is how wide the comments bar would like to be: enough for
// its userpics, its text and its arrow.
func commentsNatural(gtx layout.Context, m model.Message, l localization.Catalog) int {
	text := l.T("comments.none")
	if m.Comments > 0 {
		text = l.Count("comments.open", m.Comments, nil)
	}
	macro := op.Record(gtx.Ops)
	gtx.Constraints.Min = image.Point{}
	dims := label(gtx, text, token.TypestyleLabelLargeEmphasized, scheme(gtx).Primary.Color, 1)
	macro.Stop()
	w := gtx.Dp(12) + dims.Size.X + gtx.Dp(20) + gtx.Dp(22)
	if n := min(len(m.Commenters), commentersMax); n > 0 {
		w += (n-1)*gtx.Dp(commenterStep) + gtx.Dp(commenterSize) + gtx.Dp(10)
	}
	return w
}

// commentsView is the discussion of a channel post, open over its channel.
type commentsView struct {
	// chat is the comments' chat and from the channel they are open over;
	// title is the channel's, and count the comments the post counted.
	chat  model.Chat
	from  int64
	title string
	count int
	// topic is set when the page is a topic of the forum from, which name
	// titles.
	topic bool
	name  string
	back  *button.Button
}

// openComments shows the comments to the channel post m.
func (a *App) openComments(m model.Message) {
	store, ok := a.store.(model.CommentsStore)
	if !ok || a.comments == nil {
		return
	}
	title := ""
	if c, ok := a.selectedChat(); ok {
		title = c.Title
	}
	a.closeComments()
	a.thread = &commentsView{chat: store.OpenComments(m), from: a.selected, title: title, count: m.Comments, back: button.Text()}
}

// openTopic shows the topic of the forum, which is open.
func (a *App) openTopic(forum model.Chat, topic model.Topic) {
	store, ok := a.store.(model.ForumSource)
	if !ok || a.comments == nil {
		return
	}
	a.closeComments()
	a.thread = &commentsView{chat: store.OpenTopic(forum.ID, topic), from: a.selected, title: forum.Title, topic: true, name: topic.Title, back: button.Text()}
}

// openTopicAt shows the topic of the forum, which is open, at message at,
// tinted for a moment as a search's jump tints it.
func (a *App) openTopicAt(forum model.Chat, topic model.Topic, at model.MessageID) {
	store, ok := a.store.(model.ForumSearcher)
	if !ok || a.comments == nil {
		a.openTopic(forum, topic)
		return
	}
	a.closeComments()
	a.thread = &commentsView{chat: store.OpenTopicAt(forum.ID, topic, at), from: a.selected, title: forum.Title, topic: true, name: topic.Title, back: button.Text()}
	a.comments.highlight, a.comments.highlightUntil = at, time.Now().Add(highlightTime)
}

// closeComments goes back from the comments to their channel.
func (a *App) closeComments() {
	if a.thread == nil {
		return
	}
	topic := a.thread.topic
	a.thread = nil
	if topic {
		// The topics may have changed while it was open.
		if source, ok := a.store.(model.ForumSource); ok {
			if c, ok := a.selectedChat(); ok {
				source.OpenForum(c.ID)
			}
		}
	}
	// The comments are no longer on screen: their group need not be
	// polled.
	if w, ok := a.store.(model.ChatWatcher); ok {
		w.WatchChat(a.comments, 0)
	}
}

// layoutComments draws the open comments in place of the channel's
// history, under a header that goes back to it.
func (a *App) layoutComments(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	t := a.thread
	if t.back.Clicked(gtx) {
		a.closeComments()
		gtx.Execute(op.InvalidateCmd{})
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	h := a.comments
	h.topic = t.topic
	history := a.store.(model.ConversationStore).History(t.chat.ID)
	// The first message is the post; the rest are the comments, of which
	// a new one may have come since the post counted them. Telegram counts
	// them with each page.
	count := t.count
	switch {
	case history.Counted:
		count = history.Count
	case !history.HasOlder:
		count = max(count, len(history.Messages)-1)
	}
	title := l.T("comments.header_none")
	if count > 0 {
		title = l.Count("comments.header", count, nil)
	}
	empty := !history.LoadingOlder && history.Err == nil && len(history.Messages) <= 1
	head := chatHead{back: t.back, title: title, subtitle: t.title}
	if t.topic {
		empty = !history.LoadingOlder && history.Err == nil && len(history.Messages) == 0
		head.title, head.subtitle = t.name, topicCount(history, l)
	}
	return layoutChatPageHead(gtx, t.chat, l, a.layoutAvatar, nil, &head, func(gtx layout.Context) layout.Dimensions {
		dims := h.Layout(gtx, t.chat, l, a.window.Motion.AnimationsEnabled())
		if empty {
			layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{}
				if t.topic {
					return pill(gtx, l.T("forum.no_messages"))
				}
				return pill(gtx, l.T("comments.empty"))
			})
		}
		return dims
	}, h)
}

// topicCount is what a topic's header says under its name, as Telegram
// Desktop's: how many messages it has, the one that made it not counted.
func topicCount(h model.History, l localization.Catalog) string {
	switch {
	case !h.Counted:
		return l.T("forum.loading")
	case h.Count > 1:
		return l.Count("forum.messages", h.Count-1, nil)
	}
	return l.T("forum.messages_none")
}
