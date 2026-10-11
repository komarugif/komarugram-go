// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Firefox is driven over WebDriver BiDi, which it serves itself: Mozilla
// removed its partial DevTools protocol in Firefox 141. The two halves of
// the transport are a preload script, which runs before the page's own and
// is handed a channel to send through, and an evaluation; the page is
// opened after the preload script is in place, so nothing reloads.
// Firefox has no --app window: a style sheet in the profile hides its
// toolbars instead.

// channelName is the BiDi channel the shim sends through. Unlike the CDP
// binding it is not a name in the page: the page holds only the function.
const channelName = "telegram-webview-proxy"

// firefoxPrefs are the preferences of a profile Firefox runs a Mini App or
// a page of the client's in, written to user.js, which Firefox reads at
// every start.
var firefoxPrefs = []struct {
	name  string
	value any
}{
	// Firefox under remote control sets preferences meant for tests, and
	// among them turns off Safe Browsing, the popup blocker and tracking
	// protection: none of that is for a stranger's page.
	{"remote.prefs.recommended", false},
	// userChrome.css, which hides the toolbars.
	{"toolkit.legacyUserProfileCustomizations.stylesheets", true},
	// Nothing of a first start: no welcome page, no question about the
	// default browser, no notices about data, the terms of use or what is
	// new.
	{"browser.shell.checkDefaultBrowser", false},
	{"browser.aboutwelcome.enabled", false},
	{"browser.startup.homepage_override.mstone", "ignore"},
	{"startup.homepage_welcome_url", "about:blank"},
	{"startup.homepage_welcome_url.additional", ""},
	{"datareporting.policy.dataSubmissionPolicyBypassNotification", true},
	{"termsofuse.bypassNotification", true},
	{"toolkit.telemetry.reportingpolicy.firstRun", false},
	// A blank start, never the pages of the last run, and no safe mode
	// offered after a crash: the page is opened over the protocol.
	{"browser.startup.page", 0},
	{"browser.sessionstore.resume_from_crash", false},
	{"browser.startup.couldRestoreSession.count", -1},
	{"toolkit.startup.max_resumed_crashes", -1},
	// No asking before the window closes.
	{"browser.tabs.warnOnClose", false},
	{"browser.warnOnQuit", false},
	// A Mini App is a client surface, not a page the user browsed to: no
	// offer to translate it, as in Chromium.
	{"browser.translations.automaticallyPopup", false},
	// LibreWolf resists fingerprinting: it asks, in a dialog that holds up
	// the page, whether pages are to be asked for in English, and makes the
	// window a size of its own choosing. A Mini App gets the user's Telegram
	// id and name in its launch parameters, so there is nothing to hide from
	// it this way.
	{"privacy.resistFingerprinting", false},
	{"privacy.spoof_english", 1},
	// LibreWolf also clears what pages stored when it quits. What a Mini App
	// keeps is the client's setting (Profile): a throwaway profile is removed
	// whole, a kept one keeps it.
	{"privacy.sanitize.sanitizeOnShutdown", false},
	// The window manager's title bar: one drawn by the browser itself, as
	// Waterfox does by default, goes with the toolbars, and the window could
	// then be neither moved nor closed.
	{"browser.tabs.inTitlebar", 0},
	// A page the Mini App opens gets a window of its own: in a tab it would
	// cover the app, with the tab bar hidden.
	{"browser.link.open_newwindow", 2},
	{"browser.link.open_newwindow.restriction", 0},
}

// firefoxPagePrefs are added for a page of the client's own: the player's
// video plays without a click in it, as the window opens because the user
// asked for the video.
var firefoxPagePrefs = []struct {
	name  string
	value any
}{
	{"media.autoplay.default", 0},
}

// userChrome hides Firefox's toolbars and sidebars, and the lines and
// margins between them and the page (Firefox draws a line under the
// toolbars, LibreWolf one over the page, Waterfox has its own sidebar and
// margins around the page), leaving the page alone in its window, and lets the window be as narrow as a Mini App's: Firefox keeps
// its own about 500 pixels wide for the toolbars.
const userChrome = `/* Written by KomaruGram at every start of a Mini App. */
#TabsToolbar, #nav-bar, #PersonalToolbar, #titlebar, #sidebar-main, #sidebar-box,
#sidebar-container, #sidebar-launcher-splitter, #sidebar-splitter {
  visibility: collapse !important;
}
:root, #main-window, #navigator-toolbox, #browser, #tabbrowser-tabbox {
  min-width: 0 !important;
}
#navigator-toolbox, .browserContainer {
  border: none !important;
}
#navigator-toolbox, #browser {
  margin: 0 !important;
  padding: 0 !important;
}
/* The robot Firefox shows while it is remote-controlled sits in the URL bar,
   which is drawn over the page even with its toolbar hidden. */
#remote-control-box {
  display: none !important;
}
/* Notices of the browser's own, such as LibreWolf's that the default search
   engine changed, go over the app; a page's own notices stay. */
.global-notificationbox {
  display: none !important;
}
`

