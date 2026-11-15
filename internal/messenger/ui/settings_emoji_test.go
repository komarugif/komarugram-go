// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/math/fixed"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/emojipacks"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
)

// emojiPackCatalog makes a catalog of a sprite pack of the grinning face
// and a font pack.
func emojiPackCatalog(t *testing.T) string {
	t.Helper()
	catalog := t.TempDir()
	picture := filepath.Join(t.TempDir(), "emoji_1.png")
	f, err := os.Create(picture)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewNRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := emojipacks.BuildSprites(catalog, emojipacks.Pack{ID: "sprites", Name: "Sprites", License: "CC0"}, emojipacks.Sprites{Cell: 8, Columns: 1, Rows: 1}, []string{picture}, [][]string{{"\U0001F600"}}); err != nil {
		t.Fatal(err)
	}
	font := filepath.Join(t.TempDir(), "font.ttf")
	if err := os.WriteFile(font, goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := emojipacks.BuildFont(catalog, emojipacks.Pack{ID: "font", Name: "Font"}, font); err != nil {
		t.Fatal(err)
	}
	return catalog
}

// emojiSettingsHarness is the emoji settings over a catalog and a store of
// the test's, drawn by hand.
type emojiSettingsHarness struct {
	t     *testing.T
	s     *emojiSettings
	files preferences.Fonts
	// applied counts the times the pack in use was applied anew.
	applied int
	woken   chan struct{}
}

func newEmojiSettingsHarness(t *testing.T, catalog string) *emojiSettingsHarness {
	skipFrames(t)
	h := &emojiSettingsHarness{t: t, woken: make(chan struct{}, 64)}
	h.s = newEmojiSettings(func() {
		select {
		case h.woken <- struct{}{}:
		default:
		}
	})
	h.s.files = func() preferences.Fonts { return h.files }
	h.s.setFiles = func(f preferences.Fonts) { h.files = f }
	h.s.store = emojipacks.Open(filepath.Join(t.TempDir(), "emoji"))
	h.s.source = emojipacks.NewSource(catalog)
	h.s.applied = func() { h.applied++ }
	return h
}

// frame updates and draws the settings once.
func (h *emojiSettingsHarness) frame() {
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(600, 900)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
	h.s.Update(gtx)
	h.s.Layout(gtx, localization.For("ru"))
}

// until draws frames until done reports true.
func (h *emojiSettingsHarness) until(what string, done func() bool) {
	h.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		h.frame()
		if done() {
			return
		}
		select {
		case <-h.woken:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			h.t.Fatalf("waited for %s", what)
		}
	}
}

func (h *emojiSettingsHarness) installed(id string) bool {
	_, ok := h.s.store.Pack(id)
	return ok
}

func TestEmojiPackIsDownloadedChosenAndDeleted(t *testing.T) {
	h := newEmojiSettingsHarness(t, emojiPackCatalog(t))
	if !h.s.available() {
		t.Fatal("the settings are hidden with a catalog")
	}
	h.until("the catalog", func() bool { return len(h.s.catalog) == 2 })
	if h.installed("sprites") {
		t.Fatal("a pack is installed before it was asked for")
	}
	// A pack that is not installed cannot be chosen.
	h.s.row("sprites").use.click.Click()
	h.frame()
	if h.files.EmojiPack != "" {
		t.Errorf("a pack that is not installed was chosen: %q", h.files.EmojiPack)
	}

	h.s.row("sprites").download.click.Click()
	h.until("the download", func() bool { return h.installed("sprites") && h.s.row("sprites").stop == nil })
	if err := h.s.row("sprites").err; err != nil {
		t.Fatalf("the download: %v", err)
	}
	if h.files.EmojiPack != "" {
		t.Error("a pack was chosen by downloading it")
	}

	h.s.row("sprites").use.click.Click()
	h.frame()
	if h.files.EmojiPack != "sprites" {
		t.Fatalf("the pack chosen: %q", h.files.EmojiPack)
	}
	// "No pack" lets it go, and it stays installed.
	h.s.none.click.Click()
	h.frame()
	if h.files.EmojiPack != "" || !h.installed("sprites") {
		t.Errorf("after choosing no pack: chosen %q, installed %v", h.files.EmojiPack, h.installed("sprites"))
	}

	// Deleting the pack in use lets it go first.
	h.files.EmojiPack = "sprites"
	h.s.row("sprites").remove.click.Click()
	h.frame()
	if h.files.EmojiPack != "" || h.installed("sprites") {
		t.Errorf("after deleting the pack in use: chosen %q, installed %v", h.files.EmojiPack, h.installed("sprites"))
	}
	// And it downloads again.
	h.s.row("sprites").download.click.Click()
	h.until("the second download", func() bool { return h.installed("sprites") && h.s.row("sprites").stop == nil })
}

