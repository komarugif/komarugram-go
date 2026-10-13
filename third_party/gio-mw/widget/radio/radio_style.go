// SPDX-License-Identifier: Unlicense OR MIT

package radio

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"image"
	"image/color"

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
	*Radios[T]
	rLabels       map[T]string
	rDisplayState map[T]*widgetState
	rKind         Kind
	rTheme        *Theme
}

func (w *widgetStyle[T]) layout(gtx layout.Context) layout.Dimensions {
	var radioButtons []block.Segment
	for _, value := range w.Radios.options {
		radioButtons = append(radioButtons, block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			switch w.rKind {
			case ButtonKind:
				return w.layoutClickable(gtx, value, func(gtx layout.Context) layout.Dimensions {
					return w.layoutButton(gtx, value)
				})
			case LeadingKind:
				// The whole row (icon and label) is clickable, not just the icon.
				return w.layoutClickable(gtx, value, func(gtx layout.Context) layout.Dimensions {
					return block.Line{
						Axis:     block.AxisHorizontal,
						Overflow: block.OverflowClip,
					}.Layout(gtx,
						block.NewSegment(func(gtx layout.Context) layout.Dimensions {
							return w.layoutButton(gtx, value)
						}).AlignMiddle(),
						block.NewFlexSegment(func(gtx layout.Context) layout.Dimensions {
							label := w.rLabels[value]
							return w.layoutLabel(gtx, label)
						}).AlignMiddle(),
					)
				})
			case TrailingKind:
				// The whole row (label and icon) is clickable, not just the icon.
				return w.layoutClickable(gtx, value, func(gtx layout.Context) layout.Dimensions {
					return block.Line{
						Axis:     block.AxisHorizontal,
						Overflow: block.OverflowClip,
					}.Layout(gtx,
						block.NewFlexSegment(func(gtx layout.Context) layout.Dimensions {
							label := w.rLabels[value]
							return w.layoutLabel(gtx, label)
						}).AlignMiddle(),
						block.NewSegment(func(gtx layout.Context) layout.Dimensions {
							return w.layoutButton(gtx, value)
						}).AlignMiddle(),
					)
				})
			default:
				panic("radio.Radios: invalid radio.Kind value")
			}
		}))
	}
	return block.Line{
		Axis:     block.AxisVertical,
		Overflow: block.OverflowClip,
	}.Layout(gtx, radioButtons...)
}

func (w *widgetStyle[T]) layoutLabel(gtx layout.Context, label string) layout.Dimensions {
	p := block.UniformPadding(unit.Dp(4))
	return p.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		lStyle := wdk.LabelStyle{
			Typestyle: token.TypestyleBodyLarge,
		}
		return wdk.LayoutLabel(gtx, lStyle, label)
	})
}

// layoutClickable makes the area of content clickable for the radio button of value.
// Disabled radio buttons receive no input.
func (w *widgetStyle[T]) layoutClickable(gtx layout.Context, value T, content layout.Widget) layout.Dimensions {
	if w.Radios.disabled[value] {
		return content(gtx)
	}
	state := w.getButtonState(gtx, value)
	if state == hovered || state == pressed {
		pointer.CursorPointer.Add(gtx.Ops)
	}
	return w.Radios.clickable[value].Layout(gtx, content)
}

// layoutButton draws the radio button icon. Input is handled by layoutClickable.
func (w *widgetStyle[T]) layoutButton(gtx layout.Context, value T) layout.Dimensions {
	state := w.getButtonState(gtx, value)
	p := block.UniformPadding(unit.Dp(4))
	return p.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return w.layoutButtonContents(gtx, value, state)
	})
}

func (w *widgetStyle[T]) layoutButtonContents(gtx layout.Context, value T, state widgetState) layout.Dimensions {
	selected := w.Radios.value == value
	anim := w.Radios.getAnimation(value)
	baseColor := anim.icon.Animate(gtx, token.MatColor(w.getButtonIconColor(state, selected))).AsNRGBA()
	dotTarget := float32(0)
	if selected {
		dotTarget = 1
	}
	dotScale := anim.dot.Animate(gtx, dotTarget)

	stateLayerSize := gtx.Dp(w.rTheme.EnabledStateLayerSize)
	iconSize := gtx.Dp(w.rTheme.EnabledIconSize)
	iconOffset := (stateLayerSize - iconSize) / 2
	iconStrokeSize := gtx.Dp(w.rTheme.EnabledIconSize)
	iconStrokeWidth := gtx.Dp(outlineStrokeWidth)
	focusOffset := gtx.Dp(6)
	focusStrokeSize := iconStrokeSize + iconStrokeWidth*4
	focusStrokeWidth := gtx.Dp(focusOutlineStrokeWidth)

	w.drawButtonStateLayer(gtx, value, anim, state, selected)

	if state == focused {
		transformStack := op.Offset(image.Pt(focusOffset, focusOffset)).Push(gtx.Ops)
		outlinePathSpec := clip.Ellipse{
			Max: image.Point{X: focusStrokeSize, Y: focusStrokeSize},
		}.Path(gtx.Ops)

		focusColor := w.rTheme.FocusedIconUnselectedColor
		if selected {
			focusColor = w.rTheme.FocusedIconSelectedColor
		}
		paint.FillShape(
			gtx.Ops,
			focusColor.AsNRGBA(),
			clip.Stroke{Path: outlinePathSpec, Width: float32(focusStrokeWidth)}.Op(),
		)
		transformStack.Pop()
	}
	transformStack := op.Offset(image.Pt(iconOffset, iconOffset)).Push(gtx.Ops)
	{
		outlinePathSpec := clip.Ellipse{Max: image.Point{X: iconStrokeSize, Y: iconStrokeSize}}.Path(gtx.Ops)

		paint.FillShape(
			gtx.Ops,
			baseColor,
			clip.Stroke{Path: outlinePathSpec, Width: float32(iconStrokeWidth)}.Op(),
		)
	}
	// The selected dot grows from and shrinks to the center.
	if dotScale > 0 {
		fullSize := float32(iconStrokeSize - iconStrokeWidth*4)
		size := fullSize * dotScale
		center := float32(iconStrokeSize) / 2
		circlePathSpec := clip.Ellipse{
			Min: image.Pt(int(center-size/2+0.5), int(center-size/2+0.5)),
			Max: image.Pt(int(center+size/2+0.5), int(center+size/2+0.5)),
		}.Path(gtx.Ops)

		paint.FillShape(
			gtx.Ops,
			baseColor,
			clip.Outline{Path: circlePathSpec}.Op(),
		)
	}
	transformStack.Pop()

	return layout.Dimensions{
		Size: image.Pt(stateLayerSize, stateLayerSize),
	}
}

