// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"strconv"
	"strings"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

const (
	// chatSearchPage is how many found messages a page has.
	chatSearchPage = 50
	// chatSearchDelay lets typing settle before the search starts.
	chatSearchDelay = 300 * time.Millisecond
	// chatSearchAhead is how close to the last found message the next page
	// is asked for.
	chatSearchAhead = 3
	// highlightTime is how long a message gone to from a search stays
	// tinted.
	highlightTime = 1500 * time.Millisecond
)

// chatSearch searches the open chat, as Telegram Desktop's search in a chat
// does: a field over the chat's header, the found messages newest first,
// and buttons that go to the one before and after. Enter goes to the one
// before, older.
type chatSearch struct {
	open bool
	// field is what to search for.
	field widget.Editor
	// typed is the text as typed, due when it is searched.
	typed string
	due   time.Time
	// query is the text searched; results what it found so far, newest
	// first, and current the one shown, -1 for none.
	query   string
	results []model.Message
	count   int
	next    string
	loading bool
	failed  bool
	current int
	// older is set when a click went past the last found message: the next
	// page goes on to the next one.
	older  bool
	gen    int
	res    chan chatSearchResult
	cancel context.CancelFunc
	up     surface
	down   surface
	close  surface
	loader loadingIndicator
}

type chatSearchResult struct {
	gen  int
	page model.ChatSearchPage
	err  error
}

// canSearchChat reports whether the store can search the open chat: a
// topic and a post's comments too, as Telegram Desktop searches them.
func (p *chatPage) canSearchChat() bool {
	_, ok := p.source.(model.ChatSearcher)
	return ok
}

// openChatSearch shows the field over the header, focused.
func (p *chatPage) openChatSearch(gtx layout.Context) {
	s := &p.chatSearch
	s.open = true
	s.field.SingleLine, s.field.Submit = true, true
	gtx.Execute(key.FocusCmd{Tag: &s.field})
	p.invalidate()
}

// closeChatSearch hides the field and forgets what it found.
func (p *chatPage) closeChatSearch() {
	s := &p.chatSearch
	if s.cancel != nil {
		s.cancel()
	}
	s.field.SetText("")
	s.open, s.typed, s.due, s.gen = false, "", time.Time{}, s.gen+1
	s.query, s.results, s.count, s.next, s.loading, s.failed, s.current, s.older = "", nil, 0, "", false, false, -1, false
}

// start searches text from its first page.
func (s *chatSearch) start(p *chatPage, text string) {
	if s.cancel != nil {
		s.cancel()
	}
	s.gen++
	s.query, s.results, s.count, s.next, s.failed, s.current, s.older = text, nil, 0, "", false, -1, false
	s.loading = false
	if text == "" {
		return
	}
	s.fetch(p)
}

// fetch asks for the next page of what the search found.
func (s *chatSearch) fetch(p *chatPage) {
	searcher, ok := p.source.(model.ChatSearcher)
	if !ok || s.loading {
		return
	}
	s.loading = true
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	s.cancel = cancel
	if s.res == nil {
		s.res = make(chan chatSearchResult, 4)
	}
	gen, chat, query, next, res := s.gen, p.chat, s.query, s.next, s.res
	go func() {
		defer cancel()
		page, err := searcher.SearchChat(ctx, chat, query, next, chatSearchPage)
		res <- chatSearchResult{gen, page, err}
		p.invalidate()
	}()
}

// update takes what the search found, and handles the field and buttons.
func (p *chatPage) chatSearchUpdate(gtx layout.Context) {
	s := &p.chatSearch
	if !s.open {
		return
	}
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
			s.results = append(s.results, r.page.Messages...)
			s.next, s.count = r.page.Next, max(r.page.Count, len(s.results))
			if s.current < 0 && len(s.results) > 0 || s.older && s.current+1 < len(s.results) {
				s.older = false
				s.show(p, s.current+1)
			}
			continue
		default:
		}
		break
	}
	if s.close.Clicked(gtx) {
		p.closeChatSearch()
		return
	}
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			p.closeChatSearch()
			return
		}
	}
	submitted := false
	for {
		ev, ok := s.field.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			submitted = true
		}
	}
	if submitted || s.up.Clicked(gtx) {
		s.goOlder(p)
	}
	if s.down.Clicked(gtx) && s.current > 0 {
		s.show(p, s.current-1)
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
				s.start(p, s.typed)
			}
		}
	}
}

// goOlder shows the found message before the current one, asking for the
// next page when it is not there yet.
func (s *chatSearch) goOlder(p *chatPage) {
	if s.current+1 < len(s.results) {
		s.show(p, s.current+1)
	} else if s.next != "" {
		s.older = true
	}
	if s.next != "" && !s.failed && s.current+chatSearchAhead >= len(s.results) {
		s.fetch(p)
	}
}

// show goes to found message i.
func (s *chatSearch) show(p *chatPage, i int) {
	if i < 0 || i >= len(s.results) {
		return
	}
	s.current = i
	id := s.results[i].Key.MessageID
	p.highlight, p.highlightUntil = id, time.Now().Add(highlightTime)
	p.jumpTo(id)
}

// counter tells which found message is shown of how many.
func (s *chatSearch) counter(l localization.Catalog) string {
	switch {
	case s.query == "":
		return ""
	case s.failed && len(s.results) == 0:
		return l.T("search.failed")
	case !s.loading && len(s.results) == 0:
		return l.T("chat_search.none")
	case len(s.results) == 0:
		return ""
	}
	amount := strconv.Itoa(s.count)
	if s.next != "" && s.count <= len(s.results) {
		amount += "+"
	}
	return l.Format("chat_search.position", map[string]string{"n": strconv.Itoa(max(s.current, 0) + 1), "amount": amount})
}

