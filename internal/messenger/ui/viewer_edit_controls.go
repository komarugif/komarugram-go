// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/photoedit"
)

// Heights of the editor's rows.
const (
	editBarRow  = unit.Dp(56)
	editToolRow = unit.Dp(48)
	editTrimRow = unit.Dp(44)
	editTextRow = unit.Dp(56)
)

// editTrim is the row that picks the part of a video kept.
type editTrim struct {
	// holding is the handle dragged: 0 for none, 1 for the start, 2 for the end.
	holding int
	press   struct{}
	asking  bool
}

// controlsHeight is how tall all the rows are.
func (e *viewerEdit) controlsHeight(gtx layout.Context) int {
	h := gtx.Dp(editBarRow) + gtx.Dp(editToolRow)
	// A video's row is kept before its length is known, for the picture not to
	// move when it comes.
	if e.video {
		h += gtx.Dp(editTrimRow)
	}
	return h
}

// takeClicks takes the clicks on the rows before anything is drawn.
func (e *viewerEdit) takeClicks(gtx layout.Context, v *mediaViewer, l localization.Catalog) {
	if e.cancelEdit.Clicked(gtx) {
		e.askClose(v)
		if !e.open {
			return
		}
	}
	if e.scene == nil {
		return
	}
	if e.done.Clicked(gtx) {
		e.finishText(gtx)
		e.finish(gtx, v, l)
		if !e.open {
			return
		}
	}
	if e.modePaint.Clicked(gtx) {
		e.mode = editPaint
	}
	if e.modeCrop.Clicked(gtx) {
		e.finishText(gtx)
		e.mode, e.selected, e.armed, e.shapesOpen = editCrop, -1, false, false
	}
	if e.undo.Clicked(gtx) {
		e.undoStep()
	}
	if e.redo.Clicked(gtx) {
		e.redoStep()
	}
	if e.mode == editCrop {
		if e.rotate.Clicked(gtx) {
			e.scene.Transform.Angle = (e.scene.Transform.Angle + 270) % 360
		}
		if e.flip.Clicked(gtx) {
			e.scene.Transform.Flip = !e.scene.Transform.Flip
		}
		for i := range e.aspects {
			if e.aspects[i].Clicked(gtx) {
				e.aspect = cropAspect(i)
				size := e.scene.Size
				switch e.aspect {
				case cropOriginal:
					e.scene.Transform.Crop = photoedit.FitAspect(image.Rectangle{Max: size}, float64(size.X)/float64(size.Y))
				case cropSquare:
					e.scene.Transform.Crop = photoedit.FitAspect(e.scene.Transform.Crop, 1)
				}
			}
		}
		return
	}
	if e.sticker.Clicked(gtx) {
		e.finishText(gtx)
		if e.stickers != nil && e.stickers.shown {
			e.stickers.shown = false
		} else {
			e.openStickers(v, l)
		}
	}
	if e.addText.Clicked(gtx) {
		e.finishText(gtx)
		e.newText(gtx)
	}
	if e.shapesButton.Clicked(gtx) {
		e.shapesOpen = !e.shapesOpen
		if !e.shapesOpen {
			e.armed = false
		}
	}
	for i := range e.tools {
		if e.tools[i].Clicked(gtx) {
			e.tool, e.armed, e.shapesOpen, e.selected = photoedit.Tool(i), false, false, -1
		}
	}
	for i := range e.shapes {
		if e.shapes[i].Clicked(gtx) {
			e.finishText(gtx)
			e.armedShape, e.armed, e.selected = photoedit.ShapeType(i), true, -1
		}
	}
	if e.fill.Clicked(gtx) {
		e.filled = !e.filled
		if i := e.selected; i >= 0 && i < len(e.scene.Elements) && e.scene.Elements[i].Shape != nil {
			el := *e.scene.Elements[i]
			s := *el.Shape
			s.Filled = e.filled
			el.Shape = &s
			e.scene.Replace(i, &el)
		}
	}
	for i := range e.colors {
		if e.colors[i].Clicked(gtx) {
			e.color = editPalette[i]
			e.recolorSelected(gtx)
		}
	}
	if e.selected >= 0 && e.selected < len(e.scene.Elements) {
		e.takeItemClicks(gtx)
	}
	x := &e.text
	if x.editing {
		if x.ok.Clicked(gtx) {
			e.finishText(gtx)
		}
		for {
			ev, ok := x.editor.Update(gtx)
			if !ok {
				break
			}
			if _, changed := ev.(widget.ChangeEvent); changed && x.editing {
				content := x.editor.Text()
				e.changeText(gtx, x.at, func(t *photoedit.Text) { t.Text = content })
			}
		}
	}
}

