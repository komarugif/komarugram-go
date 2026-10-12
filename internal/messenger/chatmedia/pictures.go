// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import "image"

// ringPictures is how many pixel buffers an animation drawn by a worker
// draws its pictures into: its first picture, kept as its still, the one
// shown, the one left for the next frame, and one to draw.
const ringPictures = 4

// pictureRing gives a worker the pixels of a picture no longer shown to draw
// the next into, instead of new ones for each.
//
// Gio keeps a picture's texture by the picture's identity, and reads its
// pixels only when it first draws it; a texture not drawn in a frame is
// let go. So each picture is a new image, sharing the pixels of one that is
// neither shown, nor left for a frame, nor the still: for a view on the
// frame's clock only the window's goroutine changes which is shown, between
// frames, and a frame never draws again a picture it left. A view woken by
// the worker, which changes the picture shown itself, gets new pixels.
type pictureRing struct {
	pix [][]uint8
}

// next returns an image of size over free pixels, or nil when there are
// none to give.
func (r *pictureRing) next(m *Manager, e *entry, size image.Point) *image.RGBA {
	if !m.clocked.Load() {
		return nil
	}
	n := size.X * size.Y * 4
	e.mu.Lock()
	used := func(pix []uint8) bool {
		for _, im := range []image.Image{e.frame, e.pending, e.still} {
			if rgba, ok := im.(*image.RGBA); ok && rgba != nil && len(rgba.Pix) > 0 && &rgba.Pix[0] == &pix[0] {
				return true
			}
		}
		return false
	}
	var free []uint8
	for _, pix := range r.pix {
		if len(pix) == n && !used(pix) {
			free = pix
			break
		}
	}
	e.mu.Unlock()
	if free == nil {
		if len(r.pix) == ringPictures {
			return nil
		}
		free = make([]uint8, n)
		r.pix = append(r.pix, free)
	}
	return &image.RGBA{Pix: free, Stride: size.X * 4, Rect: image.Rectangle{Max: size}}
}
