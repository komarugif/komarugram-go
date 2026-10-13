// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"gio-mw/token"

	"gioui.org/font"
	"gioui.org/text"
	"golang.org/x/image/math/fixed"

	"komarugram/internal/messenger/model"
)

// emojiDrawn tells the emoji the fonts of the theme draw from those they
// do not, so that the picker offers only what is seen: an emoji newer than
// the fonts is a box, and a sequence they do not know falls apart into its
// parts. The answers are of one shaper, and are asked again of another: a
// theme made anew has the fonts of the settings as they are now.
type emojiDrawn struct {
	shaper   *text.Shaper
	typeface font.Typeface
	known    map[string]bool
	// seen are the answers of shows.
	seen map[string]bool
	// sections are the static sections without what is not drawn.
	sections *[len(emojiSections)][]model.PickerItem
}

// use makes d answer for the fonts of theme.
func (d *emojiDrawn) use(theme *token.Theme) {
	if theme == nil || d.shaper == theme.TextShaper {
		return
	}
	*d = emojiDrawn{shaper: theme.TextShaper, typeface: theme.Typescale[token.TypestyleBodyLarge].Font, known: map[string]bool{}, seen: map[string]bool{}}
}

// draws reports whether the fonts draw emoji as one glyph. Without a theme
// everything is taken for drawn.
func (d *emojiDrawn) draws(emoji string) bool {
	if d.shaper == nil || emoji == "" {
		return true
	}
	if drawn, ok := d.known[emoji]; ok {
		return drawn
	}
	d.shaper.LayoutString(text.Parameters{Font: font.Font{Typeface: d.typeface}, PxPerEm: fixed.I(16), MaxWidth: 1 << 20}, emoji)
	glyphs, drawn := 0, true
	for g, ok := d.shaper.NextGlyph(); ok; g, ok = d.shaper.NextGlyph() {
		// The glyphs that take no room are those of joiners and selectors,
		// and the one that ends the paragraph.
		if g.Advance <= 0 {
			continue
		}
		glyphs++
		if g.ID.Notdef() {
			drawn = false
		}
	}
	drawn = drawn && glyphs == 1
	d.known[emoji] = drawn
	return drawn
}

// shows reports whether the fonts show something for emoji that a reader can
// take in: a picture, or the letters that a font without flags draws a flag
// as, and not the box of a character no font has. Without a theme everything
// is taken for shown.
func (d *emojiDrawn) shows(emoji string) bool {
	if d.shaper == nil || emoji == "" {
		return true
	}
	if shown, ok := d.seen[emoji]; ok {
		return shown
	}
	d.shaper.LayoutString(text.Parameters{Font: font.Font{Typeface: d.typeface}, PxPerEm: fixed.I(16), MaxWidth: 1 << 20}, emoji)
	glyphs, shown := 0, true
	for g, ok := d.shaper.NextGlyph(); ok; g, ok = d.shaper.NextGlyph() {
		if g.Advance <= 0 {
			continue
		}
		glyphs++
		if g.ID.Notdef() {
			shown = false
		}
	}
	shown = shown && glyphs > 0
	d.seen[emoji] = shown
	return shown
}

// filter returns the items without the emoji that are not drawn; stickers
// and custom emoji, which are pictures, stay. It returns items itself when
// all of them do.
func (d *emojiDrawn) filter(items []model.PickerItem) []model.PickerItem {
	if d.shaper == nil {
		return items
	}
	shown := func(item model.PickerItem) bool {
		return item.Media.Media != nil || d.draws(item.Emoji)
	}
	for i, item := range items {
		if shown(item) {
			continue
		}
		out := append([]model.PickerItem(nil), items[:i]...)
		for _, item := range items[i+1:] {
			if shown(item) {
				out = append(out, item)
			}
		}
		return out
	}
	return items
}

// staticSections are the emoji of the static sections that are drawn.
func (d *emojiDrawn) staticSections() [len(emojiSections)][]model.PickerItem {
	all := emojiSectionItems()
	if d.shaper == nil {
		return all
	}
	if d.sections == nil {
		d.sections = new([len(emojiSections)][]model.PickerItem)
		for i, items := range all {
			d.sections[i] = d.filter(items)
		}
	}
	return *d.sections
}
