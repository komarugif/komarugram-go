// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strconv"
	"unicode"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"golang.org/x/exp/shiny/materialdesign/icons"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// The bar of the article window holds what Telegram Desktop's window does:
// the steps back and ahead through the anchors gone to, the search of the
// article (Ctrl+F), sharing, and the zoom (Ctrl with =, - and 0, by 10 %
// from 25 to 400 %, the same in every window and kept in the settings).

var (
	iconShare   = wdk.RequireIconWidget(icons.SocialShare)
	iconZoomIn  = wdk.RequireIconWidget(icons.ContentAdd)
	iconZoomOut = wdk.RequireIconWidget(icons.ContentRemove)
)

// Zoom bounds and step, in percent, as Telegram Desktop's.
const (
	articleZoomMin  = 25
	articleZoomMax  = 400
	articleZoomStep = 10
)

// articleTools are the window's search, sharing and zoom.
type articleTools struct {
	// zoom is how big articles are drawn, in percent, 0 for 100; setZoom
	// keeps it.
	zoom                       func() int
	setZoom                    func(int)
	zoomOut, zoomIn, zoomReset surface
	share, openFile            surface
	searchButton               surface
	search                     articleSearch
}

// articleSearch is the search of the article: what it looks for, where in
// the article's text it is found, and which of those is shown.
type articleSearch struct {
	open           bool
	focus          bool
	field          widget.Editor
	query          string
	matches        []int
	length         int
	current        int
	up, down, shut surface
}

// zoomPercent is the zoom of the window, in percent.
func (a *articleWindow) zoomPercent() int {
	if a.tools.zoom == nil {
		return 100
	}
	if z := a.tools.zoom(); z != 0 {
		return z
	}
	return 100
}

// zoomBy changes the zoom by step percent, or back to 100 for 0.
func (a *articleWindow) zoomBy(step int) {
	z := 100
	if step != 0 {
		z = min(max(a.zoomPercent()+step, articleZoomMin), articleZoomMax)
	}
	if a.tools.setZoom != nil {
		a.tools.setZoom(z)
	}
	a.invalidate()
}

// canShare reports whether the message can be forwarded from the window.
func (a *articleWindow) canShare() bool {
	_, ok := a.page.source.(model.MessageForwarder)
	return ok && a.page.chats != nil && !a.message.NoForwards
}

