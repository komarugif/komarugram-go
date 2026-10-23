// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strings"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"komarugram/internal/messenger/countries"
	"komarugram/internal/messenger/localization"
)

// phoneField is the input of the phone number, as Telegram's web client has
// it: the country the number is in, found from its calling code, which a
// click changes through a list; and the number itself, written in groups
// as it is typed, so that it is easy to check against the one the user has
// in mind, "+7 900 123 45 67".
type phoneField struct {
	*textField
	picker  *countryPicker
	country surface
	// chosen is the ISO code of the country picked from the list, which
	// stays the country of a calling code that several share.
	chosen string
	// text and caret are the text and the caret as the last frame left
	// them, to tell what an edit took out.
	text  string
	caret int
}

func newPhoneField() *phoneField {
	return &phoneField{
		textField: newTextField(0, "0123456789+-() "),
		picker:    newCountryPicker(),
	}
}

// Digits returns the digits of the number typed, the calling code first.
func (f *phoneField) Digits() string { return countries.Digits(f.Text()) }

// Submitted reports whether Enter was pressed in the field, and keeps the
// number in groups while it is typed, pasted or cut.
func (f *phoneField) Submitted(gtx layout.Context) bool {
	submitted, changed := false, false
	for {
		ev, ok := f.editor.Update(gtx)
		if !ok {
			break
		}
		switch ev.(type) {
		case widget.SubmitEvent:
			submitted = true
		case widget.ChangeEvent:
			f.regroup()
			changed = true
		}
	}
	if !changed {
		// The caret may have moved since, for the next edit to be told from.
		f.caret, _ = f.editor.Selection()
	}
	return submitted
}

// Update opens the list of countries when the country is clicked.
func (f *phoneField) Update(gtx layout.Context) {
	if f.country.Clicked(gtx) {
		f.picker.Open(gtx)
	}
}

// Dialog draws the list of countries over the whole window when it is open,
// and takes the country picked.
func (f *phoneField) Dialog(gtx layout.Context, l localization.Catalog) {
	if iso, ok := f.picker.Layout(gtx, l); ok {
		f.pick(gtx, iso)
	}
}

// pick makes the country picked from the list the number's: the number
// becomes its calling code, to go on from.
func (f *phoneField) pick(gtx layout.Context, iso string) {
	c, ok := countries.ByISO(iso)
	if !ok {
		return
	}
	f.chosen = iso
	text := countries.Format(c.Digits())
	f.editor.SetText(text)
	end := len([]rune(text))
	f.editor.SetCaret(end, end)
	f.text, f.caret = text, end
	f.Focus(gtx)
}

// regroup writes the number in groups again after an edit, the caret staying
// after the digit it was after.
func (f *phoneField) regroup() {
	text := f.editor.Text()
	runes := []rune(text)
	caret, _ := f.editor.Selection()
	caret = min(max(caret, 0), len(runes))
	before := countries.Digits(string(runes[:caret]))
	digits := countries.Digits(text)

	// An edit that took out only separators took no digit from the number,
	// and would be undone by writing it in groups again: the key that took
	// them out meant the digit beside them.
	if previous := countries.Digits(f.text); len(runes) < len([]rune(f.text)) && len(digits) == len(previous) {
		at := len(before) // Delete took what was after the caret.
		if caret < f.caret {
			at-- // Backspace took what was before it.
		}
		if at >= 0 && at < len(digits) {
			digits = digits[:at] + digits[at+1:]
			before = digits[:min(at, len(digits))]
		}
	}
	if len(digits) > countries.MaxDigits {
		digits = digits[:countries.MaxDigits]
	}
	before = before[:min(len(before), len(digits))]

	grouped := countries.Format(digits)
	if grouped == "" && strings.Contains(text, "+") {
		// The plus typed first stays.
		grouped = "+"
	}
	if grouped != text {
		caret = caretAfterDigits(grouped, len(before))
		f.editor.SetText(grouped)
		f.editor.SetCaret(caret, caret)
	}
	f.text, f.caret = grouped, caret
}

// caretAfterDigits is where, in text, the caret is after its n-th digit, in
// runes: after the plus when n is zero, the end of the text for the last.
func caretAfterDigits(text string, n int) int {
	runes := []rune(text)
	if n <= 0 {
		if len(runes) > 0 && runes[0] == '+' {
			return 1
		}
		return 0
	}
	seen := 0
	for i, r := range runes {
		if r >= '0' && r <= '9' {
			seen++
			if seen == n {
				return i + 1
			}
		}
	}
	return len(runes)
}

// Layout draws the country, then the number. invalid draws the number in the
// error color.
func (f *phoneField) Layout(gtx layout.Context, l localization.Catalog, title string, invalid bool) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return f.layoutCountry(gtx, l)
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return f.textField.Layout(gtx, title, invalid)
		}),
	)
}

// layoutCountry draws the country of the number as a field, which opens the
// list of them.
func (f *phoneField) layoutCountry(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	name, color := l.T("login.country_choose"), sc.SurfaceVariant.OnColor
	if digits := f.Digits(); digits != "" {
		if c, ok := countries.Match(digits, f.chosen); ok {
			name, color = l.T("country."+c.ISO), sc.Surface.OnColor
		} else {
			name = l.T("login.country_unknown")
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 6, Left: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("login.field_country"), token.TypestyleLabelMedium, sc.SurfaceVariant.OnColor, 1)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(fieldHeight))
			radius := gtx.Dp(fieldRadius)
			fillRounded(gtx, sc.Surface.Color, size, radius)
			style := surfaceStyle{radius: radius, background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: name}
			f.country.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
				pad := gtx.Dp(16)
				icon := gtx.Dp(24)
				text := gtx
				text.Constraints = layout.Constraints{Max: image.Pt(max(size.X-3*pad-icon, 0), size.Y)}
				macro := op.Record(gtx.Ops)
				dims := label(text, name, token.TypestyleBodyLarge, color, 1)
				call := macro.Stop()
				offset(gtx, image.Pt(pad, (size.Y-dims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
					call.Add(gtx.Ops)
					return dims
				})
				offset(gtx, image.Pt(size.X-pad-icon, (size.Y-icon)/2), func(gtx layout.Context) layout.Dimensions {
					return exact(gtx, image.Pt(icon, icon), func(gtx layout.Context) layout.Dimensions {
						return iconExpandMore(gtx, sc.SurfaceVariant.OnColor)
					})
				})
				return layout.Dimensions{Size: size}
			})
			paint.FillShape(gtx.Ops, sc.Outline.AsNRGBA(), clip.Stroke{
				Path:  clip.UniformRRect(image.Rectangle{Max: size}, radius).Path(gtx.Ops),
				Width: float32(gtx.Dp(1)),
			}.Op())
			return layout.Dimensions{Size: size}
		}),
	)
}
