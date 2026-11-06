// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"slices"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"komarugram/internal/messenger/model"
)

// A photo or video of the box can be hidden under a spoiler, and shown again;
// its picture is blurred meanwhile, and the place of what is hidden goes with
// the files. Sent as documents, nothing is.
func TestFilesBoxHidesUnderSpoiler(t *testing.T) {
	h := newMenuHarness(t, nil)
	c := h.page.composer
	paths := boxPaths(t, "photo", "photo", "photo")
	c.files.addPaths(c, paths, false)
	settleBox(t, h)
	first, second := c.files.files[0], c.files.files[1]
	first.hide.click.Click()
	second.hide.click.Click()
	h.frames(3)
	second.hide.click.Click()
	h.frames(3)
	if !first.spoiler || second.spoiler {
		t.Fatalf("hidden: %v, %v", first.spoiler, second.spoiler)
	}
	if first.shownThumb() == first.thumb || first.shownThumb() == nil || second.shownThumb() != second.thumb {
		t.Fatal("the tile of what is hidden is not blurred, or the other is")
	}
	c.files.send.click.Click()
	m := waitSent(t, h, 1)[0]
	if !slices.Equal(m.Files.Spoilers, []int{0}) {
		t.Fatalf("spoilers %v", m.Files.Spoilers)
	}
	h.frames(60)
	c.files.addPaths(c, paths, true)
	settleBox(t, h)
	c.files.files[0].spoiler = true
	c.files.send.click.Click()
	if m := waitSent(t, h, 2)[1]; m.Files.Spoilers != nil {
		t.Fatalf("documents under a spoiler: %v", m.Files.Spoilers)
	}
}

// A picture of the grid dragged onto another's place takes it, the others
// making room, and the files go in that order; what was hidden stays with its
// file. Let go where it was, it opens nothing.
func TestFilesBoxReordersByDragging(t *testing.T) {
	h := newMenuHarness(t, nil)
	c := h.page.composer
	opened := 0
	h.page.openBoxFiles = func(*boxMedia, int64, model.Message, []model.Message, func()) { opened++ }
	paths := boxPaths(t, "photo", "photo", "photo")
	c.files.addPaths(c, paths, false)
	settleBox(t, h)
	c.files.files[0].hide.click.Click()
	h.frames(3)
	tiles := tilePoints(t, &h.router, 3)
	drag := func(from, to f32.Point) {
		h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: from, Time: time.Duration(h.now.UnixNano())})
		h.frame()
		for i := 1; i <= 8; i++ {
			at := from.Add(to.Sub(from).Mul(float32(i) / 8))
			h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at, Time: time.Duration(h.now.UnixNano())})
			h.frame()
		}
		h.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: to, Time: time.Duration(h.now.UnixNano())})
		h.frames(3)
	}
	// Away and back: nothing moves, nothing opens.
	drag(tiles[0], tiles[1])
	h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: tiles[1]})
	h.frame()
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: tiles[0]})
	h.frame()
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: tiles[1]})
	h.frame()
	h.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: tiles[1]})
	h.frames(3)
	if opened != 0 {
		t.Fatalf("a drag opened the viewer %d times", opened)
	}
	// The first, dragged onto the second, took its place: 1 0 2.
	order := func() []string {
		var names []string
		for _, f := range c.files.files {
			names = append(names, f.Path)
		}
		return names
	}
	if want := []string{paths[1], paths[0], paths[2]}; !slices.Equal(order(), want) {
		t.Fatalf("order %v, want %v", order(), want)
	}
	// Then the last onto the first: 2 1 0.
	drag(tiles[2], tiles[0])
	if want := []string{paths[2], paths[1], paths[0]}; !slices.Equal(order(), want) {
		t.Fatalf("order %v, want %v", order(), want)
	}
	c.files.send.click.Click()
	m := waitSent(t, h, 1)[0]
	if want := []string{paths[2], paths[1], paths[0]}; !slices.Equal(m.Files.Paths, want) || !slices.Equal(m.Files.Spoilers, []int{2}) {
		t.Fatalf("sent %v, spoilers %v", m.Files.Paths, m.Files.Spoilers)
	}
}

