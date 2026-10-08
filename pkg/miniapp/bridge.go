// SPDX-License-Identifier: Unlicense OR MIT

// Package miniapp runs a Telegram Mini App in the user's own browser and
// speaks to it over the browser's automation protocol: the Chrome DevTools
// Protocol of a Chromium-based browser, or WebDriver BiDi of Firefox.
//
// Official clients embed a webview and inject an object into it. There is no
// webview here and no intention of shipping one, but the interface a Mini App
// expects is small enough to provide from the outside: the page reaches the
// client through TelegramWebviewProxy.postEvent, the client reaches the page
// through Telegram.WebView.receiveEvent, and the launch parameters arrive in
// the URL fragment. Both protocols can do all three — a script that runs
// before the page's own with a way to call the client for the first, an
// evaluation for the second, a navigation for the third.
//
// The browser runs as a separate process with its own profile, so a Mini App
// never sees the user's cookies and cannot touch this process's memory.
package miniapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// bindingName is the CDP binding the injected shim calls. It is deliberately
// obscure: the page should reach the client through the documented proxy, not
// by guessing this name.
const bindingName = "__tgWebviewProxyPostEvent"

// proxy is the object the Mini App SDK looks for, sending each event through
// SEND as JSON.
const proxy = `
window.TelegramWebviewProxy = {
  postEvent: function (eventType, eventData) {
    SEND(JSON.stringify({eventType: eventType, eventData: eventData}));
  }
};
`

var (
	// shim runs before any script of the page, which is when the Mini App
	// SDK looks for its transport. It sends through the CDP binding.
	shim = strings.ReplaceAll(proxy, "SEND", bindingName)
	// bidiShim is the shim as a BiDi preload script: a function given the
	// channel to send through.
	bidiShim = "(send) => {" + strings.ReplaceAll(proxy, "SEND", "send") + "}"
)

// Event is one message from the Mini App.
type Event struct {
	Type string
	Data string
	At   time.Time
}

// Params are the launch parameters a client puts in the URL fragment.
type Params struct {
	InitData    string // as returned by messages.requestWebView
	Version     string
	Platform    string
	ThemeParams string // JSON
	// Extra is more of the fragment, as a client got it from Telegram and
	// escaped as it was: launch fields this package does not know, such as
	// tgWebAppStartParam.
	Extra string
}

// Storage decides what a Mini App leaves behind and who may read it.
type Storage int

const (
	// Ephemeral gives every launch a profile of its own, removed on Close.
	// Nothing survives the window being closed.
	Ephemeral Storage = iota
	// PerApp keeps one profile per Mini App: what an app stores it finds again
	// next time, and no other app can reach it.
	PerApp
	// Shared keeps one profile for every Mini App of an account, which is what
	// an official client does.
	Shared
)

// Profile says where a Mini App's browsing data lives.
//
// A Mini App is an ordinary web application: it writes cookies and localStorage
// and expects them back. Official clients let it. Telegram Desktop hands every
// bot webview the same storage id (resolveStorageIdBots), keeps it in the
// account directory as wvbots and clears it only on logout; Telegram for
// Android runs its webview with DOM storage, databases and third-party cookies
// enabled and flushes the cookie jar to disk. So Shared is what an official
// client does, PerApp is stricter than any of them — one bot cannot read what
// another left — and Ephemeral, the zero value, keeps nothing at all.
type Profile struct {
	Storage Storage

	// Root is where persistent profiles are kept. Empty means a directory under
	// the user's cache directory.
	Root string

	// App identifies the Mini App — in a real client, the bot it belongs to.
	// PerApp needs it; the other modes ignore it.
	App string

	// Account identifies whose apps share a profile under Shared. Empty names a
	// single unnamed account, which is enough while there is only one.
	Account string
}

// root is where persistent profiles are kept: Root, or a directory under
// the user's cache directory.
func (p Profile) root() (string, error) {
	if p.Root != "" {
		return p.Root, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "gio-kitchen", "miniapp"), nil
}

// resolve returns the directory to launch the browser on and whether that
// directory is this launch's alone. A throwaway profile is made in tmp, the
// system's temporary directory when it is "".
func (p Profile) resolve(tmp string) (dir string, ephemeral bool, err error) {
	if p.Storage == Ephemeral {
		dir, err = os.MkdirTemp(tmp, "kitchen-miniapp-")
		return dir, true, err
	}
	root, err := p.root()
	if err != nil {
		return "", false, err
	}
	switch p.Storage {
	case PerApp:
		if p.App == "" {
			return "", false, fmt.Errorf("a per-app profile needs an app to name it after")
		}
		dir = filepath.Join(root, "apps", profileKey(p.App))
	case Shared:
		dir = filepath.Join(root, "shared", profileKey(p.Account))
	default:
		return "", false, fmt.Errorf("unknown storage mode %d", p.Storage)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, err
	}
	return dir, false, nil
}

