// SPDX-License-Identifier: Unlicense OR MIT

package checkbox

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"image"
	"image/color"
	"math"
	"slices"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// widgetState represents the state of a checkbox.
type widgetState int

const (
	enabled widgetState = iota
	disabled
	hovered
	focused
	pressed
)

type widgetStyle[T comparable] struct {
	*Checkboxes[T]
	cLabels       map[T]string
	cLabel        string
	cDisplayState map[T]*widgetState
	cKind         Kind
	cTheme        *Theme
}

func (s *widgetStyle[T]) layoutWithParent(gtx layout.Context) layout.Dimensions {
	return block.Line{
		Axis:     block.AxisVertical,
		Overflow: block.OverflowClip,
	}.Layout(gtx,
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			cSelected := false
			cIndeterminate := false
			if len(s.Checkboxes.values) == len(s.Checkboxes.options) {
				cSelected = true
			} else if len(s.Checkboxes.values) > 0 {
				cIndeterminate = true
				cSelected = true
			}
			cError := false
			for _, checkboxError := range s.error {
				if checkboxError {
					cError = true
					break
				}
			}
			return checkboxStyle{
				cAnimation:     s.Checkboxes.getParentAnimation(),
				cClickable:     s.Checkboxes.parent,
				cError:         cError,
				cIndeterminate: cIndeterminate,
				cKind:          s.cKind,
				cLabel:         s.cLabel,
				cSelected:      cSelected,
				cState:         s.getParentCheckboxState(gtx),
				cTheme:         s.cTheme,
			}.layout(gtx)
		}),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return block.Padding{
				Start: unit.Dp(28),
			}.Layout(gtx, s.layoutWithKind)
		}),
	)
}

func (s *widgetStyle[T]) layoutWithKind(gtx layout.Context) layout.Dimensions {
	var checkboxElements []block.Segment
	for _, value := range s.Checkboxes.options {
		checkboxElements = append(checkboxElements, block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return checkboxStyle{
				cAnimation:     s.Checkboxes.getAnimation(value),
				cClickable:     s.Checkboxes.clickable[value],
				cError:         s.getCheckboxError(value),
				cIndeterminate: false,
				cKind:          s.cKind,
				cLabel:         s.cLabels[value],
				cSelected:      slices.Contains(s.Checkboxes.values, value),
				cState:         s.getCheckboxState(gtx, value),
				cTheme:         s.cTheme,
			}.layout(gtx)
		}))
	}
	return block.Line{
		Axis:     block.AxisVertical,
		Overflow: block.OverflowClip,
	}.Layout(gtx, checkboxElements...)
}

func (s *widgetStyle[T]) getCheckboxError(value T) bool {
	for errValue, state := range s.error {
		if errValue == value {
			return state
		}
	}
	panic("checkbox.Checkboxes: invalid error value")
}

func (s *widgetStyle[T]) getParentCheckboxState(gtx layout.Context) widgetState {
	cDisabled := false
	for _, checkboxDisabled := range s.disabled {
		if checkboxDisabled {
			cDisabled = true
			break
		}
	}
	// States are sorted in the order of their priority.
	if cDisabled {
		return disabled
	} else if s.parent.Pressed() {
		return pressed
	} else if s.parent.Hovered() {
		return hovered
	} else if gtx.Focused(s.parent) {
		return focused
	}
	return enabled
}

func (s *widgetStyle[T]) getCheckboxState(gtx layout.Context, value T) widgetState {
	// States are sorted in the order of their priority.
	if s.Checkboxes.disabled[value] {
		return disabled
	} else if s.Checkboxes.clickable[value].Pressed() {
		return pressed
	} else if s.Checkboxes.clickable[value].Hovered() {
		return hovered
	} else if gtx.Focused(s.Checkboxes.clickable[value]) {
		return focused
	}
	return enabled
}

type checkboxStyle struct {
	cAnimation     *animation
	cClickable     *widget.Clickable
	cError         bool
	cIndeterminate bool
	cKind          Kind
	cLabel         string
	cSelected      bool
	cState         widgetState
	cTheme         *Theme
}

