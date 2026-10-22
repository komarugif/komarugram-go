// SPDX-License-Identifier: Unlicense OR MIT

// Package miniapps runs the Mini Apps of Telegram bots for the messenger: it
// asks Telegram for the link (messages.requestWebView), opens it in the user's
// Chromium with pkg/miniapp, and does what the app asks of its client.
//
// Official clients draw the app's header and its main and back buttons around
// a webview. The app here lives in a window of the browser, which cannot be
// drawn around, so those two buttons are drawn inside the page.
package miniapps

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/miniapp"
)

const (
	// version and platform are what Mini Apps are told, unless Telegram's
	// link says otherwise. Apps know Telegram's own platform names.
	version  = "7.0"
	platform = "tdesktop"
	// poll is how often the events of a running app are looked at, and
	// prolongEvery how often Telegram is told an inline app is still open
	// (it asks for at least once a minute).
	poll         = 100 * time.Millisecond
	prolongEvery = 45 * time.Second
	// requestTimeout is how long Telegram has to hand out the link.
	requestTimeout = time.Minute
)

// App is a running Mini App: a *miniapp.Bridge, or what a test gives instead.
type App interface {
	Events() []miniapp.Event
	Send(ctx context.Context, eventType, data string) error
	Eval(ctx context.Context, expression string) (string, error)
	Running() bool
	Err() error
	Close()
}

// Hooks are what the runner asks of the messenger's UI. They are called from
// the runner's goroutines.
type Hooks struct {
	// Link asks to open a link the app wants opened.
	Link func(url string)
	// Failed tells that an app did not open or stopped, with why.
	Failed func(err error)
}

// Launch is one app to open.
type Launch struct {
	Request model.WebViewRequest
	// Button is the text of the keyboard button it came from, which
	// sendData names.
	Button string
	// Account identifies whose apps share a profile.
	Account string
}

// Runner opens Mini Apps and keeps the ones running.
type Runner struct {
	store   model.WebViewStore
	hooks   Hooks
	storage func() miniapp.Storage
	root    string
	// open starts an app; miniapp.Open, which tests replace.
	open func(ctx context.Context, url string, params miniapp.Params, profile miniapp.Profile) (App, error)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	running map[*run]bool
}

// New returns a runner for store. storage says what apps may keep, and root
// is where kept profiles live.
func New(store model.WebViewStore, hooks Hooks, storage func() miniapp.Storage, root string) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{
		store: store, hooks: hooks, storage: storage, root: root,
		open: func(ctx context.Context, url string, params miniapp.Params, profile miniapp.Profile) (App, error) {
			return miniapp.Open(ctx, url, params, profile)
		},
		ctx: ctx, cancel: cancel, running: map[*run]bool{},
	}
}

// Available reports whether there is a browser to run apps in.
func Available() bool { return miniapp.Available() }

// Close closes every running app and stops the runner.
func (r *Runner) Close() {
	r.cancel()
	r.mu.Lock()
	var apps []*run
	for a := range r.running {
		apps = append(apps, a)
	}
	r.mu.Unlock()
	for _, a := range apps {
		a.app.Close()
	}
	r.wg.Wait()
}

// Open asks Telegram for the app and opens it, in the background.
func (r *Runner) Open(l Launch, theme string) {
	l.Request.Theme = theme
	r.wg.Add(1)
	go func() {
		defer crash.Recover("mini app", func(e *crash.Panic) {
			if r.ctx.Err() == nil && r.hooks.Failed != nil {
				r.hooks.Failed(e)
			}
		})
		defer r.wg.Done()
		if err := r.launch(l); err != nil && r.ctx.Err() == nil && r.hooks.Failed != nil {
			r.hooks.Failed(err)
		}
	}()
}

func (r *Runner) launch(l Launch) error {
	ctx, cancel := context.WithTimeout(r.ctx, requestTimeout)
	view, err := r.store.RequestWebView(ctx, l.Request)
	cancel()
	if err != nil {
		return err
	}
	base, params := LaunchParams(view.URL, l.Request.Theme)
	profile := miniapp.Profile{Storage: r.storage(), Root: r.root, Account: l.Account}
	profile.App = itoa(l.Request.Bot)
	app, err := r.open(r.ctx, base, params, profile)
	if err != nil {
		return err
	}
	run := &run{r: r, app: app, launch: l, queryID: view.QueryID, theme: l.Request.Theme}
	r.mu.Lock()
	r.running[run] = true
	r.mu.Unlock()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		func() {
			// The app is closed after a panic too.
			defer crash.Recover("mini app", nil)
			run.loop()
		}()
		r.mu.Lock()
		delete(r.running, run)
		r.mu.Unlock()
		app.Close()
	}()
	return nil
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// LaunchParams splits the link Telegram answers with into the page to open
// and the launch parameters of its fragment. What Telegram put in the
// fragment stays; what it left out (this client's version, platform and
// colors) is added, and the rest is kept as it came.
func LaunchParams(link, theme string) (string, miniapp.Params) {
	base, fragment, _ := strings.Cut(link, "#")
	p := miniapp.Params{Version: version, Platform: platform, ThemeParams: url.QueryEscape(theme)}
	var extra []string
	for _, field := range strings.Split(fragment, "&") {
		if field == "" {
			continue
		}
		key, value, _ := strings.Cut(field, "=")
		switch key {
		case "tgWebAppData":
			p.InitData = value
		case "tgWebAppVersion":
			p.Version = value
		case "tgWebAppPlatform":
			p.Platform = value
		case "tgWebAppThemeParams":
			p.ThemeParams = value
		default:
			extra = append(extra, field)
		}
	}
	p.Extra = strings.Join(extra, "&")
	return base, p
}
