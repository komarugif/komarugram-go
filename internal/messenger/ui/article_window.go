// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"log"
	"sync"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/exp/appearance"
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/scroll"

	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// articleWindow shows the whole of a rich message in a window of its own,
// as Telegram Desktop's Instant View window does for one Telegram sent cut
// short (iv::Instance::showRichMessage). It shows the part the history has
// at once, and the whole article once the store loads it. Its article is
// drawn by a page of its own, as the history draws one, with its own photo
// viewer: a window draws on its own GPU context and runs on its own
// goroutine, and shares only the store with the chat.
type articleWindow struct {
	w *appwindow.Window
	// invalidate asks for a frame: the window's, or a test's.
	invalidate func()
	page       *chatPage
	viewer     *photoViewer
	images     imageOps
	catalog    localization.Catalog
	mode       func() themeMode
	list       scroll.List
	// message is the rich message shown: the part, then the whole one.
	message model.Message
	// fragment is the anchor to go to once the article that has it is
	// src is what the article is of, which the bar opens as the system
	// would: a Markdown file or a page; nothing for a rich message.
	src articleSource
	// shown; whole is set once the whole article is, or will not be.
	fragment string
	whole    bool
	// goTo takes the anchors links in the chat ask the open window to go
	// to.
	goTo chan string
	// back and ahead are where the window was before the anchors it went
	// to, and after them once it went back, as Telegram Desktop's window
	// steps through its history; backButton and aheadButton step.
	back, ahead             []int
	backButton, aheadButton surface
	tools                   articleTools
	loaded                  chan articleLoad
	cancel                  context.CancelFunc
	theme                   *token.Theme
	dark                    bool
	// themeFonts is the version of the fonts the theme was made with.
	themeFonts uint64
	closing    bool
}

// articleLoad is what the store answered for the whole article.
type articleLoad struct {
	page model.RichPage
	err  error
}

// Width of the article in the window, and its margins, in dp.
const (
	articleWindowWidth  = 680
	articleWindowMargin = 20
)

// articleWindowHost is what the account window gives an article window:
// the theme's mode, the zoom kept in the settings, and the chats to share
// to.
type articleWindowHost struct {
	mode    func() themeMode
	zoom    func() int
	setZoom func(int)
	chats   func() []model.Chat
}

func newArticleWindow(w *appwindow.Window, source model.ConversationStore, catalog localization.Catalog, host articleWindowHost, m model.Message, fragment string) *articleWindow {
	a := newArticleView(source, catalog, m, fragment, w.Invalidate)
	a.w, a.mode = w, host.mode
	a.tools.zoom, a.tools.setZoom = host.zoom, host.setZoom
	a.page.chats = host.chats
	return a
}

