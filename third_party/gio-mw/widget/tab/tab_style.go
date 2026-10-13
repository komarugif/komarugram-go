// SPDX-License-Identifier: Unlicense OR MIT

package tab

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"gio-mw/widget/divider"
	"image"

	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	absoluteMinWidth    = unit.Dp(24)
	horizontalPadding   = unit.Dp(12)
	navigationCloseIcon = wdk.RequireIconWidget(icons.NavigationClose)
)

type widgetState int

const (
	Enabled widgetState = iota
	Disabled
	Hovered
	Focused
	Pressed
)

type widgetStyle struct {
	*Group
	tKind  Kind
	tTheme *Theme
}

func (s *widgetStyle) layout(gtx layout.Context, trailingWidget layout.Widget) layout.Dimensions {
	switch s.tKind {
	case Primary:
		return s.layoutPrimary(gtx, s.Items, trailingWidget)
	case Secondary:
		return s.layoutSecondary(gtx, s.Items, trailingWidget)
	}
	return layout.Dimensions{Size: gtx.Constraints.Min}
}

func (s *widgetStyle) layoutPrimary(gtx layout.Context, tabs []*Item, trailingWidget layout.Widget) layout.Dimensions {
	gtx.Constraints.Min.Y = gtx.Dp(s.tTheme.EnabledContainerHeight)

	var elements []block.Segment
	for _, tab := range tabs {
		if tab == nil {
			continue
		}

		itemMinWidth := s.ItemMinWidth
		if itemMinWidth == 0 {
			itemMinWidth = absoluteMinWidth
		}
		if s.Narrow {
			elements = append(elements, block.Segment{
				BaseSize: itemMinWidth,
				Widget: func(gtx layout.Context) layout.Dimensions {
					isActive := tab == s.Active
					return s.layoutItem(gtx, tab, isActive)
				},
			})
		} else {
			elements = append(elements, block.Segment{
				BaseSize: itemMinWidth,
				Flex:     1,
				Widget: func(gtx layout.Context) layout.Dimensions {
					isActive := tab == s.Active
					return s.layoutItem(gtx, tab, isActive)
				},
			})
		}
	}

	if trailingWidget != nil {
		elements = append(elements, block.NewSegment(trailingWidget).AlignMiddle())
	}

	macroOp := op.Record(gtx.Ops)
	var widgetDimensions layout.Dimensions
	if s.Narrow {
		widgetDimensions = block.Line{
			Axis:     block.AxisHorizontal,
			Overflow: block.OverflowClip,
		}.Layout(gtx, elements...)
	} else {
		widgetDimensions = block.Line{
			Axis:     block.AxisHorizontal,
			Overflow: block.OverflowWrap,
			Expand:   true,
		}.Layout(gtx, elements...)
	}
	callOp := macroOp.Stop()

	// Paint the background.
	backgroundSize := image.Point{X: widgetDimensions.Size.X, Y: widgetDimensions.Size.Y}
	if !s.Narrow {
		backgroundSize.X = gtx.Constraints.Max.X
		widgetDimensions.Size.X = gtx.Constraints.Max.X
	}
	shapeArea := clip.Rect(image.Rectangle{Max: backgroundSize}).Op()
	paint.FillShape(gtx.Ops, s.tTheme.EnabledContainerColor.AsNRGBA(), shapeArea)

	callOp.Add(gtx.Ops)
	s.layoutSlidingIndicator(gtx, tabs, widgetDimensions)

	dividerStyle := divider.Divider()
	tOffset := image.Point{X: 0, Y: widgetDimensions.Size.Y - dividerStyle.Thickness(gtx)}
	transformStack := op.Offset(tOffset).Push(gtx.Ops)
	dividerStyle.Layout(gtx)
	transformStack.Pop()

	return widgetDimensions
}

func (s *widgetStyle) layoutSecondary(gtx layout.Context, tabs []*Item, trailingWidget layout.Widget) layout.Dimensions {
	return s.layoutPrimary(gtx, tabs, trailingWidget)
}

