// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"math"
	"sync"

	"golang.org/x/image/math/fixed"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/font"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/photoedit"
)

// A text's pictures are drawn by the GPU: only Gio shapes text, with the
// client's fonts and emoji. Its ops are recorded in a frame, which has the
// shaper, and drawn off it, as a snapshot of messages is.

// The proportions of a text's plate, Telegram Desktop's (scene_item_text.cpp).
const (
	textPadding   = .4
	textLinePad   = 1. / 3.
	textRadius    = 1. / 3.
	textMaxWidth  = .8
	textStartSize = .08
	// textBucket is the step of the font sizes the view's pictures are drawn
	// at, stretched between them: not one a frame while a text grows.
	textBucket = 1.19
	// textCacheBytes bounds the pictures kept while the editor is open.
	textCacheBytes = 96 << 20
)

// editText is the text being written, and the pictures of the texts.
type editText struct {
	editing bool
	editor  widget.Editor
	// at is the element written in.
	at int
	ok surface

	inbox *textInbox
	cache map[textKey]*image.NRGBA
	// latest is the last picture of a text, stretched until its size comes.
	latest map[textLook]*image.NRGBA
	asked  map[textKey]bool
	failed bool
	cached int
}

// textInbox brings the pictures drawn off the frame. It is one for each opening
// of the editor: one drawn after it closed goes to nobody.
type textInbox struct {
	mu   sync.Mutex
	came []textRaster
}

func (b *textInbox) give(r textRaster) {
	b.mu.Lock()
	b.came = append(b.came, r)
	b.mu.Unlock()
}

// textLook is what makes a text's picture but its size.
type textLook struct {
	text     string
	color    color.NRGBA
	style    photoedit.TextStyle
	typeface photoedit.TextTypeface
	align    photoedit.TextAlign
	wrap     float64
}

type textKey struct {
	look textLook
	px   int
}

type textRaster struct {
	key textKey
	img *image.NRGBA
	err error
}

func textLookOf(t *photoedit.Text) textLook {
	return textLook{t.Text, t.Color, t.Style, t.Typeface, t.Align, t.Wrap}
}

// textFont is the font of a text in the theme.
func textFont(th *token.Theme, t *photoedit.Text) font.Font {
	f := font.Font{Typeface: th.Typescale[token.TypestyleBodyLarge].Font, Weight: font.Medium}
	switch t.Typeface {
	case photoedit.TypefaceItalic:
		f.Style = font.Italic
	case photoedit.TypefaceSerif:
		f.Typeface = "Noto Serif, Liberation Serif, Georgia, Times New Roman, serif"
	case photoedit.TypefaceCondensed:
		f.Typeface = "Roboto Condensed, Noto Sans Condensed, Arial Narrow, " + f.Typeface
	case photoedit.TypefaceMonospace:
		f.Typeface = "Noto Sans Mono, Liberation Mono, Consolas, Menlo, monospace"
	}
	return f
}

