// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"embed"
	"image"
	"image/color"
	"image/png"
	"sync"

	xdraw "golang.org/x/image/draw"

	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
)

// The hint beside a lone collectible username in the info: "collectible
// username" written by hand, with an arrow that points to the name. It is
// a picture of each language's, which cmd/hint-images draws, so that the
// client carries no handwritten font: white on a clear background, tinted
// here.
//
//go:embed hints/*.png
var hintFiles embed.FS

const (
	// hintScale is cmd/hint-images': the pictures' pixels to a dp. The
	// arrow's tip and the middle of the text are on their middle.
	hintScale = 3
	// hintGap is between the name and the arrow's tip.
	hintGap = unit.Dp(8)
)

// hintMasks are the pictures by language, nil for one with none, read
// once.
var hintMasks sync.Map // localization.Language → *image.Alpha

func collectibleHintMask(lang localization.Language) *image.Alpha {
	if m, ok := hintMasks.Load(lang); ok {
		return m.(*image.Alpha)
	}
	var mask *image.Alpha
	if data, err := hintFiles.ReadFile("hints/collectible_" + string(lang) + ".png"); err == nil {
		if im, err := png.Decode(bytes.NewReader(data)); err == nil {
			mask = image.NewAlpha(im.Bounds())
			xdraw.Draw(mask, mask.Rect, im, im.Bounds().Min, xdraw.Src)
		}
	}
	hintMasks.Store(lang, mask)
	return mask
}

// collectibleHint is what the info keeps of the hint drawn last: the
// picture at the screen's size, in its color.
type collectibleHint struct {
	lang  localization.Language
	px    float32
	color color.NRGBA
	image *image.NRGBA
}

// hintImage is the hint for l at gtx's scale in col, or nil when the
// language has none.
func (p *chatInfo) hintImage(gtx layout.Context, l localization.Catalog, col color.NRGBA) *image.NRGBA {
	h := &p.collectibleHint
	lang := l.Language()
	if h.image != nil && h.lang == lang && h.px == gtx.Metric.PxPerDp && h.color == col {
		return h.image
	}
	mask := collectibleHintMask(lang)
	if mask == nil {
		return nil
	}
	size := mask.Rect.Size()
	scaled := image.Pt(max(1, size.X*gtx.Dp(100)/(100*hintScale)), max(1, size.Y*gtx.Dp(100)/(100*hintScale)))
	small := image.NewAlpha(image.Rectangle{Max: scaled})
	xdraw.CatmullRom.Scale(small, small.Rect, mask, mask.Rect, xdraw.Src, nil)
	out := image.NewNRGBA(small.Rect)
	for i, a := range small.Pix {
		out.Pix[4*i] = col.R
		out.Pix[4*i+1] = col.G
		out.Pix[4*i+2] = col.B
		out.Pix[4*i+3] = uint8(uint16(a) * uint16(col.A) / 255)
	}
	*h = collectibleHint{lang: lang, px: gtx.Metric.PxPerDp, color: col, image: out}
	return out
}

// layoutCollectibleHint draws the hint right of a name of width, on a line
// of height, its arrow's tip and its text at the line's middle, if it fits
// in room. It tells whether it was drawn.
func (p *chatInfo) layoutCollectibleHint(gtx layout.Context, l localization.Catalog, name, height, room int) bool {
	im := p.hintImage(gtx, l, scheme(gtx).Primary.Color.AsNRGBA())
	if im == nil {
		return false
	}
	x := name + gtx.Dp(hintGap)
	size := im.Rect.Size()
	if x+size.X > room {
		return false
	}
	y := (height - size.Y) / 2
	offset(gtx, image.Pt(x, y), func(gtx layout.Context) layout.Dimensions {
		return drawImage(gtx, p.renderer.images, im, size)
	})
	return true
}