// recolorSelected gives the element selected the color chosen.
func (e *viewerEdit) recolorSelected(gtx layout.Context) {
	i := e.selected
	if i < 0 || i >= len(e.scene.Elements) {
		return
	}
	el := *e.scene.Elements[i]
	switch {
	case el.Text != nil:
		col := e.color
		e.changeText(gtx, i, func(t *photoedit.Text) { t.Color = col })
	case el.Shape != nil:
		s := *el.Shape
		s.Color = e.color
		el.Shape = &s
		e.scene.Replace(i, &el)
	}
}

// takeItemClicks takes the clicks on what acts on the element selected.
func (e *viewerEdit) takeItemClicks(gtx layout.Context) {
	i := e.selected
	el := e.scene.Elements[i]
	if e.remove.Clicked(gtx) {
		e.text.editing = false
		e.removeSelected()
		gtx.Execute(key.FocusCmd{Tag: &e.keys})
		return
	}
	if e.flipItem.Clicked(gtx) {
		c := *el
		c.Flipped = !c.Flipped
		e.scene.Replace(i, &c)
	}
	if e.repeat.Clicked(gtx) {
		c := *el
		shift := float64(gtx.Dp(24)) / e.view.scale
		c.Center = c.Center.Add(photoedit.Point{X: shift, Y: shift})
		e.selected = e.scene.Add(&c)
		e.text.editing = false
		return
	}
	if el.Text != nil {
		if e.styleButton.Clicked(gtx) {
			e.changeText(gtx, i, func(t *photoedit.Text) { t.Style = (t.Style + 1) % 4 })
		}
		if e.fontButton.Clicked(gtx) {
			e.changeText(gtx, i, func(t *photoedit.Text) { t.Typeface = (t.Typeface + 1) % 5 })
		}
		if e.alignButton.Clicked(gtx) {
			e.changeText(gtx, i, func(t *photoedit.Text) { t.Align = (t.Align + 1) % 3 })
		}
	}
}

// layoutControls draws the rows in area.
func (e *viewerEdit) layoutControls(gtx layout.Context, v *mediaViewer, l localization.Catalog, area image.Rectangle) {
	y := area.Min.Y
	row := func(h int, w layout.Widget) {
		inRect(gtx, image.Rect(area.Min.X, y, area.Max.X, y+h), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = gtx.Constraints.Max
			return w(gtx)
		})
		y += h
	}
	if e.text.editing {
		// Over the bottom of the picture: see placeStage.
		y -= gtx.Dp(editTextRow)
		row(gtx.Dp(editTextRow), func(gtx layout.Context) layout.Dimensions { return e.layoutTextRow(gtx, l) })
	}
	// What needs the picture comes with it, within the rows' own fade.
	ready := func(w layout.Widget) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			if e.scene == nil || e.transition.tools <= 0 {
				return layout.Dimensions{Size: gtx.Constraints.Max}
			}
			defer paint.PushOpacity(gtx.Ops, e.transition.tools).Pop()
			return w(gtx)
		}
	}
	if e.video {
		row(gtx.Dp(editTrimRow), ready(func(gtx layout.Context) layout.Dimensions {
			if e.media.Duration <= 0 {
				return layout.Dimensions{Size: gtx.Constraints.Max}
			}
			return e.layoutTrim(gtx)
		}))
	}
	row(gtx.Dp(editToolRow), ready(func(gtx layout.Context) layout.Dimensions { return e.layoutToolRow(gtx, l) }))
	row(gtx.Dp(editBarRow), func(gtx layout.Context) layout.Dimensions { return e.layoutBarRow(gtx, l) })
}