func (s *widgetStyle) layoutItem(gtx layout.Context, i *Item, active bool) layout.Dimensions {
	anim := i.getAnimation()
	gtx.Constraints.Min.Y = gtx.Dp(s.tTheme.EnabledContainerHeight)
	gtx.Constraints.Min.X = max(gtx.Dp(s.ItemMinWidth), gtx.Constraints.Min.X, gtx.Dp(absoluteMinWidth))

	var tabElements []block.Segment
	showCloseButton := active && i.CloseLabel != ""
	labelElement := block.NewSegment(func(gtx layout.Context) layout.Dimensions {
		c := block.Container{
			Gravity: block.GravityMiddleCenter,
			MinSize: image.Point{
				X: gtx.Dp(s.ItemMinWidth),
				Y: gtx.Dp(s.tTheme.EnabledContainerHeight),
			},
			MaxSize: image.Point{
				X: gtx.Dp(s.ItemMaxWidth),
			},
		}
		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			p := block.Padding{Start: horizontalPadding, End: horizontalPadding}
			if showCloseButton {
				p.End = 0
			}
			return p.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				semantic.LabelOp(i.Label).Add(gtx.Ops)
				labelColor := s.tTheme.EnabledLabelActiveColor
				if !active {
					labelColor = s.tTheme.EnabledLabelInactiveColor
				}
				labelColor = anim.label.Animate(gtx, labelColor)
				presentation := wdk.LabelStyle{
					Typestyle: token.TypestyleLabelMediumEmphasized,
					Color:     labelColor,
					MaxLines:  1,
				}
				return wdk.LayoutLabel(gtx, presentation, i.Label)
			})
		})
	}).AlignMiddle()
	tabElements = append(tabElements, labelElement)
	if showCloseButton {
		tabElements = append(tabElements, block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return i.CloseButton.LayoutIconOnly(gtx, i.CloseLabel, navigationCloseIcon)
		}).AlignMiddle())
	}

	macroOp := op.Record(gtx.Ops)
	labelDimensions := block.Line{
		Axis:     block.AxisHorizontal,
		Overflow: block.OverflowClip,
	}.Layout(gtx, tabElements...)
	callOp := macroOp.Stop()

	indicatorTarget := float32(0)
	if active {
		indicatorTarget = 1
	}
	indicatorProgress := anim.indicator.Animate(gtx, indicatorTarget)
	s.itemWidths[i] = labelDimensions.Size.X
	layoutContent := func(gtx layout.Context) layout.Dimensions {
		if s.wrapped {
			s.layoutActiveIndicator(gtx, labelDimensions, indicatorProgress)
		}
		callOp.Add(gtx.Ops)
		return labelDimensions
	}
	if !active {
		itemState := i.getItemState(gtx)
		if itemState == Hovered || itemState == Pressed {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		semantic.Button.Add(gtx.Ops)
		s.layoutStateLayer(gtx, i, anim, labelDimensions, itemState, active)
		return i.Clickable.Layout(gtx, layoutContent)
	}
	// The active tab is not clickable; let a leftover highlight fade out.
	s.layoutStateLayer(gtx, i, anim, labelDimensions, Enabled, active)
	return layoutContent(gtx)

}

func (s *widgetStyle) layoutStateLayer(gtx layout.Context, i *Item, anim *animation, labelDimensions layout.Dimensions, itemState widgetState, active bool) {
	ripples := wdk.RipplesEnabled(gtx)
	if ripples && itemState == Pressed {
		// The ripple shows the press; keep the hover highlight under it.
		itemState = Enabled
		if i.Clickable.Hovered() {
			itemState = Hovered
		}
	}
	stateLayerColor := s.tTheme.FocusedStateLayerActiveColor.SetOpacity(0)
	if itemState == Hovered {
		if active {
			stateLayerColor = s.tTheme.HoveredStateLayerActiveColor.SetOpacity(s.tTheme.HoveredStateLayerActiveOpacity)
		} else {
			stateLayerColor = s.tTheme.HoveredStateLayerInactiveColor.SetOpacity(s.tTheme.HoveredStateLayerInactiveOpacity)
		}
	}
	if itemState == Focused {
		if active {
			stateLayerColor = s.tTheme.FocusedStateLayerActiveColor.SetOpacity(s.tTheme.FocusedStateLayerActiveOpacity)
		} else {
			stateLayerColor = s.tTheme.FocusedStateLayerInactiveColor.SetOpacity(s.tTheme.FocusedStateLayerInactiveOpacity)
		}
	}
	if itemState == Pressed {
		if active {
			stateLayerColor = s.tTheme.PressedStateLayerActiveColor.SetOpacity(s.tTheme.PressedStateLayerActiveOpacity)
		} else {
			stateLayerColor = s.tTheme.PressedStateLayerInactiveColor.SetOpacity(s.tTheme.PressedStateLayerInactiveOpacity)
		}
	}
	stateLayerColor = anim.stateLayer.Animate(gtx, stateLayerColor)
	shape := wdk.Box{
		EndPoint: labelDimensions.Size,
		Shape:    wdk.FromCornerShapesToken(gtx, s.tTheme.EnabledContainerShape),
	}
	if stateLayerColor.A > 0 {
		paint.FillShape(gtx.Ops, stateLayerColor.AsNRGBA(), shape.Outline(gtx))
	}
	if ripples {
		// A pressed tab becomes active, so the ripple is drawn for both.
		rippleColor := s.tTheme.PressedStateLayerInactiveColor.SetOpacity(s.tTheme.PressedStateLayerInactiveOpacity)
		if active {
			rippleColor = s.tTheme.PressedStateLayerActiveColor.SetOpacity(s.tTheme.PressedStateLayerActiveOpacity)
		}
		wdk.Ripple{
			Color:  rippleColor,
			Bounds: image.Rectangle{Max: labelDimensions.Size},
			Clip:   func() clip.Op { return shape.Outline(gtx) },
		}.Draw(gtx, i.Clickable.History())
	}
}

