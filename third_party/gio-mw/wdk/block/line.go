// SPDX-License-Identifier: Unlicense OR MIT

package block

import (
	"fmt"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"image"
	"log"
)

type Segment struct {
	BaseSize   unit.Dp
	Flex       int
	CrossAlign Align
	Widget     layout.Widget
}

func (s Segment) AlignBaseline() Segment {
	s.CrossAlign = AlignBaseline
	return s
}

func (s Segment) AlignStart() Segment {
	s.CrossAlign = AlignStart
	return s
}

func (s Segment) AlignMiddle() Segment {
	s.CrossAlign = AlignMiddle
	return s
}

func (s Segment) AlignEnd() Segment {
	s.CrossAlign = AlignEnd
	return s
}

func NewSegment(widget layout.Widget) Segment {
	return Segment{Widget: widget}
}

func NewFlexSegment(widget layout.Widget) Segment {
	return Segment{Flex: 1, Widget: widget}
}

func NewHorizontalSpacer(size unit.Dp) Segment {
	return Segment{Widget: func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: image.Point{X: gtx.Dp(size), Y: 0}}
	}}
}

func NewVerticalSpacer(size unit.Dp) Segment {
	return Segment{Widget: func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: image.Point{X: 0, Y: gtx.Dp(size)}}
	}}
}

// Line represents a layout strategy for arranging segments either horizontally or vertically based on the Axis field.
// Axis specifies the orientation (horizontal or vertical).
// Expand indicates whether the layout should expand to use the maximum available space in the specified Axis direction.
//
// For RTL layouts, non-expanding lines change the direction of child segments, but not the
// position. A wrapping widget must properly position the whole line.
type Line struct {
	Axis     Axis
	Expand   bool
	Overflow Overflow
}

type renderedSegment struct {
	callOp     op.CallOp
	dimensions layout.Dimensions
	offset     image.Point
	line       int
}

func (l Line) Layout(gtx layout.Context, segments ...Segment) layout.Dimensions {
	if err := l.validateConstraints(gtx, segments); err != nil {
		log.Printf("Invalid Line constraints: %v", err)
		return layout.Dimensions{}
	}

	switch l.Axis {
	case AxisHorizontal:
		if l.Overflow == OverflowClip {
			dimensions, _ := l.layoutHorizontalClip(gtx, segments)
			return dimensions
		} else if l.Overflow == OverflowWrap {
			dimensions, _ := l.layoutHorizontalWrap(gtx, segments)
			return dimensions
		}
		panic(fmt.Errorf("Line.Layout: invalid Overflow: %d", l.Overflow))
	case AxisVertical:
		if l.Overflow == OverflowClip {
			dimensions, _ := l.layoutVertical(gtx, segments)
			return dimensions
		}
		panic(fmt.Errorf("Line.Layout: OverflowWrap is not implemented yet"))
	default:
		panic(fmt.Errorf("Line.Layout: Invalid Axis: %d", l.Axis))
	}
}

func (l Line) validateConstraints(gtx layout.Context, segments []Segment) error {
	if len(segments) == 0 {
		return fmt.Errorf("no segments provided")
	}
	if gtx.Constraints.Max.X == 0 {
		return fmt.Errorf("no horizontal space available")
	}
	if gtx.Constraints.Max.Y == 0 {
		return fmt.Errorf("no vertical space available")
	}
	switch l.Overflow {
	case OverflowClip:
	case OverflowWrap:
		if l.Axis == AxisVertical {
			return fmt.Errorf("OverflowWrap doesn't support AxisVertical")
		}
		if !l.Expand {
			return fmt.Errorf("OverflowWrap requires Expand to be true")
		}
		for _, segment := range segments {
			if segment.Flex > 0 {
				if segment.BaseSize == 0 {
					return fmt.Errorf("segments with Flex must have a non-zero BaseSize for OverflowWrap")
				}
			}
		}
	default:
		return fmt.Errorf("invalid Overflow: %d", l.Overflow)
	}
	return nil
}

type renderedLine struct {
	index             int
	flexCount         int
	maxBaselineOffset int
	size              image.Point
	usedMainSize      int
	yOffset           int
}