// editIcon draws a round button of the editor, lit when it is what is in use.
func editIcon(gtx layout.Context, c *widget.Clickable, icon wdk.IconWidget, lit, enabled bool) layout.Dimensions {
	d := gtx.Dp(40)
	size := image.Pt(d, d)
	if !enabled {
		gtx = gtx.Disabled()
	}
	gap := gtx.Dp(2)
	gtx.Constraints = layout.Exact(size.Add(image.Pt(2*gap, 0)))
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if enabled {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		offset(gtx, image.Pt(gap, 0), func(gtx layout.Context) layout.Dimensions {
			background := color.NRGBA{R: 40, G: 44, B: 52, A: 110}
			if c.Hovered() && enabled {
				background.A = 170
			}
			ink := token.NewMatColorFromHexRGB(0xffffff)
			switch {
			case !enabled:
				background.A = 50
				ink = token.NewMatColorFromHexRGB(0x6c727c)
			case lit:
				ink = token.NewMatColorFromHexRGB(0x5eb5f7)
			}
			paint.FillShape(gtx.Ops, background, clip.Ellipse{Max: size}.Op(gtx.Ops))
			inner := d * 3 / 5
			offset(gtx, size.Sub(image.Pt(inner, inner)).Div(2), func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(image.Pt(inner, inner))
				return icon(gtx, ink)
			})
			return layout.Dimensions{Size: size}
		})
		return layout.Dimensions{Size: size.Add(image.Pt(2*gap, 0))}
	})
}

// editBarButton draws a button at the end of the row, of its own height: the
// row's Flex hands its height down as the least, which would make the plate as
// tall as the row with the text at its top.
func editBarButton(gtx layout.Context, s *surface, txt string, col token.MatColor) layout.Dimensions {
	gtx.Constraints.Min = image.Point{}
	return textButtonColor(gtx, s, txt, col)
}

// layoutBarRow is the row of the modes and what they do, between Cancel and Done.
func (e *viewerEdit) layoutBarRow(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	white := token.NewMatColorFromHexRGB(0xffffff)
	accent := token.NewMatColorFromHexRGB(0x5eb5f7)
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return editBarButton(gtx, &e.cancelEdit, l.T("editor.cancel"), white)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			if e.scene == nil || e.transition.tools <= 0 {
				return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 0)}
			}
			defer paint.PushOpacity(gtx.Ops, e.transition.tools).Pop()
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{}
				icon := func(c *widget.Clickable, w wdk.IconWidget, lit, enabled bool) layout.FlexChild {
					return layout.Rigid(func(gtx layout.Context) layout.Dimensions { return editIcon(gtx, c, w, lit, enabled) })
				}
				items := []layout.FlexChild{
					icon(&e.modeCrop, iconEditCrop, e.mode == editCrop, true),
					icon(&e.modePaint, iconEditBrush, e.mode == editPaint, true),
					layout.Rigid(layout.Spacer{Width: 12}.Layout),
				}
				if e.mode == editCrop {
					items = append(items, icon(&e.rotate, iconEditRotate, false, true), icon(&e.flip, iconEditFlip, e.scene.Transform.Flip, true))
				} else {
					items = append(items,
						icon(&e.undo, iconEditUndo, false, e.scene.CanUndo()),
						icon(&e.redo, iconEditRedo, false, e.scene.CanRedo()),
						layout.Rigid(layout.Spacer{Width: 12}.Layout),
						icon(&e.sticker, iconEditSticker, e.stickers != nil && e.stickers.shown, true),
						icon(&e.addText, iconEditText, e.text.editing, true),
						icon(&e.shapesButton, iconEditShapes, e.shapesOpen, true),
					)
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, items...)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if e.scene == nil || e.transition.tools <= 0 {
				return layout.Dimensions{}
			}
			defer paint.PushOpacity(gtx.Ops, e.transition.tools).Pop()
			return layout.Inset{Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return editBarButton(gtx, &e.done, l.T("editor.done"), accent)
			})
		}),
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// layoutToolRow is the row of what the mode offers: the crop's proportions, or
// the brushes, the figures, or what acts on the element selected, with the
// colors and the size.
func (e *viewerEdit) layoutToolRow(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	var widgets []layout.Widget
	icon := func(c *widget.Clickable, w wdk.IconWidget, lit bool) {
		widgets = append(widgets, func(gtx layout.Context) layout.Dimensions { return editIcon(gtx, c, w, lit, true) })
	}
	gap := func() {
		widgets = append(widgets, layout.Spacer{Width: 10}.Layout)
	}
	white := token.NewMatColorFromHexRGB(0xffffff)
	word := func(s *surface, text string) {
		widgets = append(widgets, func(gtx layout.Context) layout.Dimensions {
			return textButtonColor(gtx, s, text, white)
		})
	}
	colors := func() {
		for i := range e.colors {
			widgets = append(widgets, func(gtx layout.Context) layout.Dimensions {
				return e.layoutSwatch(gtx, i)
			})
		}
	}
	size := func() {
		widgets = append(widgets, func(gtx layout.Context) layout.Dimensions {
			return e.layoutSizeSlider(gtx)
		})
	}
	var selected *photoedit.Element
	if e.selected >= 0 && e.selected < len(e.scene.Elements) {
		selected = e.scene.Elements[e.selected]
	}
	switch {
	case e.mode == editCrop:
		for i, key := range [...]string{"editor.crop_free", "editor.crop_original", "editor.crop_square"} {
			widgets = append(widgets, func(gtx layout.Context) layout.Dimensions {
				col := white
				if e.aspect == cropAspect(i) {
					col = token.NewMatColorFromHexRGB(0x5eb5f7)
				}
				return textButtonColor(gtx, &e.aspects[i], l.T(key), col)
			})
		}
	case selected != nil:
		icon(&e.remove, iconEditDelete, false)
		icon(&e.repeat, iconEditCopy, false)
		if selected.Text == nil {
			icon(&e.flipItem, iconEditFlip, selected.Flipped)
		}
		switch {
		case selected.Text != nil:
			gap()
			word(&e.styleButton, l.T(textStyleKeys[selected.Text.Style]))
			word(&e.fontButton, l.T(textFontKeys[selected.Text.Typeface]))
			icon(&e.alignButton, [...]wdk.IconWidget{iconEditAlignC, iconEditAlignL, iconEditAlignR}[selected.Text.Align], false)
			gap()
			colors()
		case selected.Shape != nil:
			icon(&e.fill, iconEditFill, selected.Shape.Filled)
			gap()
			colors()
		}
	case e.shapesOpen:
		for i, w := range [...]wdk.IconWidget{iconEditCircle, iconEditShapes, iconEditStar, iconEditBubble, iconEditArrow} {
			icon(&e.shapes[i], w, e.armed && e.armedShape == photoedit.ShapeType(i))
		}
		icon(&e.fill, iconEditFill, e.filled)
		gap()
		size()
		gap()
		colors()
	default:
		for i, w := range [...]wdk.IconWidget{iconEditPen, iconEditArrow, iconEditMarker, iconEditEraser, iconEditBlur} {
			icon(&e.tools[i], w, e.tool == photoedit.Tool(i))
		}
		gap()
		size()
		if e.tool != photoedit.Eraser && e.tool != photoedit.Blur {
			gap()
			colors()
		}
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Pt(0, gtx.Constraints.Max.Y)
		e.toolList.Alignment = layout.Middle
		return e.toolList.Layout(gtx, len(widgets), func(gtx layout.Context, i int) layout.Dimensions {
			gtx.Constraints.Min.X = 0
			return layout.Center.Layout(gtx, widgets[i])
		})
	})
}