// profileKey turns an identifier into one directory name. Bot and account ids
// are plain numbers, but nothing here guarantees that, so anything else is
// hashed rather than flattened into a name that another key could also produce
// — or into one that climbs out of the root.
func profileKey(id string) string {
	if id == "" {
		return "default"
	}
	safe := id != "." && id != ".."
	for _, r := range id {
		if !(r == '-' || r == '_' || r == '.' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			safe = false
			break
		}
	}
	if safe {
		return id
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:8])
}

// Bridge is one running Mini App: a browser process, a connection to it and
// the two halves of the Telegram transport.
type Bridge struct {
	browser   *exec.Cmd
	profile   string
	ephemeral bool
	exited    chan struct{}
	proto     protocol
	cancel    context.CancelFunc

	mu     sync.Mutex
	events []Event
	err    error
	closed bool
}

// protocol is how the bridge drives the browser: the DevTools protocol of a
// Chromium-based one (cdp.go) or WebDriver BiDi of Firefox (bidi.go).
type protocol interface {
	// eval runs an expression in the page. With value set it waits for a
	// promise and returns the value, when it is a string.
	eval(ctx context.Context, expression string, value bool) (string, error)
	setWindowBounds(ctx context.Context, left, top, width, height int) error
	// shutdown asks the browser to close; Close waits for it to exit.
	shutdown(ctx context.Context) error
	close()
}

// Open starts the browser on url and installs the bridge. profile decides what
// the Mini App is allowed to keep between launches; its zero value keeps
// nothing.
func Open(ctx context.Context, url string, params Params, profile Profile) (*Bridge, error) {
	// The fragment carries the launch parameters, exactly as a webview would
	// receive them.
	return launch(ctx, url+"#"+params.fragment(), profile, Page{Width: 420, Height: 720}, true)
}

// Page is how OpenPage shows a page of the client's own.
type Page struct {
	// Width and Height are the size of the window, in the browser's pixels.
	Width, Height int
	// Args are more switches for a Chromium-based browser; Firefox takes
	// none.
	Args []string
}

// OpenPage starts the browser on url in a window of its own, on a throwaway
// profile, without the Telegram transport: for a page the client itself
// shows in the browser, such as a video player. Eval reaches the page.
func OpenPage(ctx context.Context, url string, page Page) (*Bridge, error) {
	return launch(ctx, url, Profile{}, page, false)
}

// launch starts the browser on url with profile and attaches to the page,
// installing the Telegram transport when telegram is set.
func launch(ctx context.Context, url string, profile Profile, page Page, telegram bool) (*Bridge, error) {
	chosen := findBrowser()
	if !chosen.found {
		if choice := os.Getenv(BrowserEnv); choice != "" {
			return nil, fmt.Errorf("%s names %q, which is neither a program nor an installed flatpak",
				BrowserEnv, choice)
		}
		return nil, fmt.Errorf("no Chromium-based browser or Firefox found")
	}
	// A snap sees a /tmp of its own and no hidden directory of the home, the
	// cache and the configuration among them: the profile goes where the
	// snap keeps its own files, ~/snap/<name>/common, or the browser would
	// open one of its own nobody here can read.
	tmp := ""
	if name := snapName(chosen.ref); !chosen.flatpak && name != "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		snapRoot := filepath.Join(home, "snap", name, "common", "komarugram-go", "miniapp")
		tmp = filepath.Join(snapRoot, "tmp")
		// Each root the caller asks for gets one of its own there.
		root, err := profile.root()
		if err != nil {
			return nil, err
		}
		profile.Root = filepath.Join(snapRoot, profileKey(root))
		if err := os.MkdirAll(tmp, 0o700); err != nil {
			return nil, err
		}
	}
	dir, ephemeral, err := profile.resolve(tmp)
	if err != nil {
		return nil, err
	}
	discard := func() {
		if ephemeral {
			os.RemoveAll(dir)
		}
	}

	firefox := chosen.engine == engineFirefox
	var args []string
	if firefox {
		err = seedFirefoxProfile(dir, page, !telegram)
		if err == nil {
			err = checkFirefoxNotRunning(dir)
		}
		args = firefoxArgs(dir)
	} else {
		err = seedProfile(dir)
		if err == nil {
			err = checkNotRunning(dir)
		}
		args = chromiumArgs(url, dir, page, telegram)
	}
	if err != nil {
		discard()
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	bridge := &Bridge{
		profile:   dir,
		ephemeral: ephemeral,
		exited:    make(chan struct{}),
		cancel:    cancel,
	}
	prog, pre := chosen.command(dir)
	bridge.browser = exec.Command(prog, append(pre, args...)...)
	if err := bridge.browser.Start(); err != nil {
		cancel()
		discard()
		return nil, fmt.Errorf("start browser: %w", err)
	}
	// One waiter owns the process: Close and the launch both need to know when
	// it is gone, and only one of them may call Wait.
	go func() {
		_ = bridge.browser.Wait()
		close(bridge.exited)
	}()

	if firefox {
		bridge.proto, err = connectBiDi(ctx, bridge, url, telegram)
	} else {
		bridge.proto, err = connectCDP(ctx, bridge, url, telegram)
	}
	if err != nil {
		bridge.Close()
		return nil, err
	}
	return bridge, nil
}

