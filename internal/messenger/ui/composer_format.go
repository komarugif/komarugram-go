// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/model"

	"gio-mw/token"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

// formatAction is a formatting the message field applies to what is
// selected, with Telegram Desktop's shortcut (lib_ui, input_field.h): a
// kind of entity, a link (EntityURL), or none, which clears the formatting.
type formatAction struct {
	kind string
	key  key.Name
	mods key.Modifiers
	// label is the localization key of its item in the field's menu.
	label string
}

// formatActions are in the order of Telegram Desktop's Formatting menu.
var formatActions = [...]formatAction{
	{model.EntityBold, "B", key.ModShortcut, "format.bold"},
	{model.EntityItalic, "I", key.ModShortcut, "format.italic"},
	{model.EntityUnderline, "U", key.ModShortcut, "format.underline"},
	{model.EntityStrike, "X", key.ModShortcut | key.ModShift, "format.strike"},
	{model.EntityQuote, ".", key.ModShortcut | key.ModShift, "format.quote"},
	{model.EntityCode, "M", key.ModShortcut | key.ModShift, "format.monospace"},
	{model.EntitySpoiler, "P", key.ModShortcut | key.ModShift, "format.spoiler"},
	{model.EntityURL, "K", key.ModShortcut, "format.link"},
	{"", "N", key.ModShortcut | key.ModShift, "format.clear"},
}

// u16Len is the length of s in UTF-16 code units, the unit of entities.
func u16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// runeToU16 is where the rune at index r of text starts, in UTF-16.
func runeToU16(text string, r int) int {
	n, i := 0, 0
	for _, c := range text {
		if i >= r {
			break
		}
		n += utf16.RuneLen(c)
		i++
	}
	return n
}

// u16ToRune is the index of the rune of text at the UTF-16 offset u, or of
// the rune it is in.
func u16ToRune(text string, u int) int {
	n, i := 0, 0
	for _, c := range text {
		if n >= u {
			break
		}
		n += utf16.RuneLen(c)
		i++
	}
	return i
}

// shiftEntities keeps entities on the text they were on when old became
// new by an edit of one place: custom emoji drop where the edit touched
// them, formatting grows or shrinks with what was typed or deleted in it,
// as Telegram Desktop's field keeps its tags. Text typed right after a
// formatted part is not formatted; inside it, it is. caret is where the
// caret is after the edit, in runes, which tells where text was typed
// among equal characters; -1 when it does not tell.
func shiftEntities(old, new string, entities []model.Entity, caret int) []model.Entity {
	a, b := []rune(old), []rune(new)
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	if n := len(b) - len(a); n > 0 && caret >= n && caret <= len(b) && caret-n < prefix {
		// Typed among equal characters: the caret ends what was typed.
		start := caret - n
		if string(b[:start]) == string(a[:start]) && string(b[caret:]) == string(a[start:]) {
			prefix, suffix = start, len(a)-start
		}
	}
	start := len(utf16.Encode(a[:prefix]))
	end := len(utf16.Encode(a[:len(a)-suffix]))
	inserted := len(utf16.Encode(b[prefix : len(b)-suffix]))
	delta := inserted - (end - start)
	var out []model.Entity
	for _, e := range entities {
		f := e.Offset + e.Length
		switch {
		case f <= start:
			out = append(out, e)
		case e.Offset >= end:
			e.Offset += delta
			out = append(out, e)
		case e.Kind == model.EntityEmoji:
			// An emoji edited is no longer the custom one.
		default:
			// What was typed in place of a part of the entity is in it
			// when the entity began before it.
			if e.Offset >= start {
				e.Offset = start + inserted
			}
			if f >= end {
				f += delta
			} else {
				f = start + inserted
			}
			if f > e.Offset {
				e.Length = f - e.Offset
				out = append(out, e)
			}
		}
	}
	return out
}