func TestEmojiPackUpdateIsAppliedToThePackInUse(t *testing.T) {
	catalog := emojiPackCatalog(t)
	h := newEmojiSettingsHarness(t, catalog)
	h.until("the catalog", func() bool { return len(h.s.catalog) == 2 })
	h.s.row("font").download.click.Click()
	h.until("the download", func() bool { return h.installed("font") && h.s.row("font").stop == nil })
	h.files.EmojiPack = "font"
	before, _ := h.s.store.Pack("font")

	// The catalog gets another font under the pack's name.
	other := filepath.Join(t.TempDir(), "newer.ttf")
	os.WriteFile(other, append([]byte(nil), goregular.TTF[:len(goregular.TTF)-1]...), 0o600)
	if _, err := emojipacks.BuildFont(catalog, emojipacks.Pack{ID: "font", Name: "Font"}, other); err != nil {
		t.Fatal(err)
	}
	h.s.asked = false
	h.until("the catalog read again", func() bool {
		for _, p := range h.s.catalog {
			if p.ID == "font" && p.Revision() != before.Revision() {
				return true
			}
		}
		return false
	})
	applied := h.applied
	h.s.row("font").download.click.Click()
	h.until("the update", func() bool {
		now, _ := h.s.store.Pack("font")
		return now.Revision() != before.Revision() && h.s.row("font").stop == nil
	})
	if h.applied != applied+1 {
		t.Errorf("the pack in use was applied %d times after its update, want once", h.applied-applied)
	}
	if h.files.EmojiPack != "font" {
		t.Errorf("the update changed the choice to %q", h.files.EmojiPack)
	}
}

