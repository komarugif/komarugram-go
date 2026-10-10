// SPDX-License-Identifier: Unlicense OR MIT

package fonts

import (
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-text/typesetting/fontscan"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/text"
	"golang.org/x/image/math/fixed"

	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"komarugram/internal/messenger/emojipacks"
)

// write puts a font into a file of the test's.
func write(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// typefaces are the families a theme made now asks for, for text and for
// preformatted text.
func typefaces() (text, mono string) {
	theme := defaults.NewTheme(layout.Context{}, schemes.SchemeBaselineLight())
	return string(theme.Typescale[token.TypestyleBodyLarge].Font), string(theme.Typescale[token.TypestylePreformatted].Font)
}

// reset returns the themes to the system's fonts after the test.
func reset(t *testing.T) {
	t.Cleanup(func() { Apply(Files{}, nil) })
}

func TestApplyPutsTheFilesFirst(t *testing.T) {
	reset(t)
	text0, mono0 := typefaces()
	version := defaults.FontsVersion()
	files := Files{Text: write(t, "text.ttf", goregular.TTF), Mono: write(t, "mono.ttf", gomono.TTF)}
	if err := Apply(files, nil); err != nil {
		t.Fatal(err)
	}
	if defaults.FontsVersion() == version {
		t.Error("the version of the fonts did not change")
	}
	text, mono := typefaces()
	if want := `"Go", ` + text0; text != want {
		t.Errorf("text typeface %q, want %q", text, want)
	}
	if want := `"Go Mono", ` + mono0; mono != want {
		t.Errorf("preformatted typeface %q, want %q", mono, want)
	}
	if Revision() == 1 {
		t.Error("the revision is the system fonts'")
	}
	// The same files again change nothing: every window applies them.
	version, rev := defaults.FontsVersion(), Revision()
	if err := Apply(files, nil); err != nil {
		t.Fatal(err)
	}
	if defaults.FontsVersion() != version || Revision() != rev {
		t.Error("applying the same files made new fonts")
	}
	if err := Apply(Files{}, nil); err != nil {
		t.Fatal(err)
	}
	if text, mono := typefaces(); text != text0 || mono != mono0 || Revision() != 1 {
		t.Errorf("after a reset: %q, %q, revision %d", text, mono, Revision())
	}
}

func TestExtraFollowsText(t *testing.T) {
	reset(t)
	text0, _ := typefaces()
	if err := Apply(Files{Extra: write(t, "extra.ttf", gomono.TTF), Text: write(t, "text.ttf", goregular.TTF)}, nil); err != nil {
		t.Fatal(err)
	}
	if text, _ := typefaces(); text != `"Go", "Go Mono", `+text0 {
		t.Errorf("text typeface %q", text)
	}
}

func TestBadFilesArePassedOver(t *testing.T) {
	reset(t)
	text0, _ := typefaces()
	files := Files{
		Text:  write(t, "text.ttf", []byte("not a font")),
		Extra: filepath.Join(t.TempDir(), "gone.ttf"),
		Mono:  write(t, "mono.ttf", gomono.TTF),
		// A text font has no emoji.
		Emoji: write(t, "emoji.ttf", goregular.TTF),
	}
	err := Apply(files, nil)
	if err == nil {
		t.Fatal("no error for files that do not load")
	}
	if !errors.Is(err, ErrNoEmoji) || !errors.Is(err, os.ErrNotExist) {
		t.Errorf("errors: %v", err)
	}
	text, mono := typefaces()
	if text != text0 {
		t.Errorf("text typeface %q, want the system's", text)
	}
	if !strings.HasPrefix(mono, `"Go Mono", `) {
		t.Errorf("preformatted typeface %q, want the good file's first", mono)
	}
}

func TestCheck(t *testing.T) {
	path := write(t, "text.ttf", goregular.TTF)
	f, err := Check(Text, path)
	if err != nil || f.Family != "Go" {
		t.Errorf("a text font: %q, %v", f.Family, err)
	}
	if _, err := Check(Emoji, path); !errors.Is(err, ErrNoEmoji) {
		t.Errorf("a text font for emoji: %v", err)
	}
	large := filepath.Join(t.TempDir(), "large.ttf")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxSize + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := Check(Text, large); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a file over the limit: %v", err)
	}
}

func TestEnvironmentOverTheSettings(t *testing.T) {
	reset(t)
	text0, _ := typefaces()
	t.Setenv("KOMARUGRAM_FONT", write(t, "env.ttf", gomono.TTF))
	if !FromEnv(Text) || FromEnv(Emoji) {
		t.Error("FromEnv does not tell the role the environment names")
	}
	if err := Apply(Files{Text: write(t, "text.ttf", goregular.TTF)}, nil); err != nil {
		t.Fatal(err)
	}
	if text, _ := typefaces(); text != `"Go Mono", `+text0 {
		t.Errorf("text typeface %q, want the environment's font first", text)
	}
}

// TestEmojiFontStaysOutOfTheTypeface checks that with an emoji font of the
// program's own no emoji family is named for text: on a system without the
// text fonts the theme names, a font of that family would draw the digits
// of every text. The shaper draws emoji with the font instead.
func TestEmojiFontStaysOutOfTheTypeface(t *testing.T) {
	t.Cleanup(func() { defaults.SetFonts(defaults.Fonts{}) })
	text0, _ := typefaces()
	if !strings.Contains(text0, "Emoji") {
		t.Skip("the theme names no emoji family")
	}
	defaults.SetFonts(defaults.Fonts{Emoji: "Noto Color Emoji"})
	if text, _ := typefaces(); strings.Contains(text, "Emoji") {
		t.Errorf("text typeface with an emoji font: %q", text)
	}
}

