// SPDX-License-Identifier: Unlicense OR MIT

// Command hint-images draws the handwritten hints the client shows as
// pictures, so that it carries no handwritten font of its own: for each
// language it comes with, the text of the key, in the font given, beside
// the arrow drawn by hand of arrow.png (dark on a clear background), which
// points to the left. They are white on a clear background, which the
// client tints. The collectible username's, which points to a lone
// collectible name in a profile, is drawn in Caveat
// (https://github.com/googlefonts/caveat):
//
//	go run ./cmd/hint-images -font Caveat.ttf
//
// writes internal/messenger/ui/hints/collectible_<language>.png. The
// pictures are drawn at hintScale pixels to a dp. The arrow's tip and the
// middle of the text's small letters are on one line, the picture's
// middle: the client puts it at the middle of the name it points to
// (internal/messenger/ui/collectible_hint.go).
package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"

	"komarugram/internal/messenger/localization"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/font/opentype"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
)

// arrow is the arrow, its darkness in the alpha.
//
//go:embed arrow.png
var arrow []byte

const (
	// hintScale is the pictures' pixels to a dp: enough for a screen of
	// three pixels to a dp.
	hintScale = 3
	// arrowWidth is the width the arrow is drawn at, and arrowGap is
	// between it and the text, in dp.
	arrowWidth = 42
	arrowGap   = 6
)

func main() {
	fontPath := flag.String("font", "", "the handwritten font file to draw the text in")
	out := flag.String("out", filepath.Join("internal", "messenger", "ui", "hints"), "the directory to write the pictures to")
	size := flag.Float64("size", 24, "the text's size, in sp")
	turn := flag.Float64("arrow-turn", 10, "how far to turn the arrow clockwise about its tip, in degrees, for its tail to point to the text")
	weight := flag.Float64("arrow-weight", 1, "how much thicker to draw the arrow's line, in arrow.png's pixels either side, to match the text's")
	flag.Parse()
	if *fontPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	data, err := os.ReadFile(*fontPath)
	if err != nil {
		log.Fatal(err)
	}
	faces, err := opentype.ParseCollection(data)
	if err != nil {
		log.Fatal(err)
	}
	mask, err := png.Decode(bytes.NewReader(arrow))
	if err != nil {
		log.Fatal(err)
	}
	h := hint{shaper: text.NewShaper(text.NoSystemFonts(), text.WithCollection(faces)), face: faces[0].Font, size: unit.Sp(*size), arrow: whiteOf(thicker(mask, *weight)), turn: float32(*turn * math.Pi / 180)}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	for _, lang := range localization.Languages() {
		img, err := h.draw(localization.For(string(lang)).T("info.collectible_hint"))
		if err != nil {
			log.Fatal(err)
		}
		path := filepath.Join(*out, fmt.Sprintf("collectible_%s.png", lang))
		if err := writePNG(path, img); err != nil {
			log.Fatal(err)
		}
		fmt.Println(path, img.Bounds().Size())
	}
}

type hint struct {
	shaper *text.Shaper
	face   font.Font
	size   unit.Sp
	arrow  *image.NRGBA
	// turn is how far the arrow is turned about its tip, clockwise, in
	// radians.
	turn float32
}

func context(ops *op.Ops, size image.Point) layout.Context {
	return layout.Context{Ops: ops, Constraints: layout.Constraints{Max: size}, Metric: unit.Metric{PxPerDp: hintScale, PxPerSp: hintScale}}
}

func (h hint) label(gtx layout.Context, txt string) layout.Dimensions {
	material := op.Record(gtx.Ops)
	paint.ColorOp{Color: color.NRGBA{R: 255, G: 255, B: 255, A: 255}}.Add(gtx.Ops)
	return widget.Label{MaxLines: 1}.Layout(gtx, h.shaper, h.face, h.size, txt, material.Stop())
}

// middle is how far below the top of a line of text the middle of its
// small letters is: halfway from the top of an x to the baseline.
func (h hint) middle() (int, error) {
	dims := h.label(context(new(op.Ops), image.Pt(1<<20, 1<<20)), "x")
	img, err := render(dims.Size, func(ops *op.Ops) { h.label(context(ops, dims.Size), "x") })
	if err != nil {
		return 0, err
	}
	top, _ := inkRows(img)
	return (top + dims.Size.Y - dims.Baseline) / 2, nil
}

