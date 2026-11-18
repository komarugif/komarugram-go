// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/messenger/model"
)

// avatarViewerTarget tracks the avatar as it was laid out, including the
// scale that the profile cover uses as the avatar moves into its header.
type avatarViewerTarget struct {
	widget.Clickable
	viewer  *mediaViewer
	release byte
	shape   clip.RRect
	scale   float32
	origin  image.Point
	located bool
}

func (t *avatarViewerTarget) Clicked(gtx layout.Context) bool {
	if t.viewer != nil {
		// Register the release filter before the gesture completes.
		t.origin, t.located = t.viewer.openPointer.origin(gtx, &t.release, t.scale)
	}
	return t.Clickable.Clicked(gtx)
}

func (t *avatarViewerTarget) layout(gtx layout.Context, scale float32, draw layout.Widget) layout.Dimensions {
	dims := t.Clickable.Layout(gtx, draw)
	if t.viewer != nil && gtx.Enabled() {
		t.shape, t.scale = avatarShape(gtx, dims.Size), scale
		trackViewerRelease(gtx, &t.release, dims.Size)
	}
	return dims
}

func (t *avatarViewerTarget) opacity(gtx layout.Context, animate bool) float32 {
	if t.viewer == nil {
		return 1
	}
	return t.viewer.opening.sourceOpacity(gtx, &t.Clickable, animate)
}

// viewerAvatar draws the avatar at an opening target. Other copies of the
// same avatar, for instance in the chat list, stay fully visible.
func (a *App) viewerAvatar(target *avatarViewerTarget) avatarLayout {
	target.viewer = a.viewer
	return func(gtx layout.Context, id int64, kind model.ChatKind, title string, size unit.Dp) layout.Dimensions {
		opacity := target.opacity(gtx, a.window.Motion.AnimationsEnabled())
		if opacity <= 0 {
			side := gtx.Dp(size)
			return layout.Dimensions{Size: image.Pt(side, side)}
		}
		if opacity < 1 {
			defer paint.PushOpacity(gtx.Ops, opacity).Pop()
		}
		return a.layoutAvatar(gtx, id, kind, title, size)
	}
}

// animateAvatarOpening seeds the viewer with the uncropped avatar photo.
// It can reuse that picture at once even without a pointer or animation.
func (a *App) animateAvatarOpening(gtx layout.Context, t *avatarViewerTarget, chat model.Chat, animate bool) {
	o := &a.viewer.opening
	im := a.photoOf(chat.ID, chat.Kind)
	o.tile.picture = im
	if !animate {
		return
	}
	o.start = gtx.Now
	if !t.located || im == nil || im.Bounds().Empty() {
		return
	}
	side := int(math.Round(float64(float32(t.shape.Rect.Dx()) * t.scale)))
	if side <= 0 {
		return
	}
	radius := min(side/2, int(math.Round(float64(float32(t.shape.NW)*t.scale))))
	bounds := image.Rectangle{Min: t.origin, Max: t.origin.Add(image.Pt(side, side))}
	o.source = &t.Clickable
	o.tile = viewerOpenTile{
		picture: im, shape: clip.UniformRRect(bounds, radius), visible: bounds,
		fit: widget.Cover, position: layout.NW, scaleCorners: true,
	}
}
