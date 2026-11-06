// SPDX-License-Identifier: Unlicense OR MIT

package photoedit

import (
	"image"
	"image/color"
	"math"
	"testing"
	"time"
)

var (
	red  = color.NRGBA{R: 255, A: 255}
	gray = color.RGBA{R: 128, G: 128, B: 128, A: 255}
)

func grayPicture(size image.Point) *image.RGBA {
	img := image.NewRGBA(image.Rectangle{Max: size})
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = gray.R, gray.G, gray.B, 255
	}
	return img
}

func wholeCanvas(size image.Point) Canvas { return CanvasOf(image.Rectangle{Max: size}, 1) }

func stroke(tool Tool, width float64, points ...Point) *Element {
	return &Element{Stroke: &Stroke{Tool: tool, Color: red, Width: width, Points: points}}
}

func TestTransformMapsBack(t *testing.T) {
	size := image.Pt(100, 60)
	for _, angle := range []int{0, 90, 180, 270} {
		for _, flip := range []bool{false, true} {
			tr := Transform{Crop: image.Rect(10, 5, 90, 55), Angle: angle, Flip: flip}
			p := Point{33, 21}
			back := tr.Unmap(tr.Map(p))
			if math.Abs(back.X-p.X) > 1e-9 || math.Abs(back.Y-p.Y) > 1e-9 {
				t.Errorf("angle %d flip %v: %v came back as %v", angle, flip, p, back)
			}
		}
	}
	if !New(size).Transform.Identity(size) {
		t.Error("a new scene has something done to it")
	}
}

// A pixel marked in the picture comes out where Map says, and where each turn
// and mirror should take it.
func TestTransformApply(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	src.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	cases := []struct {
		tr   Transform
		size image.Point
		at   image.Point
	}{
		{Transform{Crop: src.Rect}, image.Pt(4, 2), image.Pt(0, 0)},
		{Transform{Crop: src.Rect, Flip: true}, image.Pt(4, 2), image.Pt(3, 0)},
		{Transform{Crop: src.Rect, Angle: 90}, image.Pt(2, 4), image.Pt(1, 0)},
		{Transform{Crop: src.Rect, Angle: 180}, image.Pt(4, 2), image.Pt(3, 1)},
		{Transform{Crop: src.Rect, Angle: 270}, image.Pt(2, 4), image.Pt(0, 3)},
		{Transform{Crop: src.Rect, Angle: 90, Flip: true}, image.Pt(2, 4), image.Pt(1, 3)},
		{Transform{Crop: image.Rect(0, 0, 2, 1), Angle: 0}, image.Pt(2, 1), image.Pt(0, 0)},
	}
	for _, c := range cases {
		out := c.tr.Apply(src)
		if out.Rect.Size() != c.size {
			t.Errorf("%+v: size %v, want %v", c.tr, out.Rect.Size(), c.size)
			continue
		}
		if got := out.RGBAAt(c.at.X, c.at.Y); got.R != 255 {
			t.Errorf("%+v: the mark is not at %v", c.tr, c.at)
		}
	}
}

func TestEvenCrop(t *testing.T) {
	c := Transform{Crop: image.Rect(3, 5, 10, 8)}.Even().Crop
	if c.Min.X%2 != 0 || c.Min.Y%2 != 0 || c.Dx()%2 != 0 || c.Dy()%2 != 0 {
		t.Errorf("crop %v is not even", c)
	}
	if c = (Transform{Crop: image.Rect(3, 3, 3, 3)}).Even().Crop; c.Dx() < 2 || c.Dy() < 2 {
		t.Errorf("crop %v of nothing", c)
	}
}

// The marker is 35% in the values of the pixels, as FFmpeg and Telegram Desktop
// blend it: 172, 83, 83 over gray, not the 185, 104, 104 of linear light.
func TestMarkerBlendsInGamma(t *testing.T) {
	size := image.Pt(64, 64)
	out := Flatten(grayPicture(size), []*Element{stroke(Marker, 16, Point{0, 32}, Point{64, 32})}, wholeCanvas(size), nil, size)
	got := out.RGBAAt(32, 32)
	if abs(int(got.R)-172) > 1 || abs(int(got.G)-83) > 1 {
		t.Errorf("marker over gray is %v, want about 172, 83, 83", got)
	}
}

