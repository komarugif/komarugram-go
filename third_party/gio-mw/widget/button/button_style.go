// SPDX-License-Identifier: Unlicense OR MIT

package button

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"image"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
)

type kind int

const (
	textKind kind = iota
	elevatedKind
	filledKind
	filledTonalKind
	outlinedKind
)

type State int

const (
	Enabled State = iota
	Disabled
	Hovered
	Focused
	Pressed
	Dragged
)

type widgetStyle struct {
	bAnimation              *animation
	bClickable              *widget.Clickable
	bDrawIcon               bool
	bDrawText               bool
	bFill                   bool
	bIcon                   wdk.IconWidget
	bKind                   kind
	bLabel                  string
	bState                  State
	bTheme                  *Theme
	bAlternativeColorScheme *AlternativeColorScheme
}

func (s *widgetStyle) layout(gtx layout.Context) layout.Dimensions {
	// Draw contents and record macro and dimensions.
	widgetMacroOp := op.Record(gtx.Ops)
	widgetDimensions := s.layoutTextAndIcon(gtx)
	widgetCallOp := widgetMacroOp.Stop()

	outlineStrokeWidth := unit.Dp(0)
	if s.bKind == outlinedKind {
		outlineStrokeWidth = s.bTheme.EnabledOutlineWidth
	}
	baseBox := wdk.Box{
		Shape:       s.getContainerShape(gtx, widgetDimensions.Size, float32(gtx.Dp(outlineStrokeWidth))),
		EndPoint:    widgetDimensions.Size,
		StrokeWidth: float32(gtx.Dp(outlineStrokeWidth)),
	}

	// Draw the button container and outline.
	s.drawContainerAndOutline(gtx, baseBox)

	// Draw the button state layer.
	s.drawStateLayer(gtx, baseBox)

	if s.bState == Disabled {
		widgetCallOp.Add(gtx.Ops)
		return widgetDimensions
	}
	return s.bClickable.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		widgetCallOp.Add(gtx.Ops)
		return widgetDimensions
	})
}

// getContainerShape morphs the container between its enabled and pressed
// shapes.
func (s *widgetStyle) getContainerShape(gtx layout.Context, size image.Point, strokeWidth float32) wdk.CornerShapes {
	target := float32(0)
	if s.bState == Pressed {
		target = 1
	}
	progress := s.bAnimation.shape.Animate(gtx, target)
	switch progress {
	case 0:
		return wdk.FromCornerShapesToken(gtx, s.bTheme.EnabledContainerShape)
	case 1:
		return wdk.FromCornerShapesToken(gtx, s.bTheme.PressedContainerShape)
	}
	enabled := wdk.FromCornerShapesToken(gtx, s.bTheme.EnabledContainerShape).Resolve(size, strokeWidth)
	pressed := wdk.FromCornerShapesToken(gtx, s.bTheme.PressedContainerShape).Resolve(size, strokeWidth)
	return wdk.LerpCornerShapes(enabled, pressed, progress)
}

func (s *widgetStyle) getContainerPadding() block.Padding {
	p := block.Padding{
		Start: paddingStart,
		End:   paddingEnd,
	}
	if s.bDrawIcon {
		if s.bDrawText {
			if s.bKind == textKind {
				p.Start = kindTextPaddingStartWithIcon
			} else {
				p.Start = paddingStartWithIcon
			}
		} else {
			p.Start = iconOnlyPadding
			p.End = iconOnlyPadding
		}
	} else {
		if s.bKind == textKind {
			p.Start = kindTextPaddingStart
			p.End = kindTextPaddingEnd
		}
	}
	return p
}