func (p Params) fragment() string {
	fields := []string{}
	add := func(key, value string) {
		if value != "" {
			fields = append(fields, key+"="+value)
		}
	}
	// InitData is itself a query string, so the caller passes it already
	// escaped; the rest are plain tokens.
	add("tgWebAppData", p.InitData)
	add("tgWebAppVersion", p.Version)
	add("tgWebAppPlatform", p.Platform)
	add("tgWebAppThemeParams", p.ThemeParams)
	if p.Extra != "" {
		fields = append(fields, p.Extra)
	}
	return strings.Join(fields, "&")
}

// Send delivers an event to the Mini App. data must be a JavaScript
// expression, usually a JSON object literal.
func (b *Bridge) Send(ctx context.Context, eventType, data string) error {
	if data == "" {
		data = "null"
	}
	if err := b.usable(); err != nil {
		return err
	}
	_, err := b.proto.eval(ctx, fmt.Sprintf("window.Telegram.WebView.receiveEvent(%q, %s)", eventType, data), false)
	return err
}

// Eval runs an expression in the Mini App and returns its value as a string.
// It is what a client uses to inspect or adjust the page directly — drawing
// the header and main button inside the page, for instance, since they cannot
// be drawn around someone else's window. A promise is waited for, and its
// value returned.
func (b *Bridge) Eval(ctx context.Context, expression string) (string, error) {
	if err := b.usable(); err != nil {
		return "", err
	}
	return b.proto.eval(ctx, expression, true)
}

// SetWindowBounds moves and resizes the window the page is in, in the
// screen's device-independent pixels, the ones window.screen measures. A
// window manager may keep a window from placing itself, as Wayland does; the
// size still applies.
func (b *Bridge) SetWindowBounds(ctx context.Context, left, top, width, height int) error {
	if err := b.usable(); err != nil {
		return err
	}
	return b.proto.setWindowBounds(ctx, left, top, width, height)
}

func (b *Bridge) usable() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return fmt.Errorf("bridge is closed")
	}
	return nil
}

// Events returns everything the Mini App has sent so far.
func (b *Bridge) Events() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Event(nil), b.events...)
}

// Err reports why the bridge stopped working, if it did.
func (b *Bridge) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// Running reports whether the browser window is still open.
func (b *Bridge) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return false
	}
	select {
	case <-b.exited:
		return false
	default:
		return true
	}
}

// Close stops the browser. A throwaway profile goes with it; a persistent one
// stays, holding whatever the Mini App stored.
func (b *Bridge) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()

	b.stopBrowser()
	b.cancel()
	if b.proto != nil {
		b.proto.close()
	}
	if b.ephemeral {
		_ = os.RemoveAll(b.profile)
	}
}

// stopBrowser ends the browser process, giving it every chance to put its
// storage away first.
//
// A browser killed outright never flushes what the Mini App stored, and a
// signal is no better while the browser is still starting up: it arrives before
// the handlers that would make it graceful, and the process dies where it
// stands. The protocol's own shutdown is the browser's own path, and is
// answered as soon as the endpoint is up, so it is what gets asked first.
func (b *Bridge) stopBrowser() {
	if b.browser.Process == nil {
		return
	}
	if b.proto != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if b.proto.shutdown(ctx) == nil {
			select {
			case <-b.exited:
			case <-ctx.Done():
			}
		}
		cancel()
	}
	select {
	case <-b.exited:
		return
	default:
	}
	if err := b.browser.Process.Signal(os.Interrupt); err != nil {
		_ = b.browser.Process.Kill()
	}
	select {
	case <-b.exited:
	case <-time.After(5 * time.Second):
		_ = b.browser.Process.Kill()
		<-b.exited
	}
}

// watch reads the connection until it ends, which is why the bridge
// stopped working unless it was closed.
func (b *Bridge) watch(ctx context.Context, r *rpc) {
	go func() {
		err := r.read(ctx)
		b.mu.Lock()
		if !b.closed && b.err == nil {
			b.err = err
		}
		b.mu.Unlock()
	}()
}

// message takes what the shim sent: an event as JSON.
func (b *Bridge) message(payload string) {
	var message struct {
		EventType string `json:"eventType"`
		EventData string `json:"eventData"`
	}
	if json.Unmarshal([]byte(payload), &message) != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, Event{Type: message.EventType, Data: message.EventData, At: time.Now()})
}

// waitForPort waits for the browser to publish its debugging port in the
// file at path, read by parse, or for it to give up. A browser that exits
// this early has usually handed its window to another process holding the
// same profile.
func waitForPort(ctx context.Context, b *Bridge, path string, parse func([]byte) (string, bool)) (string, error) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if port, ok := parse(data); ok {
				return port, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-b.exited:
			return "", fmt.Errorf("the browser exited without opening a debugging port; " +
				"another window may already hold this profile")
		case <-time.After(50 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("the browser never opened a debugging port")
}