func (s checkboxStyle) layout(gtx layout.Context) layout.Dimensions {
	switch s.cKind {
	case ButtonKind:
		return s.layoutClickable(gtx, s.layoutTarget)
	case LeadingKind:
		// The whole row (checkbox and label) is clickable, not just the checkbox.
		return s.layoutClickable(gtx, func(gtx layout.Context) layout.Dimensions {
			return block.Line{
				Axis:     block.AxisHorizontal,
				Overflow: block.OverflowClip,
			}.Layout(gtx,
				block.NewSegment(func(gtx layout.Context) layout.Dimensions {
					return s.layoutTarget(gtx)
				}).AlignMiddle(),
				block.NewFlexSegment(func(gtx layout.Context) layout.Dimensions {
					return s.layoutLabel(gtx, s.cLabel)
				}).AlignMiddle(),
			)
		})
	case TrailingKind:
		// The whole row (label and checkbox) is clickable, not just the checkbox.
		return s.layoutClickable(gtx, func(gtx layout.Context) layout.Dimensions {
			return block.Line{
				Axis:     block.AxisHorizontal,
				Overflow: block.OverflowClip,
			}.Layout(gtx,
				block.NewFlexSegment(func(gtx layout.Context) layout.Dimensions {
					return s.layoutLabel(gtx, s.cLabel)
				}).AlignMiddle(),
				block.NewSegment(func(gtx layout.Context) layout.Dimensions {
					return s.layoutTarget(gtx)
				}).AlignMiddle(),
			)
		})
	default:
		panic("checkbox.Checkboxes: invalid checkbox.Kind value")
	}
}

// layoutClickable makes the area of content clickable. Disabled checkboxes
// receive no input.
func (s checkboxStyle) layoutClickable(gtx layout.Context, content layout.Widget) layout.Dimensions {
	if s.cState == disabled {
		return content(gtx)
	}
	if s.cState == hovered || s.cState == pressed {
		pointer.CursorPointer.Add(gtx.Ops)
	}
	return s.cClickable.Layout(gtx, content)
}

func (s checkboxStyle) layoutLabel(gtx layout.Context, label string) layout.Dimensions {
	p := block.UniformPadding(unit.Dp(4))
	return p.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		lStyle := wdk.LabelStyle{
			Typestyle: token.TypestyleBodyLarge,
		}
		return wdk.LayoutLabel(gtx, lStyle, label)
	})
}

func (s checkboxStyle) layoutTarget(gtx layout.Context) layout.Dimensions {
	if s.cState == focused {
		s.drawFocusIndicator(gtx)
	}
	p := block.UniformPadding(unit.Dp(4))
	return p.Layout(gtx, s.layoutCheckboxGraphic)
}

func (s checkboxStyle) drawFocusIndicator(gtx layout.Context) {
	// Build the state layer shape.
	stateLayerSize := gtx.Dp(s.cTheme.EnabledStateLayerSize)
	indicatorOffset := gtx.Dp(s.cTheme.FocusedFocusIndicatorOffset)
	indicatorThickness := gtx.Dp(s.cTheme.FocusedFocusIndicatorThickness)
	indicatorSize := stateLayerSize + indicatorOffset*2

	focusIndicatorBox := wdk.Box{
		Shape: wdk.UniformCornerShapes(wdk.CornerShape{
			Kind:        wdk.CornerKindRound,
			AdaptToSize: true,
		}),
		EndPoint:    image.Point{X: indicatorSize, Y: indicatorSize},
		StrokeWidth: float32(indicatorThickness),
	}

	// Draw the state layer.
	transformStack := op.Offset(image.Pt(indicatorOffset, indicatorOffset)).Push(gtx.Ops)
	indicatorColor := s.cTheme.FocusedFocusIndicatorColor.AsNRGBA()
	paint.FillShape(
		gtx.Ops,
		indicatorColor,
		focusIndicatorBox.Stroke(gtx),
	)
	transformStack.Pop()
}

func (s checkboxStyle) layoutCheckboxGraphic(gtx layout.Context) layout.Dimensions {
	// Draw state layer.
	s.drawStateLayer(gtx)

	stateLayerSize := gtx.Dp(s.cTheme.EnabledStateLayerSize)
	iconSize := gtx.Dp(s.cTheme.EnabledIconSize)
	iconOffset := int(stateLayerSize-iconSize) / 2
	transformStack := op.Offset(image.Pt(iconOffset, iconOffset)).Push(gtx.Ops)
	// The outline fades out while the container fades in and the check mark
	// is drawn, and the other way around.
	s.drawOutline(gtx)
	s.drawContainer(gtx)
	s.drawIcon(gtx)
	transformStack.Pop()

	return layout.Dimensions{
		Size: image.Point{X: stateLayerSize, Y: stateLayerSize},
	}
}