// seedFirefoxProfile writes what Firefox is to start with into profile, at
// every start: the preferences, the style sheet, and the size of the window,
// which Firefox keeps in xulstore.json. These files are the client's; what
// the Mini App stores lives in others.
func seedFirefoxProfile(profile string, page Page, own bool) error {
	if err := os.MkdirAll(filepath.Join(profile, "chrome"), 0o700); err != nil {
		return err
	}
	var prefs strings.Builder
	prefs.WriteString("// Written by KomaruGram at every start of a Mini App.\n")
	write := func(name string, value any) {
		encoded, _ := json.Marshal(value)
		fmt.Fprintf(&prefs, "user_pref(%q, %s);\n", name, encoded)
	}
	for _, pref := range firefoxPrefs {
		write(pref.name, pref.value)
	}
	if own {
		for _, pref := range firefoxPagePrefs {
			write(pref.name, pref.value)
		}
	}
	window, _ := json.Marshal(map[string]any{
		"chrome://browser/content/browser.xhtml": map[string]any{
			"main-window": map[string]string{
				"width":    strconv.Itoa(page.Width),
				"height":   strconv.Itoa(page.Height),
				"sizemode": "normal",
			},
		},
	})
	for path, data := range map[string]string{
		filepath.Join(profile, "user.js"):                  prefs.String(),
		filepath.Join(profile, "chrome", "userChrome.css"): userChrome,
		filepath.Join(profile, "xulstore.json"):            string(window),
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			return fmt.Errorf("seed browser profile: %w", err)
		}
	}
	return nil
}

// firefoxArgs are the switches that start Firefox on profile dir, with its
// WebDriver BiDi endpoint on a port of its choosing. It opens a blank page:
// the page itself comes once the shim is in place.
func firefoxArgs(dir string) []string {
	return []string{
		"--profile", dir,
		// A process of its own, even while the user's Firefox runs.
		"--no-remote",
		"--remote-debugging-port=0",
		"about:blank",
	}
}

// bidiPortFile is where Firefox writes the address of its BiDi endpoint,
// in the profile.
const bidiPortFile = "WebDriverBiDiServer.json"

// readBiDiPort reads the port out of bidiPortFile.
func readBiDiPort(data []byte) (string, bool) {
	var server struct {
		Port int `json:"ws_port"`
	}
	if json.Unmarshal(data, &server) != nil || server.Port <= 0 {
		return "", false
	}
	return strconv.Itoa(server.Port), true
}