// newArticleView is what an article window shows of m, at its anchor
// fragment unless it is empty, without the window; invalidate asks for a
// frame.
func newArticleView(source model.ConversationStore, catalog localization.Catalog, m model.Message, fragment string, invalidate func()) *articleWindow {
	a := &articleWindow{invalidate: invalidate, catalog: catalog, message: m, fragment: fragment, goTo: make(chan string, 1)}
	a.list.Axis = layout.Vertical
	a.page = newChatPage(source, invalidate)
	a.page.images = &a.images
	a.page.rows = map[model.MessageID]*messageRow{}
	a.page.chat = m.Key.ChatID
	a.viewer = newPhotoViewer(source, &a.images, invalidate)
	a.page.openAlone = func(photo model.Message) { a.viewer.OpenAlone(m.Key.ChatID, photo) }
	// A link to an anchor the part does not have goes there once the whole
	// article is shown.
	a.page.openArticle = func(_ model.Message, name string) {
		a.fragment = name
		if a.whole {
			a.goToFragment()
		}
	}
	// The fragment is gone to in the first frame, or once the whole
	// article is shown when the part does not have it.
	if fragment != "" {
		a.page.anchorJump = m.Key.MessageID
	}
	store, ok := source.(model.RichStore)
	if !ok || m.Rich != nil && !m.Rich.Part {
		a.whole = true
		return a
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.loaded = make(chan articleLoad, 1)
	go func() {
		page, err := store.RichMessage(ctx, m.Key)
		a.loaded <- articleLoad{page: page, err: err}
		invalidate()
	}()
	return a
}

func (a *articleWindow) Theme(gtx layout.Context) *token.Theme {
	mode := a.mode()
	dark := mode == themeDark || mode == themeAuto && a.w.Appearance.Scheme() == appearance.Dark
	if v := defaults.FontsVersion(); a.theme == nil || v != a.themeFonts || dark != a.dark {
		a.themeFonts, a.dark = v, dark
		scheme := schemes.SchemeBaselineLight()
		if dark {
			scheme = schemes.SchemeBaselineDark()
		}
		a.theme = defaults.NewTheme(gtx, scheme)
	}
	a.w.SetFrameDark(dark)
	a.w.SetFrameColor(a.theme.Scheme.Surface.Color.AsNRGBA())
	return a.theme
}

func (a *articleWindow) Update(layout.Context) {}

func (a *articleWindow) Layout(gtx layout.Context) {
	a.images.BeginFrame()
	defer a.images.EndFrame()
	a.layout(gtx, a.w.Motion.AnimationsEnabled())
}

// layout draws the article, and over it what it opens: menus, dialogs,
// toasts and the photo viewer.
func (a *articleWindow) layout(gtx layout.Context, animate bool) {
	p, l := a.page, a.catalog
	p.animate = animate
	p.entityMenu.watch(gtx)
	a.steps(gtx)
	a.toolEvents(gtx)
	select {
	case name := <-a.goTo:
		a.fragment = name
		a.goToFragment()
	default:
	}
	select {
	case got := <-a.loaded:
		a.whole = true
		if got.err != nil {
			p.toast.Show(mediaErrorText(got.err))
		} else {
			a.message.Rich = &got.page
			a.message.ContentRevision++
		}
		if a.fragment != "" {
			a.goToFragment()
		}
	default:
	}
	if id := p.anchorJump; id != 0 {
		p.anchorJump = 0
		a.scrollToAnchor(gtx, id)
	}
	size := gtx.Constraints.Max
	bar := a.stepsBar(gtx)
	p.viewHeight = size.Y - bar
	fillRect(gtx, scheme(gtx).Surface.Color, size)
	body := gtx
	body.Constraints = layout.Exact(image.Pt(size.X, max(size.Y-bar, 0)))
	// The zoom scales what the article draws, as if its window's density
	// were higher.
	zoom := float32(a.zoomPercent()) / 100
	body.Metric.PxPerDp *= zoom
	body.Metric.PxPerSp *= zoom
	offset(body, image.Pt(0, bar), func(gtx layout.Context) layout.Dimensions {
		// What scrolled out of the view must not reach over the bar.
		defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
		return a.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
			return layout.UniformInset(articleWindowMargin).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(articleWindowWidth))
					gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, 0)
					return a.article(gtx, l, animate)
				})
			})
		})
	})
	a.layoutSteps(gtx, l)
	p.keyboardEvents(gtx)
	p.botUpdate(l)
	p.errorMu.Lock()
	if err := p.mediaError; err != nil {
		p.mediaError = nil
		p.toast.Show(mediaErrorText(err))
	}
	p.errorMu.Unlock()
	p.toast.Layout(gtx, image.Rectangle{Max: size})
	p.entityMenuLayout(gtx, l)
	p.layoutDialogs(gtx, l)
	if a.viewer.open {
		a.viewer.Layout(gtx, l, animate)
	}
}

// article draws the article, and under it, while the whole one loads, that
// it is loading. A fragment waits for the article that has it to be laid
// out.
func (a *articleWindow) article(gtx layout.Context, l localization.Catalog, animate bool) layout.Dimensions {
	p, m := a.page, a.message
	r := p.rows[m.Key.MessageID]
	if r == nil || r.revision != m.ContentRevision {
		r = newMessageRow(m, l, gtx.Now)
		p.rows[m.Key.MessageID] = r
	}
	r.refreshDates(gtx, m, l)
	if r.article == nil {
		return layout.Dimensions{}
	}
	// The article is the window's one item, under its margin.
	r.viewTop, r.viewKnown = gtx.Dp(articleWindowMargin)-a.list.Position.Offset, true
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			dims := p.articleLayout(gtx, r, m, l, animate)
			a.layoutMatches(gtx, r)
			return dims
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if a.whole {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return p.loader.sized(gtx, l, 32)
				})
			})
		}))
}

