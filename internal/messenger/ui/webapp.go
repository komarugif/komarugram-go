// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gio-mw/token"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/miniapps"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/miniapp"
)

// Mini Apps are opened by their bots' buttons: a WebView button under a
// message or in a keyboard, or the menu button beside the composer. They run
// in the user's Chromium (internal/messenger/miniapps); what happens to one
// while it runs comes back to the window through webApps.

// webApps is the window's Mini Apps.
type webApps struct {
	runner *miniapps.Runner
	// account names whose apps share a profile.
	account func() string

	mu sync.Mutex
	// links and failure are what the runner's goroutines ask the window to
	// show on its next frame.
	links   []string
	failure error
}

// initMiniApps makes the window's runner, if the store can ask for Mini Apps.
func (a *App) initMiniApps(store model.Store, storage func() miniapp.Storage, account func() string) {
	source, ok := store.(model.WebViewStore)
	if !ok {
		return
	}
	root := ""
	if dir, err := os.UserConfigDir(); err == nil {
		root = filepath.Join(dir, "komarugram-go", "miniapp")
	}
	w := &webApps{account: account}
	w.runner = miniapps.New(source, miniapps.Hooks{
		Link: func(link string) {
			w.mu.Lock()
			w.links = append(w.links, link)
			w.mu.Unlock()
			a.window.Invalidate()
		},
		Failed: func(err error) {
			w.mu.Lock()
			w.failure = err
			w.mu.Unlock()
			a.window.Invalidate()
		},
	}, storage, root)
	a.mini = w
}

// launchWebApp opens the Mini App that req asks for; button is the text of the
// keyboard button it came from.
func (a *App) launchWebApp(gtx layout.Context, p *chatPage, req model.WebViewRequest, button string) {
	w := a.mini
	if w == nil {
		return
	}
	l := a.catalog()
	if !miniapps.Available() {
		if p != nil {
			p.toast.Show(l.T("miniapp.no_browser"))
		} else {
			a.toast.Show(l.T("miniapp.no_browser"))
		}
		return
	}
	account := ""
	if w.account != nil {
		account = w.account()
	}
	w.runner.Open(miniapps.Launch{Request: req, Button: button, Account: account}, webAppTheme(gtx))
}

// updateMiniApps shows on page what the running Mini Apps asked of the window:
// a link to open, or why one did not open.
func (a *App) updateMiniApps(p *chatPage, l localization.Catalog) {
	w := a.mini
	if w == nil || p == nil {
		return
	}
	w.mu.Lock()
	links, failure := w.links, w.failure
	w.links, w.failure = nil, nil
	w.mu.Unlock()
	if failure != nil {
		p.toast.Show(webAppErrorText(failure, l))
	}
	for _, link := range links {
		p.askLink(link)
	}
}

// closeMiniApps closes what is running, when the window goes.
func (a *App) closeMiniApps() {
	if a.mini != nil {
		a.mini.runner.Close()
	}
}

// webAppErrorText says why a Mini App did not open.
func webAppErrorText(err error, l localization.Catalog) string {
	return programErrorText(err, "", l)
}

// webAppTheme is the colors of the window, as Mini Apps are told them
// (themeParams of the Bot API).
func webAppTheme(gtx layout.Context) string {
	sc := scheme(gtx)
	hex := func(c token.MatColor) string {
		n := c.AsNRGBA()
		return fmt.Sprintf("#%02x%02x%02x", n.R, n.G, n.B)
	}
	theme := map[string]string{
		"bg_color":                  hex(sc.Surface.Color),
		"text_color":                hex(sc.Surface.OnColor),
		"hint_color":                hex(sc.SurfaceVariant.OnColor),
		"link_color":                hex(sc.Primary.Color),
		"button_color":              hex(sc.Primary.Color),
		"button_text_color":         hex(sc.Primary.OnColor),
		"secondary_bg_color":        hex(sc.SurfaceContainerLow),
		"header_bg_color":           hex(sc.Surface.Color),
		"accent_text_color":         hex(sc.Primary.Color),
		"section_bg_color":          hex(sc.Surface.Color),
		"section_header_text_color": hex(sc.Primary.Color),
		"subtitle_text_color":       hex(sc.SurfaceVariant.OnColor),
		"destructive_text_color":    "#e53935",
	}
	b, _ := json.Marshal(theme)
	return string(b)
}

// botOf is the bot a message of the chat is from, for the Mini App its
// buttons open: the message's sender, or the bot the chat is with.
func (p *chatPage) botOf(chat int64, m model.Message) int64 {
	if m.SenderID != 0 && !m.Outgoing {
		return m.SenderID
	}
	if p.kind == model.KindBot {
		return chat
	}
	return 0
}

// pressWebView opens the Mini App of a WebView button b of message m.
func (p *chatPage) pressWebView(gtx layout.Context, chat int64, m model.Message, b model.MessageButton) {
	if p.openWebApp == nil {
		return
	}
	bot := p.botOf(chat, m)
	if bot == 0 {
		return
	}
	kind := model.WebViewInline
	if b.Kind == "simple_webview" {
		kind = model.WebViewSimple
	}
	p.openWebApp(gtx, p, model.WebViewRequest{Kind: kind, Chat: chat, Bot: bot, URL: b.URL}, b.Text)
}