var (
	textStyleKeys = [...]string{"editor.style_plain", "editor.style_framed", "editor.style_semi", "editor.style_opaque"}
	textFontKeys  = [...]string{"editor.font_default", "editor.font_italic", "editor.font_serif", "editor.font_condensed", "editor.font_monospace"}
)

// layoutSwatch draws a color of the palette, ringed when it is the one in use.
func (e *viewerEdit) layoutSwatch(gtx layout.Context, i int) layout.Dimensions {
	c := &e.colors[i]
	d := gtx.Dp(30)
	gtx.Constraints = layout.Exact(image.Pt(d, d))
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		r := gtx.Dp(10)
		center := image.Pt(d/2, d/2)
		rect := image.Rect(center.X-r, center.Y-r, center.X+r, center.Y+r)
		paint.FillShape(gtx.Ops, editPalette[i], clip.Ellipse(rect).Op(gtx.Ops))
		ring := color.NRGBA{R: 255, G: 255, B: 255, A: 90}
		width := float32(1)
		if e.color == editPalette[i] {
			ring.A, width = 255, float32(max(2, gtx.Dp(2)))
			rect = rect.Inset(-gtx.Dp(2))
		}
		paint.FillShape(gtx.Ops, ring, clip.Stroke{Path: clip.Ellipse(rect).Path(gtx.Ops), Width: width}.Op())
		return layout.Dimensions{Size: image.Pt(d, d)}
	})
}

// layoutSizeSlider draws the brush's size: a track that widens to the end, with
// its handle.
func (e *viewerEdit) layoutSizeSlider(gtx layout.Context) layout.Dimensions {
	size := image.Pt(gtx.Dp(120), gtx.Dp(36))
	gtx.Constraints = layout.Exact(size)
	e.size.Update(gtx)
	dims := e.size.Layout(gtx, layout.Horizontal, 8)
	track := image.Rect(0, size.Y/2-1, size.X, size.Y/2+1)
	paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 90}, clip.Rect(track).Op())
	x := int(e.size.Value * float32(size.X))
	r := max(gtx.Dp(5), int(e.size.Value*float32(gtx.Dp(10))))
	knob := image.Rect(x-r, size.Y/2-r, x+r, size.Y/2+r)
	paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, clip.Ellipse(knob).Op(gtx.Ops))
	return dims
}