func (s checkboxStyle) drawStateLayer(gtx layout.Context) {
	ripples := wdk.RipplesEnabled(gtx) && s.cState != disabled
	layerStyle := s
	if ripples && s.cState == pressed {
		// The ripple shows the press; keep the hover highlight under it.
		layerStyle.cState = enabled
		if s.cClickable.Hovered() {
			layerStyle.cState = hovered
		}
	}

	// Build the state layer shape.
	stateLayerSize := gtx.Dp(s.cTheme.EnabledStateLayerSize)
	stateLayerBox := wdk.Box{
		Shape: wdk.UniformCornerShapes(wdk.CornerShape{
			Kind:        wdk.CornerKindRound,
			AdaptToSize: true,
		}),
		EndPoint: image.Point{X: stateLayerSize, Y: stateLayerSize},
	}

	// Draw the state layer.
	stateLayerColor := s.cAnimation.stateLayer.Animate(gtx, token.MatColor(layerStyle.getStateLayerColor())).AsNRGBA()
	if stateLayerColor.A > 0 {
		paint.FillShape(
			gtx.Ops,
			stateLayerColor,
			stateLayerBox.Outline(gtx),
		)
	}
	if ripples {
		pressedStyle := s
		pressedStyle.cState = pressed
		wdk.Ripple{
			Color:    token.MatColor(pressedStyle.getStateLayerColor()),
			Bounds:   image.Rectangle{Max: stateLayerBox.EndPoint},
			Clip:     func() clip.Op { return stateLayerBox.Outline(gtx) },
			Centered: true,
		}.Draw(gtx, s.cClickable.History())
	}
}

func (s checkboxStyle) getStateLayerColor() color.NRGBA {
	switch s.cState {
	case hovered:
		if s.cError {
			return s.cTheme.HoveredStateLayerErrorColor.SetOpacity(s.cTheme.HoveredStateLayerErrorOpacity).AsNRGBA()
		}
		if s.cSelected {
			return s.cTheme.HoveredStateLayerSelectedColor.SetOpacity(s.cTheme.HoveredStateLayerSelectedOpacity).AsNRGBA()
		} else {
			return s.cTheme.HoveredStateLayerUnselectedColor.SetOpacity(s.cTheme.HoveredStateLayerUnselectedOpacity).AsNRGBA()
		}
	case focused:
		if s.cError {
			return s.cTheme.FocusedStateLayerErrorColor.SetOpacity(s.cTheme.FocusedStateLayerErrorOpacity).AsNRGBA()
		}
		if s.cSelected {
			return s.cTheme.FocusedStateLayerSelectedColor.SetOpacity(s.cTheme.FocusedStateLayerSelectedOpacity).AsNRGBA()
		} else {
			return s.cTheme.FocusedStateLayerUnselectedColor.SetOpacity(s.cTheme.FocusedStateLayerUnselectedOpacity).AsNRGBA()
		}
	case pressed:
		if s.cError {
			return s.cTheme.PressedStateLayerErrorColor.SetOpacity(s.cTheme.PressedStateLayerErrorOpacity).AsNRGBA()
		}
		if s.cSelected {
			return s.cTheme.PressedStateLayerSelectedColor.SetOpacity(s.cTheme.PressedStateLayerSelectedOpacity).AsNRGBA()
		} else {
			return s.cTheme.PressedStateLayerUnselectedColor.SetOpacity(s.cTheme.PressedStateLayerUnselectedOpacity).AsNRGBA()
		}
	default:
		return token.NewTransparentMatColor().AsNRGBA()
	}
}

