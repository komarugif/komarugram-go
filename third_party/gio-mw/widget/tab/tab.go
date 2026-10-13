// SPDX-License-Identifier: Unlicense OR MIT

package tab

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/button"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
)

type Kind int

const (
	Primary Kind = iota
	Secondary
)

type Item struct {
	Label       string
	CloseLabel  string
	Icon        wdk.IconWidget
	Clickable   widget.Clickable
	CloseButton button.Button
	Screen      any
	animation   animation
}

// animation holds the animated drawing parameters of a tab.
type animation struct {
	initialized bool
	indicator   wdk.FloatTween // Width of the active indicator, 0 to 1.
	label       wdk.ColorTween
	stateLayer  wdk.ColorTween
}

func (i *Item) getAnimation() *animation {
	a := &i.animation
	if !a.initialized {
		a.initialized = true
		a.indicator.Duration = token.DurationMedium1
		a.stateLayer.Duration = token.DurationShort3
	}
	return a
}

func (i *Item) getItemState(gtx layout.Context) widgetState {
	// States are sorted in the order of their priority.
	if i.Clickable.Pressed() {
		return Pressed
	} else if i.Clickable.Hovered() {
		return Hovered
	} else if gtx.Focused(&i.Clickable) {
		return Focused
	}
	return Enabled
}

type Group struct {
	Items        []*Item
	Active       *Item
	Kind         Kind
	Narrow       bool
	ItemMinWidth unit.Dp
	ItemMaxWidth unit.Dp
	// TODO: Implement scrolling.

	// The active indicator slides between tabs unless they wrap into several
	// rows, as they did in the previous frame.
	indicatorX     wdk.FloatTween
	indicatorWidth wdk.FloatTween
	wrapped        bool
	// itemWidths are the widths of the tabs laid out in this frame, kept
	// between frames so that no frame makes a map of its own.
	itemWidths map[*Item]int
}

func (g *Group) Layout(gtx layout.Context, trailingWidget layout.Widget) layout.Dimensions {
	if g.itemWidths == nil {
		g.itemWidths = make(map[*Item]int, len(g.Items))
	}
	clear(g.itemWidths)
	style := &widgetStyle{
		Group:  g,
		tKind:  g.Kind,
		tTheme: BuildTheme(gtx),
	}
	return style.layout(gtx, trailingWidget)
}