func (s *widgetStyle) layoutTextAndIcon(gtx layout.Context) layout.Dimensions {
	if gtx.Constraints.Max.X < gtx.Dp(defaultMinWidth) {
		return layout.Dimensions{}
	}
	// Ignore existing height enforcement.
	gtx.Constraints.Min.Y = 0
	minWidth := gtx.Constraints.Min.X
	if s.bDrawText {
		minWidth = min(minWidth, gtx.Dp(defaultMinWidth))
	}
	c := block.Container{
		MinSize: image.Point{
			X: minWidth,
			Y: gtx.Dp(s.bTheme.EnabledContainerHeight),
		},
		Gravity: block.GravityMiddleCenter,
	}
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		p := s.getContainerPadding()
		return p.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return block.Line{
				Axis:     block.AxisHorizontal,
				Overflow: block.OverflowClip,
			}.Layout(gtx,
				block.NewSegment(s.layoutIcon).AlignMiddle(),
				block.NewSegment(s.layoutLabel).AlignMiddle(),
			)
		})
	})
}

func (s *widgetStyle) layoutIcon(gtx layout.Context) layout.Dimensions {
	if !s.bDrawIcon {
		return layout.Dimensions{}
	}
	iconColor := s.getIconColor()
	if iconColor.A == 0 {
		return layout.Dimensions{}
	}
	iconColor = s.bAnimation.icon.Animate(gtx, iconColor)
	p := block.Padding{}
	if s.bDrawText {
		p.End = paddingEndOfIcon
	}
	return p.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return s.bIcon(gtx, iconColor)
	})
}

func (s *widgetStyle) getIconColor() token.MatColor {
	switch s.bState {
	case Enabled:
		if s.bAlternativeColorScheme != nil {
			return s.bAlternativeColorScheme.EnabledIconColor
		}
		return s.bTheme.EnabledIconColor
	case Disabled:
		return s.bTheme.DisabledIconColor.SetOpacity(s.bTheme.DisabledIconOpacity)
	case Hovered:
		return s.bTheme.HoveredIconColor
	case Focused:
		return s.bTheme.FocusedIconColor
	case Pressed:
		return s.bTheme.PressedIconColor
	case Dragged:
		return s.bTheme.DraggedIconColor
	default:
		return token.NewTransparentMatColor()
	}
}

func (s *widgetStyle) layoutLabel(gtx layout.Context) layout.Dimensions {
	if !s.bDrawText {
		return layout.Dimensions{}
	}
	textColor := s.getLabelColor()
	if textColor.A == 0 {
		return layout.Dimensions{}
	}
	textColor = s.bAnimation.label.Animate(gtx, textColor)
	presentation := wdk.LabelStyle{
		Alignment: text.Middle,
		Color:     textColor,
		MaxLines:  1,
		Typestyle: token.TypestyleLabelMediumEmphasized,
	}
	return wdk.LayoutLabel(gtx, presentation, s.bLabel)
}

func (s *widgetStyle) getLabelColor() token.MatColor {
	switch s.bState {
	case Enabled:
		if s.bAlternativeColorScheme != nil {
			return s.bAlternativeColorScheme.EnabledLabelColor
		}
		return s.bTheme.EnabledLabelColor
	case Disabled:
		return s.bTheme.DisabledLabelColor.SetOpacity(s.bTheme.DisabledLabelOpacity)
	case Hovered:
		return s.bTheme.HoveredLabelColor
	case Focused:
		return s.bTheme.FocusedLabelColor
	case Pressed:
		return s.bTheme.PressedLabelColor
	case Dragged:
		return s.bTheme.DraggedLabelColor
	default:
		return token.NewTransparentMatColor()
	}
}

func (s *widgetStyle) drawContainerAndOutline(gtx layout.Context, baseBox wdk.Box) {
	shadowSize := s.bAnimation.shadow.Animate(gtx, wdk.ShadowSize(s.getContainerElevation()))
	wdk.DrawShadow(gtx, baseBox, s.bTheme.EnabledContainerShadowColor, shadowSize)

	containerColor := s.bAnimation.container.Animate(gtx, s.getContainerColor())
	if containerColor.A != 0 {
		paint.FillShape(
			gtx.Ops,
			containerColor.AsNRGBA(),
			baseBox.Outline(gtx),
		)
	}

	outlineColor := s.bAnimation.outline.Animate(gtx, s.getOutlineColor())
	if outlineColor.A != 0 {
		paint.FillShape(
			gtx.Ops,
			outlineColor.AsNRGBA(),
			baseBox.Stroke(gtx),
		)
	}
}

