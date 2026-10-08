// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestIdentify checks how a browser is told from what it printed for
// --version, where no platform.ini is found beside it.
func TestIdentify(t *testing.T) {
	for banner, want := range map[string]struct {
		engine engine
		err    error
	}{
		"Chromium 152.0.7977.82 for Linux Mint": {engineChromium, nil},
		"Mozilla Firefox 157.0.1":               {engineFirefox, nil},
		"Mozilla Firefox 140.3.0esr":            {engineFirefox, nil},
		"LibreWolf 157.0-1":                     {engineFirefox, nil},
		// Windows' version resource.
		"Firefox 157.0.1.9301":        {engineFirefox, nil},
		"Mozilla Firefox 128.14.0esr": {engineFirefox, ErrOldFirefox},
		// Waterfox's own version says nothing of its engine, which is asked
		// once it runs.
		"BrowserWorks Waterfox 6.7.5": {engineFirefox, nil},
	} {
		b := browser{ref: filepath.Join(t.TempDir(), "browser"), found: true}
		b.identify(banner)
		if b.engine != want.engine || !errors.Is(b.usable(), want.err) {
			t.Errorf("%q: engine %d, %v; want %d, %v", banner, b.engine, b.usable(), want.engine, want.err)
		}
	}
}

// TestCheckGecko checks that a Firefox too old is turned down by the version
// it gives for its session.
func TestCheckGecko(t *testing.T) {
	for reply, want := range map[string]error{
		`{"sessionId":"x","capabilities":{"browserName":"waterfox","browserVersion":"153.4.0"}}`: nil,
		`{"sessionId":"x","capabilities":{"browserName":"firefox","browserVersion":"128.14.0"}}`: ErrOldFirefox,
		`{"sessionId":"x","capabilities":{}}`:                                                    nil,
	} {
		if got := checkGecko(json.RawMessage(reply)); !errors.Is(got, want) {
			t.Errorf("%s: got %v, want %v", reply, got, want)
		}
	}
}

// TestGeckoVersion checks that the version of Waterfox's engine is read out of
// platform.ini beside the program it links to.
func TestGeckoVersion(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "opt", "waterfox", "waterfox")
	if err := os.MkdirAll(filepath.Dir(program), 0o755); err != nil {
		t.Fatal(err)
	}
	ini := "[Build]\nBuildID=20260930075311\nMilestone=153.4.0\n"
	if err := os.WriteFile(filepath.Join(dir, "opt", "waterfox", "platform.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "waterfox")
	if err := os.Symlink(program, link); err != nil {
		t.Skip(err)
	}
	b := browser{ref: link, found: true}
	b.identify("BrowserWorks Waterfox 6.7.5")
	if !slices.Equal(b.version, version{153, 4, 0}) || b.usable() != nil {
		t.Errorf("version %v, %v; want 153.4.0", b.version, b.usable())
	}
}

// TestNewestFirefox checks that Firefox is driven only where no
// Chromium-based browser is, and never one too old.
func TestNewestFirefox(t *testing.T) {
	chromium := browser{ref: "/usr/bin/chromium", found: true, version: version{140, 0, 7339, 80}}
	firefox := browser{ref: "/usr/bin/firefox", found: true, engine: engineFirefox,
		banner: "Mozilla Firefox 157.0.1", version: version{157, 0, 1}}
	esr := browser{ref: "/usr/bin/firefox-esr", found: true, engine: engineFirefox,
		banner: "Mozilla Firefox 140.3.0esr", version: version{140, 3, 0}}
	old := browser{ref: "/opt/firefox/firefox", found: true, engine: engineFirefox,
		banner: "Mozilla Firefox 128.14.0esr", version: version{128, 14, 0}}
	for _, test := range []struct {
		name  string
		found []browser
		want  string
	}{
		{"Chromium before a newer Firefox", []browser{firefox, chromium}, chromium.ref},
		{"Firefox when it is all there is", []browser{esr, firefox}, firefox.ref},
		{"never a Firefox too old", []browser{old}, ""},
	} {
		if got := newest(test.found); got.ref != test.want {
			t.Errorf("%s: got %q, want %q", test.name, got.ref, test.want)
		}
	}
}

func TestReadBiDiPort(t *testing.T) {
	if port, ok := readBiDiPort([]byte(`{"ws_host": "127.0.0.1", "ws_port": 41288}`)); !ok || port != "41288" {
		t.Errorf("got %q, %t", port, ok)
	}
	for _, data := range []string{``, `{}`, `{"ws_port": 0}`, `{"ws_host"`} {
		if _, ok := readBiDiPort([]byte(data)); ok {
			t.Errorf("%q was read as a port", data)
		}
	}
}

// TestSeedFirefoxProfile checks what a profile of Firefox starts with: the
// test preferences of remote control off, the toolbars hidden, the window's
// size, and autoplay only for a page of the client's own.
func TestSeedFirefoxProfile(t *testing.T) {
	dir := t.TempDir()
	if err := seedFirefoxProfile(dir, Page{Width: 420, Height: 720}, false); err != nil {
		t.Fatal(err)
	}
	prefs, err := os.ReadFile(filepath.Join(dir, "user.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		`user_pref("remote.prefs.recommended", false);`,
		`user_pref("toolkit.legacyUserProfileCustomizations.stylesheets", true);`,
		`user_pref("startup.homepage_welcome_url", "about:blank");`,
		`user_pref("browser.startup.page", 0);`,
		`user_pref("privacy.resistFingerprinting", false);`,
		`user_pref("browser.tabs.inTitlebar", 0);`,
		`user_pref("privacy.sanitize.sanitizeOnShutdown", false);`,
	} {
		if !strings.Contains(string(prefs), line+"\n") {
			t.Errorf("user.js lacks %s", line)
		}
	}
	if strings.Contains(string(prefs), "media.autoplay") {
		t.Error("a Mini App may play sound without a click")
	}
	if css, err := os.ReadFile(filepath.Join(dir, "chrome", "userChrome.css")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(css), "#nav-bar") || !strings.Contains(string(css), "#remote-control-box") ||
		!strings.Contains(string(css), ".global-notificationbox") {
		t.Errorf("the toolbars are not hidden: %s", css)
	}
	data, err := os.ReadFile(filepath.Join(dir, "xulstore.json"))
	if err != nil {
		t.Fatal(err)
	}
	var store map[string]map[string]map[string]string
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatal(err)
	}
	if window := store["chrome://browser/content/browser.xhtml"]["main-window"]; window["width"] != "420" || window["height"] != "720" {
		t.Errorf("the window's size: %s", data)
	}

	if err := seedFirefoxProfile(dir, Page{Width: 960, Height: 600}, true); err != nil {
		t.Fatal(err)
	}
	if prefs, err := os.ReadFile(filepath.Join(dir, "user.js")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(prefs), `user_pref("media.autoplay.default", 0);`) {
		t.Error("the client's own page does not play by itself")
	}
}
