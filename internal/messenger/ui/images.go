// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"

	"gioui.org/op/paint"
)

type imageTexture struct {
	op   paint.ImageOp
	seen uint64
}

// ImageOp owns Gio's texture identity. Recreate it only when decoded pixels
// change, otherwise even an unchanged photo is converted/uploaded every frame.
type imageOps struct {
	generation uint64
	entries    map[image.Image]imageTexture
	colored    map[coloredKey]coloredImage
}

// coloredKey is a picture in a color: see Colored.
type coloredKey struct {
	im image.Image
	c  color.NRGBA
}

type coloredImage struct {
	im   image.Image
	seen uint64
}

// Colored is im's shape in c, its alpha kept, as Telegram Desktop draws a
// custom emoji of the text's color. One is made once while it is drawn,
// and forgotten with its texture.
func (c *imageOps) Colored(im image.Image, col color.NRGBA) image.Image {
	if c.colored == nil {
		c.colored = make(map[coloredKey]coloredImage)
	}
	key := coloredKey{im, col}
	out, ok := c.colored[key]
	if !ok {
		out.im = colorImage(im, col)
	}
	out.seen = c.generation
	c.colored[key] = out
	return out.im
}

// colorImage is im with every pixel c, of im's alpha times c's.
func colorImage(im image.Image, c color.NRGBA) image.Image {
	b := im.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			_, _, _, a := im.At(b.Min.X+x, b.Min.Y+y).RGBA()
			p := c
			p.A = uint8(uint32(c.A) * (a >> 8) / 255)
			out.SetNRGBA(x, y, p)
		}
	}
	return out
}

func (c *imageOps) BeginFrame() { c.generation++ }
func (c *imageOps) Op(im image.Image) paint.ImageOp {
	if c.entries == nil {
		c.entries = make(map[image.Image]imageTexture)
	}
	texture, ok := c.entries[im]
	if !ok {
		texture.op = paint.NewImageOp(im)
	}
	texture.seen = c.generation
	c.entries[im] = texture
	return texture.op
}

// Release forgets every image, so that a hidden window keeps no pixels
// alive through its texture handles.
func (c *imageOps) Release() { c.entries, c.colored = nil, nil }
func (c *imageOps) EndFrame() {
	for im, texture := range c.entries {
		if texture.seen+1 < c.generation {
			delete(c.entries, im)
		}
	}
	for key, colored := range c.colored {
		if colored.seen+1 < c.generation {
			delete(c.colored, key)
		}
	}
}
