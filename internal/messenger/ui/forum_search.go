// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"strings"
	"time"

	"gio-mw/widget/scroll"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// forumSearchPage is how many found messages a page of a forum's search
// has.
const forumSearchPage = 50

// forumSearch searches a whole forum from its list of topics, as Telegram
// Desktop's search of a forum does: a field over the forum's header, and the
// messages found, each under its topic, in place of the topics. One chosen
// opens its topic at it; going back finds the search as it was.
type forumSearch struct {
	open bool
	// focus asks for the keyboard in the frame the field is drawn in: a
	// tag not drawn in the frame loses the focus at its end.
	focus bool
	// chat is the forum searched.
	chat  int64
	field widget.Editor
	// typed is the text as typed, due when it is searched; query the text
	// searched, found what it found so far, newest first.
	typed   string
	due     time.Time
	query   string
	found   []model.FoundInTopic
	count   int
	next    string
	loading bool
	failed  bool
	gen     int
	res     chan forumSearchResult
	cancel  context.CancelFunc
	// button opens the search from the header; close closes it.
	button, close surface
	rows          map[model.MessageKey]*surface
	list          scroll.List
	loader        loadingIndicator
}

type forumSearchResult struct {
	gen  int
	page model.ForumSearchPage
	err  error
}

// openSearch shows the field over the forum's header, focused.
func (s *forumSearch) openSearch(gtx layout.Context, chat int64) {
	if s.chat != chat {
		s.reset()
		s.chat = chat
	}
	s.open, s.focus = true, true
	s.field.SingleLine, s.field.Submit = true, true
	gtx.Execute(op.InvalidateCmd{})
}

// reset closes the search and forgets what it found.
func (s *forumSearch) reset() {
	if s.cancel != nil {
		s.cancel()
	}
	s.field.SetText("")
	s.open, s.typed, s.due, s.gen = false, "", time.Time{}, s.gen+1
	s.query, s.found, s.count, s.next, s.loading, s.failed = "", nil, 0, "", false, false
	s.list.Position = layout.Position{}
}

// start searches text from its first page.
func (s *forumSearch) start(searcher model.ForumSearcher, text string, invalidate func()) {
	if s.cancel != nil {
		s.cancel()
	}
	s.gen++
	s.query, s.found, s.count, s.next, s.failed, s.loading = text, nil, 0, "", false, false
	s.list.Position = layout.Position{}
	if text != "" {
		s.fetch(searcher, invalidate)
	}
}

// fetch asks for the next page of what the search found.
func (s *forumSearch) fetch(searcher model.ForumSearcher, invalidate func()) {
	if s.loading {
		return
	}
	s.loading = true
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	s.cancel = cancel
	if s.res == nil {
		s.res = make(chan forumSearchResult, 4)
	}
	gen, chat, query, next, res := s.gen, s.chat, s.query, s.next, s.res
	go func() {
		defer cancel()
		page, err := searcher.SearchForum(ctx, chat, query, next, forumSearchPage)
		res <- forumSearchResult{gen, page, err}
		invalidate()
	}()
}

// update takes what the search found, the field's text and the close
// button.
func (s *forumSearch) update(gtx layout.Context, searcher model.ForumSearcher, invalidate func()) {
	for {
		select {
		case r := <-s.res:
			if r.gen != s.gen {
				continue
			}
			s.loading = false
			if r.err != nil {
				s.failed = true
				continue
			}
			s.found = append(s.found, r.page.Found...)
			s.next, s.count = r.page.Next, max(r.page.Count, len(s.found))
			continue
		default:
		}
		break
	}
	if !s.open {
		return
	}
	if s.close.Clicked(gtx) {
		s.reset()
		return
	}
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			s.reset()
			return
		}
	}
	for {
		if _, ok := s.field.Update(gtx); !ok {
			break
		}
	}
	text := strings.TrimSpace(s.field.Text())
	if text != s.typed {
		s.typed, s.due = text, gtx.Now.Add(chatSearchDelay)
	}
	if !s.due.IsZero() {
		if gtx.Now.Before(s.due) {
			gtx.Execute(op.InvalidateCmd{At: s.due})
		} else {
			s.due = time.Time{}
			if s.typed != s.query {
				s.start(searcher, s.typed, invalidate)
			}
		}
	}
}

// row is the surface of the found message key.
func (s *forumSearch) row(key model.MessageKey) *surface {
	if s.rows == nil {
		s.rows = map[model.MessageKey]*surface{}
	}
	r := s.rows[key]
	if r == nil {
		r = new(surface)
		s.rows[key] = r
	}
	return r
}