// draw draws txt right of the arrow, the arrow's tip and the middle of
// txt's small letters on the picture's middle.
func (h hint) draw(txt string) (*image.RGBA, error) {
	dp := hintScale
	textSize := h.label(context(new(op.Ops), image.Pt(1<<20, 1<<20)), txt).Size
	textMiddle, err := h.middle()
	if err != nil {
		return nil, err
	}
	arrowSize := h.arrow.Rect.Size()
	scale := float32(arrowWidth*dp) / float32(arrowSize.X)
	// The arrow is turned about its tip, so that its tail points to the
	// text; what it covers then is measured on what is drawn of it.
	tipX, tipY := tip(h.arrow)
	at := f32.Pt((float32(tipX)+.5)*scale, (float32(tipY)+.5)*scale)
	turn := f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(scale, scale)).Rotate(at, h.turn)
	drawn := inkBounds(h.arrow, turn)
	// The line both are on is as far from the top as from the bottom.
	middle := int(math.Ceil(float64(max(float32(textMiddle), float32(textSize.Y-textMiddle), at.Y-drawn.Min.Y, drawn.Max.Y-at.Y))))
	textX := int(math.Ceil(float64(drawn.Dx()))) + arrowGap*dp
	size := image.Pt(textX+textSize.X+2*dp, 2*middle)
	return render(size, func(ops *op.Ops) {
		stack := op.Affine(turn.Offset(f32.Pt(-drawn.Min.X, float32(middle)-at.Y))).Push(ops)
		paint.NewImageOp(h.arrow).Add(ops)
		paint.PaintOp{}.Add(ops)
		stack.Pop()
		stack = op.Offset(image.Pt(textX, middle-textMiddle)).Push(ops)
		h.label(context(ops, size), txt)
		stack.Pop()
	})
}

// render draws what draw adds, in a picture of size, on a clear background.
func render(size image.Point, draw func(ops *op.Ops)) (*image.RGBA, error) {
	ops := new(op.Ops)
	draw(ops)
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		return nil, err
	}
	defer win.Release()
	if err := win.Frame(ops); err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		return nil, err
	}
	return img, nil
}

// inkRows are the first and the last row of img with anything drawn.
func inkRows(img *image.RGBA) (top, bottom int) {
	top, bottom = -1, -1
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			if img.RGBAAt(x, y).A > 64 {
				if top < 0 {
					top = y
				}
				bottom = y
				break
			}
		}
	}
	return top, bottom
}

// tip is where the arrow's tip is: the middle of what is drawn in its
// leftmost column.
func tip(im *image.NRGBA) (x, y int) {
	for x := im.Rect.Min.X; x < im.Rect.Max.X; x++ {
		sum, n := 0, 0
		for y := im.Rect.Min.Y; y < im.Rect.Max.Y; y++ {
			if im.NRGBAAt(x, y).A > 128 {
				sum, n = sum+y, n+1
			}
		}
		if n > 0 {
			return x, sum / n
		}
	}
	return 0, im.Rect.Dy() / 2
}

// bounds are bounds in fractions of pixels.
type bounds struct{ Min, Max f32.Point }

func (b bounds) Dx() float32 { return b.Max.X - b.Min.X }

// inkBounds are the bounds of what is drawn of im, by t.
func inkBounds(im *image.NRGBA, t f32.Affine2D) bounds {
	var r bounds
	first := true
	for y := im.Rect.Min.Y; y < im.Rect.Max.Y; y++ {
		for x := im.Rect.Min.X; x < im.Rect.Max.X; x++ {
			if im.NRGBAAt(x, y).A <= 64 {
				continue
			}
			for _, c := range []f32.Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 0, Y: 1}, {X: 1, Y: 1}} {
				p := t.Transform(f32.Pt(float32(x)+c.X, float32(y)+c.Y))
				if first {
					r = bounds{Min: p, Max: p}
					first = false
				}
				r.Min = f32.Pt(min(r.Min.X, p.X), min(r.Min.Y, p.Y))
				r.Max = f32.Pt(max(r.Max.X, p.X), max(r.Max.Y, p.Y))
			}
		}
	}
	return r
}

// thicker is im with what is drawn grown by radius pixels: each pixel
// takes the most alpha within radius of it.
func thicker(im image.Image, radius float64) image.Image {
	if radius <= 0 {
		return im
	}
	b := im.Bounds()
	r := int(math.Ceil(radius))
	out := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			var most uint32
			for dy := -r; dy <= r; dy++ {
				for dx := -r; dx <= r; dx++ {
					if math.Hypot(float64(dx), float64(dy)) > radius || !image.Pt(x+dx, y+dy).In(b) {
						continue
					}
					_, _, _, a := im.At(x+dx, y+dy).RGBA()
					most = max(most, a)
				}
			}
			out.SetNRGBA(x, y, color.NRGBA{A: uint8(most >> 8)})
		}
	}
	return out
}

// whiteOf is im's alpha in white.
func whiteOf(im image.Image) *image.NRGBA {
	b := im.Bounds()
	out := image.NewNRGBA(image.Rectangle{Max: b.Size()})
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := im.At(x, y).RGBA()
			out.SetNRGBA(x-b.Min.X, y-b.Min.Y, color.NRGBA{R: 255, G: 255, B: 255, A: uint8(a >> 8)})
		}
	}
	return out
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
