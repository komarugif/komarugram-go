// SPDX-License-Identifier: Unlicense OR MIT

package block

import (
	"fmt"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"image"
)

// Padding adds some spacing around a widget and positions it inside the container,
// respecting the Locale.Direction from layout.Context for spacing and position.
type Padding struct {
	Bottom unit.Dp
	End    unit.Dp
	Start  unit.Dp
	Top    unit.Dp
}

func HorizontalPadding(size unit.Dp) Padding {
	return Padding{
		End:   size,
		Start: size,
	}
}

func VerticalPadding(size unit.Dp) Padding {
	return Padding{
		Bottom: size,
		Top:    size,
	}
}

func UniformPadding(size unit.Dp) Padding {
	return Padding{
		Bottom: size,
		End:    size,
		Start:  size,
		Top:    size,
	}
}

func (p Padding) Layout(gtx layout.Context, widget layout.Widget) layout.Dimensions {
	if err := p.validateConstraints(gtx); err != nil {
		//log.Printf("Invalid Padding constraints: %v", err)
		return layout.Dimensions{}
	}
	gtx = p.updateConstraints(gtx)

	layoutWidget, _ := p.layoutWidget(gtx, widget)
	return layoutWidget
}

func (p Padding) validateConstraints(gtx layout.Context) error {
	if gtx.Constraints.Max.X == 0 {
		return fmt.Errorf("no horizontal space available")
	}
	if gtx.Constraints.Max.Y == 0 {
		return fmt.Errorf("no vertical space available")
	}
	if p.Start < 0 {
		return fmt.Errorf("negative Start: %v", p.Start)
	}
	if p.End < 0 {
		return fmt.Errorf("negative End: %v", p.End)
	}
	if p.Top < 0 {
		return fmt.Errorf("negative Top: %v", p.Top)
	}
	if p.Bottom < 0 {
		return fmt.Errorf("negative Bottom: %v", p.Bottom)
	}
	if gtx.Constraints.Max.X <= gtx.Dp(p.Start+p.End) {
		return fmt.Errorf("not enough horizontal space available: %v", gtx.Constraints.Max.X)
	}
	if gtx.Constraints.Max.Y <= gtx.Dp(p.Top+p.Bottom) {
		return fmt.Errorf("not enough vertical space available: %v", gtx.Constraints.Max.Y)
	}
	return nil
}

func (p Padding) updateConstraints(gtx layout.Context) layout.Context {
	horizontalPadding := gtx.Dp(p.Start + p.End)
	verticalPadding := gtx.Dp(p.Top + p.Bottom)
	if gtx.Constraints.Min.X >= horizontalPadding {
		gtx.Constraints.Min.X -= horizontalPadding
	}
	if gtx.Constraints.Min.Y >= verticalPadding {
		gtx.Constraints.Min.Y -= verticalPadding
	}
	gtx.Constraints.Max.X -= horizontalPadding
	gtx.Constraints.Max.Y -= verticalPadding
	return gtx
}

func (p Padding) layoutWidget(gtx layout.Context, widget layout.Widget) (layout.Dimensions, image.Point) {
	// Render widget and record macro.
	macroOp := op.Record(gtx.Ops)
	widgetDimensions := widget(gtx)
	callOp := macroOp.Stop()

	offsetPoint := image.Point{
		X: gtx.Dp(p.Start),
		Y: gtx.Dp(p.Top),
	}
	if gtx.Locale.Direction == system.RTL {
		offsetPoint.X = gtx.Dp(p.End)
	}
	transformStack := op.Offset(offsetPoint).Push(gtx.Ops)
	callOp.Add(gtx.Ops)
	transformStack.Pop()

	extraSpace := image.Point{
		X: gtx.Dp(p.Start + p.End),
		Y: gtx.Dp(p.Top + p.Bottom),
	}
	return layout.Dimensions{
		Baseline: widgetDimensions.Baseline + gtx.Dp(p.Bottom),
		Size:     widgetDimensions.Size.Add(extraSpace),
	}, offsetPoint
}
