// SPDX-License-Identifier: Unlicense OR MIT

package toggle

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"image"
	"slices"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// widgetState represents the state of a radio button.
type widgetState int

const (
	enabled widgetState = iota
	disabled
	hovered
	focused
	pressed
)

type widgetStyle[T comparable] struct {
	Toggle  *Toggle[T]
	tLabels map[T]string
	tTheme  *Theme
}

func (s widgetStyle[T]) layout(gtx layout.Context) layout.Dimensions {
	var segments []block.Segment

	for _, value := range s.Toggle.options {
		segments = append(segments, block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return s.layoutOption(gtx, value)
		}))
	}

	return block.Line{
		Axis:     block.AxisVertical,
		Overflow: block.OverflowClip,
	}.Layout(gtx, segments...)
}

func (s widgetStyle[T]) layoutOption(gtx layout.Context, value T) layout.Dimensions {
	return block.Line{
		Axis:     block.AxisHorizontal,
		Overflow: block.OverflowClip,
		Expand:   true,
	}.Layout(gtx,
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return s.layoutTarget(gtx, value)
		}).AlignMiddle(),
		block.NewHorizontalSpacer(unit.Dp(4)),
		block.NewFlexSegment(func(gtx layout.Context) layout.Dimensions {
			label := s.tLabels[value]
			return s.layoutLabel(gtx, label)
		}).AlignMiddle(),
	)
}

func (s widgetStyle[T]) layoutLabel(gtx layout.Context, label string) layout.Dimensions {
	p := block.UniformPadding(unit.Dp(4))
	return p.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		lStyle := wdk.LabelStyle{
			Typestyle: token.TypestyleBodyLarge,
		}
		return wdk.LayoutLabel(gtx, lStyle, label)
	})
}

func (s widgetStyle[T]) layoutTarget(gtx layout.Context, value T) layout.Dimensions {
	state := s.getButtonState(gtx, value)
	if state == focused {
		s.drawFocusIndicator(gtx)
	}
	padding := s.tTheme.FocusIndicatorOffset + s.tTheme.FocusIndicatorThickness
	return block.UniformPadding(padding).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// The graphic is drawn outside the clickable, which clips its content,
		// because the state layer extends beyond the track.
		dims := s.layoutToggleGraphic(gtx, value, state)
		if state == disabled {
			return dims
		}
		if state == hovered || state == pressed {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		return s.Toggle.clickable[value].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return dims
		})
	})
}

func (s widgetStyle[T]) drawFocusIndicator(gtx layout.Context) {
	trackHeight := gtx.Dp(s.tTheme.TrackHeight)
	trackWidth := gtx.Dp(s.tTheme.TrackWidth)
	focusIndicatorOffset := gtx.Dp(s.tTheme.FocusIndicatorOffset)

	offsetPoint := image.Pt(focusIndicatorOffset, focusIndicatorOffset).Mul(2)
	indicatorThickness := gtx.Dp(s.tTheme.FocusIndicatorThickness)
	focusIndicatorExtra := image.Point{X: indicatorThickness, Y: indicatorThickness}.Mul(2)
	focusIndicatorEnd := image.Point{X: trackWidth, Y: trackHeight}.Add(offsetPoint).Add(focusIndicatorExtra)
	baseBox := wdk.Box{
		Shape:       wdk.FromCornerShapesToken(gtx, s.tTheme.StateLayerShape),
		EndPoint:    focusIndicatorEnd,
		StrokeWidth: float32(indicatorThickness),
	}
	stateLayerColor := s.tTheme.FocusIndicatorColor
	paint.FillShape(gtx.Ops, stateLayerColor.AsNRGBA(), baseBox.Stroke(gtx))
}

func (s widgetStyle[T]) layoutToggleGraphic(gtx layout.Context, value T, state widgetState) layout.Dimensions {
	selected := slices.Contains(s.Toggle.values, value)
	anim := s.Toggle.getAnimation(value)

	trackHeight := float32(gtx.Dp(s.tTheme.TrackHeight))
	trackWidth := float32(gtx.Dp(s.tTheme.TrackWidth))

	position := float32(0)
	if selected {
		position = 1
	}
	position = anim.position.Animate(gtx, position)
	handleSize := anim.handleSize.Animate(gtx, float32(gtx.Dp(s.getHandleSize(state, selected))))
	// The handle moves between the centers of the track's rounded ends.
	handleCenter := f32.Point{
		X: trackHeight/2 + (trackWidth-trackHeight)*position,
		Y: trackHeight / 2,
	}

	s.drawTrack(gtx, anim, state, selected)
	s.drawStateLayer(gtx, value, anim, state, selected, handleCenter)
	s.drawHandle(gtx, anim, state, selected, handleCenter, handleSize)

	return layout.Dimensions{
		Size: image.Pt(int(trackWidth), int(trackHeight)),
	}
}

