// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"sync"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
	"komarugram/pkg/resample"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// avatarSprites keep avatars as pictures, for what draws them otherwise
// than layoutAvatar, as motion blur does: a photo cut to the avatars'
// shape, or the initials, which the GPU draws once off the frame, as
// layoutAvatar does.
type avatarSprites struct {
	// photo is the last photo cut, for the source and the size it was
	// cut from and to.
	photo struct {
		src        image.Image
		px, radius int
		out        *image.RGBA
	}
	mu       sync.Mutex
	initials map[initialsKey]*image.RGBA
	pending  map[initialsKey]bool
}

// initialsKey is an avatar of initials as it is drawn.
type initialsKey struct {
	id         int64
	kind       model.ChatKind
	title      string
	px, radius int
}

// spriteStep is how sizes are rounded up for an avatar's picture, which
// is scaled to the size it is drawn at: an avatar that shrinks is not
// drawn again every frame.
const spriteStep = 16

// avatarRadius is the radius of the corners of an avatar of side px, as
// avatarShape cuts it.
func avatarRadius(gtx layout.Context, px int) int {
	corners := min(max(lookOf(gtx).AvatarCorners, 0), preferences.AvatarRound)
	return px / 2 * corners / preferences.AvatarRound
}

// avatarSprite is id's avatar as a picture of side px: see avatarSprites.
// The initials come a few frames later, the first time; nil until then.
func (a *App) avatarSprite(gtx layout.Context, id int64, kind model.ChatKind, title string, px int) image.Image {
	if px <= 0 {
		return nil
	}
	base := (px + spriteStep - 1) / spriteStep * spriteStep
	radius := avatarRadius(gtx, base)
	s := &a.sprites
	var sprite *image.RGBA
	if photo := a.photoOf(id, kind); photo != nil {
		if s.photo.src != photo || s.photo.px != base || s.photo.radius != radius {
			s.photo.src, s.photo.px, s.photo.radius = photo, base, radius
			s.photo.out = roundedSprite(photo, base, radius)
		}
		sprite = s.photo.out
	} else {
		key := initialsKey{id, kind, title, base, radius}
		s.mu.Lock()
		sprite = s.initials[key]
		start := sprite == nil && !s.pending[key]
		if start {
			if s.pending == nil {
				s.pending, s.initials = map[initialsKey]bool{}, map[initialsKey]*image.RGBA{}
			}
			s.pending[key] = true
		}
		s.mu.Unlock()
		if start {
			a.drawInitials(gtx, key)
		}
	}
	switch {
	case sprite == nil:
		// Not the nil *image.RGBA, which an image.Image is not nil of.
		return nil
	case base == px:
		return sprite
	}
	return resample.Resize(sprite, px, px)
}

// photoOf is the photo of id's avatar, which a chat of kind may have.
func (a *App) photoOf(id int64, kind model.ChatKind) image.Image {
	if a.avatars == nil || kind == model.KindSaved {
		return nil
	}
	return a.avatarPhoto(id)
}

// drawInitials has the GPU draw the avatar of key, off the frame, for
// avatarSprite.
func (a *App) drawInitials(gtx layout.Context, key initialsKey) {
	ops := new(op.Ops)
	sgtx := gtx
	sgtx.Ops = ops
	sgtx.Source = input.Source{}
	size := image.Pt(key.px, key.px)
	sgtx.Constraints = layout.Exact(size)
	avatar(sgtx, key.id, key.kind, key.title, unit.Dp(float32(key.px)/gtx.Metric.PxPerDp))
	s, invalidate := &a.sprites, a.window.Invalidate
	go func() {
		defer crash.Recover("avatar sprite", func(*crash.Panic) {})
		im, err := renderSnapshot(ops, size)
		s.mu.Lock()
		defer s.mu.Unlock()
		// A failed one is not tried again: the avatar moves unblurred.
		if err == nil {
			// The few avatars an info shows; others are dropped.
			if len(s.initials) >= 16 {
				clear(s.initials)
			}
			s.initials[key] = im
			delete(s.pending, key)
		}
		invalidate()
	}()
}