// A line that crosses itself covers once: the marker does not darken where it
// crosses, as within one stroke Telegram Desktop's does not.
func TestMarkerStrokeCoversOnce(t *testing.T) {
	size := image.Pt(64, 64)
	out := Flatten(grayPicture(size), []*Element{stroke(Marker, 8, Point{10, 32}, Point{54, 32}, Point{32, 10}, Point{32, 54})}, wholeCanvas(size), nil, size)
	cross, line := out.RGBAAt(32, 32), out.RGBAAt(15, 32)
	if cross != line {
		t.Errorf("where it crosses %v, along the line %v", cross, line)
	}
}

// Laying the layer over the picture gives what drawing on the picture gives:
// what a video gets is what the view shows.
func TestLayerOverPictureIsFlat(t *testing.T) {
	size := image.Pt(96, 96)
	s := New(size)
	s.Add(stroke(Pen, 10, Point{0, 20}, Point{96, 20}))
	s.Add(stroke(Marker, 10, Point{48, 0}, Point{48, 96}))
	s.Add(&Element{Shape: &Shape{Type: ShapeCircle, Color: color.NRGBA{B: 255, A: 255}, Width: 3}, Center: Point{60, 60}, Width: 40, Height: 40})
	want := Flatten(grayPicture(size), s.Elements, wholeCanvas(size), nil, size)
	steps := Layers(s, nil)
	if len(steps) != 1 || steps[0].Over == nil {
		t.Fatalf("steps: %+v", steps)
	}
	got := grayPicture(size)
	for i := 0; i < len(got.Pix); i += 4 {
		o := steps[0].Over.Pix[i : i+4]
		for c := range 3 {
			got.Pix[i+c] = mix(got.Pix[i+c], o[c], uint32(o[3]))
		}
	}
	for y := range size.Y {
		for x := range size.X {
			a, b := want.RGBAAt(x, y), got.RGBAAt(x, y)
			if abs(int(a.R)-int(b.R)) > 2 || abs(int(a.G)-int(b.G)) > 2 || abs(int(a.B)-int(b.B)) > 2 {
				t.Fatalf("at %d,%d the flat picture is %v, the layer over it %v", x, y, a, b)
			}
		}
	}
}

func TestEraserTakesFromStrokesOnly(t *testing.T) {
	size := image.Pt(64, 64)
	line := stroke(Pen, 10, Point{0, 32}, Point{64, 32})
	sticker := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for i := 0; i < len(sticker.Pix); i += 4 {
		sticker.Pix[i+1], sticker.Pix[i+3] = 255, 255
	}
	els := []*Element{line, {Sticker: &Sticker{Image: sticker}, Center: Point{50, 32}, Width: 16, Height: 16}, stroke(Eraser, 20, Point{32, 0}, Point{32, 64}), stroke(Eraser, 20, Point{50, 0}, Point{50, 64})}
	out := Flatten(grayPicture(size), els, wholeCanvas(size), nil, size)
	if got := out.RGBAAt(32, 32); got != gray {
		t.Errorf("under the eraser the stroke left %v", got)
	}
	if got := out.RGBAAt(10, 32); got.R != 255 || got.G != 0 {
		t.Errorf("beside the eraser the stroke is %v", got)
	}
	if got := out.RGBAAt(50, 32); got.G != 255 {
		t.Errorf("the eraser took the sticker: %v", got)
	}
}