// formatCovers tells whether entities of kind cover all of [start, end).
func formatCovers(entities []model.Entity, kind string, start, end int) bool {
	at := start
	for _, e := range model.SortEntities(entities) {
		if e.Kind != kind || e.Offset > at {
			continue
		}
		at = max(at, e.Offset+e.Length)
	}
	return at >= end
}

// removeFormat takes the entities that match off [start, end), splitting
// those that reach out of it.
func removeFormat(entities []model.Entity, start, end int, match func(model.Entity) bool) []model.Entity {
	var out []model.Entity
	for _, e := range entities {
		f := e.Offset + e.Length
		if !match(e) || f <= start || e.Offset >= end {
			out = append(out, e)
			continue
		}
		if e.Offset < start {
			left := e
			left.Length = start - e.Offset
			out = append(out, left)
		}
		if f > end {
			right := e
			right.Offset, right.Length = end, f-end
			out = append(out, right)
		}
	}
	return out
}

// toggleFormat formats [start, end) as kind, merged with the entities of
// kind it touches, or takes kind off it where all of it has kind already,
// as Telegram Desktop's toggleSelectionMarkdown.
func toggleFormat(entities []model.Entity, kind string, start, end int) []model.Entity {
	if start >= end {
		return entities
	}
	if formatCovers(entities, kind, start, end) {
		return model.SortEntities(removeFormat(entities, start, end, func(e model.Entity) bool { return e.Kind == kind }))
	}
	var out []model.Entity
	for _, e := range entities {
		if e.Kind == kind && e.Offset <= end && e.Offset+e.Length >= start {
			start, end = min(start, e.Offset), max(end, e.Offset+e.Length)
			continue
		}
		out = append(out, e)
	}
	return model.SortEntities(append(out, model.Entity{Kind: kind, Offset: start, Length: end - start}))
}

// clearFormat takes every formatting off [start, end); custom emoji stay.
func clearFormat(entities []model.Entity, start, end int) []model.Entity {
	return model.SortEntities(removeFormat(entities, start, end, func(e model.Entity) bool { return e.Kind != model.EntityEmoji }))
}

// setLink makes [start, end) a link to url, in place of the links it had;
// an empty url takes them off.
func setLink(entities []model.Entity, start, end int, url string) []model.Entity {
	out := removeFormat(entities, start, end, func(e model.Entity) bool { return e.Kind == model.EntityURL })
	if url != "" && start < end {
		out = append(out, model.Entity{Kind: model.EntityURL, Offset: start, Length: end - start, URL: url})
	}
	return model.SortEntities(out)
}

// linkAt is the link of entities over [start, end), "" for none.
func linkAt(entities []model.Entity, start, end int) string {
	for _, e := range entities {
		if e.Kind == model.EntityURL && e.Offset < max(end, start+1) && e.Offset+e.Length > start {
			return e.URL
		}
	}
	return ""
}

// formatted tells whether the field draws entities over the editor's glyphs.
func formatted(entities []model.Entity) bool {
	return len(entities) > 0
}

// formatShortcuts are the key filters of the formatting shortcuts for the
// field e, by the keys' places, which do not change with the layout.
func formatShortcuts(e *widget.Editor) []key.Filter {
	out := make([]key.Filter, len(formatActions))
	for i, a := range formatActions {
		out[i] = key.Filter{Focus: e, Physical: a.key, Required: a.mods}
	}
	return out
}

// formatOfKey is the action of a key pressed, if it is one's.
func formatOfKey(e key.Event) (formatAction, bool) {
	name := e.Physical
	if name == "" {
		name = e.Name
	}
	for _, a := range formatActions {
		if a.key == name && e.Modifiers == a.mods {
			return a, true
		}
	}
	return formatAction{}, false
}

// formatShortcutText is how a's shortcut is written in the field's menu.
func formatShortcutText(a formatAction) string {
	var parts []string
	if a.mods.Contain(key.ModShortcut) {
		if key.ModShortcut == key.ModCommand {
			parts = append(parts, "⌘")
		} else {
			parts = append(parts, "Ctrl")
		}
	}
	if a.mods.Contain(key.ModShift) {
		parts = append(parts, "Shift")
	}
	return strings.Join(append(parts, string(a.key)), "+")
}