func (l Line) layoutHorizontalWrap(gtx layout.Context, segments []Segment) (layout.Dimensions, []*renderedSegment) {
	originalConstraints := gtx.Constraints
	gtx.Constraints.Min.X = 0

	var rLines []*renderedLine
	hastFlex := false
	rSegments := make([]*renderedSegment, len(segments))
	getRenderedSegment := func(idx int) *renderedSegment {
		rSegment := &renderedSegment{}
		rSegments[idx] = rSegment
		return rSegment
	}
	{
		getNewLine := func() *renderedLine {
			gtx.Constraints.Max.X = originalConstraints.Max.X
			newLine := &renderedLine{index: len(rLines)}
			rLines = append(rLines, newLine)
			return newLine
		}
		currentLine := getNewLine()
		for idx, segment := range segments {
			rSegment := getRenderedSegment(idx)
			if segment.Flex > 0 {
				baseSize := gtx.Dp(segment.BaseSize)
				if currentLine.usedMainSize > 0 {
					if currentLine.usedMainSize+baseSize >= originalConstraints.Max.X {
						currentLine = getNewLine()
					}
				}
				rSegment.line = currentLine.index
				currentLine.usedMainSize += baseSize
				currentLine.flexCount++
				hastFlex = true
				continue
			}
			macroOp := op.Record(gtx.Ops)
			rSegment.dimensions = segment.Widget(gtx)
			rSegment.callOp = macroOp.Stop()
			baselineOffset := getBaselineOffset(segment, rSegment)
			rSegment.dimensions.Baseline = rSegment.dimensions.Size.Y - baselineOffset

			if currentLine.usedMainSize+rSegment.dimensions.Size.X >= originalConstraints.Max.X {
				currentLine = getNewLine()
			}
			currentLine.maxBaselineOffset = max(currentLine.maxBaselineOffset, baselineOffset)
			currentLine.usedMainSize += rSegment.dimensions.Size.X
			rSegment.line = currentLine.index
		}
	}

	if hastFlex {
		currentLine := rLines[0]
		currentLineFlexBasis := (originalConstraints.Max.X - currentLine.usedMainSize) / currentLine.flexCount
		for idx, segment := range segments {
			if segment.Flex == 0 {
				continue
			}
			rSegment := rSegments[idx]
			if rSegment.line != currentLine.index {
				currentLine = rLines[rSegment.line]
				currentLineFlexBasis = (originalConstraints.Max.X - currentLine.usedMainSize) / currentLine.flexCount
			}

			flexSize := currentLineFlexBasis*segment.Flex + gtx.Dp(segment.BaseSize)
			gtx.Constraints.Min.X = flexSize
			gtx.Constraints.Max.X = flexSize
			macroOp := op.Record(gtx.Ops)
			rSegment.dimensions = segment.Widget(gtx)
			rSegment.callOp = macroOp.Stop()

			baselineOffset := getBaselineOffset(segment, rSegment)
			rSegment.dimensions.Baseline = rSegment.dimensions.Size.Y - baselineOffset
			currentLine.maxBaselineOffset = max(currentLine.maxBaselineOffset, baselineOffset)
			currentLine.usedMainSize += rSegment.dimensions.Size.X - gtx.Dp(segment.BaseSize)
		}
	}

	for _, rLine := range rLines {
		if rLine.usedMainSize < originalConstraints.Max.X {
			rLine.usedMainSize = originalConstraints.Max.X
		}
	}

	currentLineYOffset := 0
	currentLine := rLines[0]
	for _, rSegment := range rSegments {
		if currentLine.index != rSegment.line {
			currentLineYOffset += currentLine.size.Y
			currentLine = rLines[rSegment.line]
		}
		xOffset := currentLine.size.X
		if gtx.Locale.Direction == system.RTL {
			xOffset = currentLine.usedMainSize - currentLine.size.X - rSegment.dimensions.Size.X
		}
		baselineOffset := rSegment.dimensions.Size.Y - rSegment.dimensions.Baseline
		inLineYOffset := currentLine.maxBaselineOffset - baselineOffset
		rSegment.offset = image.Pt(xOffset, currentLineYOffset+inLineYOffset)
		transformStack := op.Offset(rSegment.offset).Push(gtx.Ops)
		rSegment.callOp.Add(gtx.Ops)
		transformStack.Pop()
		currentLine.size.X += rSegment.dimensions.Size.X
		currentLine.size.Y = max(currentLine.size.Y, rSegment.dimensions.Size.Y+inLineYOffset)
	}

	lineSize := image.Point{X: originalConstraints.Max.X, Y: 0}
	for _, rLine := range rLines {
		lineSize.Y += rLine.size.Y
	}
	baseline := 0
	if len(rLines) == 1 {
		baseline = rLines[0].size.Y - rLines[0].maxBaselineOffset
	}
	return layout.Dimensions{Size: lineSize, Baseline: baseline}, rSegments
}

