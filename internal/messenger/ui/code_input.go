// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

const (
	// codeMaxLength is the longest code that has a box for each digit.
	codeMaxLength = 8
	codeBoxWidth  = unit.Dp(72)
	codeBoxGap    = unit.Dp(8)
)

// codeInput is the field of the login code, which Telegram says the length
// of: a box for each digit, as many sites and applications have it. The
// digits are typed, pasted and deleted as in a field of one line, which is
// what the boxes are drawn over; the caret is always after the last digit,
// and the box it is in shows it. A code that is all there is taken at once.
// Where the length is not known, it is a field as any other.
type codeInput struct {
	plain *textField
	boxes widget.Editor
	// length is the number of digits, 0 where it is not known.
	length int
	// taken is set once a code that is all there was taken, until a digit
	// is deleted, so that the same one is not taken twice.
	taken bool
}

func newCodeInput() *codeInput {
	return &codeInput{
		plain: newTextField(0, "0123456789 "),
		boxes: widget.Editor{SingleLine: true, Submit: true, Filter: "0123456789"},
	}
}

// setLength tells how many digits the code has.
func (c *codeInput) setLength(n int) {
	if n < 1 || n > codeMaxLength {
		n = 0
	}
	c.length = n
	c.boxes.MaxLen = n
}

// boxed reports whether the code has a box for each digit.
func (c *codeInput) boxed() bool { return c.length > 0 }

// Text is the digits typed.
func (c *codeInput) Text() string {
	if c.boxed() {
		return c.boxes.Text()
	}
	return c.plain.Text()
}

// Clear takes the digits away.
func (c *codeInput) Clear() {
	c.plain.Clear()
	c.boxes.SetText("")
	c.taken = false
}

// Focus gives the keyboard to the field.
func (c *codeInput) Focus(gtx layout.Context) {
	if c.boxed() {
		gtx.Execute(key.FocusCmd{Tag: &c.boxes})
		return
	}
	c.plain.Focus(gtx)
}

// Submitted reports whether the code is there to take: Enter was pressed, or
// the last digit was typed or pasted.
func (c *codeInput) Submitted(gtx layout.Context) bool {
	if !c.boxed() {
		return c.plain.Submitted(gtx)
	}
	submitted := false
	for {
		ev, ok := c.boxes.Update(gtx)
		if !ok {
			break
		}
		switch ev.(type) {
		case widget.SubmitEvent:
			submitted = true
		case widget.ChangeEvent:
			if len([]rune(c.boxes.Text())) < c.length {
				c.taken = false
			} else if !c.taken {
				c.taken, submitted = true, true
			}
		}
	}
	// The caret stays after the last digit: a box is not clicked to move it.
	if start, end := c.boxes.Selection(); start == end {
		if n := len([]rune(c.boxes.Text())); start != n {
			c.boxes.SetCaret(n, n)
		}
	}
	return submitted
}

// Layout draws the field, which needs no label of its own: the title of the
// step says what it is for. invalid draws it in the error color; title is the
// label of a field without boxes.
func (c *codeInput) Layout(gtx layout.Context, title string, invalid bool) layout.Dimensions {
	if !c.boxed() {
		return c.plain.Layout(gtx, title, invalid)
	}
	sc := scheme(gtx)
	theme := wdk.GetMaterialTheme(gtx)
	focused := gtx.Focused(&c.boxes)
	gap := gtx.Dp(codeBoxGap)
	avail := gtx.Constraints.Max.X
	width := min(gtx.Dp(codeBoxWidth), (avail-gap*(c.length-1))/c.length)
	height := gtx.Dp(fieldHeight)
	row := image.Pt(c.length*width+(c.length-1)*gap, height)
	start := (avail - row.X) / 2
	digits := []rune(c.boxes.Text())
	// The box that has the caret: the next to be typed, or the last.
	current := min(len(digits), c.length-1)
	radius := gtx.Dp(fieldRadius)
	for i := range c.length {
		box := image.Rectangle{Max: image.Pt(width, height)}
		offset(gtx, image.Pt(start+i*(width+gap), 0), func(gtx layout.Context) layout.Dimensions {
			fillRounded(gtx, sc.Surface.Color, box.Size(), radius)
			outline, line := sc.Outline, gtx.Dp(1)
			switch {
			case invalid:
				outline, line = sc.Error.Color, gtx.Dp(2)
			case focused && i == current:
				outline, line = sc.Primary.Color, gtx.Dp(2)
			}
			paint.FillShape(gtx.Ops, outline.AsNRGBA(), clip.Stroke{
				Path:  clip.UniformRRect(box, radius).Path(gtx.Ops),
				Width: float32(line),
			}.Op())
			if i < len(digits) {
				digit := gtx
				digit.Constraints = layout.Constraints{Max: box.Size()}
				macro := op.Record(gtx.Ops)
				dims := centeredLabel(digit, string(digits[i]), token.TypestyleHeadlineSmall, sc.Surface.OnColor, 1)
				call := macro.Stop()
				offset(gtx, image.Pt((width-dims.Size.X)/2, (height-dims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
					call.Add(gtx.Ops)
					return dims
				})
			}
			return layout.Dimensions{Size: box.Size()}
		})
	}
	// The editor is over all the boxes, and sees nothing of itself:
	// it takes the keys, the paste and the clicks.
	style := theme.Typescale[token.TypestyleBodyLarge]
	c.boxes.LineHeight = style.LineHeight
	hidden := op.Record(gtx.Ops)
	paint.ColorOp{Color: color.NRGBA{}}.Add(gtx.Ops)
	text := hidden.Stop()
	over := gtx
	over.Constraints = layout.Exact(row)
	offset(gtx, image.Pt(start, 0), func(gtx layout.Context) layout.Dimensions {
		return c.boxes.Layout(over, theme.TextShaper, style.AsRegularFont(), style.Size, text, text)
	})
	return layout.Dimensions{Size: image.Pt(avail, height)}
}
