// SPDX-License-Identifier: Unlicense OR MIT

package rail

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"image"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

type ButtonStyle struct {
	Icon  wdk.IconWidget
	Label string

	active   bool
	button   *Button
	expanded bool
	theme    *Theme
}

type Button struct {
	Clickable widget.Clickable
	Data      any
	animation animation
}

// animation holds the animated drawing parameters of a rail button.
type animation struct {
	initialized bool
	indicator   wdk.ColorTween
	label       wdk.ColorTween
	stateLayer  wdk.ColorTween
}

func (b *Button) getAnimation() *animation {
	a := &b.animation
	if !a.initialized {
		a.initialized = true
		a.indicator.Duration = token.DurationMedium1
		a.stateLayer.Duration = token.DurationShort3
	}
	return a
}

func (b *Button) layout(gtx layout.Context, bStyle *ButtonStyle) layout.Dimensions {
	return b.Clickable.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		if bStyle.expanded {
			return b.layoutExpanded(gtx, bStyle)
		} else {
			return b.layoutCompact(gtx, bStyle)
		}
	})
}

func (b *Button) layoutCompact(gtx layout.Context, bStyle *ButtonStyle) layout.Dimensions {
	return block.Line{
		Axis:     block.AxisVertical,
		Overflow: block.OverflowClip,
	}.Layout(gtx,
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return bStyle.Icon(gtx, bStyle.theme.ItemInactiveLabelColor)
		}).AlignMiddle(),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			lStyle := wdk.LabelStyle{
				Color:     bStyle.theme.ItemInactiveLabelColor,
				MaxLines:  1,
				Typestyle: token.TypestyleLabelMedium,
			}
			return wdk.LayoutLabel(gtx, lStyle, bStyle.Label)
		}).AlignMiddle(),
	)
}

func (b *Button) layoutExpanded(gtx layout.Context, bStyle *ButtonStyle) layout.Dimensions {
	anim := b.getAnimation()
	theme := bStyle.theme
	indicatorColor := theme.ItemActiveIndicatorColor.SetOpacity(0)
	labelColor := theme.ItemInactiveLabelColor
	if bStyle.active {
		indicatorColor = theme.ItemActiveIndicatorColor
		labelColor = theme.ItemActiveLabelTextColor
	}
	ripples := wdk.RipplesEnabled(gtx)
	stateLayerColor := labelColor.SetOpacity(0)
	if b.Clickable.Pressed() && !ripples {
		stateLayerColor = labelColor.SetOpacity(theme.ItemPressedStateLayerOpacity)
	} else if b.Clickable.Hovered() {
		stateLayerColor = labelColor.SetOpacity(theme.ItemHoveredStateLayerOpacity)
	}
	indicatorColor = anim.indicator.Animate(gtx, indicatorColor)
	labelColor = anim.label.Animate(gtx, labelColor)
	stateLayerColor = anim.stateLayer.Animate(gtx, stateLayerColor)

	macroOp := op.Record(gtx.Ops)
	dims := block.Padding{
		Top:    unit.Dp(8),
		Bottom: unit.Dp(8),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return block.Line{
			Axis:     block.AxisHorizontal,
			Overflow: block.OverflowClip,
		}.Layout(gtx,
			block.NewHorizontalSpacer(theme.ItemActiveIndicatorLeadingSpace),
			block.NewSegment(func(gtx layout.Context) layout.Dimensions {
				return bStyle.Icon(gtx, labelColor)
			}).AlignMiddle(),
			block.NewHorizontalSpacer(theme.ItemActiveIndicatorIconLabelSpace),
			block.NewSegment(func(gtx layout.Context) layout.Dimensions {
				lStyle := wdk.LabelStyle{
					Color:     labelColor,
					MaxLines:  1,
					Typestyle: token.TypestyleLabelMedium,
				}
				return wdk.LayoutLabel(gtx, lStyle, bStyle.Label)
			}).AlignMiddle(),
			block.NewHorizontalSpacer(theme.ItemActiveIndicatorTrailingSpace),
		)
	})
	content := macroOp.Stop()

	box := wdk.Box{
		Shape:    wdk.FromCornerShapesToken(gtx, theme.ItemActiveIndicatorShape),
		EndPoint: dims.Size,
	}
	for _, c := range []token.MatColor{indicatorColor, stateLayerColor} {
		if c.A > 0 {
			paint.FillShape(gtx.Ops, c.AsNRGBA(), box.Outline(gtx))
		}
	}
	if ripples {
		wdk.Ripple{
			Color:  labelColor.SetOpacity(theme.ItemPressedStateLayerOpacity),
			Bounds: image.Rectangle{Max: dims.Size},
			Clip:   func() clip.Op { return box.Outline(gtx) },
		}.Draw(gtx, b.Clickable.History())
	}
	content.Add(gtx.Ops)
	return dims
}
