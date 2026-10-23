// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

const (
	fieldHeight = unit.Dp(56)
	fieldRadius = unit.Dp(12)
)

// textField is a one-line outlined field with its label above it. The Material
// input widget cannot hide what is typed, which the 2FA password needs.
type textField struct {
	editor widget.Editor
	// mask is what the editor shows for each character, when it is not peeked
	// into.
	mask rune
	// peek, if set, is an eye at the end of the field that shows what is
	// typed for as long as it is held down.
	peek *peekButton
}

// newTextField returns a field that shows every character as mask, if mask
// is not zero, and accepts only the characters in filter, if it is not empty.
func newTextField(mask rune, filter string) *textField {
	return &textField{editor: widget.Editor{SingleLine: true, Submit: true, Mask: mask, Filter: filter}, mask: mask}
}

// newPasswordField is newTextField for a password to be chosen, which the user
// may want to see once: it has an eye that shows it while it is held down.
func newPasswordField() *textField {
	f := newTextField('•', "")
	f.peek = new(peekButton)
	return f
}

func (f *textField) Text() string { return f.editor.Text() }

func (f *textField) Clear() { f.editor.SetText("") }

func (f *textField) Focus(gtx layout.Context) { gtx.Execute(key.FocusCmd{Tag: &f.editor}) }

// Submitted reports whether Enter was pressed in the field.
func (f *textField) Submitted(gtx layout.Context) bool {
	submitted := false
	for {
		ev, ok := f.editor.Update(gtx)
		if !ok {
			return submitted
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			submitted = true
		}
	}
}

// Layout draws the label and the field; invalid draws it in the error color.
func (f *textField) Layout(gtx layout.Context, title string, invalid bool) layout.Dimensions {
	sc := scheme(gtx)
	theme := wdk.GetMaterialTheme(gtx)
	if f.peek != nil {
		f.peek.update(gtx, &f.editor)
		if f.peek.down {
			f.editor.Mask = 0
		} else {
			f.editor.Mask = f.mask
		}
	}
	focused := gtx.Focused(&f.editor)

	outline, width := sc.Outline, gtx.Dp(1)
	switch {
	case invalid:
		outline, width = sc.Error.Color, gtx.Dp(2)
	case focused:
		outline, width = sc.Primary.Color, gtx.Dp(2)
	}
	labelColor := sc.SurfaceVariant.OnColor
	if invalid {
		labelColor = sc.Error.Color
	} else if focused {
		labelColor = sc.Primary.Color
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 6, Left: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, title, token.TypestyleLabelMedium, labelColor, 1)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(fieldHeight))
			radius := gtx.Dp(fieldRadius)
			box := image.Rectangle{Max: size}
			fillRounded(gtx, sc.Surface.Color, size, radius)
			paint.FillShape(gtx.Ops, outline.AsNRGBA(), clip.Stroke{
				Path:  clip.UniformRRect(box, radius).Path(gtx.Ops),
				Width: float32(width),
			}.Op())

			pad := gtx.Dp(16)
			// The eye takes the end of the box, and the text what is left of it.
			eye := 0
			if f.peek != nil {
				eye = gtx.Dp(peekSize)
				color := sc.SurfaceVariant.OnColor
				if f.peek.down {
					color = sc.Primary.Color
				}
				offset(gtx, image.Pt(size.X-eye-gtx.Dp(4), (size.Y-eye)/2), func(gtx layout.Context) layout.Dimensions {
					return f.peek.layout(gtx, eye, color)
				})
			}
			style := theme.Typescale[token.TypestyleBodyLarge]
			f.editor.LineHeight = style.LineHeight

			color := op.Record(gtx.Ops)
			paint.ColorOp{Color: sc.Surface.OnColor.AsNRGBA()}.Add(gtx.Ops)
			text := color.Stop()
			color = op.Record(gtx.Ops)
			paint.ColorOp{Color: sc.Primary.Color.SetOpacity(token.OpacityLevel3).AsNRGBA()}.Add(gtx.Ops)
			selection := color.Stop()

			// The line sits in the middle of the box and the editor reaches
			// down to the bottom of it, so a click anywhere below the top edge
			// of the line lands in the editor.
			top := max((size.Y-gtx.Sp(style.LineHeight))/2, 0)
			lineGtx := gtx
			lineGtx.Constraints = layout.Exact(image.Pt(max(size.X-2*pad-eye, 0), size.Y-top))
			offset(gtx, image.Pt(pad, top), func(gtx layout.Context) layout.Dimensions {
				return f.editor.Layout(lineGtx, theme.TextShaper, style.AsRegularFont(), style.Size, text, selection)
			})
			return layout.Dimensions{Size: size}
		}),
	)
}

// peekSize is the side of the eye's square, which is what a press has to hit.
const peekSize = unit.Dp(40)

// peekButton is held down to see what a masked field holds, and shows it for
// as long as it is: it is not a click, and takes no keyboard focus, so that
// what is typed goes on where it was.
type peekButton struct {
	down bool
	// focused is whether the field had the keyboard focus when the press
	// came, to give it back when it ends.
	focused bool
}

// update takes the presses and releases that came since the last frame.
func (b *peekButton) update(gtx layout.Context, editor *widget.Editor) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: b, Kinds: pointer.Press | pointer.Release | pointer.Cancel})
		if !ok {
			return
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			b.down, b.focused = true, gtx.Focused(editor)
		case pointer.Release, pointer.Cancel:
			if b.down && b.focused {
				gtx.Execute(key.FocusCmd{Tag: editor})
			}
			b.down = false
		}
	}
}

// layout draws the eye in a square of size, and takes the presses on it.
func (b *peekButton) layout(gtx layout.Context, size int, color token.MatColor) layout.Dimensions {
	area := clip.Rect{Max: image.Pt(size, size)}.Push(gtx.Ops)
	event.Op(gtx.Ops, b)
	pointer.CursorPointer.Add(gtx.Ops)
	area.Pop()
	icon := gtx.Dp(24)
	offset(gtx, image.Pt((size-icon)/2, (size-icon)/2), func(gtx layout.Context) layout.Dimensions {
		return exact(gtx, image.Pt(icon, icon), func(gtx layout.Context) layout.Dimensions {
			return iconPeek(gtx, color)
		})
	})
	return layout.Dimensions{Size: image.Pt(size, size)}
}