// A blur blurs what is under it, and what comes after is sharp; a video gets
// the same two passes.
func TestBlurOrder(t *testing.T) {
	size := image.Pt(128, 64)
	s := New(size)
	s.Add(stroke(Pen, 4, Point{0, 32}, Point{128, 32}))
	s.Add(stroke(Blur, 10, Point{0, 32}, Point{64, 32}))
	s.Add(stroke(Pen, 4, Point{0, 12}, Point{128, 12}))
	out := Flatten(grayPicture(size), s.Elements, wholeCanvas(size), nil, size)
	if got := out.RGBAAt(30, 32); got.R == 255 {
		t.Errorf("the line under the blur stays sharp: %v", got)
	}
	if got := out.RGBAAt(100, 32); got.R != 255 {
		t.Errorf("the line beside the blur changed: %v", got)
	}
	if got := out.RGBAAt(30, 12); got.R != 255 {
		t.Errorf("the line over the blur is blurred: %v", got)
	}
	steps := Layers(s, nil)
	if len(steps) != 3 || steps[0].Over == nil || steps[1].Blur == nil || steps[2].Over == nil {
		t.Fatalf("steps: %+v", steps)
	}
	if steps[1].Radius != BlurRadius(size) {
		t.Errorf("radius %d", steps[1].Radius)
	}
}

func TestBlursInARowAreOne(t *testing.T) {
	size := image.Pt(64, 64)
	s := New(size)
	s.Add(stroke(Blur, 5, Point{0, 10}, Point{64, 10}))
	s.Add(stroke(Blur, 5, Point{0, 50}, Point{64, 50}))
	steps := Layers(s, nil)
	if len(steps) != 1 || steps[0].Blur == nil {
		t.Fatalf("steps: %+v", steps)
	}
	if steps[0].Blur.AlphaAt(30, 10).A == 0 || steps[0].Blur.AlphaAt(30, 50).A == 0 {
		t.Error("the mask lost a stroke")
	}
}

func TestUndoRedo(t *testing.T) {
	s := New(image.Pt(10, 10))
	if s.Changed() || s.CanUndo() {
		t.Fatal("a new scene has steps")
	}
	a, b := stroke(Pen, 1, Point{}), stroke(Pen, 1, Point{1, 1})
	s.Add(a)
	s.Add(b)
	s.Undo()
	if len(s.Elements) != 1 || s.Elements[0] != a {
		t.Fatalf("undone: %v", s.Elements)
	}
	s.Redo()
	if len(s.Elements) != 2 || s.Elements[1] != b {
		t.Fatalf("redone: %v", s.Elements)
	}
	s.Undo()
	s.Add(b)
	if s.CanRedo() {
		t.Error("a new step leaves the old one to redo")
	}
	// A step made of many changes undoes as one, and one of none is none.
	before := len(s.Elements)
	s.Begin()
	i := s.Add(stroke(Pen, 1, Point{}))
	s.Replace(i, stroke(Pen, 2, Point{}))
	s.Replace(i, stroke(Pen, 3, Point{}))
	s.End()
	s.Undo()
	if len(s.Elements) != before {
		t.Errorf("after undoing the step: %d elements, want %d", len(s.Elements), before)
	}
	s.Redo()
	steps := len(s.undo)
	s.Begin()
	s.End()
	if len(s.undo) != steps {
		t.Error("a step that changed nothing was kept")
	}
}

func TestAtPicksTopmostInTurnedBox(t *testing.T) {
	s := New(image.Pt(100, 100))
	s.Add(&Element{Shape: &Shape{}, Center: Point{50, 50}, Width: 60, Height: 10, Rotation: 90})
	if got := s.At(Point{50, 25}, 0); got != 0 {
		t.Errorf("along the turned box: %d", got)
	}
	if got := s.At(Point{25, 50}, 0); got != -1 {
		t.Errorf("where the box was before the turn: %d", got)
	}
	s.Add(&Element{Shape: &Shape{}, Center: Point{50, 25}, Width: 20, Height: 20})
	if got := s.At(Point{50, 25}, 0); got != 1 {
		t.Errorf("the one on top: %d", got)
	}
}