// recordText records a text at a font of px pixels on ops, and returns the size
// of its picture: lines no wider than maxWidth, within its plate.
func recordText(gtx layout.Context, ops *op.Ops, t *photoedit.Text, px, maxWidth int) image.Point {
	th := wdk.GetMaterialTheme(gtx)
	shaper := th.TextShaper
	f := textFont(th, t)
	params := text.Parameters{Font: f, PxPerEm: fixed.I(px), MaxWidth: maxWidth, Locale: gtx.Locale}
	shaper.LayoutString(params, t.Text)
	type line struct {
		width    int
		top, bot int
		used     bool
	}
	var lines []line
	cur := line{}
	endLine := func() {
		lines = append(lines, cur)
		cur = line{}
	}
	for {
		g, ok := shaper.NextGlyph()
		if !ok {
			break
		}
		if !cur.used {
			cur.used = true
			cur.top, cur.bot = int(g.Y)-g.Ascent.Ceil(), int(g.Y)+g.Descent.Ceil()
		}
		cur.width = max(cur.width, (g.X + g.Advance).Ceil())
		if g.Flags&text.FlagLineBreak != 0 {
			endLine()
		}
	}
	if cur.used {
		endLine()
	}
	if len(lines) == 0 {
		return image.Point{}
	}
	width, height := 0, 0
	for _, l := range lines {
		width = max(width, l.width)
		height = max(height, l.bot)
	}
	plate, ink := t.Plate()
	pad := int(math.Ceil(float64(px) * textPadding))
	if plate.A == 0 {
		// What a glyph may reach beyond its advance.
		pad = max(1, px/8)
	}
	size := image.Pt(width+2*pad, height+2*pad)
	if plate.A != 0 {
		linePad, radius := int(float64(px)*textLinePad), int(float64(px)*textRadius)
		for _, l := range lines {
			x := pad
			switch t.Align {
			case photoedit.AlignCenter:
				x += (width - l.width) / 2
			case photoedit.AlignRight:
				x += width - l.width
			}
			r := image.Rect(x-linePad, pad+l.top, x+l.width+linePad, pad+l.bot).Intersect(image.Rectangle{Max: size})
			paint.FillShape(ops, plate, clip.UniformRRect(r, min(radius, r.Dy()/2)).Op(ops))
		}
	}
	lgtx := layout.Context{Ops: ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Now: gtx.Now, Locale: gtx.Locale, Values: gtx.Values,
		Constraints: layout.Exact(image.Pt(width, height))}
	shift := op.Offset(image.Pt(pad, pad)).Push(ops)
	colorMacro := op.Record(ops)
	paint.ColorOp{Color: ink}.Add(ops)
	colorCall := colorMacro.Stop()
	alignment := text.Middle
	switch t.Align {
	case photoedit.AlignLeft:
		alignment = text.Start
	case photoedit.AlignRight:
		alignment = text.End
	}
	widget.Label{Alignment: alignment}.Layout(lgtx, shaper, f, unit.Sp(float32(px)), t.Text, colorCall)
	shift.Pop()
	return size
}

// drawText is the picture of ops of size, as the GPU draws it, off the frame.
func drawText(ops *op.Ops, size image.Point) (*image.NRGBA, error) {
	img, err := renderSnapshot(ops, size)
	if err != nil {
		return nil, err
	}
	return photoedit.Straighten(img), nil
}

// textRasters gives the view's pictures of texts: asked for in this frame when
// there are none, and in the meantime the last of the same text stretched.
func (e *viewerEdit) textRasters(gtx layout.Context, v *mediaViewer) photoedit.Rasters {
	return func(t *photoedit.Text, px float64) *image.NRGBA {
		if t.Text == "" {
			return nil
		}
		x := &e.text
		bucket := int(math.Round(math.Pow(textBucket, math.Round(math.Log(max(px, 4))/math.Log(textBucket)))))
		k := textKey{textLookOf(t), bucket}
		if img := x.cache[k]; img != nil {
			return img
		}
		if !x.asked[k] {
			if x.asked == nil {
				x.asked = map[textKey]bool{}
			}
			x.asked[k] = true
			ops := new(op.Ops)
			size := recordText(gtx, ops, t, bucket, textWrapAt(t, bucket))
			if size.X <= 0 || size.Y <= 0 || size.X > snapshotMaxHeight || size.Y > snapshotMaxHeight {
				return x.latest[k.look]
			}
			inbox, invalidate := x.inbox, v.invalidate
			go func() {
				defer crash.Recover("picture of a text", func(p *crash.Panic) { inbox.give(textRaster{key: k, err: p}); invalidate() })
				img, err := drawText(ops, size)
				inbox.give(textRaster{key: k, img: img, err: err})
				invalidate()
			}()
		}
		return x.latest[k.look]
	}
}

// textWrapAt is how wide the lines of t go at a font of px: as many ems at any
// size, so that a text growing keeps its lines.
func textWrapAt(t *photoedit.Text, px int) int {
	return max(px*2, int(t.Wrap*float64(px)))
}