func (s checkboxStyle) drawContainer(gtx layout.Context) {
	target := token.MatColor(s.getContainerColor())
	if !s.cSelected {
		target = target.SetOpacity(0)
	}
	containerColor := s.cAnimation.container.Animate(gtx, target).AsNRGBA()
	if containerColor.A == 0 {
		return
	}

	containerSize := gtx.Dp(s.cTheme.EnabledContainerSize)
	baseBox := wdk.Box{
		Shape:    wdk.FromCornerShapesToken(gtx, s.cTheme.EnabledContainerShape),
		EndPoint: image.Point{X: containerSize, Y: containerSize},
	}

	paint.FillShape(
		gtx.Ops,
		containerColor,
		baseBox.Outline(gtx),
	)
}

func (s checkboxStyle) getContainerColor() color.NRGBA {
	switch s.cState {
	case enabled:
		if s.cError {
			return s.cTheme.EnabledContainerSelectedErrorColor.AsNRGBA()
		} else {
			return s.cTheme.EnabledContainerSelectedColor.AsNRGBA()
		}
	case disabled:
		return s.cTheme.DisabledContainerSelectedColor.SetOpacity(s.cTheme.DisabledContainerSelectedOpacity).AsNRGBA()
	case hovered:
		if s.cError {
			return s.cTheme.HoveredContainerSelectedErrorColor.AsNRGBA()
		} else {
			return s.cTheme.HoveredContainerSelectedColor.AsNRGBA()
		}
	case focused:
		if s.cError {
			return s.cTheme.FocusedContainerSelectedErrorColor.AsNRGBA()
		} else {
			return s.cTheme.FocusedContainerSelectedColor.AsNRGBA()
		}
	case pressed:
		if s.cError {
			return s.cTheme.PressedContainerSelectedErrorColor.AsNRGBA()
		} else {
			return s.cTheme.PressedContainerSelectedColor.AsNRGBA()
		}
	default:
		return token.NewTransparentMatColor().AsNRGBA()
	}
}

func (s checkboxStyle) drawOutline(gtx layout.Context) {
	target := token.MatColor(s.getOutlineColor())
	if s.cSelected {
		target = target.SetOpacity(0)
	}
	outlineColor := s.cAnimation.outline.Animate(gtx, target).AsNRGBA()
	if outlineColor.A == 0 {
		return
	}
	outlineWidth := gtx.Dp(s.cTheme.EnabledOutlineUnselectedWidth)

	containerSize := gtx.Dp(s.cTheme.EnabledContainerSize)
	baseBox := wdk.Box{
		Shape:       wdk.FromCornerShapesToken(gtx, s.cTheme.EnabledContainerShape),
		EndPoint:    image.Point{X: containerSize, Y: containerSize},
		StrokeWidth: float32(outlineWidth),
	}

	paint.FillShape(
		gtx.Ops,
		outlineColor,
		baseBox.Stroke(gtx),
	)
}

func (s checkboxStyle) getOutlineColor() color.NRGBA {
	switch s.cState {
	case enabled:
		if s.cError {
			return s.cTheme.EnabledOutlineUnselectedErrorColor.AsNRGBA()
		} else {
			return s.cTheme.EnabledOutlineUnselectedColor.AsNRGBA()
		}
	case disabled:
		return s.cTheme.DisabledOutlineUnselectedColor.SetOpacity(s.cTheme.DisabledOutlineUnselectedOpacity).AsNRGBA()
	case hovered:
		if s.cError {
			return s.cTheme.HoveredOutlineUnselectedErrorColor.AsNRGBA()
		} else {
			return s.cTheme.HoveredOutlineUnselectedColor.AsNRGBA()
		}
	case focused:
		if s.cError {
			return s.cTheme.FocusedOutlineUnselectedErrorColor.AsNRGBA()
		} else {
			return s.cTheme.FocusedOutlineUnselectedColor.AsNRGBA()
		}
	case pressed:
		if s.cError {
			return s.cTheme.PressedOutlineUnselectedErrorColor.AsNRGBA()
		} else {
			return s.cTheme.PressedOutlineUnselectedColor.AsNRGBA()
		}
	default:
		return token.NewTransparentMatColor().AsNRGBA()
	}
}