// Painted on a canvas that shows a part at another scale, a stroke falls where
// it falls in the picture.
func TestCanvasOfPart(t *testing.T) {
	size := image.Pt(200, 200)
	s := stroke(Pen, 10, Point{100, 100}, Point{100, 150})
	c := CanvasOf(image.Rect(50, 50, 150, 150), .5)
	base := grayPicture(c.Size())
	out := Flatten(base, []*Element{s}, c, nil, size)
	if got := out.RGBAAt(25, 40); got.R != 255 {
		t.Errorf("on the canvas: %v", got)
	}
	if got := out.RGBAAt(5, 5); got != gray {
		t.Errorf("off the stroke: %v", got)
	}
}

func TestPreviewMatchesFlatten(t *testing.T) {
	size := image.Pt(80, 80)
	base := grayPicture(size)
	var p Preview
	c := wholeCanvas(size)
	p.SetBase(base, c, size)
	list := []*Element{stroke(Pen, 6, Point{0, 10}, Point{80, 10})}
	check := func(name string, settled bool) {
		t.Helper()
		img, _ := p.Render(list, settled, nil)
		want := Flatten(base, list, c, nil, size)
		for i := range want.Pix {
			if want.Pix[i] != img.Pix[i] {
				t.Fatalf("%s: differs at byte %d", name, i)
			}
		}
	}
	check("first", true)
	list = append(list[:1:1], stroke(Pen, 6, Point{0, 30}, Point{80, 30}))
	check("a stroke added", false)
	list = append(list[:2:2], stroke(Eraser, 12, Point{40, 0}, Point{40, 80}))
	check("erased", true)
	list = list[:2:2]
	check("eraser undone", true)
	list = []*Element{list[1]}
	check("the first taken out", true)
	if _, changed := p.Render(list, true, nil); changed {
		t.Error("the same list drew anew")
	}
}

// Straighten undoes what Gio does to a translucent color: 188 is red in linear
// light at half alpha, encoded (measured on the GPU).
func TestStraighten(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 1))
	copy(img.Pix, []uint8{188, 0, 0, 128, 93, 93, 93, 128, 10, 20, 30, 255, 0, 0, 0, 0})
	out := Straighten(img)
	if got := out.NRGBAAt(0, 0); got != (color.NRGBA{R: 255, A: 128}) {
		t.Errorf("red: %v", got)
	}
	if got := out.NRGBAAt(1, 0); abs(int(got.R)-128) > 1 || got.A != 128 {
		t.Errorf("gray: %v", got)
	}
	if got := out.NRGBAAt(2, 0); got != (color.NRGBA{R: 10, G: 20, B: 30, A: 255}) {
		t.Errorf("opaque: %v", got)
	}
	if got := out.NRGBAAt(3, 0); got != (color.NRGBA{}) {
		t.Errorf("nothing: %v", got)
	}
}

func TestShapesCover(t *testing.T) {
	size := image.Pt(100, 100)
	for _, typ := range []ShapeType{ShapeCircle, ShapeRectangle, ShapeStar, ShapeBubble, ShapeArrow} {
		e := &Element{Shape: &Shape{Type: typ, Color: red, Filled: true, Width: 4}, Center: Point{50, 50}, Width: 60, Height: 60}
		if typ == ShapeArrow {
			e.Height = 10
		}
		out := Flatten(grayPicture(size), []*Element{e}, wholeCanvas(size), nil, size)
		if got := out.RGBAAt(50, 50); typ != ShapeBubble && got.R != 255 {
			t.Errorf("shape %d does not cover its middle: %v", typ, got)
		}
		if got := out.RGBAAt(2, 2); got != gray {
			t.Errorf("shape %d covers its surroundings: %v", typ, got)
		}
	}
	// Not filled, a circle leaves its middle.
	e := &Element{Shape: &Shape{Type: ShapeCircle, Color: red, Width: 4}, Center: Point{50, 50}, Width: 60, Height: 60}
	out := Flatten(grayPicture(size), []*Element{e}, wholeCanvas(size), nil, size)
	if got := out.RGBAAt(50, 50); got != gray {
		t.Errorf("an outline fills: %v", got)
	}
	if got := out.RGBAAt(80, 50); got.R != 255 {
		t.Errorf("an outline has no line: %v", got)
	}
}

