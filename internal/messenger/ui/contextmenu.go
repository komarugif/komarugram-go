// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// menuCorner is the corner a context menu opens from: the one nearest to
// what opened it.
type menuCorner int

const (
	menuFromTopLeft menuCorner = iota
	menuFromTopRight
	menuFromBottomLeft
	menuFromBottomRight
)

// Context menus open as tdesktop's do: they fade in while they expand from
// their corner, and close the same way back, faster.
const (
	menuEnterDuration = token.DurationMedium1
	menuExitDuration  = token.DurationShort3
	// menuStartWidth and menuStartHeight are the part of the menu shown
	// when it starts to open.
	menuStartWidth  = 0.3
	menuStartHeight = 0.1
)

// contextMenu animates a panel that opens over the content from a corner:
// the emoji and sticker picker, the attachment menu and the like.
type contextMenu struct {
	visibility wdk.FloatTween
	height     heightTransition
	top        wdk.FloatTween
	bounds     image.Rectangle
	viewport   image.Point
	placed     bool
	anchor     image.Point
	corner     menuCorner
}

// Layout draws content in rect while the menu is open or animating out,
// reporting whether it drew. radius is the corner radius of the menu, which
// its expanding outline keeps.
func (m *contextMenu) Layout(gtx layout.Context, open bool, rect image.Rectangle, from menuCorner, radius int, content layout.Widget) bool {
	// Resizing the window is not a disclosure transition. Snap to the new
	// clamped rectangle, finishing any pending entrance at the same time.
	resized := !m.bounds.Empty() && m.viewport != gtx.Constraints.Max
	m.viewport = gtx.Constraints.Max
	motion := gtx
	if resized {
		motion.Now = time.Time{}
	}
	visibility := m.animate(motion, open)
	if visibility == 0 {
		m.height = heightTransition{}
		m.top = wdk.FloatTween{}
		m.bounds = image.Rectangle{}
		if !open {
			m.placed = false
		}
		return false
	}
	// Changing contents (reactions, fetched actions, picker rows) resize an
	// already-open menu too. Interpolate its top with its height so bottom-
	// anchored menus and their hit regions keep moving together.
	geometry := gtx
	geometry.Constraints.Max.X = rect.Dx()
	if resized || m.height.initialized && m.height.width != rect.Dx() {
		m.top = wdk.FloatTween{}
	}
	height := m.height.Value(geometry, rect.Dy(), !resized)
	m.top.Duration, m.top.Easing = heightDuration, &token.EasingStandard
	y := int(math.Round(float64(m.top.Animate(gtx, float32(rect.Min.Y)))))
	m.bounds = image.Rect(rect.Min.X, y, rect.Max.X, y+height)
	rect = m.bounds
	if !open {
		// A closing menu takes no input.
		gtx = gtx.Disabled()
	}
	if visibility == 1 {
		inRect(gtx, rect, content)
		return true
	}
	size := rect.Size()
	macro := op.Record(gtx.Ops)
	inRect(gtx, image.Rectangle{Max: size}, content)
	call := macro.Stop()

	shown := image.Pt(
		int(float32(size.X)*(menuStartWidth+(1-menuStartWidth)*visibility)+.5),
		int(float32(size.Y)*(menuStartHeight+(1-menuStartHeight)*visibility)+.5),
	)
	area := image.Rectangle{Max: shown}
	if from == menuFromTopRight || from == menuFromBottomRight {
		area = area.Add(image.Pt(size.X-shown.X, 0))
	}
	if from == menuFromBottomLeft || from == menuFromBottomRight {
		area = area.Add(image.Pt(0, size.Y-shown.Y))
	}
	defer op.Offset(rect.Min).Push(gtx.Ops).Pop()
	defer clip.UniformRRect(area, min(radius, shown.X/2, shown.Y/2)).Push(gtx.Ops).Pop()
	defer paint.PushOpacity(gtx.Ops, visibility).Pop()
	call.Add(gtx.Ops)
	return true
}

// animate returns how much the menu shows, from 0 to 1.
func (m *contextMenu) animate(gtx layout.Context, open bool) float32 {
	if m.visibility.Duration == 0 {
		// Start closed, so that the menu animates in when it first opens.
		m.visibility.Animate(gtx, 0)
	}
	target := float32(0)
	m.visibility.Duration = menuExitDuration
	m.visibility.Easing = &token.EasingEmphasizedAccelerate
	if open {
		target = 1
		m.visibility.Duration = menuEnterDuration
		m.visibility.Easing = &token.EasingEmphasizedDecelerate
	}
	return m.visibility.Animate(gtx, target)
}

// Place chooses the opening side once. Resizing an open menu must not flip
// it across the pointer: it grows against the window edge when it runs out
// of room. A new pointer anchor starts a fresh placement.
func (m *contextMenu) Place(gtx layout.Context, at, viewport, size image.Point) (image.Rectangle, menuCorner) {
	margin := gtx.Dp(8)
	size.X = min(max(0, size.X), max(0, viewport.X-2*margin))
	size.Y = min(max(0, size.Y), max(0, viewport.Y-2*margin))
	if !m.placed || m.anchor != at {
		m.placed, m.anchor, m.corner = true, at, menuFromTopLeft
		if at.X+size.X > viewport.X-margin {
			m.corner = menuFromTopRight
		}
		if at.Y+size.Y > viewport.Y-margin {
			m.corner += menuFromBottomLeft
		}
	}
	x, y := at.X, at.Y
	if m.corner == menuFromTopRight || m.corner == menuFromBottomRight {
		x -= size.X
	}
	if m.corner == menuFromBottomLeft || m.corner == menuFromBottomRight {
		y -= size.Y
	}
	x = max(margin, min(x, viewport.X-margin-size.X))
	y = max(margin, min(y, viewport.Y-margin-size.Y))
	return image.Rect(x, y, x+size.X, y+size.Y), m.corner
}