// tools takes the clicks and keys of the bar: the zoom, sharing and the
// search, before the text's keys.
func (a *articleWindow) toolEvents(gtx layout.Context) {
	t := &a.tools
	if t.zoomOut.Clicked(gtx) {
		a.zoomBy(-articleZoomStep)
	}
	if t.zoomIn.Clicked(gtx) {
		a.zoomBy(articleZoomStep)
	}
	if t.zoomReset.Clicked(gtx) {
		a.zoomBy(0)
	}
	if t.share.Clicked(gtx) && a.canShare() {
		a.page.openForwardParts(gtx, []model.Message{a.message}, false)
	}
	if t.searchButton.Clicked(gtx) {
		a.openSearch()
	}
	if t.openFile.Clicked(gtx) {
		switch {
		case a.src.file != nil:
			a.page.openAttachment(*a.src.file)
		case a.src.url != "":
			a.page.askLink(a.src.url)
		}
	}
	for {
		ev, ok := gtx.Event(
			key.Filter{Name: "F", Required: key.ModShortcut},
			key.Filter{Name: "А", Required: key.ModShortcut},
			key.Filter{Name: "=", Required: key.ModShortcut, Optional: key.ModShift},
			key.Filter{Name: "+", Required: key.ModShortcut, Optional: key.ModShift},
			key.Filter{Name: "-", Required: key.ModShortcut},
			key.Filter{Name: "0", Required: key.ModShortcut},
		)
		if !ok {
			break
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		switch e.Name {
		case "F", "А":
			a.openSearch()
		case "=", "+":
			a.zoomBy(articleZoomStep)
		case "-":
			a.zoomBy(-articleZoomStep)
		case "0":
			a.zoomBy(0)
		}
	}
	a.searchEvents(gtx)
}

func (a *articleWindow) openSearch() {
	s := &a.tools.search
	s.open, s.focus = true, true
	s.field.SingleLine, s.field.Submit = true, true
	a.invalidate()
}

func (a *articleWindow) closeSearch() {
	s := &a.tools.search
	s.open, s.query, s.matches = false, "", nil
	s.field.SetText("")
	a.page.activeText = nil
	a.invalidate()
}

// searchEvents follows the search's field and buttons: a new query looks
// again from the first match, Enter and the arrows go to the next match
// and the one before, Escape closes it.
func (a *articleWindow) searchEvents(gtx layout.Context) {
	s := &a.tools.search
	if !s.open {
		return
	}
	if s.shut.Clicked(gtx) {
		a.closeSearch()
		return
	}
	if s.down.Clicked(gtx) {
		a.goToMatch(gtx, s.current+1)
	}
	if s.up.Clicked(gtx) {
		a.goToMatch(gtx, s.current-1)
	}
	for {
		ev, ok := s.field.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			a.goToMatch(gtx, s.current+1)
		}
	}
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &s.field, Name: key.NameEscape}, key.Filter{Focus: &s.field, Name: key.NameReturn, Required: key.ModShift})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			if e.Name == key.NameEscape {
				a.closeSearch()
				return
			}
			a.goToMatch(gtx, s.current-1)
		}
	}
	if q := s.field.Text(); q != s.query {
		s.query = q
		a.findMatches()
		a.goToMatch(gtx, 0)
	}
}

// findMatches finds the query in the article's text, letters of any case.
func (a *articleWindow) findMatches() {
	s := &a.tools.search
	s.matches, s.current = nil, 0
	r := a.page.rows[a.message.Key.MessageID]
	query := []rune(s.query)
	s.length = len(query)
	if r == nil || len(query) == 0 {
		return
	}
	for i := range query {
		query[i] = unicode.ToLower(query[i])
	}
	var text []rune
	for _, run := range r.runs {
		for _, c := range run.Text {
			text = append(text, unicode.ToLower(c))
		}
	}
	for i := 0; i+len(query) <= len(text); i++ {
		match := true
		for j, c := range query {
			if text[i+j] != c {
				match = false
				break
			}
		}
		if match {
			s.matches = append(s.matches, i)
			i += len(query) - 1
		}
	}
}

// goToMatch selects match i, around the ends, and scrolls the window to
// it.
func (a *articleWindow) goToMatch(gtx layout.Context, i int) {
	s := &a.tools.search
	r := a.page.rows[a.message.Key.MessageID]
	if r == nil || len(s.matches) == 0 {
		return
	}
	i = (i%len(s.matches) + len(s.matches)) % len(s.matches)
	s.current = i
	start := s.matches[i]
	a.page.activeText = r
	r.text.anchor, r.text.caret = start, start+s.length
	for _, f := range r.text.fragments {
		for _, c := range f.Clusters {
			if c.Start <= start && start < c.End {
				zoom := float32(a.zoomPercent()) / 100
				a.scrollTo(int(float32(gtx.Dp(articleWindowMargin))*zoom) + c.Bounds.Min.Y - a.page.viewHeight/3)
				return
			}
		}
	}
}

// layoutMatches tints the matches of the search in the article but the
// one selected, lighter than the selection, over the text: under it the
// plates of code and tables would hide them.
func (a *articleWindow) layoutMatches(gtx layout.Context, r *messageRow) {
	s := &a.tools.search
	if !s.open || len(s.matches) == 0 {
		return
	}
	col := scheme(gtx).Tertiary.Color.SetOpacity(.14).AsNRGBA()
	for i, start := range s.matches {
		if i == s.current {
			continue
		}
		match := textInteraction{fragments: r.text.fragments, anchor: start, caret: start + s.length}
		for _, rect := range match.selectionRegions() {
			paint.FillShape(gtx.Ops, col, clip.Rect(rect).Op())
		}
	}
}

