// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"sync"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/token"

	"gioui.org/layout"
)

// testThemeKey is a theme as testTheme keeps it: of a scheme, with the
// fonts set when it was made.
type testThemeKey struct {
	scheme token.Scheme
	fonts  uint64
}

var (
	testThemesMu sync.Mutex
	testThemes   = map[testThemeKey]*token.Theme{}
)

// testTheme is defaults.NewTheme made once for a scheme and the fonts of
// the moment, as a window keeps its theme. A theme made for each frame
// makes a text shaper each frame, and lays all text out from scratch: the
// tests took five times as long. The tests draw one at a time, and so share
// the shaper, which is not for two goroutines at once; a test that changes
// the fonts (defaults.SetFonts) gets a theme of its own.
func testTheme(gtx layout.Context, scheme *token.Scheme) *token.Theme {
	key := testThemeKey{scheme: *scheme, fonts: defaults.FontsVersion()}
	testThemesMu.Lock()
	defer testThemesMu.Unlock()
	if t := testThemes[key]; t != nil {
		return t
	}
	t := defaults.NewTheme(gtx, scheme)
	testThemes[key] = t
	return t
}

// skipFrames skips a test that draws frames when go test runs with -short,
// as it does between commits (AGENTS.md, "Tests"): the harnesses that draw
// frames call it as they are made.
func skipFrames(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("draws frames: run without -short")
	}
}

// waitFor draws frames until done, what a goroutine does having come back,
// and fails after five seconds. A test waits for what it needs, not for a
// number of frames: frames drawn fast, or a machine of one core, leave a
// goroutine no time between them.
func waitFor(t testing.TB, what string, frame func(), done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !done(); {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %s", what)
		}
		frame()
		time.Sleep(time.Millisecond)
	}
}
