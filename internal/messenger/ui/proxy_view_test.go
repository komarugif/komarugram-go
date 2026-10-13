// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
	"komarugram/internal/messenger/proxy"
)

// pingLog is a ping that answers at once, and remembers whom it was asked
// about: "" for the direct connection.
type pingLog struct {
	mu    sync.Mutex
	asked []string
	fail  map[string]error
}

func (p *pingLog) ping(_ context.Context, pr *proxy.Proxy) (time.Duration, error) {
	id := proxy.DirectID
	if pr != nil {
		id = pr.ID()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, id)
	if err := p.fail[id]; err != nil {
		return 0, err
	}
	return 42 * time.Millisecond, nil
}

func (p *pingLog) ids() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := slices.Clone(p.asked)
	slices.Sort(ids)
	return ids
}

// bareContext is a context for Update, which draws nothing.
func bareContext() layout.Context {
	return layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(600, 600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
}

// settle updates the view until what it is doing in the background is done.
func settle(t *testing.T, v *proxyView, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v.Update(bareContext())
		if done() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestProxyViewMeasuresEveryProxyWhenItOpens(t *testing.T) {
	store := proxy.NewStore("", nil)
	socks := proxy.Proxy{Kind: proxy.SOCKS5, Host: "127.0.0.1", Port: 2080}
	store.Add(socks)
	log := &pingLog{}
	v := newProxyView(store, log.ping, nil)
	v.Update(bareContext())
	tg := proxy.Telegram()
	want := []string{proxy.DirectID, socks.ID(), tg.ID()}
	slices.Sort(want)
	settle(t, v, func() bool { return len(log.ids()) == len(want) })
	if got := log.ids(); !slices.Equal(got, want) {
		t.Errorf("measured %v, want %v", got, want)
	}
	// Not again with every frame.
	for range 5 {
		v.Update(bareContext())
	}
	time.Sleep(20 * time.Millisecond)
	if got := len(log.ids()); got != len(want) {
		t.Errorf("measured %d times for the one opening", got)
	}
	// But again where it is opened again.
	v.Open()
	settle(t, v, func() bool { return len(log.ids()) == 2*len(want) })
}

func TestProxyViewAddsALink(t *testing.T) {
	store := proxy.NewStore("", nil)
	log := &pingLog{}
	v := newProxyView(store, log.ping, nil)
	v.Update(bareContext())

	v.link.editor.SetText("socks5://bob:hunter2@127.0.0.1:1080")
	v.Update(bareContext())
	v.beginAdd()
	settle(t, v, func() bool { return len(store.List()) == 1 })
	want := proxy.Proxy{Kind: proxy.SOCKS5, Host: "127.0.0.1", Port: 1080, User: "bob", Password: "hunter2"}
	if got := store.List()[0]; got != want {
		t.Errorf("added %+v, want %+v", got, want)
	}
	if p, ok := store.Active(); !ok || p != want {
		t.Errorf("the one added is not in use: %v %v", p, ok)
	}
	if v.link.Text() != "" || v.problem != nil {
		t.Errorf("the field keeps %q (%v) after the link was added", v.link.Text(), v.problem)
	}
	// And it is measured, the first time.
	settle(t, v, func() bool { return slices.Contains(log.ids(), want.ID()) })
}

func TestProxyViewTellsWhatIsWrongWithALink(t *testing.T) {
	store := proxy.NewStore("", nil)
	v := newProxyView(store, (&pingLog{}).ping, nil)
	l := localization.For("en")
	for link, want := range map[string]string{
		"https://example.org":          l.T("proxy.err_unknown"),
		"socks5://127.0.0.1:99999":     l.T("proxy.err_port"),
		"tg://proxy?server=x&port=1":   l.T("proxy.err_secret_missing"),
		"socks5://bob:@127.0.0.1:1080": l.T("proxy.err_password"),
	} {
		v.link.editor.SetText(link)
		v.Update(bareContext())
		v.beginAdd()
		settle(t, v, func() bool { return v.problem != nil })
		if got := proxyErrorText(v.problem, l); got != want {
			t.Errorf("%q: %q, want %q", link, got, want)
		}
		if v.link.Text() != link {
			t.Errorf("%q: the link was taken out of the field", link)
		}
		// The problem goes away when the link is edited.
		v.link.editor.SetText(link + "0")
		v.Update(bareContext())
		if v.problem != nil {
			t.Errorf("%q: the problem stays after an edit", link)
		}
	}
	if len(store.List()) != 0 {
		t.Errorf("something was added: %v", store.List())
	}
}

func TestProxyViewSummary(t *testing.T) {
	store := proxy.NewStore("", nil)
	v := newProxyView(store, nil, nil)
	l := localization.For("en")
	if got := v.Summary(l); got != l.T("settings.proxy_none") {
		t.Errorf("no proxy: %q", got)
	}
	store.Add(proxy.Proxy{Kind: proxy.SOCKS5, Host: "127.0.0.1", Port: 2080})
	if got := v.Summary(l); got != "127.0.0.1:2080" {
		t.Errorf("a proxy: %q", got)
	}
	store.Select(proxy.Telegram().ID())
	if got := v.Summary(l); got != l.T("proxy.websocket") {
		t.Errorf("the WebSocket: %q", got)
	}
}

func TestProxyPingTexts(t *testing.T) {
	l := localization.For("en")
	v := newProxyView(nil, nil, nil)
	v.pings["fast"] = proxyPing{took: 87 * time.Millisecond}
	v.pings["slow"] = proxyPing{took: 2 * time.Second}
	v.pings["busy"] = proxyPing{busy: true}
	v.pings["late"] = proxyPing{err: context.DeadlineExceeded}
	v.pings["auth"] = proxyPing{err: errors.New("socks: username/password authentication failed")}
	v.pings["gone"] = proxyPing{err: errors.New("dial tcp: connection refused")}
	v.pings["odd"] = proxyPing{err: errors.New("something")}
	for id, want := range map[string]pingShown{
		"fast": {"87 ms", 1}, "slow": {"2000 ms", 2}, "busy": {l.T("proxy.checking"), 0},
		"late": {l.T("proxy.fail_timeout"), 3}, "auth": {l.T("proxy.fail_auth"), 3},
		"gone": {l.T("proxy.fail_refused"), 3}, "odd": {l.T("proxy.fail_other"), 3}, "unknown": {"", 0},
	} {
		if got := v.pingText(l, id); got != want {
			t.Errorf("%s: %+v, want %+v", id, got, want)
		}
	}
}

func TestCornerCycles(t *testing.T) {
	if nextTheme(themeAuto) != themeLight || nextTheme(themeLight) != themeDark || nextTheme(themeDark) != themeAuto {
		t.Error("the theme does not go round auto, light, dark")
	}
	codes := []string{"ru", "en"}
	if nextLanguage("ru", codes) != "en" || nextLanguage("en", codes) != "ru" {
		t.Error("the language does not go round")
	}
	if got := nextLanguage("xx", codes); got != "ru" {
		t.Errorf("an unknown language goes to %q", got)
	}
	if got := nextLanguage("ru", nil); got != "ru" {
		t.Errorf("no languages: %q", got)
	}
}

// cornerPage is the sign-in at its phone step, with the controls of the
// corner over preferences kept in the variables it returns.
func cornerPage(t *testing.T, store *proxy.Store, ping pingFunc) (*loginPage, *themeMode, *string) {
	t.Helper()
	signIn := login.New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go signIn.Phone(ctx, nil)
	for signIn.State().Step != login.StepPhone {
		time.Sleep(time.Millisecond)
	}
	page := newLoginPage(signIn, nil, nil)
	mode, language := themeAuto, "ru"
	page.corner = &loginCorner{
		mode:        func() themeMode { return mode },
		setMode:     func(m themeMode) { mode = m },
		language:    func() string { return language },
		setLanguage: func(s string) { language = s },
		proxy:       newProxyDialog(newProxyView(store, ping, nil)),
	}
	return page, &mode, &language
}

// TestLoginCardKeepsOutOfTheCorner checks that the card of the sign-in is
// never as high as to reach the buttons in the corner, however low the window.
func TestLoginCardKeepsOutOfTheCorner(t *testing.T) {
	l := localization.For("ru")
	for _, height := range []int{250, 400, 700} {
		page, _, _ := cornerPage(t, proxy.NewStore("", nil), nil)
		ops := new(op.Ops)
		for range 3 {
			ops.Reset()
			gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(image.Pt(900, height)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
			wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
			page.Update(gtx, l)
			page.Layout(gtx, l, false)
		}
		if room := height - 2*int(cornerMargin); page.height.value > max(room, 0) && room > 0 {
			t.Errorf("a window %d high: the card is %d, more than the %d between the corners", height, page.height.value, room)
		}
	}
}

// TestRenderProxy draws the sign-in with the buttons in its corner, with the
// dialog of proxies open, and the same list in the card of the settings, in
// ACCOUNTS_PNG_DIR.
func TestRenderProxy(t *testing.T) {
	dir := os.Getenv("ACCOUNTS_PNG_DIR")
	if dir == "" {
		t.Skip("set ACCOUNTS_PNG_DIR to a directory")
	}
	newStore := func() *proxy.Store {
		store := proxy.NewStore("", nil)
		for _, p := range []proxy.Proxy{
			{Kind: proxy.SOCKS5, Host: "127.0.0.1", Port: 2080},
			{Kind: proxy.SOCKS5, Host: "proxy.example.org", Port: 1080, User: "bob", Password: "x"},
			{Kind: proxy.MTProto, Host: "mt.example.org", Port: 443, Secret: "dddf83f4ddaee1f1171b2e7a6e7403c60d"},
			{Kind: proxy.SOCKS5, Host: "10.0.0.7", Port: 9050},
		} {
			store.Add(p)
		}
		store.Select(proxy.Telegram().ID())
		return store
	}
	log := &pingLog{fail: map[string]error{
		"mtproto|mt.example.org|443|":       context.DeadlineExceeded,
		"socks5|10.0.0.7|9050|":             errors.New("dial tcp: connection refused"),
		"socks5|proxy.example.org|1080|bob": errors.New("socks: username/password authentication failed"),
	}}
	for _, lang := range []string{"ru", "en"} {
		l := localization.For(lang)
		for name, scene := range map[string]struct {
			dialog bool
			mode   themeMode
			typed  string
		}{
			"proxy-corner": {}, "proxy-dialog": {dialog: true}, "proxy-dialog-typed": {dialog: true, typed: "socks5://bob:hunter2@192.168.1.5:1080"},
		} {
			page, _, _ := cornerPage(t, newStore(), log.ping)
			frame := 0
			renderPNG(t, filepath.Join(dir, name+"-"+lang+".png"), image.Pt(900, 700), 14, func(gtx layout.Context) {
				frame++
				if frame == 2 && scene.dialog {
					page.corner.proxy.Open(gtx)
				}
				if frame == 3 && scene.typed != "" {
					page.corner.proxy.view.link.editor.SetText(scene.typed)
				}
				page.Update(gtx, l)
				page.Layout(gtx, l, false)
			})
		}
	}

	l := localization.For("ru")
	view := newProxyView(newStore(), log.ping, nil)
	view.link.editor.SetText("socks5://127.0.0.1:")
	renderPNG(t, filepath.Join(dir, "settings-proxy.png"), image.Pt(700, 700), 10, func(gtx layout.Context) {
		view.Update(gtx)
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(560))
		layout.Inset{Top: 20, Left: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return card(gtx, func(gtx layout.Context) layout.Dimensions { return view.Layout(gtx, l) }, defaultCardPadding)
		})
	})
}