// layoutChatSearch draws the search over the header of size.
func (p *chatPage) layoutChatSearch(gtx layout.Context, size image.Point, l localization.Catalog) {
	s := &p.chatSearch
	sc := scheme(gtx)
	fillWindowSurface(gtx, sc.Surface.Color, size)
	button := gtx.Dp(40)
	pad := gtx.Dp(8)
	right := size.X - pad
	iconButton := func(sf *surface, icon func(gtx layout.Context, col token.MatColor) layout.Dimensions, name string, enabled bool) {
		right -= button
		col := sc.SurfaceVariant.OnColor
		if !enabled {
			col = col.SetOpacity(.38)
		}
		offset(gtx, image.Pt(right, (size.Y-button)/2), func(gtx layout.Context) layout.Dimensions {
			sz := image.Pt(button, button)
			style := surfaceStyle{radius: button / 2, content: sc.Surface.OnColor, button: name}
			return sf.Layout(gtx, sz, style, func(gtx layout.Context) layout.Dimensions {
				px := gtx.Dp(24)
				return offset(gtx, image.Pt((button-px)/2, (button-px)/2), func(gtx layout.Context) layout.Dimensions {
					return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions { return icon(gtx, col) })
				})
			})
		})
	}
	iconButton(&s.close, iconClear, l.T("chat_search.close"), true)
	iconButton(&s.down, iconExpandMore, l.T("chat_search.newer"), s.current > 0)
	iconButton(&s.up, iconExpandLess, l.T("chat_search.older"), s.current+1 < len(s.results) || s.next != "")
	counter := s.counter(l)
	right -= gtx.Dp(4)
	if s.loading && len(s.results) == 0 {
		px := gtx.Dp(20)
		right -= px
		offset(gtx, image.Pt(right, (size.Y-px)/2), func(gtx layout.Context) layout.Dimensions {
			return s.loader.sized(gtx, l, 20)
		})
	} else if counter != "" {
		rec := op.Record(gtx.Ops)
		cgtx := gtx
		cgtx.Constraints = layout.Constraints{Max: image.Pt(size.X/3, size.Y)}
		dims := label(cgtx, counter, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
		call := rec.Stop()
		right -= dims.Size.X
		offset(gtx, image.Pt(right, (size.Y-dims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return dims
		})
	}
	right -= gtx.Dp(8)
	offset(gtx, image.Pt(pad, 0), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(max(right-pad, 0), size.Y))
		return s.layoutField(gtx, l)
	})
	offset(gtx, image.Pt(0, size.Y-gtx.Dp(1)), func(gtx layout.Context) layout.Dimensions {
		fillRect(gtx, sc.OutlineVariant, image.Pt(size.X, gtx.Dp(1)))
		return layout.Dimensions{}
	})
}

// layoutField draws the field, a pill with the search icon, across the
// middle of gtx.
func (s *chatSearch) layoutField(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	return layoutSearchField(gtx, &s.field, l)
}

// layoutSearchField draws field as a pill with the search icon, across
// the middle of gtx, as a search over a chat's header has it.
func layoutSearchField(gtx layout.Context, field *widget.Editor, l localization.Catalog) layout.Dimensions {
	return layoutSearchFieldHint(gtx, field, l.T("chat_search.hint"))
}

// layoutSearchFieldHint is layoutSearchField with hint in the empty field.
func layoutSearchFieldHint(gtx layout.Context, field *widget.Editor, hint string) layout.Dimensions {
	sc := scheme(gtx)
	theme := wdk.GetMaterialTheme(gtx)
	size := gtx.Constraints.Max
	height := gtx.Dp(40)
	top := (size.Y - height) / 2
	offset(gtx, image.Pt(0, top), func(gtx layout.Context) layout.Dimensions {
		fillRounded(gtx, sc.SurfaceContainerHigh, image.Pt(size.X, height), height/2)
		return layout.Dimensions{}
	})
	px := gtx.Dp(20)
	offset(gtx, image.Pt(gtx.Dp(12), (size.Y-px)/2), func(gtx layout.Context) layout.Dimensions {
		return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions {
			return iconSearch(gtx, sc.SurfaceVariant.OnColor)
		})
	})
	style := theme.Typescale[token.TypestyleBodyLarge]
	field.LineHeight = style.LineHeight
	textX := gtx.Dp(12) + px + gtx.Dp(10)
	line := gtx.Sp(style.LineHeight)
	fieldGtx := gtx
	fieldGtx.Constraints = layout.Exact(image.Pt(max(size.X-textX-gtx.Dp(12), 0), line))
	offset(fieldGtx, image.Pt(textX, (size.Y-line)/2), func(gtx layout.Context) layout.Dimensions {
		if field.Len() == 0 {
			label(gtx, hint, token.TypestyleBodyLarge, sc.SurfaceVariant.OnColor, 1)
		}
		color := op.Record(gtx.Ops)
		paint.ColorOp{Color: sc.Surface.OnColor.AsNRGBA()}.Add(gtx.Ops)
		text := color.Stop()
		color = op.Record(gtx.Ops)
		paint.ColorOp{Color: sc.Primary.Color.SetOpacity(token.OpacityLevel3).AsNRGBA()}.Add(gtx.Ops)
		selection := color.Stop()
		return field.Layout(gtx, theme.TextShaper, style.AsRegularFont(), style.Size, text, selection)
	})
	return layout.Dimensions{Size: size}
}