// takeTexts keeps the pictures of texts that came, for the view to draw again.
func (v *mediaViewer) takeTexts() {
	e := &v.edit
	x := &e.text
	x.inbox.mu.Lock()
	came := x.inbox.came
	x.inbox.came = nil
	x.inbox.mu.Unlock()
	for _, r := range came {
		if r.err != nil {
			if !x.failed {
				x.failed = true
				e.toast.Show(mediaErrorText(r.err))
			}
			continue
		}
		if x.cache == nil {
			x.cache, x.latest = map[textKey]*image.NRGBA{}, map[textLook]*image.NRGBA{}
		}
		if x.cached > textCacheBytes {
			x.cache, x.latest, x.asked, x.cached = map[textKey]*image.NRGBA{}, map[textLook]*image.NRGBA{}, nil, 0
		}
		x.cache[r.key], x.latest[r.key.look] = r.img, r.img
		x.cached += len(r.img.Pix)
		e.preview.Invalidate()
	}
}

// newText starts writing a text in the middle of what shows, upright.
func (e *viewerEdit) newText(gtx layout.Context) {
	t := e.viewTransform()
	out := t.Size()
	rotation, flipped := t.Upright()
	size := float64(max(min(out.X, out.Y), 1)) * textStartSize
	el := &photoedit.Element{
		Text:     &photoedit.Text{Color: e.color, Size: max(size, 12), Wrap: float64(out.X) * textMaxWidth / max(size, 12)},
		Center:   t.Unmap(photoedit.Point{X: float64(out.X) / 2, Y: float64(out.Y) / 2}),
		Width:    size * 4,
		Height:   size * 1.6,
		Rotation: rotation, Flipped: flipped,
	}
	e.selected = e.scene.Add(el)
	e.editTextOf(gtx, e.selected)
}

// editTextOf writes in the text at i.
func (e *viewerEdit) editTextOf(gtx layout.Context, i int) {
	x := &e.text
	x.editing, x.at = true, i
	x.editor.SetText(e.scene.Elements[i].Text.Text)
	x.editor.SetCaret(x.editor.Len(), x.editor.Len())
	gtx.Execute(key.FocusCmd{Tag: &x.editor})
}

// finishText ends writing: a text left empty is no text.
func (e *viewerEdit) finishText(gtx layout.Context) {
	x := &e.text
	if !x.editing {
		return
	}
	x.editing = false
	gtx.Execute(key.FocusCmd{Tag: &e.keys})
	if x.at < len(e.scene.Elements) && e.scene.Elements[x.at].Text != nil && e.scene.Elements[x.at].Text.Text == "" {
		e.scene.Remove(x.at)
		e.selected = -1
	}
}

// changeText puts a change of the text at i in the scene, its box measured anew,
// its center staying.
func (e *viewerEdit) changeText(gtx layout.Context, i int, change func(*photoedit.Text)) {
	if i < 0 || i >= len(e.scene.Elements) || e.scene.Elements[i].Text == nil {
		return
	}
	el := *e.scene.Elements[i]
	t := *el.Text
	change(&t)
	el.Text = &t
	if t.Text != "" {
		px := max(1, int(t.Size+.5))
		size := recordText(gtx, new(op.Ops), &t, px, textWrapAt(&t, px))
		k := t.Size / float64(px)
		el.Width, el.Height = float64(size.X)*k, float64(size.Y)*k
	}
	e.scene.Replace(i, &el)
}

// exactTexts records all the texts of the elements at the size a picture of the
// source has them, for a job off the frame to draw.
type exactText struct {
	text *photoedit.Text
	ops  *op.Ops
	size image.Point
}

func (e *viewerEdit) exactTexts(gtx layout.Context) []exactText {
	var out []exactText
	for _, el := range e.scene.Elements {
		if el.Text == nil || el.Text.Text == "" {
			continue
		}
		// As large as it comes out; the largest a texture is stretched from.
		px := max(1, int(el.Text.Size+.5))
		ops := new(op.Ops)
		size := recordText(gtx, ops, el.Text, px, textWrapAt(el.Text, px))
		if size.X > snapshotMaxHeight || size.Y > snapshotMaxHeight {
			k := float64(snapshotMaxHeight) / float64(max(size.X, size.Y))
			px = max(1, int(float64(px)*k))
			ops = new(op.Ops)
			size = recordText(gtx, ops, el.Text, px, textWrapAt(el.Text, px))
		}
		if size.X > 0 && size.Y > 0 {
			out = append(out, exactText{el.Text, ops, size})
		}
	}
	return out
}