// goToFragment goes to the anchor the window was asked for, and tells
// when the whole article does not have it; one the part does not have
// waits for the whole. The row of an article that just came is made in the
// next frame: the jump waits for it.
func (a *articleWindow) goToFragment() {
	name := a.fragment
	if name == "" {
		return
	}
	r := a.page.rows[a.message.Key.MessageID]
	if r == nil || r.revision != a.message.ContentRevision || r.article == nil {
		a.page.anchorJump, a.page.anchorWait = a.message.Key.MessageID, 0
		a.invalidate()
		return
	}
	if !r.articleState.openTo(r.article, name) {
		if a.whole {
			a.fragment = ""
			a.page.toast.Show(a.catalog.T("rich.anchor_missing"))
		}
		return
	}
	a.fragment = ""
	r.articleState.jump = name
	a.page.anchorJump, a.page.anchorWait = r.key.MessageID, 0
	a.invalidate()
}

// scrollToAnchor scrolls the window to the anchor a link asked for, once it
// is laid out.
func (a *articleWindow) scrollToAnchor(gtx layout.Context, id model.MessageID) {
	if a.fragment != "" {
		// The row of the whole article is made now: the fragment is gone to
		// in the next frame.
		a.goToFragment()
		return
	}
	r := a.page.rows[id]
	top, ok := a.page.anchorTop(r)
	if !ok {
		return
	}
	a.back, a.ahead = append(a.back, a.list.Position.Offset), nil
	a.scrollTo(gtx.Dp(articleWindowMargin) + top)
}

// scrollTo puts the window y px into the article.
func (a *articleWindow) scrollTo(y int) {
	a.list.Position = layout.Position{First: 0, Offset: max(y, 0), BeforeEnd: true}
	a.invalidate()
}

// step goes back to where the window was before the last anchor it went
// to, or ahead again to where it was before it went back.
func (a *articleWindow) step(back bool) {
	from, to := &a.back, &a.ahead
	if !back {
		from, to = to, from
	}
	if len(*from) == 0 {
		return
	}
	y := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*to = append(*to, a.list.Position.Offset)
	a.scrollTo(y)
}

// steps takes the buttons and keys that step back and ahead: Alt with an
// arrow, or ⌘ with a bracket, as in browsers. They come before the text's
// keys, which move a selection by words with Alt and an arrow.
func (a *articleWindow) steps(gtx layout.Context) {
	if a.backButton.Clicked(gtx) {
		a.step(true)
	}
	if a.aheadButton.Clicked(gtx) {
		a.step(false)
	}
	for {
		ev, ok := gtx.Event(
			key.Filter{Name: key.NameLeftArrow, Required: key.ModAlt},
			key.Filter{Name: key.NameRightArrow, Required: key.ModAlt},
			key.Filter{Name: "[", Required: key.ModShortcut},
			key.Filter{Name: "]", Required: key.ModShortcut},
		)
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			a.step(e.Name == key.NameLeftArrow || e.Name == "[")
		}
	}
}

// stepsBar is how high the window's bar over the article is.
func (a *articleWindow) stepsBar(gtx layout.Context) int {
	return gtx.Dp(48)
}

// layoutSteps draws the bar of the buttons that step back and ahead, each
// dimmed while there is nowhere to step to, as Telegram Desktop's window
// has them over its page.
func (a *articleWindow) layoutSteps(gtx layout.Context, l localization.Catalog) {
	bar := a.stepsBar(gtx)
	if bar == 0 {
		return
	}
	sc := scheme(gtx)
	fillRect(gtx, sc.Surface.Color, image.Pt(gtx.Constraints.Max.X, bar))
	offset(gtx, image.Pt(0, bar-max(gtx.Dp(1), 1)), func(gtx layout.Context) layout.Dimensions {
		fillRect(gtx, sc.OutlineVariant, image.Pt(gtx.Constraints.Max.X, max(gtx.Dp(1), 1)))
		return layout.Dimensions{}
	})
	x := gtx.Dp(4)
	for _, s := range []struct {
		enabled bool
		button  *surface
		icon    wdk.IconWidget
		label   string
	}{{len(a.back) > 0, &a.backButton, iconBack, l.T("rich.back")}, {len(a.ahead) > 0, &a.aheadButton, iconAhead, l.T("rich.forward")}} {
		offset(gtx, image.Pt(x, 0), func(gtx layout.Context) layout.Dimensions {
			if !s.enabled {
				gtx = gtx.Disabled()
			}
			size := image.Pt(gtx.Dp(48), bar)
			gtx.Constraints = layout.Exact(size)
			content := sc.Surface.OnColor
			if !s.enabled {
				content = content.SetOpacity(.38)
			}
			style := surfaceStyle{radius: size.Y / 2, background: content.SetOpacity(0), content: content, button: s.label}
			return s.button.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(24), gtx.Dp(24)))
					return s.icon(gtx, content)
				})
			})
		})
		x += gtx.Dp(48)
	}
	a.layoutTools(gtx, bar, x+gtx.Dp(8), l)
}

