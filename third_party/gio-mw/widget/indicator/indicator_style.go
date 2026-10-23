// SPDX-License-Identifier: Unlicense OR MIT

package indicator

import (
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"image"

	"gioui.org/f32"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

var (
	diameter             = unit.Dp(48)
	magicDiffAngle       = float32(15)
	magicStartAngle      = float32(90) - magicDiffAngle/2
	magicRevolutionAngle = float32(360) - magicDiffAngle
)

type widgetStyle struct {
	iIndeterminate bool
	iProgress      float32
	iType          kind
	iTheme         *Theme
}

func (s *widgetStyle) layout(gtx layout.Context) layout.Dimensions {
	s.iTheme = BuildTheme(gtx)
	switch s.iType {
	case linearKind:
		// Across the whole width, as Material 3's: EnabledSpacing is the
		// gap between the active indicator and the track, which upstream
		// also put at both ends, and the bar fell short of what was laid
		// out beside it.
		return s.linearWidget(gtx)
	case circularKind:
		return block.Container{
			Gravity: block.GravityMiddleCenter,
		}.Layout(gtx, s.circularWidget)
	default:
		return layout.Dimensions{}
	}
}

func (s *widgetStyle) circularWidget(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Max.Y = gtx.Dp(diameter)
	actualDiameter := float32(gtx.Dp(diameter))
	progressAngle := s.iProgress * magicRevolutionAngle

	trackStartAngle := magicStartAngle
	trackAngle := magicRevolutionAngle
	if s.iProgress > 0 {
		activeShape := wdk.Arc{
			Center:     f32.Pt(actualDiameter/2, actualDiameter/2),
			Diameter:   actualDiameter,
			StartAngle: magicStartAngle,
			SweepAngle: progressAngle,
		}
		activeClipStack := clip.Stroke{
			Path:  activeShape.Path(gtx),
			Width: float32(gtx.Dp(s.iTheme.EnabledActiveIndicatorThickness)),
		}.Op().Push(gtx.Ops)
		paint.Fill(gtx.Ops, s.iTheme.EnabledActiveIndicatorColor.AsNRGBA())
		activeClipStack.Pop()
		if s.iProgress < 1 {
			trackStartAngle = magicStartAngle - progressAngle - magicDiffAngle
			trackAngle = magicRevolutionAngle - progressAngle - magicDiffAngle
		}
	}

	if trackAngle > 0 && s.iProgress < 1 {
		trackShape := wdk.Arc{
			Center:     f32.Pt(actualDiameter/2, actualDiameter/2),
			Diameter:   actualDiameter,
			StartAngle: trackStartAngle,
			SweepAngle: trackAngle,
		}
		trackClipStack := clip.Stroke{
			Path:  trackShape.Path(gtx),
			Width: float32(gtx.Dp(s.iTheme.EnabledTrackThickness)),
		}.Op().Push(gtx.Ops)
		paint.Fill(gtx.Ops, s.iTheme.EnabledTrackColor.AsNRGBA())
		trackClipStack.Pop()
	}

	return layout.Dimensions{
		Size: image.Point{
			X: gtx.Dp(diameter),
			Y: gtx.Dp(diameter),
		},
	}
}

func (s *widgetStyle) linearWidget(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Max.Y = gtx.Dp(s.iTheme.EnabledTrackThickness)
	maxX := gtx.Constraints.Max.X
	progressWidth := s.iProgress * float32(maxX)
	if s.iProgress > 0 {
		minProgressWidth := float32(gtx.Dp(s.iTheme.EnabledActiveIndicatorThickness))
		progressWidth = max(progressWidth, minProgressWidth)
		if s.iProgress < 1 {
			maxProgressWidth := float32(gtx.Constraints.Max.X - gtx.Dp(s.iTheme.EnabledSpacing*2))
			progressWidth = min(progressWidth, maxProgressWidth)
		}
	}

	trackStartX := float32(0)
	if s.iProgress > 0 {
		trackStartPoint := image.Point{X: 0, Y: 0}
		trackEndPoint := image.Point{X: int(progressWidth), Y: gtx.Dp(s.iTheme.EnabledActiveIndicatorThickness)}
		if gtx.Locale.Direction == system.RTL {
			trackStartPoint.X = maxX - int(progressWidth)
			trackEndPoint.X = maxX
		}
		activeIndicatorBox := wdk.Box{
			Shape:      wdk.FromCornerShapesToken(gtx, s.iTheme.EnabledActiveIndicatorShape),
			StartPoint: trackStartPoint,
			EndPoint:   trackEndPoint,
		}
		activeClipStack := clip.Outline{Path: activeIndicatorBox.Path(gtx)}.Op().Push(gtx.Ops)
		paint.Fill(gtx.Ops, s.iTheme.EnabledActiveIndicatorColor.AsNRGBA())
		activeClipStack.Pop()
		trackStartX = progressWidth + float32(gtx.Dp(s.iTheme.EnabledSpacing))
	}

	{
		trackStartPoint := image.Point{X: int(trackStartX)}
		trackEndPoint := image.Point{X: maxX, Y: gtx.Dp(s.iTheme.EnabledTrackThickness)}
		if gtx.Locale.Direction == system.RTL {
			trackStartPoint.X = 0
			trackEndPoint.X = maxX - int(trackStartX)
		}
		trackBox := wdk.Box{
			Shape:      wdk.FromCornerShapesToken(gtx, s.iTheme.EnabledTrackShape),
			StartPoint: trackStartPoint,
			EndPoint:   trackEndPoint,
		}
		trackClipStack := clip.Outline{Path: trackBox.Path(gtx)}.Op().Push(gtx.Ops)
		paint.Fill(gtx.Ops, s.iTheme.EnabledTrackColor.AsNRGBA())
		trackClipStack.Pop()
	}

	// No stop indicator at zero thickness, the default here.
	if stopIndicatorThickness := gtx.Dp(s.iTheme.EnabledStopIndicatorThickness); s.iProgress < 1 && stopIndicatorThickness > 0 {
		stopIndicatorStartX := gtx.Constraints.Max.X - stopIndicatorThickness
		if gtx.Locale.Direction == system.RTL {
			stopIndicatorStartX = 0
		}
		stopIndicatorBox := wdk.Box{
			Shape:    wdk.FromCornerShapesToken(gtx, s.iTheme.EnabledStopIndicatorShape),
			EndPoint: image.Point{X: stopIndicatorThickness, Y: stopIndicatorThickness},
		}
		transformStack := op.Offset(image.Point{X: stopIndicatorStartX}).Push(gtx.Ops)
		stopClipStack := clip.Outline{Path: stopIndicatorBox.Path(gtx)}.Op().Push(gtx.Ops)
		paint.Fill(gtx.Ops, s.iTheme.EnabledStopIndicatorColor.AsNRGBA())
		stopClipStack.Pop()
		transformStack.Pop()
	}

	return layout.Dimensions{
		Size: image.Point{
			X: maxX,
			Y: gtx.Dp(s.iTheme.EnabledActiveIndicatorThickness),
		},
	}
}
