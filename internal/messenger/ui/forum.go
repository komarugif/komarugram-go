// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/scroll"

	"gioui.org/f32"
	"gioui.org/io/pointer"
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

// A forum opens as the list of its topics, in place of a history, as Telegram
// Desktop's does; a topic opens as a page of its own, as the comments to a
// post do (commentsView), and goes back to the list.

// topicIconSize is how large a topic's icon is in the list, in dp.
const topicIconSize = unit.Dp(44)

var iconGeneral = wdk.RequireIconWidget(icons.ActionHome)

// forumPage draws the topics of the forum that is open.
type forumPage struct {
	list scroll.List
	// header takes a click on the forum's header, which opens its info.
	header widget.Clickable
	// avatar takes a click on the avatar in it, which shows its photo.
	avatar widget.Clickable
	rows   map[int]*surface
	retry  surface
	// spinner is the indicator of the first page on its way.
	spinner loadingIndicator
	// emoji draws a custom emoji, for a topic's icon.
	emoji func(gtx layout.Context, id int64, size unit.Dp) layout.Dimensions
	// open shows a topic; openAt shows it at a message.
	open   func(forum model.Chat, topic model.Topic)
	openAt func(forum model.Chat, topic model.Topic, at model.MessageID)
	// search searches the whole forum.
	search forumSearch
	// chat is the forum the page was last drawn for.
	chat int64
}

func newForumPage() *forumPage {
	f := &forumPage{rows: map[int]*surface{}}
	f.list.Axis = layout.Vertical
	return f
}

func (f *forumPage) row(id int) *surface {
	r := f.rows[id]
	if r == nil {
		r = new(surface)
		f.rows[id] = r
	}
	return r
}

// layoutForum draws the forum c: the chat's header over its topics.
func (a *App) layoutForum(gtx layout.Context, c model.Chat, l localization.Catalog) layout.Dimensions {
	f := a.forum
	source, _ := a.store.(model.ForumSource)
	if source == nil {
		return layoutEmptyPage(gtx, l)
	}
	if f.chat != c.ID {
		f.chat = c.ID
		f.list.Position = layout.Position{}
		f.rows = map[int]*surface{}
		f.search.reset()
		f.search.chat = c.ID
		source.OpenForum(c.ID)
	}
	searcher, canSearch := a.store.(model.ForumSearcher)
	invalidate := a.window.Invalidate
	if canSearch {
		f.search.update(gtx, searcher, invalidate)
	}
	searching := canSearch && f.search.open
	dims := layoutChatPage(gtx, c, l, a.layoutAvatar, a.badges, func(gtx layout.Context) layout.Dimensions {
		if searching {
			return f.layoutResults(gtx, c, searcher, invalidate, l)
		}
		return f.layout(gtx, c, source, l)
	}, nil)
	header := image.Pt(gtx.Constraints.Max.X, gtx.Dp(chatHeaderSize))
	hgtx := gtx
	hgtx.Constraints = layout.Exact(header)
	if searching {
		// The search's field takes the header's place.
		f.search.layoutHeader(hgtx, header, l)
		return dims
	}
	// The header opens the forum's info: the click is taken over it.
	f.header.Layout(hgtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		return layout.Dimensions{Size: header}
	})
	layoutAvatarTarget(gtx, &f.avatar)
	if canSearch {
		f.search.layoutSearchButton(hgtx, header, c.ID, l)
	}
	return dims
}

// layout draws the topics, or what tells why there are none.
func (f *forumPage) layout(gtx layout.Context, c model.Chat, source model.ForumSource, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	fillRect(gtx, sc.Surface.Color, size)
	list := source.Topics(c.ID)
	switch {
	case len(list.Topics) == 0 && list.Err != nil:
		return f.layoutFailed(gtx, c.ID, source, l)
	case len(list.Topics) == 0 && (list.Loading || list.More):
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			px := gtx.Dp(32)
			gtx.Constraints = layout.Exact(image.Pt(px, px))
			return f.spinner.Layout(gtx, l)
		})
	case len(list.Topics) == 0:
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			return pill(gtx, l.T("forum.no_topics"))
		})
	}
	now := time.Now()
	dims := f.list.Layout(gtx, len(list.Topics), func(gtx layout.Context, i int) layout.Dimensions {
		t := list.Topics[i]
		row := f.row(t.ID)
		if row.Clicked(gtx) && f.open != nil {
			f.open(c, t)
		}
		return f.layoutTopic(gtx, row, t, now, l)
	})
	if list.More && !list.Loading && f.list.Position.First+f.list.Position.Count >= len(list.Topics)-3 {
		source.LoadMoreTopics(c.ID)
	}
	return dims
}