func (s widgetStyle[T]) getHandleSize(state widgetState, selected bool) unit.Dp {
	switch {
	case state == pressed:
		return s.tTheme.PressedHandleWidth
	case selected:
		return s.tTheme.SelectedHandleWidth
	default:
		return s.tTheme.UnselectedHandleWidth
	}
}

func (s widgetStyle[T]) drawTrack(gtx layout.Context, anim *animation, state widgetState, selected bool) {
	trackHeight := gtx.Dp(s.tTheme.TrackHeight)
	trackWidth := gtx.Dp(s.tTheme.TrackWidth)
	baseBox := wdk.Box{
		Shape:       wdk.FromCornerShapesToken(gtx, s.tTheme.TrackShape),
		EndPoint:    image.Point{X: trackWidth, Y: trackHeight},
		StrokeWidth: float32(gtx.Dp(s.tTheme.TrackOutlineWidth)),
	}
	trackColor := anim.track.Animate(gtx, s.getTrackColor(state, selected))
	paint.FillShape(gtx.Ops, trackColor.AsNRGBA(), baseBox.Outline(gtx))

	outlineColor := anim.trackOutline.Animate(gtx, s.getTrackOutlineColor(state, selected))
	if outlineColor.A > 0 {
		paint.FillShape(gtx.Ops, outlineColor.AsNRGBA(), baseBox.Stroke(gtx))
	}
}

// drawStateLayer draws the hover and press highlight centered on the handle.
func (s widgetStyle[T]) drawStateLayer(gtx layout.Context, value T, anim *animation, state widgetState, selected bool, center f32.Point) {
	clickable := s.Toggle.clickable[value]
	ripples := wdk.RipplesEnabled(gtx) && state != disabled
	layerState := state
	if ripples && state == pressed {
		// The ripple shows the press; keep the hover highlight under it.
		layerState = enabled
		if clickable.Hovered() {
			layerState = hovered
		}
	}
	size := gtx.Dp(s.tTheme.StateLayerSize)
	origin := image.Pt(int(center.X+0.5)-size/2, int(center.Y+0.5)-size/2)
	baseBox := wdk.Box{
		Shape:      wdk.FromCornerShapesToken(gtx, s.tTheme.StateLayerShape),
		StartPoint: origin,
		EndPoint:   origin.Add(image.Pt(size, size)),
	}
	layerColor := anim.stateLayer.Animate(gtx, s.getStateLayerColor(layerState, selected))
	if layerColor.A > 0 {
		paint.FillShape(gtx.Ops, layerColor.AsNRGBA(), baseBox.Outline(gtx))
	}
	if ripples {
		// The ripple follows the handle while it moves.
		wdk.Ripple{
			Color:    s.getStateLayerColor(pressed, selected),
			Bounds:   image.Rectangle{Min: baseBox.StartPoint, Max: baseBox.EndPoint},
			Clip:     func() clip.Op { return baseBox.Outline(gtx) },
			Centered: true,
		}.Draw(gtx, clickable.History())
	}
}

func (s widgetStyle[T]) getStateLayerColor(state widgetState, selected bool) token.MatColor {
	switch state {
	case hovered:
		if selected {
			return s.tTheme.HoverSelectedStateLayerColor.SetOpacity(s.tTheme.HoverSelectedStateLayerOpacity)
		}
		return s.tTheme.HoverUnselectedStateLayerColor.SetOpacity(s.tTheme.HoverUnselectedStateLayerOpacity)
	case pressed:
		if selected {
			return s.tTheme.PressedSelectedStateLayerColor.SetOpacity(s.tTheme.PressedSelectedStateLayerOpacity)
		}
		return s.tTheme.PressedUnselectedStateLayerColor.SetOpacity(s.tTheme.PressedUnselectedStateLayerOpacity)
	default:
		// Focus is shown by the focus indicator instead.
		return token.NewTransparentMatColor()
	}
}

func (s widgetStyle[T]) drawHandle(gtx layout.Context, anim *animation, state widgetState, selected bool, center f32.Point, size float32) {
	handleSize := int(size + 0.5)
	origin := image.Pt(int(center.X-size/2+0.5), int(center.Y-size/2+0.5))
	baseBox := wdk.Box{
		Shape:      wdk.FromCornerShapesToken(gtx, s.tTheme.HandleShape),
		StartPoint: origin,
		EndPoint:   origin.Add(image.Pt(handleSize, handleSize)),
	}
	handleColor := anim.handle.Animate(gtx, s.getHandleColor(state, selected))
	paint.FillShape(gtx.Ops, handleColor.AsNRGBA(), baseBox.Outline(gtx))

	// The check icon fades in and out with the selection.
	iconColor := s.getIconColor(state, selected)
	if !selected {
		iconColor = iconColor.SetOpacity(0)
	}
	iconColor = anim.icon.Animate(gtx, iconColor)
	if iconColor.A == 0 {
		return
	}
	transformStack := op.Offset(origin).Push(gtx.Ops)
	containerSize := float32(handleSize)
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Point{X: containerSize * 0.22, Y: containerSize * 0.56})
	p.LineTo(f32.Point{X: containerSize * 0.39, Y: containerSize * 0.69})
	p.LineTo(f32.Point{X: containerSize * 0.78, Y: containerSize * 0.33})
	pathSpec := p.End()
	paint.FillShape(
		gtx.Ops,
		iconColor.AsNRGBA(),
		clip.Stroke{Path: pathSpec, Width: float32(gtx.Dp(iconStrokeWidth))}.Op(),
	)
	transformStack.Pop()
}