// fieldStyle is how a part of the text of the field is drawn.
type fieldStyle struct {
	bold, italic, underline, strike, code, spoiler, link, quote bool
	emoji                                                       int64
}

// fieldRun is a part of the text of the field in one style, in runes.
type fieldRun struct {
	start, end int
	style      fieldStyle
}

// fieldRuns splits text into the parts entities style alike.
func fieldRuns(text string, entities []model.Entity) []fieldRun {
	n := utf8.RuneCountInString(text)
	type span struct {
		start, end int
		kind       string
		document   int64
	}
	var spans []span
	cuts := []int{0, n}
	for _, e := range entities {
		if e.Length <= 0 {
			continue
		}
		s := span{u16ToRune(text, e.Offset), u16ToRune(text, e.Offset+e.Length), e.Kind, e.DocumentID}
		if s.start >= s.end {
			continue
		}
		spans = append(spans, s)
		cuts = append(cuts, s.start, s.end)
	}
	if len(spans) == 0 {
		return nil
	}
	seen := map[int]bool{}
	var points []int
	for _, c := range cuts {
		if !seen[c] && c >= 0 && c <= n {
			seen[c] = true
			points = append(points, c)
		}
	}
	for i := 1; i < len(points); i++ {
		for j := i; j > 0 && points[j] < points[j-1]; j-- {
			points[j], points[j-1] = points[j-1], points[j]
		}
	}
	var runs []fieldRun
	for i := 0; i+1 < len(points); i++ {
		r := fieldRun{start: points[i], end: points[i+1]}
		for _, s := range spans {
			if s.start > r.start || s.end < r.end {
				continue
			}
			switch s.kind {
			case model.EntityEmoji:
				r.style.emoji = s.document
			case model.EntityBold:
				r.style.bold = true
			case model.EntityItalic:
				r.style.italic = true
			case model.EntityUnderline:
				r.style.underline = true
			case model.EntityStrike:
				r.style.strike = true
			case model.EntityCode, model.EntityPre:
				r.style.code = true
			case model.EntitySpoiler:
				r.style.spoiler = true
			case model.EntityURL:
				r.style.link = true
			case model.EntityQuote:
				r.style.quote = true
			}
		}
		runs = append(runs, r)
	}
	return runs
}

// italicShear is how much the field slants italic text, as a synthetic
// oblique: about 11°.
const italicShear = -0.2

// formattedEditor lays out the message field e as flatEditor does and
// draws its text with the formatting of entities over it, as Telegram
// Desktop's field shows what will be sent: links in the accent color,
// code and spoilers on a plate, lines under and through the text, and
// bold and italic drawn from the font's regular face, thickened and
// slanted, the field having one face for all of its text.
type fieldEmojiMedia struct {
	media   *chatmedia.Manager
	images  *imageOps
	animate bool
}