// layoutFailed draws the plain retry of a list that could not be read.
func (f *forumPage) layoutFailed(gtx layout.Context, chat int64, source model.ForumSource, l localization.Catalog) layout.Dimensions {
	if f.retry.Clicked(gtx) {
		source.OpenForum(chat)
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		return textButton(gtx, &f.retry, l.T("history.retry"))
	})
}

// layoutTopic draws a topic as the chat list draws a chat: its icon, its
// title and time, its last message and what is unread.
func (f *forumPage) layoutTopic(gtx layout.Context, row *surface, t model.Topic, now time.Time, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	margin := gtx.Dp(chatRowMargin)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(chatRowHeight))
	style := surfaceStyle{
		area:       image.Rectangle{Min: image.Pt(margin, 0), Max: image.Pt(size.X-margin, size.Y)},
		radius:     gtx.Dp(chatSelectRadius),
		background: sc.Primary.Color.SetOpacity(0),
		content:    sc.Surface.OnColor,
		button:     t.Title,
	}
	return row.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		pad := gtx.Dp(chatRowPadding)
		iconPx := gtx.Dp(topicIconSize)
		iconX := gtx.Dp(chatAvatarInset)
		offset(gtx, image.Pt(iconX, (size.Y-iconPx)/2), func(gtx layout.Context) layout.Dimensions {
			return f.layoutTopicIcon(gtx, t, iconPx)
		})
		textX := iconX + iconPx + pad
		area := image.Rect(textX, gtx.Dp(11), size.X-margin-pad, size.Y-gtx.Dp(11))
		f.layoutTopicText(gtx, t, area, now, l)
		return layout.Dimensions{Size: size}
	})
}

// topicColor is the color of a topic's icon: Telegram's, or the theme's
// for a topic that has none.
func topicColor(gtx layout.Context, t model.Topic) token.MatColor {
	if t.IconColor == 0 {
		return scheme(gtx).Primary.Color
	}
	return token.NewMatColorFromHexRGB(uint32(t.IconColor))
}

// layoutTopicIcon draws the icon of a topic in a square of px: its custom
// emoji, or else a plate of its color with a hash mark, or a house for the
// General topic.
func (f *forumPage) layoutTopicIcon(gtx layout.Context, t model.Topic, px int) layout.Dimensions {
	size := image.Pt(px, px)
	if t.IconEmoji != 0 && f.emoji != nil {
		inner := px * 4 / 5
		var dims layout.Dimensions
		offset(gtx, image.Pt((px-inner)/2, (px-inner)/2), func(gtx layout.Context) layout.Dimensions {
			dims = f.emoji(gtx, t.IconEmoji, unit.Dp(float32(inner)/gtx.Metric.PxPerDp))
			return dims
		})
		if dims.Size.X > 0 {
			return layout.Dimensions{Size: size}
		}
	}
	color := topicColor(gtx, t)
	if t.General {
		fillRounded(gtx, scheme(gtx).PrimaryContainer.Color, size, px*3/10)
		inner := px * 3 / 5
		offset(gtx, image.Pt((px-inner)/2, (px-inner)/2), func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(inner, inner), func(gtx layout.Context) layout.Dimensions {
				return iconGeneral(gtx, scheme(gtx).PrimaryContainer.OnColor)
			})
		})
		return layout.Dimensions{Size: size}
	}
	fillRounded(gtx, color, size, px*3/10)
	white := token.NewMatColorFromHexRGB(0xffffff)
	offset(gtx, image.Pt(0, 0), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(size)
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			return label(gtx, "#", token.TypestyleTitleLarge, white, 1)
		})
	})
	return layout.Dimensions{Size: size}
}