func (l Line) layoutHorizontalClip(gtx layout.Context, segments []Segment) (layout.Dimensions, []*renderedSegment) {
	originalConstraints := gtx.Constraints
	gtx.Constraints.Min.X = 0

	maxBaselineOffset := 0
	totalWidth := 0
	rSegments := make([]*renderedSegment, len(segments))
	flexCount := 0
	for idx, segment := range segments {
		if segment.Flex > 0 {
			flexCount++
			continue
		}
		rSegment := &renderedSegment{}
		rSegments[idx] = rSegment

		gtx.Constraints.Max.X = originalConstraints.Max.X - totalWidth
		if l.Overflow == OverflowClip {
			if gtx.Constraints.Max.X <= 0 {
				macroOp := op.Record(gtx.Ops)
				rSegment.callOp = macroOp.Stop()
				continue
			}
		}
		macroOp := op.Record(gtx.Ops)
		rSegment.dimensions = segment.Widget(gtx)
		rSegment.callOp = macroOp.Stop()

		baselineOffset := getBaselineOffset(segment, rSegment)
		rSegment.dimensions.Baseline = rSegment.dimensions.Size.Y - baselineOffset
		maxBaselineOffset = max(maxBaselineOffset, baselineOffset)
		totalWidth += rSegment.dimensions.Size.X
	}

	if flexCount > 0 {
		flexBasis := (originalConstraints.Max.X - totalWidth) / flexCount
		for idx, segment := range segments {
			if segment.Flex == 0 {
				continue
			}
			rSegment := &renderedSegment{}
			rSegments[idx] = rSegment

			flexSize := flexBasis * segment.Flex
			gtx.Constraints.Min.X = flexSize
			gtx.Constraints.Max.X = flexSize
			macroOp := op.Record(gtx.Ops)
			rSegment.dimensions = segment.Widget(gtx)
			rSegment.callOp = macroOp.Stop()

			baselineOffset := getBaselineOffset(segment, rSegment)
			rSegment.dimensions.Baseline = rSegment.dimensions.Size.Y - baselineOffset
			maxBaselineOffset = max(maxBaselineOffset, baselineOffset)
			totalWidth += rSegment.dimensions.Size.X
		}
	}

	if totalWidth < originalConstraints.Min.X {
		totalWidth = originalConstraints.Min.X
	}

	if l.Expand {
		if totalWidth < originalConstraints.Max.X {
			totalWidth = originalConstraints.Max.X
		}
	}

	lineSize := image.Point{X: 0, Y: 0}
	for _, rSegment := range rSegments {
		xOffset := lineSize.X
		if gtx.Locale.Direction == system.RTL {
			xOffset = totalWidth - lineSize.X - rSegment.dimensions.Size.X
		}
		baselineOffset := rSegment.dimensions.Size.Y - rSegment.dimensions.Baseline
		yOffset := maxBaselineOffset - baselineOffset
		rSegment.offset = image.Pt(xOffset, yOffset)
		transformStack := op.Offset(rSegment.offset).Push(gtx.Ops)
		rSegment.callOp.Add(gtx.Ops)
		transformStack.Pop()
		lineSize.X += rSegment.dimensions.Size.X
		lineSize.Y = max(lineSize.Y, rSegment.dimensions.Size.Y+yOffset)
	}

	if lineSize.X < totalWidth {
		lineSize.X = totalWidth
	}

	baseline := lineSize.Y - maxBaselineOffset
	return layout.Dimensions{Size: lineSize, Baseline: baseline}, rSegments
}