func (s checkboxStyle) drawIcon(gtx layout.Context) {
	target := float32(0)
	if s.cSelected {
		target = 1
	}
	progress := s.cAnimation.check.Animate(gtx, target)
	iconColor := s.cAnimation.icon.Animate(gtx, token.MatColor(s.getIconColor())).AsNRGBA()
	if progress <= 0 {
		return
	}

	// Build the checkbox icon PathSpec, drawn up to progress of its length.
	containerSize := float32(gtx.Dp(s.cTheme.EnabledIconSize))
	// The dash has a middle point too, so that the check mark can morph
	// into it and back.
	dashTarget := float32(0)
	if s.cIndeterminate {
		dashTarget = 1
	}
	dash := s.cAnimation.dash.Animate(gtx, dashTarget)
	checkPoints := [3]f32.Point{{X: 0.22, Y: 0.56}, {X: 0.39, Y: 0.69}, {X: 0.78, Y: 0.33}}
	dashPoints := [3]f32.Point{{X: 0.22, Y: 0.5}, {X: 0.5, Y: 0.5}, {X: 0.78, Y: 0.5}}
	points := make([]f32.Point, len(checkPoints))
	for idx := range points {
		pt := checkPoints[idx].Add(dashPoints[idx].Sub(checkPoints[idx]).Mul(dash))
		points[idx] = pt.Mul(containerSize)
	}
	var p clip.Path
	p.Begin(gtx.Ops)
	tracePolyline(&p, points, progress)
	pathSpec := p.End()

	// Draw the checkbox icon.
	paint.FillShape(
		gtx.Ops,
		iconColor,
		clip.Stroke{Path: pathSpec, Width: float32(gtx.Dp(iconStrokeWidth))}.Op(),
	)
}

// tracePolyline adds the first fraction of the polyline through points to p.
func tracePolyline(p *clip.Path, points []f32.Point, fraction float32) {
	total := float32(0)
	for i := 1; i < len(points); i++ {
		total += distance(points[i-1], points[i])
	}
	remaining := total * min(fraction, 1)
	p.MoveTo(points[0])
	for i := 1; i < len(points) && remaining > 0; i++ {
		segment := distance(points[i-1], points[i])
		if segment <= remaining {
			p.LineTo(points[i])
			remaining -= segment
			continue
		}
		t := remaining / segment
		p.LineTo(points[i-1].Add(points[i].Sub(points[i-1]).Mul(t)))
		break
	}
}

func distance(a, b f32.Point) float32 {
	d := b.Sub(a)
	return float32(math.Hypot(float64(d.X), float64(d.Y)))
}

func (s checkboxStyle) getIconColor() color.NRGBA {
	switch s.cState {
	case enabled:
		if s.cError {
			if s.cSelected {
				return s.cTheme.EnabledIconSelectedErrorColor.AsNRGBA()
			} else {
				return s.cTheme.EnabledIconUnselectedErrorColor.AsNRGBA()
			}
		}
		if s.cSelected {
			return s.cTheme.EnabledIconSelectedColor.AsNRGBA()
		} else {
			return s.cTheme.EnabledIconUnselectedColor.AsNRGBA()
		}
	case hovered:
		if s.cError {
			return s.cTheme.HoveredIconErrorColor.AsNRGBA()
		}
		if s.cSelected {
			return s.cTheme.HoveredIconSelectedColor.AsNRGBA()
		} else {
			return s.cTheme.HoveredIconUnselectedColor.AsNRGBA()
		}
	case focused:
		if s.cError {
			return s.cTheme.FocusedIconErrorColor.AsNRGBA()
		}
		if s.cSelected {
			return s.cTheme.FocusedIconSelectedColor.AsNRGBA()
		} else {
			return s.cTheme.FocusedIconUnselectedColor.AsNRGBA()
		}
	case pressed:
		if s.cError {
			return s.cTheme.PressedIconErrorColor.AsNRGBA()
		}
		if s.cSelected {
			return s.cTheme.PressedIconSelectedColor.AsNRGBA()
		} else {
			return s.cTheme.PressedIconUnselectedColor.AsNRGBA()
		}
	case disabled:
		if s.cSelected {
			return s.cTheme.DisabledIconSelectedColor.SetOpacity(s.cTheme.DisabledIconSelectedOpacity).AsNRGBA()
		} else {
			return s.cTheme.DisabledIconUnselectedColor.SetOpacity(s.cTheme.DisabledIconUnselectedOpacity).AsNRGBA()
		}
	}
	return s.cTheme.EnabledIconSelectedColor.AsNRGBA()
}