// layoutTextRow is the field of the text being written, and its button.
func (e *viewerEdit) layoutTextRow(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	x := &e.text
	return layout.Inset{Left: 24, Right: 24, Top: 6, Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				size := gtx.Constraints.Max
				paint.FillShape(gtx.Ops, color.NRGBA{R: 40, G: 44, B: 52, A: 255}, clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(10)).Op(gtx.Ops))
				layout.Inset{Left: 12, Right: 12, Top: 10, Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return flatEditor(gtx, &x.editor, l.T("editor.text_placeholder"))
				})
				return layout.Dimensions{Size: size}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Point{}
					return textButtonColor(gtx, &x.ok, l.T("editor.done"), token.NewMatColorFromHexRGB(0x5eb5f7))
				})
			}),
		)
	})
}

// layoutTrim is the row of a video: its length as a track, the part kept bright
// between two handles, and the times of both ends.
func (e *viewerEdit) layoutTrim(gtx layout.Context) layout.Dimensions {
	size := gtx.Constraints.Max
	length := e.media.Duration
	pad := gtx.Dp(90)
	track := image.Rect(pad, size.Y/2-gtx.Dp(3), max(pad+1, size.X-pad), size.Y/2+gtx.Dp(3))
	trim := e.scene.Trim.Clamped(length)
	to := trim.To
	if to == 0 {
		to = length
	}
	toX := func(d time.Duration) int { return track.Min.X + int(float64(track.Dx())*d.Seconds()/length.Seconds()) }
	timeAt := func(x float32) time.Duration {
		share := max(0, min(1, (float64(x)-float64(track.Min.X))/float64(track.Dx())))
		return time.Duration(share * float64(length))
	}
	t := &e.trim
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &t.press, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		pe, isPointer := ev.(pointer.Event)
		if !isPointer || e.send.busy() {
			continue
		}
		switch pe.Kind {
		case pointer.Press:
			if math.Abs(float64(pe.Position.X)-float64(toX(trim.From))) <= math.Abs(float64(pe.Position.X)-float64(toX(to))) {
				t.holding = 1
			} else {
				t.holding = 2
			}
			fallthrough
		case pointer.Drag:
			at := timeAt(pe.Position.X)
			switch t.holding {
			case 1:
				trim.From = min(at, to-photoedit.MinTrim)
				e.frameAt = trim.From
			case 2:
				trim.To = max(at, trim.From+photoedit.MinTrim)
				to = trim.To
				e.frameAt = trim.To
			}
			trim = trim.Clamped(length)
			e.scene.Trim = trim
			if to = trim.To; to == 0 {
				to = length
			}
			e.frameDue = gtx.Now.Add(editFramePause)
		case pointer.Release, pointer.Cancel:
			t.holding = 0
		}
	}
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	pointer.CursorPointer.Add(gtx.Ops)
	event.Op(gtx.Ops, &t.press)
	area.Pop()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 50}, clip.UniformRRect(track, gtx.Dp(3)).Op(gtx.Ops))
	kept := image.Rect(toX(trim.From), track.Min.Y, toX(to), track.Max.Y)
	paint.FillShape(gtx.Ops, editAccent, clip.Rect(kept).Op())
	r := gtx.Dp(7)
	for _, x := range [...]int{kept.Min.X, kept.Max.X} {
		knob := image.Rect(x-r, size.Y/2-r, x+r, size.Y/2+r)
		paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, clip.Ellipse(knob).Op(gtx.Ops))
	}
	white := token.NewMatColorFromHexRGB(0xffffff)
	label2 := func(x int, d time.Duration) {
		offset(gtx, image.Pt(x, (size.Y-gtx.Dp(16))/2), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			return label(gtx, editTime(d), token.TypestyleLabelMedium, white, 1)
		})
	}
	label2(gtx.Dp(16), trim.From)
	label2(track.Max.X+gtx.Dp(14), to)
	return layout.Dimensions{Size: size}
}

// editTime writes a time of a video, with tenths for cutting it.
func editTime(d time.Duration) string {
	tenths := int(d / (100 * time.Millisecond))
	return fmt.Sprintf("%d:%02d.%d", tenths/600, tenths/10%60, tenths%10)
}