// checkFirefoxNotRunning reports whether a Firefox already holds this
// profile, as checkNotRunning does for Chromium: a second one on it would
// stop at Firefox's own message that the profile is in use.
func checkFirefoxNotRunning(profile string) error {
	path := filepath.Join(profile, bidiPortFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if port, ok := readBiDiPort(data); ok {
		client := http.Client{Timeout: time.Second}
		if resp, err := client.Get("http://127.0.0.1:" + port + "/"); err == nil {
			resp.Body.Close()
			return fmt.Errorf("this Mini App is already open in another window")
		}
	}
	// Nobody answered, so the file is what an earlier run left behind.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// bidi is a page of Firefox, driven over WebDriver BiDi.
type bidi struct {
	rpc     *rpc
	context string // the page's browsing context
	window  string // the window it is in
}

// connectBiDi waits for the BiDi endpoint, starts a session, installs the
// shim when telegram is set, and opens url.
func connectBiDi(ctx context.Context, b *Bridge, url string, telegram bool) (protocol, error) {
	port, err := waitForPort(ctx, b, filepath.Join(b.profile, bidiPortFile), readBiDiPort)
	if err != nil {
		return nil, err
	}
	p := &bidi{}
	p.rpc, err = dialRPC(ctx, "ws://127.0.0.1:"+port+"/session", true, func(method string, params json.RawMessage) {
		if method != "script.message" {
			return
		}
		var message struct {
			Channel string `json:"channel"`
			Data    struct {
				Value string `json:"value"`
			} `json:"data"`
		}
		if json.Unmarshal(params, &message) == nil && message.Channel == channelName {
			b.message(message.Data.Value)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("connect to Firefox: %w", err)
	}
	b.watch(ctx, p.rpc)
	if err := p.start(ctx, url, telegram); err != nil {
		p.close()
		return nil, err
	}
	return p, nil
}

func (p *bidi) start(ctx context.Context, url string, telegram bool) error {
	// A session over BiDi alone dismisses no dialog of the page's: the
	// Mini App's alert and confirm stay for the user to answer.
	session, err := p.rpc.call(ctx, "session.new", map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": map[string]any{
				"unhandledPromptBehavior": map[string]any{"default": "ignore"},
			},
		},
	})
	if err != nil {
		return err
	}
	if err := checkGecko(session); err != nil {
		return err
	}
	if _, err := p.rpc.call(ctx, "session.subscribe", map[string]any{
		"events": []string{"script.message"},
	}); err != nil {
		return err
	}
	result, err := p.rpc.call(ctx, "browsingContext.getTree", map[string]any{"maxDepth": 0})
	if err != nil {
		return err
	}
	var tree struct {
		Contexts []struct {
			Context      string `json:"context"`
			ClientWindow string `json:"clientWindow"`
		} `json:"contexts"`
	}
	if err := json.Unmarshal(result, &tree); err != nil {
		return err
	}
	if len(tree.Contexts) == 0 {
		return fmt.Errorf("Firefox opened no page")
	}
	p.context, p.window = tree.Contexts[0].Context, tree.Contexts[0].ClientWindow
	if telegram {
		if _, err := p.rpc.call(ctx, "script.addPreloadScript", map[string]any{
			"functionDeclaration": bidiShim,
			"arguments": []any{map[string]any{
				"type":  "channel",
				"value": map[string]any{"channel": channelName},
			}},
		}); err != nil {
			return err
		}
	}
	_, err = p.rpc.call(ctx, "browsingContext.navigate", map[string]any{
		"context": p.context,
		"url":     url,
		"wait":    "none",
	})
	return err
}

// checkGecko turns down a Firefox older than minFirefox by the version of
// its engine it gave for the session, "153.4.0" for Waterfox 6.7.5: the
// version a browser started through a launcher script could not be told
// before.
func checkGecko(session json.RawMessage) error {
	var reply struct {
		Capabilities struct {
			BrowserVersion string `json:"browserVersion"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(session, &reply); err != nil {
		return err
	}
	if v := parseVersion(reply.Capabilities.BrowserVersion); len(v) > 0 && v[0] < minFirefox {
		return ErrOldFirefox
	}
	return nil
}

func (p *bidi) eval(ctx context.Context, expression string, value bool) (string, error) {
	result, err := p.rpc.call(ctx, "script.evaluate", map[string]any{
		"expression":      expression,
		"target":          map[string]any{"context": p.context},
		"awaitPromise":    value,
		"resultOwnership": "none",
	})
	if err != nil || !value {
		return "", err
	}
	// An exception in the page is not an error of the bridge's, as with
	// the DevTools protocol: there is no value to return.
	var evaluated struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result, &evaluated); err != nil {
		return "", err
	}
	text, _ := evaluated.Result.Value.(string)
	return text, nil
}

func (p *bidi) windowSize(ctx context.Context) (width, height int, err error) {
	result, err := p.rpc.call(ctx, "browser.getClientWindows", nil)
	if err != nil {
		return 0, 0, err
	}
	var reply struct {
		ClientWindows []struct {
			ClientWindow  string `json:"clientWindow"`
			Width, Height int
		} `json:"clientWindows"`
	}
	if err := json.Unmarshal(result, &reply); err != nil {
		return 0, 0, err
	}
	for _, window := range reply.ClientWindows {
		if window.ClientWindow == p.window {
			return window.Width, window.Height, nil
		}
	}
	return 0, 0, fmt.Errorf("Firefox did not tell the size of the page's window")
}

// setWindowBounds needs Firefox 151, which added
// browser.setClientWindowState.
func (p *bidi) setWindowBounds(ctx context.Context, left, top, width, height int) error {
	if p.window == "" {
		return fmt.Errorf("Firefox did not tell which window the page is in")
	}
	set := func(bounds map[string]any) error {
		bounds["clientWindow"] = p.window
		bounds["state"] = "normal"
		_, err := p.rpc.call(ctx, "browser.setClientWindowState", bounds)
		return err
	}
	// A window that may not place itself should still be given its size.
	if err := set(map[string]any{"width": width, "height": height, "x": left, "y": top}); err != nil {
		return set(map[string]any{"width": width, "height": height})
	}
	return nil
}

// shutdown asks Firefox to close, which it does as its own quit: the
// profile is written out.
func (p *bidi) shutdown(ctx context.Context) error {
	return p.rpc.send(ctx, "browser.close", nil)
}

func (p *bidi) close() { p.rpc.close() }