// layoutSlidingIndicator draws the active indicator of a single row of tabs,
// moving it between tabs. The tab positions are rebuilt from their widths,
// as block.Line places segments next to each other.
func (s *widgetStyle) layoutSlidingIndicator(gtx layout.Context, tabs []*Item, lineDims layout.Dimensions) {
	itemHeight := gtx.Dp(s.tTheme.EnabledContainerHeight)
	s.Group.wrapped = lineDims.Size.Y > itemHeight
	if s.Group.wrapped {
		return
	}
	offset, width, found := 0, 0, false
	for _, tab := range tabs {
		if tab == s.Active {
			width, found = s.itemWidths[tab], true
			break
		}
		offset += s.itemWidths[tab]
	}
	if !found {
		return
	}
	if gtx.Locale.Direction == system.RTL {
		offset = lineDims.Size.X - offset - width
	}
	padding := gtx.Dp(horizontalPadding)
	s.Group.indicatorX.Duration = token.DurationMedium2
	s.Group.indicatorWidth.Duration = token.DurationMedium2
	x := s.Group.indicatorX.Animate(gtx, float32(offset+padding))
	w := s.Group.indicatorWidth.Animate(gtx, float32(width-padding*2))
	if w <= 0 {
		return
	}
	indicatorHeight := gtx.Dp(s.tTheme.EnabledActiveIndicatorHeight)
	baseBox := wdk.Box{
		Shape:    wdk.FromCornerShapesToken(gtx, s.tTheme.EnabledActiveIndicatorShape),
		EndPoint: image.Pt(int(w+0.5), indicatorHeight),
	}
	transformStack := op.Offset(image.Pt(int(x+0.5), itemHeight-indicatorHeight)).Push(gtx.Ops)
	paint.FillShape(gtx.Ops, s.tTheme.EnabledActiveIndicatorColor.AsNRGBA(), baseBox.Outline(gtx))
	transformStack.Pop()
}

// layoutActiveIndicator draws the indicator at progress of its full width,
// growing from the center.
func (s *widgetStyle) layoutActiveIndicator(gtx layout.Context, labelDimensions layout.Dimensions, progress float32) {
	fullWidth := labelDimensions.Size.X - gtx.Dp(horizontalPadding)*2
	width := int(float32(fullWidth)*progress + 0.5)
	if width <= 0 {
		return
	}
	activeIndicatorSize := image.Point{
		X: width,
		Y: gtx.Dp(s.tTheme.EnabledActiveIndicatorHeight),
	}
	baseBox := wdk.Box{
		Shape:    wdk.FromCornerShapesToken(gtx, s.tTheme.EnabledActiveIndicatorShape),
		EndPoint: activeIndicatorSize,
	}

	tOffset := image.Point{
		X: gtx.Dp(horizontalPadding) + (fullWidth-width)/2,
		Y: labelDimensions.Size.Y - activeIndicatorSize.Y,
	}
	transformStack := op.Offset(tOffset).Push(gtx.Ops)
	paint.FillShape(
		gtx.Ops,
		s.tTheme.EnabledActiveIndicatorColor.AsNRGBA(),
		baseBox.Outline(gtx),
	)
	transformStack.Pop()
}