// layoutTools draws the bar's right side: the search's field, from from,
// its count and arrows while it is open, else the zoom, sharing and the
// search's button.
func (a *articleWindow) layoutTools(gtx layout.Context, bar int, from int, l localization.Catalog) {
	// What the bar holds takes its own size.
	gtx.Constraints.Min = image.Point{}
	sc := scheme(gtx)
	t := &a.tools
	right := gtx.Constraints.Max.X - gtx.Dp(4)
	button := func(s *surface, icon wdk.IconWidget, label string, enabled bool) {
		right -= gtx.Dp(44)
		offset(gtx, image.Pt(right, 0), func(gtx layout.Context) layout.Dimensions {
			if !enabled {
				gtx = gtx.Disabled()
			}
			size := image.Pt(gtx.Dp(44), bar)
			content := sc.Surface.OnColor
			if !enabled {
				content = content.SetOpacity(.38)
			}
			return s.Layout(gtx, size, surfaceStyle{radius: size.X / 2, background: content.SetOpacity(0), content: content, button: label}, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(size)
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(22), gtx.Dp(22)))
					return icon(gtx, content)
				})
			})
		})
	}
	textAt := func(text string, s *surface) {
		macro := op.Record(gtx.Ops)
		dims := label(gtx, text, token.TypestyleLabelLarge, sc.SurfaceVariant.OnColor, 1)
		call := macro.Stop()
		w := dims.Size.X + gtx.Dp(12)
		right -= w
		offset(gtx, image.Pt(right, 0), func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(w, bar)
			draw := func(gtx layout.Context) layout.Dimensions {
				offset(gtx, image.Pt(gtx.Dp(6), (bar-dims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
					call.Add(gtx.Ops)
					return dims
				})
				return layout.Dimensions{Size: size}
			}
			if s == nil {
				return draw(gtx)
			}
			return s.Layout(gtx, size, surfaceStyle{radius: gtx.Dp(8), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: text}, draw)
		})
	}
	if s := &t.search; s.open {
		button(&s.shut, iconClear, l.T("chat_search.close"), true)
		button(&s.down, iconExpandMore, l.T("chat_search.newer"), len(s.matches) > 1)
		button(&s.up, iconExpandLess, l.T("chat_search.older"), len(s.matches) > 1)
		switch {
		case len(s.matches) > 0:
			textAt(l.Format("chat_search.position", map[string]string{"n": strconv.Itoa(s.current + 1), "amount": strconv.Itoa(len(s.matches))}), nil)
		case s.query != "":
			textAt(l.T("chat_search.none"), nil)
		}
		right -= gtx.Dp(4)
		offset(gtx, image.Pt(from, 0), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(max(right-from, 0), bar))
			return layoutSearchFieldHint(gtx, &s.field, l.T("rich.search"))
		})
		if s.focus {
			s.focus = false
			gtx.Execute(key.FocusCmd{Tag: &s.field})
		}
		return
	}
	button(&t.searchButton, iconSearch, l.T("rich.search"), true)
	switch {
	case a.src.file != nil:
		button(&t.openFile, iconOpenInNew, l.T("rich.open_file"), true)
	case a.src.url != "":
		button(&t.openFile, iconOpenInNew, l.T("rich.open_in_browser"), true)
	}
	if a.canShare() {
		button(&t.share, iconShare, l.T("rich.share"), true)
	}
	zoom := a.zoomPercent()
	button(&t.zoomIn, iconZoomIn, l.T("rich.zoom_in"), zoom < articleZoomMax)
	textAt(strconv.Itoa(zoom)+" %", &t.zoomReset)
	button(&t.zoomOut, iconZoomOut, l.T("rich.zoom_out"), zoom > articleZoomMin)
}