func (a *articleWindow) Locale() system.Locale {
	return system.Locale{Language: string(a.catalog.Language()), Direction: system.LTR}
}

func (a *articleWindow) SetSuspended(hidden bool) {
	if hidden {
		a.viewer.Release()
		a.images.Release()
	}
}

// Close is called by the window loop after the window is gone.
func (a *articleWindow) Close() {
	if a.cancel != nil {
		a.cancel()
	}
	a.viewer.Destroy()
	// The page saves no viewport of the chat: it shows none of it.
	a.page.chat = 0
	a.page.Close()
}

// articleWindows are the article windows opened from one account window,
// by message. They read that account's store, so they close with it.
type articleWindows struct {
	mu   sync.Mutex
	open map[model.MessageKey]*articleWindow
}

// raise brings the window of key to the front, going to the anchor
// fragment unless it is empty, and reports whether one is open.
func (ws *articleWindows) raise(key model.MessageKey, fragment string) bool {
	ws.mu.Lock()
	a := ws.open[key]
	ws.mu.Unlock()
	if a == nil {
		return false
	}
	if fragment != "" {
		select {
		case a.goTo <- fragment:
		default:
		}
	}
	a.w.PerformLater(system.ActionRaise)
	a.w.Invalidate()
	return true
}

func (ws *articleWindows) add(key model.MessageKey, a *articleWindow) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.open == nil {
		ws.open = map[model.MessageKey]*articleWindow{}
	}
	ws.open[key] = a
}

func (ws *articleWindows) remove(key model.MessageKey) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	delete(ws.open, key)
}

func (ws *articleWindows) closeAll() {
	ws.mu.Lock()
	windows := make([]*appwindow.Window, 0, len(ws.open))
	for _, a := range ws.open {
		windows = append(windows, a.w)
	}
	ws.mu.Unlock()
	for _, w := range windows {
		w.PerformLater(system.ActionClose)
	}
}

// openArticleWindow shows the whole of rich message m in a window, at its
// anchor fragment unless it is empty; the window of m, when open, comes to
// the front.
func (a *App) openArticleWindow(m model.Message, fragment string) {
	title := ""
	for _, c := range a.store.Chats() {
		if c.ID == m.Key.ChatID {
			title = c.Title
		}
	}
	a.openArticleWindowOf(m, fragment, title, articleSource{})
}

// openSourceWindow shows article, of src, a Markdown file or a page with
// an Instant View, in a window titled title, whose bar opens src as the
// system would.
func (a *App) openSourceWindow(article model.Message, title string, src articleSource) {
	a.openArticleWindowOf(article, "", title, src)
}

// openArticleWindowOf shows m in a window titled title, at fragment; src
// is what m is the article of, if it is not a rich message.
func (a *App) openArticleWindowOf(m model.Message, fragment, title string, src articleSource) {
	if a.articleWindows.raise(m.Key, fragment) {
		return
	}
	catalog := a.catalog()
	source := a.history.source
	a.openWindow(appwindow.Spec{
		Options: appwindow.Options{Title: title, Width: unit.Dp(articleWindowWidth + 2*articleWindowMargin + 40), Height: unit.Dp(860), Locale: a.Locale()},
		Build: func(w *appwindow.Window) appwindow.Content {
			host := articleWindowHost{
				mode:  a.themeMode,
				zoom:  func() int { return a.preferences.Global().ArticleZoom },
				chats: a.store.Chats,
				setZoom: func(z int) {
					if err := a.preferences.SetArticleZoom(z); err != nil {
						log.Printf("save settings: %v", err)
					}
				},
			}
			window := newArticleWindow(w, source, catalog, host, m, fragment)
			window.src = src
			a.articleWindows.add(m.Key, window)
			return window
		},
		// Build and Closed run on the new window's goroutine, in that order.
		Closed: func() { a.articleWindows.remove(m.Key) },
	})
}