func (w *widgetStyle[T]) getButtonIconColor(state widgetState, selected bool) color.NRGBA {
	switch state {
	case enabled:
		if selected {
			return w.rTheme.EnabledIconSelectedColor.AsNRGBA()
		} else {
			return w.rTheme.EnabledIconUnselectedColor.AsNRGBA()
		}
	case hovered:
		if selected {
			return w.rTheme.HoveredIconSelectedColor.AsNRGBA()
		} else {
			return w.rTheme.HoveredIconUnselectedColor.AsNRGBA()
		}
	case focused:
		if selected {
			return w.rTheme.FocusedIconSelectedColor.AsNRGBA()
		} else {
			return w.rTheme.FocusedIconUnselectedColor.AsNRGBA()
		}
	case pressed:
		if selected {
			return w.rTheme.PressedIconSelectedColor.AsNRGBA()
		} else {
			return w.rTheme.PressedIconUnselectedColor.AsNRGBA()
		}
	case disabled:
		if selected {
			return w.rTheme.DisabledIconSelectedColor.SetOpacity(w.rTheme.DisabledIconSelectedOpacity).AsNRGBA()
		} else {
			return w.rTheme.DisabledIconUnselectedColor.SetOpacity(w.rTheme.DisabledIconUnselectedOpacity).AsNRGBA()
		}
	}
	return w.rTheme.EnabledIconUnselectedColor.AsNRGBA()
}

func (w *widgetStyle[T]) drawButtonStateLayer(gtx layout.Context, value T, anim *animation, state widgetState, selected bool) {
	clickable := w.Radios.clickable[value]
	ripples := wdk.RipplesEnabled(gtx) && state != disabled
	layerState := state
	if ripples && state == pressed {
		// The ripple shows the press; keep the hover highlight under it.
		layerState = enabled
		if clickable.Hovered() {
			layerState = hovered
		}
	}
	stateLayerSize := gtx.Dp(w.rTheme.EnabledStateLayerSize)
	baseBox := wdk.Box{
		Shape:    wdk.FromCornerShapesToken(gtx, w.rTheme.EnabledStateLayerShape),
		EndPoint: image.Pt(stateLayerSize, stateLayerSize),
	}
	layerColor := anim.stateLayer.Animate(gtx, w.getStateLayerColor(layerState, selected))
	if layerColor.A > 0 {
		paint.FillShape(gtx.Ops, layerColor.AsNRGBA(), baseBox.Outline(gtx))
	}
	if ripples {
		wdk.Ripple{
			Color:    w.getStateLayerColor(pressed, selected),
			Bounds:   image.Rectangle{Max: baseBox.EndPoint},
			Clip:     func() clip.Op { return baseBox.Outline(gtx) },
			Centered: true,
		}.Draw(gtx, clickable.History())
	}
}

func (w *widgetStyle[T]) getStateLayerColor(state widgetState, selected bool) token.MatColor {
	switch state {
	case hovered:
		if selected {
			return w.rTheme.HoveredStateLayerSelectedColor.SetOpacity(w.rTheme.HoveredStateLayerSelectedOpacity)
		}
		return w.rTheme.HoveredStateLayerUnselectedColor.SetOpacity(w.rTheme.HoveredStateLayerUnselectedOpacity)
	case pressed:
		if selected {
			return w.rTheme.PressedStateLayerSelectedColor.SetOpacity(w.rTheme.PressedStateLayerSelectedOpacity)
		}
		return w.rTheme.PressedStateLayerUnselectedColor.SetOpacity(w.rTheme.PressedStateLayerUnselectedOpacity)
	default:
		// Focus is shown by the focus ring instead.
		return token.NewTransparentMatColor()
	}
}

func (w *widgetStyle[T]) getButtonState(gtx layout.Context, value T) widgetState {
	// States are sorted in the order of their priority.
	if w.Radios.disabled[value] {
		return disabled
	} else if w.Radios.clickable[value].Pressed() {
		return pressed
	} else if w.Radios.clickable[value].Hovered() {
		return hovered
	} else if gtx.Focused(w.Radios.clickable[value]) {
		return focused
	}
	return enabled
}