// spriteCatalog makes a catalog with a sprite pack of one emoji, the
// grinning face, and a font pack whose font has no emoji.
func spriteCatalog(t *testing.T) (catalog string, sprites, font emojipacks.Pack) {
	t.Helper()
	catalog = t.TempDir()
	picture := filepath.Join(t.TempDir(), "emoji_1.png")
	f, err := os.Create(picture)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewNRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	sprites, err = emojipacks.BuildSprites(catalog, emojipacks.Pack{ID: "sprites", Name: "Sprites"}, emojipacks.Sprites{Cell: 8, Columns: 1, Rows: 1}, []string{picture}, [][]string{{"\U0001F600"}})
	if err != nil {
		t.Fatal(err)
	}
	font, err = emojipacks.BuildFont(catalog, emojipacks.Pack{ID: "font", Name: "Font"}, write(t, "text.ttf", goregular.TTF))
	if err != nil {
		t.Fatal(err)
	}
	return catalog, sprites, font
}

// pictures reports whether a theme made now draws the grinning face as a
// picture: one glyph that advances as pictures do, 20 pixels at 16.
func pictures() bool {
	theme := defaults.NewTheme(layout.Context{}, schemes.SchemeBaselineLight())
	theme.TextShaper.LayoutString(text.Parameters{PxPerEm: fixed.I(16), MaxWidth: 1000}, "\U0001F600")
	n, picture := 0, false
	for g, ok := theme.TextShaper.NextGlyph(); ok; g, ok = theme.TextShaper.NextGlyph() {
		if g.Advance > 0 {
			n++
			picture = g.Advance == fixed.I(20) && !g.ID.Notdef()
		}
	}
	return n == 1 && picture
}

func TestEmojiPackOfSprites(t *testing.T) {
	reset(t)
	catalog, sprites, _ := spriteCatalog(t)
	store := emojipacks.Open(filepath.Join(t.TempDir(), "emoji"))
	if pictures() {
		t.Fatal("pictures before a pack is chosen")
	}
	// A pack chosen and not installed is passed over, and said.
	if err := Apply(Files{EmojiPack: "sprites"}, store); err == nil {
		t.Error("no error for a pack that is not installed")
	}
	if pictures() || Revision() != 1 {
		t.Error("a pack that is not installed changed the fonts")
	}
	if err := store.Install(context.Background(), emojipacks.NewSource(catalog), sprites, nil); err != nil {
		t.Fatal(err)
	}
	version := defaults.FontsVersion()
	if err := Apply(Files{EmojiPack: "sprites"}, store); err != nil {
		t.Fatal(err)
	}
	if !pictures() {
		t.Error("the installed pack does not draw its emoji")
	}
	if defaults.FontsVersion() == version || Revision() == 1 {
		t.Error("the pack did not make new fonts, or messages measured with it are taken for the system fonts'")
	}
	// Applied again, as every window does, it is the same fonts.
	version = defaults.FontsVersion()
	if err := Apply(Files{EmojiPack: "sprites"}, store); err != nil || defaults.FontsVersion() != version {
		t.Errorf("the same pack applied again: %v, new fonts %v", err, defaults.FontsVersion() != version)
	}
	if err := Apply(Files{}, store); err != nil {
		t.Fatal(err)
	}
	if pictures() || Revision() != 1 {
		t.Error("the pack is still drawn with once none is chosen")
	}
}

func TestEmojiPackOfAFont(t *testing.T) {
	reset(t)
	catalog, _, font := spriteCatalog(t)
	store := emojipacks.Open(filepath.Join(t.TempDir(), "emoji"))
	if err := store.Install(context.Background(), emojipacks.NewSource(catalog), font, nil); err != nil {
		t.Fatal(err)
	}
	// The pack's font takes the place of the emoji font's file: this one
	// has no emoji, and is turned down as that file would be.
	err := Apply(Files{EmojiPack: "font", Emoji: "unused.ttf"}, store)
	if !errors.Is(err, ErrNoEmoji) || !strings.Contains(err.Error(), store.Path("font", font.Font)) {
		t.Errorf("a font pack without emoji: %v", err)
	}
}

func TestEmojiPackFromTheEnvironment(t *testing.T) {
	reset(t)
	catalog, _, _ := spriteCatalog(t)
	t.Setenv(SetEnv, filepath.Join(catalog, "sprites"))
	if err := Apply(Files{}, nil); err != nil {
		t.Fatal(err)
	}
	if !pictures() {
		t.Error("the pack the environment names does not draw its emoji")
	}
	t.Setenv(SetEnv, t.TempDir())
	if err := Apply(Files{}, nil); err == nil {
		t.Error("no error for a directory that is not a pack")
	}
	if pictures() {
		t.Error("pictures without a pack")
	}
}

// TestHasEmoji checks that the system has emoji only when a font of it
// covers them.
func TestHasEmoji(t *testing.T) {
	var latin, emoji fontscan.Footprint
	for r := 'a'; r <= 'z'; r++ {
		latin.Runes.Add(r)
	}
	emoji.Runes.Add(emojiProbe)
	if hasEmoji([]fontscan.Footprint{latin}) {
		t.Error("emoji found in a Latin font")
	}
	if !hasEmoji([]fontscan.Footprint{latin, emoji}) {
		t.Error("emoji font not found")
	}
}