// layoutHeader draws the field over the forum's header: the field, what
// the search found, and the button that closes it.
func (s *forumSearch) layoutHeader(gtx layout.Context, size image.Point, l localization.Catalog) {
	sc := scheme(gtx)
	fillWindowSurface(gtx, sc.Surface.Color, size)
	button := gtx.Dp(40)
	pad := gtx.Dp(8)
	right := size.X - pad - button
	offset(gtx, image.Pt(right, (size.Y-button)/2), func(gtx layout.Context) layout.Dimensions {
		style := surfaceStyle{radius: button / 2, content: sc.Surface.OnColor, button: l.T("chat_search.close")}
		return s.close.Layout(gtx, image.Pt(button, button), style, func(gtx layout.Context) layout.Dimensions {
			px := gtx.Dp(24)
			return offset(gtx, image.Pt((button-px)/2, (button-px)/2), func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions { return iconClear(gtx, sc.SurfaceVariant.OnColor) })
			})
		})
	})
	right -= gtx.Dp(12)
	if s.loading && len(s.found) == 0 {
		px := gtx.Dp(20)
		right -= px
		offset(gtx, image.Pt(right, (size.Y-px)/2), func(gtx layout.Context) layout.Dimensions {
			return s.loader.sized(gtx, l, 20)
		})
		right -= gtx.Dp(8)
	}
	offset(gtx, image.Pt(pad, 0), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(max(right-pad, 0), size.Y))
		return layoutSearchField(gtx, &s.field, l)
	})
	if s.focus {
		s.focus = false
		gtx.Execute(key.FocusCmd{Tag: &s.field})
	}
	offset(gtx, image.Pt(0, size.Y-gtx.Dp(1)), func(gtx layout.Context) layout.Dimensions {
		fillRect(gtx, sc.OutlineVariant, image.Pt(size.X, gtx.Dp(1)))
		return layout.Dimensions{}
	})
}

// layoutResults draws what the search found in place of the topics: each
// message as its topic's row shows its last one. A click opens the topic at
// the message.
func (f *forumPage) layoutResults(gtx layout.Context, c model.Chat, searcher model.ForumSearcher, invalidate func(), l localization.Catalog) layout.Dimensions {
	s := &f.search
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	fillRect(gtx, sc.Surface.Color, size)
	center := func(text string) layout.Dimensions {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			return pill(gtx, text)
		})
	}
	switch {
	case s.query == "":
		return layout.Dimensions{Size: size}
	case len(s.found) == 0 && s.failed:
		return center(l.T("search.failed"))
	case len(s.found) == 0 && s.loading:
		return layout.Dimensions{Size: size}
	case len(s.found) == 0:
		return center(l.T("chat_search.none"))
	}
	now := time.Now()
	s.list.Axis = layout.Vertical
	dims := s.list.Layout(gtx, len(s.found), func(gtx layout.Context, i int) layout.Dimensions {
		found := s.found[i]
		row := s.row(found.Message.Key)
		if row.Clicked(gtx) && f.openAt != nil {
			f.openAt(c, found.Topic, found.Message.Key.MessageID)
		}
		return f.layoutTopic(gtx, row, foundTopicRow(found, l), now, l)
	})
	if s.next != "" && !s.loading && !s.failed && s.list.Position.First+s.list.Position.Count >= len(s.found)-3 {
		s.fetch(searcher, invalidate)
	}
	return dims
}

// foundTopicRow is the row of a found message: its topic, with the message
// as what the topic says last.
func foundTopicRow(found model.FoundInTopic, l localization.Catalog) model.Topic {
	t := found.Topic
	m := found.Message
	if t.Title == "" && t.General {
		t.Title = "General"
	}
	t.Pinned, t.Closed, t.Muted, t.Unread, t.Mentions = false, false, false, 0, 0
	// The row tells the sender apart, as a topic's does.
	t.LastSender = m.SenderName
	if m.Outgoing {
		t.LastSender = l.T("history.you")
	}
	m.SenderName = ""
	t.LastMessage, t.LastTime = foundText(m, l), m.Date
	return t
}

// layoutSearchButton draws the button that opens the search at the end
// of the forum's header, over its click.
func (s *forumSearch) layoutSearchButton(gtx layout.Context, header image.Point, chat int64, l localization.Catalog) {
	if s.button.Clicked(gtx) {
		s.openSearch(gtx, chat)
	}
	sc := scheme(gtx)
	button := gtx.Dp(headButton)
	offset(gtx, image.Pt(header.X-gtx.Dp(8)-button, (header.Y-button)/2), func(gtx layout.Context) layout.Dimensions {
		style := surfaceStyle{radius: button / 2, background: sc.Surface.Color.SetOpacity(1), content: sc.Surface.OnColor, button: l.T("chat_menu.search")}
		return s.button.Layout(gtx, image.Pt(button, button), style, func(gtx layout.Context) layout.Dimensions {
			px := gtx.Dp(24)
			return offset(gtx, image.Pt((button-px)/2, (button-px)/2), func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions {
					return iconSearch(gtx, sc.SurfaceVariant.OnColor)
				})
			})
		})
	})
}