// A sticker turned and mirrored comes out turned and mirrored.
func TestPictureInBox(t *testing.T) {
	size := image.Pt(100, 100)
	src := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	for y := range 40 {
		for x := range 40 {
			if x < 20 {
				src.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
			} else {
				src.SetNRGBA(x, y, color.NRGBA{B: 255, A: 255})
			}
		}
	}
	draw := func(e Element) *image.RGBA {
		e.Sticker = &Sticker{Image: src}
		e.Center, e.Width, e.Height = Point{50, 50}, 40, 40
		return Flatten(grayPicture(size), []*Element{&e}, wholeCanvas(size), nil, size)
	}
	if out := draw(Element{}); out.RGBAAt(40, 50).R != 255 || out.RGBAAt(60, 50).B != 255 {
		t.Error("as it is")
	}
	if out := draw(Element{Flipped: true}); out.RGBAAt(40, 50).B != 255 || out.RGBAAt(60, 50).R != 255 {
		t.Error("mirrored")
	}
	if out := draw(Element{Rotation: 90}); out.RGBAAt(50, 40).R != 255 || out.RGBAAt(50, 60).B != 255 {
		t.Error("turned")
	}
	if got := draw(Element{}).RGBAAt(10, 10); got != gray {
		t.Errorf("around it: %v", got)
	}
}

// An element placed while the picture is turned or mirrored comes out upright:
// its left stays to the left.
func TestUpright(t *testing.T) {
	size := image.Pt(100, 100)
	src := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	for y := range 40 {
		for x := range 20 {
			src.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
		for x := 20; x < 40; x++ {
			src.SetNRGBA(x, y, color.NRGBA{B: 255, A: 255})
		}
	}
	for _, angle := range []int{0, 90, 180, 270} {
		for _, flip := range []bool{false, true} {
			tr := Transform{Crop: image.Rectangle{Max: size}, Angle: angle, Flip: flip}
			rotation, flipped := tr.Upright()
			e := &Element{Sticker: &Sticker{Image: src}, Center: Point{50, 50}, Width: 40, Height: 40, Rotation: rotation, Flipped: flipped}
			out := tr.Apply(Flatten(grayPicture(size), []*Element{e}, wholeCanvas(size), nil, size))
			if out.RGBAAt(40, 50).R != 255 || out.RGBAAt(60, 50).B != 255 {
				t.Errorf("angle %d flip %v: left %v right %v", angle, flip, out.RGBAAt(40, 50), out.RGBAAt(60, 50))
			}
		}
	}
}

func TestTrimClamped(t *testing.T) {
	length := 10 * time.Second
	cases := []struct{ in, want Trim }{
		{Trim{}, Trim{}},
		{Trim{From: time.Second, To: 2 * time.Second}, Trim{From: time.Second, To: 2 * time.Second}},
		{Trim{To: length}, Trim{}},
		{Trim{To: 2 * length}, Trim{}},
		{Trim{From: 5 * time.Second, To: 5 * time.Second}, Trim{From: 5 * time.Second, To: 5*time.Second + MinTrim}},
		{Trim{From: length}, Trim{From: length - MinTrim}},
	}
	for _, c := range cases {
		if got := c.in.Clamped(length); got != c.want {
			t.Errorf("%+v: %+v, want %+v", c.in, got, c.want)
		}
	}
	if got := (Trim{From: time.Second, To: 3 * time.Second}).Length(length); got != 2*time.Second {
		t.Errorf("length %v", got)
	}
	if got := (Trim{From: time.Second}).Length(length); got != 9*time.Second {
		t.Errorf("length to the end %v", got)
	}
	if (Trim{}).Set() {
		t.Error("all of it is set")
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