func TestEmojiPackDownloadFailureAndCatalogFailure(t *testing.T) {
	catalog := emojiPackCatalog(t)
	h := newEmojiSettingsHarness(t, catalog)
	h.until("the catalog", func() bool { return len(h.s.catalog) == 2 })
	// The file is not what the catalog says.
	os.WriteFile(filepath.Join(catalog, "sprites", "order.txt"), []byte("x"), 0o600)
	h.s.row("sprites").download.click.Click()
	h.until("the download to fail", func() bool { return h.s.row("sprites").err != nil })
	if h.installed("sprites") {
		t.Error("a pack that failed to download is installed")
	}
	if !strings.Contains(h.s.row("sprites").err.Error(), "order.txt") {
		t.Errorf("the error does not name the file: %v", h.s.row("sprites").err)
	}

	// A catalog that is not there is said, and the settings stay.
	gone := newEmojiSettingsHarness(t, filepath.Join(t.TempDir(), "nothing"))
	gone.until("the catalog to fail", func() bool { return gone.s.catalogErr != nil })
	if !gone.s.available() {
		t.Error("the settings are hidden when the catalog fails")
	}

	// Without a catalog and without packs there is nothing to show; a pack
	// installed before is still there to choose and delete.
	none := newEmojiSettingsHarness(t, catalog)
	none.s.source = nil
	if none.s.available() {
		t.Error("the settings are shown without a catalog and without packs")
	}
	packs, err := emojipacks.ReadIndex(context.Background(), emojipacks.NewSource(catalog))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range packs {
		if p.ID == "font" {
			if err := none.s.store.Install(context.Background(), emojipacks.NewSource(catalog), p, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	none.s.haveAt = time.Time{}
	if !none.s.available() {
		t.Fatal("the settings are hidden with a pack installed")
	}
	none.s.row("font").use.click.Click()
	none.frame()
	if none.files.EmojiPack != "font" {
		t.Errorf("an installed pack chosen without a catalog: %q", none.files.EmojiPack)
	}
}

// TestChosenEmojiPackReachesTheWindow checks the whole way: a pack
// installed beside the settings and chosen in them is what the window's
// next theme draws emoji with, and the picker offers them.
func TestChosenEmojiPackReachesTheWindow(t *testing.T) {
	prefs, err := preferences.OpenPath(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { applyFonts(preferences.Fonts{}, nil) })
	window := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
	a := New(window, mockstore.New(time.Now(), 0), Services{Preferences: prefs, MiniApps: miniappprefs.New(miniapp.Ephemeral)})
	defer func() {
		a.Close()
		window.Motion.Close()
	}()
	if a.emojiPacks == nil || a.emojiPacks.Dir() != prefs.DataDir("emoji") || prefs.DataDir("emoji") == "" {
		t.Fatalf("the window's packs are not beside the settings: %v", a.emojiPacks)
	}
	catalog := emojiPackCatalog(t)
	packs, err := emojipacks.ReadIndex(context.Background(), emojipacks.NewSource(catalog))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range packs {
		if err := a.emojiPacks.Install(context.Background(), emojipacks.NewSource(catalog), p, nil); err != nil {
			t.Fatal(err)
		}
	}
	gtx := layout.Context{Ops: new(op.Ops)}
	// picture reports whether the window's theme draws the grinning face
	// as a picture: a glyph that advances as pictures do.
	picture := func() bool {
		shaper := a.Theme(gtx).TextShaper
		shaper.LayoutString(text.Parameters{PxPerEm: fixed.I(16), MaxWidth: 1000}, "\U0001F600")
		found := false
		for g, ok := shaper.NextGlyph(); ok; g, ok = shaper.NextGlyph() {
			found = found || (g.Advance == fixed.I(20) && !g.ID.Notdef())
		}
		return found
	}
	if picture() {
		t.Fatal("a picture before a pack is chosen")
	}
	if err := prefs.SetFonts(preferences.Fonts{EmojiPack: "sprites"}); err != nil {
		t.Fatal(err)
	}
	if !picture() {
		t.Fatal("the chosen pack does not draw in the window's theme")
	}
	var drawn emojiDrawn
	drawn.use(a.Theme(gtx))
	if !drawn.draws("\U0001F600") {
		t.Error("the picker leaves out an emoji the pack draws")
	}
	if err := prefs.SetFonts(preferences.Fonts{}); err != nil {
		t.Fatal(err)
	}
	if picture() {
		t.Error("a picture after the pack was let go")
	}
}

// TestEmojiSettingsOfferTheSetsBuiltIn checks what a client that was told
// of no catalog offers: the emoji sets of Telegram Desktop, in its order,
// and in the demo, which has no way to Telegram's cloud, only the one that
// is downloaded from elsewhere.
func TestEmojiSettingsOfferTheSetsBuiltIn(t *testing.T) {
	t.Setenv(emojipacks.CatalogEnv, "")
	shown := func(h *emojiSettingsHarness) string {
		packs, _ := h.s.packs()
		var ids []string
		for _, p := range packs {
			ids = append(ids, p.ID)
		}
		return strings.Join(ids, " ")
	}
	demo := newEmojiSettingsHarness(t, "")
	demo.s.source = emojipacks.SourceFromEnv()
	if !demo.s.available() {
		t.Fatal("the settings are hidden without a catalog named")
	}
	demo.until("the catalog", func() bool { return len(demo.s.catalog) > 0 })
	if got := shown(demo); got != "apple" {
		t.Errorf("the demo offers %q, want the set that needs no account", got)
	}

	account := newEmojiSettingsHarness(t, "")
	account.s.source = emojipacks.WithTelegram(emojipacks.SourceFromEnv(), func(context.Context, string, int, func(int64, int64)) ([]byte, error) {
		return nil, context.Canceled
	})
	account.until("the catalog", func() bool { return len(account.s.catalog) > 0 })
	if got := shown(account); got != "apple android twemoji joypixels" {
		t.Errorf("with an account the settings offer %q", got)
	}

	// The window of the demo has the catalog, and no way to Telegram.
	window := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
	a := New(window, mockstore.New(time.Now(), 0), Services{Preferences: preferences.Memory(), MiniApps: miniappprefs.New(miniapp.Ephemeral)})
	defer func() {
		a.Close()
		window.Motion.Close()
	}()
	view := a.settings.emojiView
	if !view.available() || view.source == nil || emojipacks.HasTelegram(view.source) {
		t.Errorf("the demo's window: available %v, source %v", view.available(), view.source)
	}
}