// layoutTopicText draws the title and time of a topic, its last message and
// what is unread, in area.
func (f *forumPage) layoutTopicText(gtx layout.Context, t model.Topic, area image.Rectangle, now time.Time, l localization.Catalog) {
	sc := scheme(gtx)
	width := area.Dx()
	title, text, accent := sc.Surface.OnColor, sc.SurfaceVariant.OnColor, sc.Primary.Color
	gtx.Constraints = layout.Constraints{Max: image.Pt(width, area.Dy())}
	macro := op.Record(gtx.Ops)
	timeDims := layout.Dimensions{}
	if !t.LastTime.IsZero() {
		timeDims = label(gtx, chatTime(t.LastTime, now, l), token.TypestyleLabelSmall, text, 1)
	}
	timeCall := macro.Stop()
	offset(gtx, image.Pt(area.Max.X-timeDims.Size.X, area.Min.Y+gtx.Dp(2)), func(gtx layout.Context) layout.Dimensions {
		timeCall.Add(gtx.Ops)
		return timeDims
	})
	// The title, with a lock after it when the topic is closed.
	titleGtx := gtx
	titleGtx.Constraints.Max.X = max(width-timeDims.Size.X-gtx.Dp(8), 0)
	offset(titleGtx, area.Min, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = 0
				return label(gtx, t.Title, token.TypestyleTitleSmallEmphasized, title, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !t.Closed {
					return layout.Dimensions{}
				}
				px := gtx.Dp(14)
				return layout.Inset{Left: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions { return iconPrivacy(gtx, text) })
				})
			}),
		)
	})
	// The last message, and on its right the counters, or the pin.
	lineY := area.Min.Y + gtx.Dp(22)
	messageWidth := width
	right := area.Max.X
	badgeBackground, badgeText := sc.Primary.Color, sc.Primary.OnColor
	if t.Muted {
		badgeBackground, badgeText = sc.Outline, sc.Surface.Color
	}
	switch {
	case t.Unread > 0:
		w := drawBadgeRight(gtx, image.Pt(right, lineY+gtx.Dp(1)), t.Unread, badgeBackground, badgeText)
		messageWidth -= w + gtx.Dp(8)
		right -= w + gtx.Dp(4)
		if t.Mentions > 0 {
			d := gtx.Dp(18)
			offset(gtx, image.Pt(right-d, lineY+gtx.Dp(1)), func(gtx layout.Context) layout.Dimensions {
				fillRounded(gtx, sc.Primary.Color, image.Pt(d, d), d/2)
				return offset(gtx, image.Pt(0, 0), func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(d, d))
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min = image.Point{}
						return label(gtx, "@", token.TypestyleLabelSmallEmphasized, sc.Primary.OnColor, 1)
					})
				})
			})
			messageWidth -= d + gtx.Dp(4)
		}
	case t.Pinned:
		px := gtx.Dp(16)
		offset(gtx, image.Pt(right-px, lineY+gtx.Dp(1)), func(gtx layout.Context) layout.Dimensions {
			return drawPin(gtx, px, text)
		})
		messageWidth -= px + gtx.Dp(8)
	}
	x := area.Min.X
	if t.LastSender != "" {
		senderGtx := gtx
		senderGtx.Constraints.Max.X = max(messageWidth/2, 0)
		dims := offset(senderGtx, image.Pt(x, lineY), func(gtx layout.Context) layout.Dimensions {
			return label(gtx, t.LastSender+": ", token.TypestyleBodyMedium, accent, 1)
		})
		x += dims.Size.X
		messageWidth -= dims.Size.X
	}
	messageGtx := gtx
	messageGtx.Constraints.Max.X = max(messageWidth, 0)
	message := t.LastMessage
	if message == "" {
		message = l.T("forum.no_messages")
	}
	offset(messageGtx, image.Pt(x, lineY), func(gtx layout.Context) layout.Dimensions {
		return label(gtx, message, token.TypestyleBodyMedium, text, 1)
	})
}

// drawPin draws a thumbtack in a square of px, in color: the mark of a
// pinned topic.
func drawPin(gtx layout.Context, px int, color token.MatColor) layout.Dimensions {
	u := float32(px) / 16
	pt := func(x, y float32) f32.Point { return f32.Pt(x*u, y*u) }
	var path clip.Path
	path.Begin(gtx.Ops)
	// The head, the body that narrows to the neck, and the needle.
	path.MoveTo(pt(3.5, 1))
	path.LineTo(pt(12.5, 1))
	path.LineTo(pt(12.5, 3.2))
	path.LineTo(pt(11, 3.2))
	path.LineTo(pt(11, 7.6))
	path.LineTo(pt(13.2, 10))
	path.LineTo(pt(8.7, 10))
	path.LineTo(pt(8.7, 15))
	path.LineTo(pt(7.3, 15))
	path.LineTo(pt(7.3, 10))
	path.LineTo(pt(2.8, 10))
	path.LineTo(pt(5, 7.6))
	path.LineTo(pt(5, 3.2))
	path.LineTo(pt(3.5, 3.2))
	path.Close()
	paint.FillShape(gtx.Ops, color.AsNRGBA(), clip.Outline{Path: path.End()}.Op())
	return layout.Dimensions{Size: image.Pt(px, px)}
}