func formattedEditor(gtx layout.Context, e *widget.Editor, hint string, entities []model.Entity, emojiMedia ...fieldEmojiMedia) layout.Dimensions {
	if !formatted(entities) {
		return flatEditor(gtx, e, hint)
	}
	e.HideText = true
	dims := flatEditor(gtx, e, hint)
	e.HideText = false
	text := e.Text()
	runs := fieldRuns(text, entities)
	sc := scheme(gtx)
	defer clip.Rect{Max: dims.Size}.Push(gtx.Ops).Pop()
	var regions []widget.Region
	// Plates under the text first.
	for _, r := range runs {
		var plate token.MatColor
		switch {
		case r.style.spoiler:
			plate = sc.Surface.OnColor.SetOpacity(0.16)
		case r.style.code:
			plate = sc.Surface.OnColor.SetOpacity(0.08)
		case r.style.quote:
			plate = sc.Primary.Color.SetOpacity(0.10)
		default:
			continue
		}
		regions = e.Regions(r.start, r.end, regions[:0])
		for _, g := range regions {
			stack := clip.UniformRRect(g.Bounds, gtx.Dp(3)).Push(gtx.Ops)
			paint.ColorOp{Color: plate.AsNRGBA()}.Add(gtx.Ops)
			paint.PaintOp{}.Add(gtx.Ops)
			stack.Pop()
		}
	}
	plain := sc.Surface.OnColor
	material := func(c token.MatColor) op.CallOp {
		m := op.Record(gtx.Ops)
		paint.ColorOp{Color: c.AsNRGBA()}.Add(gtx.Ops)
		return m.Stop()
	}
	// What no run styles is drawn as it is.
	plainMaterial := material(plain)
	covered := 0
	paintPlain := func(start, end int) {
		if start >= end {
			return
		}
		regions = e.Regions(start, end, regions[:0])
		for _, g := range regions {
			stack := clip.Rect(g.Bounds).Push(gtx.Ops)
			e.PaintText(gtx, plainMaterial)
			stack.Pop()
		}
	}
	thickness := max(1, gtx.Dp(1))
	bold := float32(math.Max(1, float64(gtx.Metric.PxPerDp)*0.6))
	for _, r := range runs {
		paintPlain(covered, r.start)
		covered = r.end
		color := plain
		if r.style.link {
			color = sc.Primary.Color
		}
		m := material(color)
		regions = e.Regions(r.start, r.end, regions[:0])
		for _, g := range regions {
			baseline := g.Bounds.Max.Y - g.Baseline
			if r.style.emoji != 0 && len(emojiMedia) > 0 {
				media := emojiMedia[0]
				px := min(g.Bounds.Dx(), g.Bounds.Dy())
				if frame := customEmoji(media.media, media.images, r.style.emoji, px, media.animate, color.AsNRGBA()); frame != nil {
					origin := image.Pt(g.Bounds.Min.X+(g.Bounds.Dx()-px)/2, g.Bounds.Min.Y+(g.Bounds.Dy()-px)/2)
					offset(gtx, origin, func(gtx layout.Context) layout.Dimensions {
						return drawImage(gtx, media.images, frame, image.Pt(px, px))
					})
					continue
				}
			}
			var stack []interface{ Pop() }
			if r.style.italic {
				shear := f32.AffineId().Shear(f32.Pt(0, float32(baseline)), italicShear, 0)
				stack = append(stack, op.Affine(shear).Push(gtx.Ops))
			}
			area := clip.Rect(g.Bounds)
			if r.style.bold {
				area.Max.X += int(math.Ceil(float64(bold)))
			}
			stack = append(stack, area.Push(gtx.Ops))
			e.PaintText(gtx, m)
			if r.style.bold {
				offset := op.Affine(f32.AffineId().Offset(f32.Pt(bold, 0))).Push(gtx.Ops)
				e.PaintText(gtx, m)
				offset.Pop()
			}
			for i := len(stack) - 1; i >= 0; i-- {
				stack[i].Pop()
			}
			if r.style.underline || r.style.link {
				y := g.Bounds.Max.Y - max(g.Baseline/2, thickness)
				fillAt(gtx, color, image.Rect(g.Bounds.Min.X, y, g.Bounds.Max.X, y+thickness))
			}
			if r.style.strike {
				y := baseline - (baseline-g.Bounds.Min.Y)*3/10
				fillAt(gtx, color, image.Rect(g.Bounds.Min.X, y, g.Bounds.Max.X, y+thickness))
			}
		}
	}
	paintPlain(covered, utf8.RuneCountInString(text))
	return dims
}

// fillAt fills rect with color.
func fillAt(gtx layout.Context, color token.MatColor, rect image.Rectangle) {
	defer clip.Rect(rect).Push(gtx.Ops).Pop()
	paint.ColorOp{Color: color.AsNRGBA()}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}