func (s *widgetStyle) getContainerElevation() token.ElevationLevel {
	if s.bKind == outlinedKind || s.bKind == textKind {
		return token.ElevationLevel0
	}
	switch s.bState {
	case Enabled:
		return s.bTheme.EnabledContainerElevation
	case Disabled:
		return s.bTheme.DisabledContainerElevation
	case Hovered:
		return s.bTheme.HoveredContainerElevation
	case Focused:
		return s.bTheme.FocusedContainerElevation
	case Pressed:
		return s.bTheme.PressedContainerElevation
	case Dragged:
		return s.bTheme.DraggedContainerElevation
	default:
		return token.ElevationLevel0
	}
}

func (s *widgetStyle) getContainerColor() token.MatColor {
	if s.bKind == outlinedKind || s.bKind == textKind {
		return token.NewTransparentMatColor()
	}
	switch s.bState {
	case Enabled:
		return s.bTheme.EnabledContainerColor
	case Disabled:
		return s.bTheme.DisabledContainerColor.SetOpacity(s.bTheme.DisabledContainerOpacity)
	default:
		return s.bTheme.EnabledContainerColor
	}
}

func (s *widgetStyle) getOutlineColor() token.MatColor {
	switch s.bState {
	case Enabled:
		return s.bTheme.EnabledOutlineColor
	case Disabled:
		return s.bTheme.DisabledOutlineColor.SetOpacity(s.bTheme.DisabledOutlineOpacity)
	case Hovered:
		return s.bTheme.HoveredOutlineColor
	case Focused:
		return s.bTheme.FocusedOutlineColor
	case Pressed:
		return s.bTheme.PressedOutlineColor
	default:
		return token.NewTransparentMatColor()
	}
}

func (s *widgetStyle) drawStateLayer(gtx layout.Context, baseBox wdk.Box) {
	target := s.getStateLayerColor()
	ripples := wdk.RipplesEnabled(gtx) && s.bState != Disabled
	if ripples && s.bState == Pressed {
		// The ripple shows the press; keep the hover highlight under it.
		target = s.bTheme.HoveredStateLayerColor.SetOpacity(s.bTheme.HoveredStateLayerOpacity)
		if !s.bClickable.Hovered() {
			target = target.SetOpacity(0)
		}
	}
	stateLayerColor := s.bAnimation.stateLayer.Animate(gtx, target)
	if stateLayerColor.A > 0 {
		paint.FillShape(
			gtx.Ops,
			stateLayerColor.AsNRGBA(),
			baseBox.Outline(gtx),
		)
	}
	if ripples {
		wdk.Ripple{
			Color:  s.bTheme.PressedStateLayerColor.SetOpacity(s.bTheme.PressedStateLayerOpacity),
			Bounds: image.Rectangle{Min: baseBox.StartPoint, Max: baseBox.EndPoint},
			Clip:   func() clip.Op { return baseBox.Outline(gtx) },
		}.Draw(gtx, s.bClickable.History())
	}
}

func (s *widgetStyle) getStateLayerColor() token.MatColor {
	switch s.bState {
	case Hovered:
		return s.bTheme.HoveredStateLayerColor.SetOpacity(s.bTheme.HoveredStateLayerOpacity)
	case Focused:
		return s.bTheme.FocusedStateLayerColor.SetOpacity(s.bTheme.FocusedStateLayerOpacity)
	case Pressed:
		return s.bTheme.PressedStateLayerColor.SetOpacity(s.bTheme.PressedStateLayerOpacity)
	case Dragged:
		return s.bTheme.DraggedStateLayerColor.SetOpacity(s.bTheme.DraggedStateLayerOpacity)
	default:
		return token.NewTransparentMatColor()
	}
}