func (s widgetStyle[T]) getTrackColor(state widgetState, selected bool) token.MatColor {
	switch state {
	case enabled:
		if selected {
			return s.tTheme.SelectedTrackColor
		} else {
			return s.tTheme.UnselectedTrackColor
		}
	case hovered:
		if selected {
			return s.tTheme.HoverSelectedTrackColor
		} else {
			return s.tTheme.HoverUnselectedTrackColor
		}
	case focused:
		if selected {
			return s.tTheme.FocusSelectedTrackColor
		} else {
			return s.tTheme.FocusUnselectedTrackColor
		}
	case pressed:
		if selected {
			return s.tTheme.PressedSelectedTrackColor
		} else {
			return s.tTheme.PressedUnselectedTrackColor
		}
	case disabled:
		if selected {
			return s.tTheme.DisabledSelectedTrackColor.SetOpacity(s.tTheme.DisabledTrackOpacity)
		} else {
			return s.tTheme.DisabledUnselectedTrackColor.SetOpacity(s.tTheme.DisabledTrackOpacity)
		}
	default:
		return token.NewTransparentMatColor()
	}
}

func (s widgetStyle[T]) getTrackOutlineColor(state widgetState, selected bool) token.MatColor {
	if selected {
		return token.NewTransparentMatColor()
	}
	switch state {
	case enabled:
		return s.tTheme.TrackOutlineColor
	case hovered:
		return s.tTheme.HoverUnselectedTrackOutlineColor
	case pressed:
		return s.tTheme.PressedUnselectedTrackOutlineColor
	case focused:
		return s.tTheme.FocusUnselectedTrackOutlineColor
	case disabled:
		return s.tTheme.DisabledUnselectedTrackOutlineColor
	default:
		return token.NewTransparentMatColor()
	}

}

func (s widgetStyle[T]) getHandleColor(state widgetState, selected bool) token.MatColor {
	switch state {
	case enabled:
		if selected {
			return s.tTheme.SelectedHandleColor
		} else {
			return s.tTheme.UnselectedHandleColor
		}
	case hovered:
		if selected {
			return s.tTheme.HoverSelectedHandleColor
		} else {
			return s.tTheme.HoverUnselectedHandleColor
		}
	case focused:
		if selected {
			return s.tTheme.FocusSelectedHandleColor
		} else {
			return s.tTheme.FocusUnselectedHandleColor
		}
	case pressed:
		if selected {
			return s.tTheme.PressedSelectedHandleColor
		} else {
			return s.tTheme.PressedUnselectedHandleColor
		}
	case disabled:
		if selected {
			return s.tTheme.DisabledSelectedHandleColor.SetOpacity(s.tTheme.DisabledSelectedHandleOpacity)
		} else {
			return s.tTheme.DisabledUnselectedHandleColor.SetOpacity(s.tTheme.DisabledUnselectedHandleOpacity)
		}
	default:
		return token.NewTransparentMatColor()
	}
}

func (s widgetStyle[T]) getIconColor(state widgetState, selected bool) token.MatColor {
	switch state {
	case enabled:
		if selected {
			return s.tTheme.SelectedIconColor
		} else {
			return s.tTheme.UnselectedIconColor
		}
	case hovered:
		if selected {
			return s.tTheme.HoverSelectedIconColor
		} else {
			return s.tTheme.HoverUnselectedIconColor
		}
	case focused:
		if selected {
			return s.tTheme.FocusSelectedIconColor
		} else {
			return s.tTheme.FocusUnselectedIconColor
		}
	case pressed:
		if selected {
			return s.tTheme.PressedSelectedIconColor
		} else {
			return s.tTheme.PressedUnselectedIconColor
		}
	case disabled:
		if selected {
			return s.tTheme.DisabledSelectedIconColor.SetOpacity(s.tTheme.DisabledSelectedIconOpacity)
		} else {
			return s.tTheme.DisabledUnselectedIconColor.SetOpacity(s.tTheme.DisabledUnselectedIconOpacity)
		}
	default:
		return token.NewTransparentMatColor()
	}
}

func (s widgetStyle[T]) getButtonState(gtx layout.Context, value T) widgetState {
	// States are sorted in the order of their priority.
	if s.Toggle.disabled[value] {
		return disabled
	} else if s.Toggle.clickable[value].Pressed() {
		return pressed
	} else if s.Toggle.clickable[value].Hovered() {
		return hovered
	} else if gtx.Focused(s.Toggle.clickable[value]) {
		return focused
	}
	return enabled
}