func (l Line) layoutVertical(gtx layout.Context, segments []Segment) (layout.Dimensions, []*renderedSegment) {
	originalConstraints := gtx.Constraints
	gtx.Constraints.Min.Y = 0

	maxElementWidth := 0
	totalHeight := 0
	rSegments := make([]*renderedSegment, len(segments))
	for idx, segment := range segments {
		rSegment := &renderedSegment{}
		rSegments[idx] = rSegment

		gtx.Constraints.Max.Y = originalConstraints.Max.Y - totalHeight
		if l.Overflow == OverflowClip {
			if gtx.Constraints.Max.Y <= 0 {
				macroOp := op.Record(gtx.Ops)
				rSegment.callOp = macroOp.Stop()
				continue
			}
		}
		macroOp := op.Record(gtx.Ops)
		rSegment.dimensions = segment.Widget(gtx)
		rSegment.callOp = macroOp.Stop()

		maxElementWidth = max(maxElementWidth, rSegment.dimensions.Size.X)
		totalHeight += rSegment.dimensions.Size.Y
	}

	if totalHeight < originalConstraints.Min.Y {
		totalHeight = originalConstraints.Min.Y
	}

	if l.Expand {
		if totalHeight < originalConstraints.Max.Y {
			totalHeight = originalConstraints.Max.Y
		}
	}

	availableWidth := maxElementWidth
	if availableWidth < originalConstraints.Min.X {
		availableWidth = originalConstraints.Min.X
	}

	lineSize := image.Point{X: 0, Y: 0}
	for idx, rSegment := range rSegments {
		segment := segments[idx]
		xOffset := getSegmentXOffset(gtx, segment, rSegment, availableWidth)
		yOffset := lineSize.Y
		rSegment.offset = image.Pt(xOffset, yOffset)
		transformStack := op.Offset(rSegment.offset).Push(gtx.Ops)
		rSegment.callOp.Add(gtx.Ops)
		transformStack.Pop()
		lineSize.Y += rSegment.dimensions.Size.Y
		lineSize.X = max(lineSize.X, rSegment.dimensions.Size.X+xOffset)
	}

	if l.Expand {
		lineSize.Y = totalHeight
	}

	baseline := rSegments[len(rSegments)-1].dimensions.Baseline
	return layout.Dimensions{Size: lineSize, Baseline: baseline}, rSegments
}

func getBaselineOffset(segment Segment, rSegment *renderedSegment) int {
	switch segment.CrossAlign {
	case AlignBaseline:
		return rSegment.dimensions.Size.Y - rSegment.dimensions.Baseline
	case AlignStart:
		return 0
	case AlignMiddle:
		halfInt := rSegment.dimensions.Size.Y / 2
		return halfInt
	case AlignEnd:
		return rSegment.dimensions.Size.Y
	default:
		panic(fmt.Errorf("getBaselineOffset: Invalid cross alignment: %d", segment.CrossAlign))
	}
}

func getSegmentXOffset(gtx layout.Context, segment Segment, rSegment *renderedSegment, availableWidth int) int {
	switch segment.CrossAlign {
	case AlignBaseline:
		fallthrough
	case AlignStart:
		if gtx.Locale.Direction == system.RTL {
			return availableWidth - rSegment.dimensions.Size.X
		}
		return 0
	case AlignMiddle:
		halfInt := (availableWidth - rSegment.dimensions.Size.X) / 2
		return halfInt
	case AlignEnd:
		if gtx.Locale.Direction == system.RTL {
			return 0
		}
		return availableWidth - rSegment.dimensions.Size.X
	default:
		panic(fmt.Errorf("getSegmentXOffset: Invalid cross alignment: %d", segment.CrossAlign))
	}
}
