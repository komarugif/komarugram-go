// SPDX-License-Identifier: Unlicense OR MIT

package miniapp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"komarugram/pkg/miniapp"
)

// TestBridge runs the bundled Mini App against Telegram's real SDK and checks
// that both directions of the bridge work.
func TestBridge(t *testing.T) {
	if !miniapp.Available() {
		t.Skip("no Chromium-based browser or Firefox found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	demo, err := miniapp.ServeDemo()
	if err != nil {
		t.Fatal(err)
	}
	defer demo.Close()

	bridge, err := miniapp.Open(ctx, demo.URL, demo.Params, miniapp.Profile{})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	// Page to client: the SDK announces itself as soon as it initialises.
	events := waitForEvent(t, bridge, "web_app_ready")
	var types []string
	for _, event := range events {
		types = append(types, event.Type)
	}
	t.Logf("the app sent %d events: %s", len(events), strings.Join(unique(types), ", "))

	for _, expected := range []string{"web_app_ready", "web_app_expand", "web_app_request_theme", "web_app_setup_back_button"} {
		if !contains(types, expected) {
			t.Errorf("expected the app to send %s", expected)
		}
	}

	// Client to page: switching the theme has to reach the SDK, which hands it
	// to the app as a themeChanged event.
	if err := bridge.Send(ctx, "theme_changed", miniapp.DarkTheme); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Send(ctx, "viewport_changed", `{height:720,is_expanded:true,is_state_stable:true}`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return strings.Contains(pageLog(ctx, t, bridge), "themeChanged")
	}, "the app to notice the theme change")

	log := pageLog(ctx, t, bridge)
	t.Logf("the app's own log:\n  %s", strings.ReplaceAll(strings.TrimSpace(log), "\n", "\n  "))
	if !strings.Contains(log, "viewportChanged") {
		t.Error("expected the app to notice the viewport change")
	}
}

// TestStorageModes checks the difference the profile makes: a Mini App opened
// on a per-app profile finds what it stored last time, and one opened on a
// throwaway profile does not.
func TestStorageModes(t *testing.T) {
	if !miniapp.Available() {
		t.Skip("no Chromium-based browser or Firefox found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// One server for the whole test: web storage is keyed by origin, so both
	// launches have to be served from the same one.
	demo, err := miniapp.ServeDemo()
	if err != nil {
		t.Fatal(err)
	}
	defer demo.Close()

	root := t.TempDir()
	kept := miniapp.Profile{Storage: miniapp.PerApp, Root: root, App: "kitchen_bot"}

	// The demo counts its launches in localStorage.
	if visits := launchAndCount(ctx, t, demo, kept); visits != "1" {
		t.Fatalf("a fresh per-app profile reported %q launches, want 1", visits)
	}
	if visits := launchAndCount(ctx, t, demo, kept); visits != "2" {
		t.Errorf("the per-app profile lost what the app stored: reported %q launches, want 2", visits)
	}

	// A different app under the same root must not see it.
	other := miniapp.Profile{Storage: miniapp.PerApp, Root: root, App: "other_bot"}
	if visits := launchAndCount(ctx, t, demo, other); visits != "1" {
		t.Errorf("another app read the first app's storage: reported %q launches, want 1", visits)
	}

	// The throwaway profile keeps nothing, however often it is opened.
	for i := 0; i < 2; i++ {
		if visits := launchAndCount(ctx, t, demo, miniapp.Profile{}); visits != "1" {
			t.Errorf("a throwaway profile reported %q launches, want 1", visits)
		}
	}
}

// TestProfileInUse checks that opening a Mini App twice on one persistent
// profile is refused rather than silently attaching to the first window.
func TestProfileInUse(t *testing.T) {
	if !miniapp.Available() {
		t.Skip("no Chromium-based browser or Firefox found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	demo, err := miniapp.ServeDemo()
	if err != nil {
		t.Fatal(err)
	}
	defer demo.Close()

	profile := miniapp.Profile{Storage: miniapp.PerApp, Root: t.TempDir(), App: "kitchen_bot"}
	first, err := miniapp.Open(ctx, demo.URL, demo.Params, profile)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	second, err := miniapp.Open(ctx, demo.URL, demo.Params, profile)
	if err == nil {
		second.Close()
		t.Fatal("opening the same profile twice was allowed")
	}
	t.Logf("the second launch was refused: %v", err)

	// The first window is still the one the bridge talks to.
	if !first.Running() {
		t.Error("the first Mini App stopped running")
	}
	waitFor(t, func() bool {
		return storedVisits(ctx, first) != ""
	}, "the first app to still answer")
}

// launchAndCount opens the demo on a profile, reads the launch counter the app
// keeps in localStorage and closes the window again.
func launchAndCount(ctx context.Context, t *testing.T, demo *miniapp.Demo, profile miniapp.Profile) string {
	t.Helper()
	bridge, err := miniapp.Open(ctx, demo.URL, demo.Params, profile)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	var visits string
	waitFor(t, func() bool {
		visits = storedVisits(ctx, bridge)
		return visits != ""
	}, "the app to report its launch count")
	return visits
}

func storedVisits(ctx context.Context, bridge *miniapp.Bridge) string {
	// The counter the app publishes after storing it, not the raw value, which
	// would race with the app's own update.
	text, err := bridge.Eval(ctx, "String(document.body.dataset.visits || '')")
	if err != nil {
		return ""
	}
	return text
}

func pageLog(ctx context.Context, t *testing.T, bridge *miniapp.Bridge) string {
	t.Helper()
	text, err := bridge.Eval(ctx, "document.getElementById('log').textContent")
	if err != nil {
		return ""
	}
	return text
}

func waitForEvent(t *testing.T, bridge *miniapp.Bridge, name string) []miniapp.Event {
	t.Helper()
	var events []miniapp.Event
	waitFor(t, func() bool {
		events = bridge.Events()
		for _, event := range events {
			if event.Type == name {
				return true
			}
		}
		return false
	}, "event "+name)
	return events
}

func waitFor(t *testing.T, ok func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func unique(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}
