// SPDX-License-Identifier: Unlicense OR MIT

package slider

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"image"
	"math"

	"gioui.org/gesture"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
)

type widgetStyle struct {
	Slider *Slider
	Theme  *Theme
}

func (s *widgetStyle) layout(gtx layout.Context) layout.Dimensions {
	switch s.Slider.sKind {
	case standardKind:
		return s.layoutStandard(gtx)
	case centeredKind:
		// TODO: Implement centered slider.
		return layout.Dimensions{}
	case rangeKind:
		// TODO: Implement range slider.
		return layout.Dimensions{}
	default:
		panic("Slider: unknown Slider kind")
	}
}

func (s *widgetStyle) layoutStandard(gtx layout.Context) layout.Dimensions {
	return s.layoutTracks(gtx)
}

func (s *widgetStyle) layoutTracks(gtx layout.Context) layout.Dimensions {
	trackHeight := gtx.Dp(s.Theme.SliderActiveTrackHeight)

	var activeStopIndicatorIndex int
	for i, opt := range s.Slider.sOptions {
		if opt == s.Slider.sStartValue {
			activeStopIndicatorIndex = i
			break
		}
	}

	stopIndicatorCount := len(s.Slider.sOptions)
	stopIndicatorSize := gtx.Dp(s.Theme.SliderStopIndicatorSize)
	// The handle slides between the stops: position is the index of the
	// stop it is at, fractional on the way, and everything is drawn from it.
	anim := &s.Slider.animation
	if anim.pressed.Duration == 0 {
		// A slider is dragged and clicked: the handle starts moving at once
		// and settles fast, not in the 200 ms of the standard curve.
		anim.position.Duration, anim.position.Easing = positionDuration, &positionEasing
		anim.pressed.Duration = token.DurationShort2
	}
	position := anim.position.Animate(gtx, float32(activeStopIndicatorIndex))
	pressed := anim.pressed.Animate(gtx, boolToFloat(s.Slider.sStartDrag.Dragging()))
	stopIndicatorBox := wdk.Box{
		Shape:    wdk.FromCornerShapesToken(gtx, s.Theme.SliderStopIndicatorShape),
		EndPoint: image.Point{X: stopIndicatorSize, Y: stopIndicatorSize},
	}
	stopIndicatorSpacing := s.getStopIndicatorSpacing(gtx)
	stopStep := float32(stopIndicatorSize) + stopIndicatorSpacing
	leadingSpace := s.Theme.SliderActiveHandleLeadingSpace
	trailingSpace := s.Theme.SliderActiveHandleTrailingSpace

	segments := []block.Segment{
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			startSpace := gtx.Dp(s.Theme.SliderStopIndicatorStartSpace)
			baseLength := startSpace - gtx.Dp(leadingSpace)
			activeTrackLength := baseLength + int(math.Round(float64(position*stopStep)))
			trackSize := s.Slider.Orientation.OrientPoint(image.Point{X: activeTrackLength, Y: trackHeight})
			// Nothing is drawn at the first stop: the track grows out of
			// nothing as the handle leaves it.
			paintLength := int(math.Round(float64(position*stopStep + float32(baseLength)*min(position, 1))))
			if paintLength <= 0 {
				return layout.Dimensions{Size: trackSize}
			}
			activeTrackColor := s.Theme.SliderActiveTrackColor.AsNRGBA()
			activeTrackBox := wdk.Box{
				Shape: s.Slider.Orientation.OrientShape(wdk.FromCornerShapesToken(gtx, token.CornerShapes{
					TopStart:    s.Theme.SliderActiveTrackOuterCornerShape,
					BottomStart: s.Theme.SliderActiveTrackOuterCornerShape,
					TopEnd:      s.Theme.SliderActiveTrackInnerCornerShape,
					BottomEnd:   s.Theme.SliderActiveTrackInnerCornerShape,
				})),
				EndPoint: s.Slider.Orientation.OrientPoint(image.Point{X: paintLength, Y: trackHeight}),
			}
			paint.FillShape(
				gtx.Ops,
				activeTrackColor,
				activeTrackBox.Outline(gtx),
			)

			yOffset := (trackHeight - stopIndicatorSize) / 2
			stopIndicatorColor := s.Theme.SliderStopIndicatorColorSelected.AsNRGBA()
			for i := range s.Slider.sOptions {
				xOffset := startSpace + int(stopIndicatorSpacing*float32(i)) + stopIndicatorSize*i
				// A stop shows once the handle has left it behind, and
				// stays hidden while the handle is over it.
				if xOffset+stopIndicatorSize > activeTrackLength && float32(i) >= position-positionEpsilon {
					continue
				}
				if gtx.Locale.Direction == system.RTL && s.Slider.Orientation == wdk.AxisHorizontal {
					xOffset = activeTrackLength - stopIndicatorSize - xOffset
				}
				offsetPoint := image.Pt(xOffset, yOffset)
				transformStack := op.Offset(s.Slider.Orientation.OrientPoint(offsetPoint)).Push(gtx.Ops)
				paint.FillShape(gtx.Ops, stopIndicatorColor, stopIndicatorBox.Outline(gtx))
				transformStack.Pop()
			}
			return layout.Dimensions{Size: trackSize}
		}).AlignMiddle(),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: s.Slider.Orientation.OrientPoint(image.Point{X: gtx.Dp(leadingSpace), Y: 0})}
		}),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			handleHeight := gtx.Dp(s.Theme.SliderHandleHeight)
			handleWidth := gtx.Dp(s.Theme.SliderHandleWidth)
			// The handle narrows while it is held, and widens back.
			activeHandleHeight := gtx.Dp(s.Theme.SliderActiveHandleHeight)
			activeHandleWidth := gtx.Dp(s.Theme.SliderActiveHandleWidth)
			handleSize := image.Point{
				X: handleWidth + int(math.Round(float64(float32(activeHandleWidth-handleWidth)*pressed))),
				Y: handleHeight + int(math.Round(float64(float32(activeHandleHeight-handleHeight)*pressed))),
			}
			handleOffset := image.Point{
				X: (handleWidth - handleSize.X) / 2,
				Y: (handleHeight - handleSize.Y) / 2,
			}
			handleBox := wdk.Box{
				Shape:      s.Slider.Orientation.OrientShape(wdk.FromCornerShapesToken(gtx, s.Theme.SliderHandleShape)),
				StartPoint: s.Slider.Orientation.OrientPoint(handleOffset),
				EndPoint:   s.Slider.Orientation.OrientPoint(handleSize.Add(handleOffset)),
			}
			handleOutline := handleBox.Outline(gtx)
			defer handleOutline.Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, s.Theme.SliderHandleColor.AsNRGBA())
			return layout.Dimensions{
				Size: s.Slider.Orientation.OrientPoint(handleSize.Add(handleOffset).Add(handleOffset)),
			}
		}).AlignMiddle(),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: s.Slider.Orientation.OrientPoint(image.Point{X: gtx.Dp(trailingSpace), Y: 0})}
		}),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			endSpace := gtx.Dp(s.Theme.SliderStopIndicatorEndSpace)
			baseLength := endSpace - gtx.Dp(trailingSpace)
			remaining := float32(stopIndicatorCount-1) - position
			if remaining <= 0 {
				return layout.Dimensions{
					Size: s.Slider.Orientation.OrientPoint(image.Point{X: baseLength, Y: trackHeight}),
				}
			}
			inactiveTrackLength := s.Slider.Orientation.OrientPoint(gtx.Constraints.Max).X
			// Nothing is drawn at the last stop: the track shrinks into
			// nothing as the handle arrives.
			paintLength := inactiveTrackLength - int(math.Round(float64(float32(baseLength)*(1-min(remaining, 1)))))
			inactiveTrackColor := s.Theme.SliderInactiveTrackColor.AsNRGBA()
			inactiveTrackBox := wdk.Box{
				Shape: s.Slider.Orientation.OrientShape(wdk.FromCornerShapesToken(gtx, token.CornerShapes{
					TopStart:    s.Theme.SliderActiveTrackInnerCornerShape,
					BottomStart: s.Theme.SliderActiveTrackInnerCornerShape,
					TopEnd:      s.Theme.SliderActiveTrackOuterCornerShape,
					BottomEnd:   s.Theme.SliderActiveTrackOuterCornerShape,
				})),
				EndPoint: s.Slider.Orientation.OrientPoint(image.Point{X: paintLength, Y: trackHeight}),
			}
			paint.FillShape(
				gtx.Ops,
				inactiveTrackColor,
				inactiveTrackBox.Outline(gtx),
			)
			yOffset := (trackHeight - stopIndicatorSize) / 2
			stopIndicatorColor := s.Theme.SliderStopIndicatorColor.AsNRGBA()
			xEndOffset := inactiveTrackLength - endSpace - stopIndicatorSize
			for i := range s.Slider.sOptions {
				inverseIndex := stopIndicatorCount - 1 - i
				xOffset := xEndOffset - stopIndicatorSize*inverseIndex - int(stopIndicatorSpacing*float32(inverseIndex))
				// A stop shows once the handle is before it, and stays
				// hidden while the handle is over it.
				if xOffset < 0 && float32(i) <= position+positionEpsilon {
					continue
				}
				if gtx.Locale.Direction == system.RTL && s.Slider.Orientation == wdk.AxisHorizontal {
					xOffset = inactiveTrackLength - endSpace - xOffset
				}
				offsetPoint := image.Pt(xOffset, yOffset)
				transformStack := op.Offset(s.Slider.Orientation.OrientPoint(offsetPoint)).Push(gtx.Ops)
				paint.FillShape(gtx.Ops, stopIndicatorColor, stopIndicatorBox.Outline(gtx))
				transformStack.Pop()
			}
			return layout.Dimensions{Size: s.Slider.Orientation.OrientPoint(image.Point{X: inactiveTrackLength, Y: trackHeight})}
		}).AlignMiddle(),
	}

	draggableValue := float32(activeStopIndicatorIndex) / float32(len(s.Slider.sOptions)-1)
	if s.Slider.Orientation == wdk.AxisVertical {
		draggableValue = 1 - draggableValue
	}
	s.Slider.sStartDrag.Value = draggableValue
	dragAxis := gesture.Horizontal
	if s.Slider.Orientation == wdk.AxisVertical {
		dragAxis = gesture.Vertical
	}
	return s.Slider.sStartDrag.Layout(gtx, dragAxis, func(gtx layout.Context) layout.Dimensions {
		if s.Slider.sStartDrag.Value != draggableValue {
			actualValue := s.Slider.sStartDrag.Value
			if s.Slider.Orientation == wdk.AxisVertical {
				actualValue = 1 - actualValue
			}
			newValueIndex := math.Round(float64(actualValue) * float64(stopIndicatorCount-1))
			s.Slider.sStartValue = s.Slider.sOptions[int(newValueIndex)]
			s.Slider.sOnChange(s.Slider.sStartValue)
		}
		lineAxis := block.AxisHorizontal
		if s.Slider.Orientation == wdk.AxisVertical {
			lineAxis = block.AxisVertical
		}
		return block.Line{
			Axis:     lineAxis,
			Overflow: block.OverflowClip,
			Expand:   true,
		}.Layout(gtx, segments...)
	})
}

func (s *widgetStyle) getStopIndicatorSpacing(gtx layout.Context) float32 {
	startSpace := gtx.Dp(s.Theme.SliderStopIndicatorStartSpace)
	endSpace := gtx.Dp(s.Theme.SliderStopIndicatorEndSpace)
	stopIndicatorCount := len(s.Slider.sOptions)
	stopIndicatorsSize := gtx.Dp(s.Theme.SliderStopIndicatorSize) * stopIndicatorCount
	availableSize := s.Slider.Orientation.OrientPoint(gtx.Constraints.Max)
	availableSpace := float32(availableSize.X - startSpace - endSpace - stopIndicatorsSize)
	return availableSpace / float32(stopIndicatorCount-1)
}

func boolToFloat(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
