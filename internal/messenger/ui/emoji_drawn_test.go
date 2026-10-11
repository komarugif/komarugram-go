// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"

	"gioui.org/layout"

	"komarugram/internal/messenger/emojipacks"
	"komarugram/internal/messenger/fonts"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

// withEmojiPack draws emoji with a pack of sprites that has every emoji of
// the picker's sections, as the client does once the pack it offers where
// the system has no emoji font is installed. Tests of the picker that need
// its emoji shown use it: otherwise the emoji the system's fonts do not
// draw are left out, and on Haiku, which has no emoji font, all of them.
func withEmojiPack(t *testing.T, more ...string) {
	t.Helper()
	seen := map[string]bool{}
	var order [][]string
	add := func(emoji string) {
		emoji = strings.ReplaceAll(emoji, "\uFE0F", "")
		if emoji != "" && !seen[emoji] {
			seen[emoji] = true
			order = append(order, []string{emoji})
		}
	}
	for _, items := range emojiSectionItems() {
		for _, item := range items {
			add(item.Emoji)
		}
	}
	for _, emoji := range more {
		add(emoji)
	}
	const cell, columns = 8, 64
	rows := (len(order) + columns - 1) / columns
	picture := filepath.Join(t.TempDir(), "emoji_1.png")
	f, err := os.Create(picture)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewNRGBA(image.Rect(0, 0, columns*cell, rows*cell))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	catalog := t.TempDir()
	if _, err := emojipacks.BuildSprites(catalog, emojipacks.Pack{ID: "all", Name: "All", License: "CC0"}, emojipacks.Sprites{Cell: cell, Columns: columns, Rows: rows}, []string{picture}, order); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fonts.SetEnv, filepath.Join(catalog, "all"))
	applyFonts(preferences.Fonts{}, nil)
	t.Cleanup(func() {
		os.Unsetenv(fonts.SetEnv)
		applyFonts(preferences.Fonts{}, nil)
	})
}

// TestPickerLeavesOutEmojiNotDrawn checks that the picker offers the emoji
// the theme's fonts draw as one glyph, and neither a character no font has
// nor a sequence that falls apart. It needs a system emoji font.
func TestPickerLeavesOutEmojiNotDrawn(t *testing.T) {
	theme := defaults.NewTheme(layout.Context{}, schemes.SchemeBaselineLight())
	var d emojiDrawn
	// Without a theme nothing is left out.
	if !d.draws("\U0010FFFD") {
		t.Error("an emoji is left out before the fonts are known")
	}
	d.use(theme)
	if !d.draws("\U0001F600") {
		t.Skip("no emoji font")
	}
	for emoji, want := range map[string]bool{
		"\U0001F44D":           true,  // thumbs up
		"\U0001F44D\U0001F3FD": true,  // with a skin tone: one glyph
		"❤️":                   true,  // a heart with the emoji selector
		"\U0010FFFD":           false, // a private use character no font has
		"\U0001F600‍❤":         false, // joined, but not a sequence of any font
	} {
		if got := d.draws(emoji); got != want {
			t.Errorf("%q (%U) drawn: %v, want %v", emoji, []rune(emoji), got, want)
		}
	}
	items := []model.PickerItem{
		{ID: "emoji/a", Emoji: "\U0001F600"},
		{ID: "emoji/b", Emoji: "\U0010FFFD"},
		{ID: "sticker", Emoji: "\U0010FFFD", Media: model.Message{Media: &model.MessageMedia{}}},
		{ID: "emoji/c", Emoji: "\U0001F44D"},
	}
	got := d.filter(items)
	if len(got) != 3 || got[0].ID != "emoji/a" || got[1].ID != "sticker" || got[2].ID != "emoji/c" {
		t.Errorf("filtered: %v", got)
	}
	if kept := d.filter(items[:1]); &kept[0] != &items[0] {
		t.Error("a list that is all drawn was copied")
	}

	start := time.Now()
	sections := d.staticSections()
	took := time.Since(start)
	all := emojiSectionItems()
	shown, total := 0, 0
	for i := range sections {
		shown += len(sections[i])
		total += len(all[i])
		for _, item := range sections[i] {
			if !d.draws(item.Emoji) {
				t.Errorf("section %d offers %q, which is not drawn", i, item.Emoji)
			}
		}
	}
	if shown == 0 || shown > total {
		t.Errorf("%d of %d emoji offered", shown, total)
	}
	t.Logf("%d of %d emoji of the static sections are drawn; asked in %v", shown, total, took)

	// A composer without a theme, as in the tests of its rows, offers all.
	var c messageComposer
	if n := len(c.drawn.staticSections()[0]); n != len(all[0]) {
		t.Errorf("without a theme %d of %d emoji are offered", n, len(all[0]))
	}
}