// tilePoints are points inside the n pictures of the box's grid, in their
// order: below and left of the button that takes each out, clear of the one
// that hides it.
func tilePoints(t *testing.T, r *input.Router, n int) []f32.Point {
	t.Helper()
	var found []image.Rectangle
	var walk func([]input.SemanticNode)
	walk = func(nodes []input.SemanticNode) {
		for _, node := range nodes {
			// A label is told on the button and the nodes inside it alike.
			if node.Desc.Label == english().T("files.remove") && !slices.Contains(found, node.Desc.Bounds) {
				found = append(found, node.Desc.Bounds)
			}
			walk(node.Children)
		}
	}
	walk(r.AppendSemantics(nil))
	if len(found) != n {
		t.Fatalf("%d pictures, want %d", len(found), n)
	}
	points := make([]f32.Point, n)
	for i, b := range found {
		points[i] = f32.Pt(float32(b.Min.X-30), float32(b.Max.Y+40))
	}
	return points
}

// The frame the picture is let go in draws the new order already: drawn in the
// order before it, the pictures jumped back for a frame, and then on.
func TestFilesBoxDropShowsTheNewOrderAtOnce(t *testing.T) {
	var b filesBox
	files := []*boxFile{{}, {}, {}}
	b.files = slices.Clone(files)
	var r input.Router
	const step = 100
	slot := func(i int) image.Point { return image.Pt(i*step, 0) }
	frame := func() []*boxFile {
		gtx := layout.Context{Ops: new(op.Ops), Source: r.Source(), Now: time.Now(), Constraints: layout.Exact(image.Pt(3*step, step)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
		order := b.dragTiles(gtx, slices.Clone(b.files), 3, step, slot)
		area := clip.Rect{Max: image.Pt(3*step, step)}.Push(gtx.Ops)
		event.Op(gtx.Ops, &b.drag)
		area.Pop()
		r.Frame(gtx.Ops)
		return order
	}
	queue := func(kind pointer.Kind, x float32) []*boxFile {
		buttons := pointer.ButtonPrimary
		if kind == pointer.Release {
			buttons = 0
		}
		r.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: buttons, Position: f32.Pt(x, 50)})
		return frame()
	}
	frame()
	queue(pointer.Press, 50)
	for x := float32(60); x <= 250; x += 20 {
		queue(pointer.Move, x)
	}
	drawn := queue(pointer.Release, 250)
	want := []*boxFile{files[1], files[2], files[0]}
	if !slices.Equal(b.files, want) {
		t.Fatal("not moved")
	}
	if !slices.Equal(drawn, want) {
		t.Fatal("the frame of the drop draws the order before it")
	}
}

// The buttons over the pictures tell what they do, as the viewer's do, and
// what the spoiler button tells follows what it would do.
func TestFilesBoxButtonTooltips(t *testing.T) {
	h := newMenuHarness(t, nil)
	c := h.page.composer
	c.files.addPaths(c, boxPaths(t, "photo", "photo"), false)
	settleBox(t, h)
	center := func(label string) f32.Point {
		t.Helper()
		var found []image.Rectangle
		var walk func([]input.SemanticNode)
		walk = func(nodes []input.SemanticNode) {
			for _, node := range nodes {
				if node.Desc.Label == label {
					found = append(found, node.Desc.Bounds)
				}
				walk(node.Children)
			}
		}
		walk(h.router.AppendSemantics(nil))
		if len(found) == 0 {
			t.Fatalf("no %q", label)
		}
		b := found[0]
		return f32.Pt(float32(b.Min.X+b.Max.X)/2, float32(b.Min.Y+b.Max.Y)/2)
	}
	hover := func(at f32.Point) string {
		h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: at, Time: time.Duration(h.now.UnixNano())})
		h.frame()
		h.now = h.now.Add(tooltipShowDelay + 50*time.Millisecond)
		h.frames(2)
		return c.files.tips.showing()
	}
	en := english()
	if got := hover(center(en.T("files.spoiler"))); got != en.T("files.spoiler") {
		t.Fatalf("over the spoiler button: %q", got)
	}
	if got := hover(center(en.T("files.remove"))); got != en.T("files.remove") {
		t.Fatalf("over the remove button: %q", got)
	}
}
