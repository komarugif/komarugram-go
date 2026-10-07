// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"
	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"komarugram/internal/messenger/model"
)

func heightContext(now time.Time) layout.Context {
	gtx := layout.Context{Ops: new(op.Ops), Now: now, Constraints: layout.Constraints{Max: image.Pt(240, 600)}, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	return gtx
}

func TestHeightTransitionReversesAndHonorsMotion(t *testing.T) {
	gtx := heightContext(time.Unix(1000, 0))
	var h heightTransition
	if h.Value(gtx, 80, true) != 80 {
		t.Fatal("initial size animated")
	}
	if h.Value(gtx, 200, true) != 80 {
		t.Fatal("expansion jumped")
	}
	gtx.Now = gtx.Now.Add(heightDuration / 2)
	mid := h.Value(gtx, 200, true)
	if mid <= 80 || mid >= 200 {
		t.Fatalf("no intermediate height: %d", mid)
	}
	if h.Value(gtx, 80, true) != mid {
		t.Fatal("reversal jumped")
	}
	gtx.Now = gtx.Now.Add(heightDuration + time.Millisecond)
	if h.Value(gtx, 80, true) != 80 {
		t.Fatal("collapse did not finish")
	}
	h.Value(gtx, 200, true)
	wdk.SetAnimationsEnabled(gtx, false)
	if h.Value(gtx, 200, true) != 200 {
		t.Fatal("reduced motion did not finish transition")
	}
	wdk.SetAnimationsEnabled(gtx, true)
	if h.Value(gtx, 80, false) != 80 {
		t.Fatal("explicit reduced motion ignored")
	}
	gtx.Constraints.Max.X = 300
	if h.Value(gtx, 150, true) != 150 {
		t.Fatal("window width change animated stale layout")
	}
}

func TestAnimatedCardClipsHiddenInput(t *testing.T) {
	var h heightTransition
	var router input.Router
	var button widget.Clickable
	now := time.Unix(1000, 0)
	height, clicks := 80, 0
	frame := func() {
		gtx := heightContext(now)
		gtx.Source = router.Source()
		h.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			if button.Clicked(gtx) {
				clicks++
			}
			offset(gtx, image.Pt(0, 180), func(gtx layout.Context) layout.Dimensions {
				return button.Layout(gtx, func(layout.Context) layout.Dimensions { return layout.Dimensions{Size: image.Pt(240, 40)} })
			})
			return layout.Dimensions{Size: image.Pt(240, height)}
		}, 0)
		router.Frame(gtx.Ops)
	}
	click := func() {
		router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(20, 200)}, pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(20, 200)})
		frame()
	}
	frame()
	height = 240
	frame()
	click()
	if clicks != 0 {
		t.Fatal("hidden control accepted a click")
	}
	now = now.Add(heightDuration + time.Millisecond)
	frame()
	click()
	if clicks != 1 {
		t.Fatalf("visible control got %d clicks", clicks)
	}
}

func TestContextMenuResizeKeepsBottomAnchor(t *testing.T) {
	now := time.Unix(1000, 0)
	var m contextMenu
	rect := image.Rect(10, 200, 220, 300)
	var drawn image.Point
	frame := func() {
		gtx := heightContext(now)
		m.Layout(gtx, true, rect, menuFromBottomLeft, 12, func(gtx layout.Context) layout.Dimensions {
			drawn = gtx.Constraints.Max
			return layout.Dimensions{Size: drawn}
		})
	}
	frame()
	now = now.Add(menuEnterDuration + time.Millisecond)
	frame()
	rect.Min.Y = 50
	frame()
	if m.bounds.Dy() != 100 {
		t.Fatal("open menu jumped to expanded height")
	}
	now = now.Add(heightDuration / 2)
	frame()
	if m.bounds.Dy() <= 100 || m.bounds.Dy() >= 250 || m.bounds.Max.Y != 300 || drawn != m.bounds.Size() {
		t.Fatalf("bad intermediate geometry: %v, content %v", m.bounds, drawn)
	}
	now = now.Add(heightDuration)
	frame()
	if m.bounds != rect {
		t.Fatalf("menu did not reach target: %v", m.bounds)
	}
}

func TestQuoteDisclosureUsesGutterAndAnimates(t *testing.T) {
	text := "one\ntwo\nthree\nfour\nfive\nsix"
	h := newInteractionHarness(t, model.TextRuns(text, []model.Entity{{Kind: "quote", Length: utf16Length(text), Collapsed: true}}))
	collapsed := h.row.text.size.Y
	bottom := 0
	for _, f := range h.row.text.fragments {
		bottom = max(bottom, f.Bounds.Max.Y)
	}
	if collapsed != bottom+6 {
		t.Fatalf("disclosure adds a footer: height %d, text bottom %d", collapsed, bottom)
	}
	at := f32.Pt(float32(h.size.X-22), float32(collapsed-18))
	h.pointer(pointer.Press, at)
	h.pointer(pointer.Release, at)
	block := &h.row.textBlocks[0]
	if !block.expanded || h.page.activeText != nil {
		t.Fatal("gutter click failed or selected text")
	}
	if h.row.text.size.Y >= block.height.target {
		t.Fatal("quote jumped to expanded height")
	}
	h.now = h.now.Add(heightDuration / 2)
	h.frame()
	if h.row.text.size.Y <= collapsed || h.row.text.size.Y >= block.height.target {
		t.Fatal("no intermediate quote height")
	}
	for _, f := range h.row.text.fragments {
		if f.Bounds.Max.Y > h.row.text.size.Y-6 {
			t.Fatal("hidden text remains hittable")
		}
	}
	h.now = h.now.Add(heightDuration)
	h.frame()
	if h.row.text.size.Y != block.height.target {
		t.Fatal("quote did not finish expanding")
	}
	h.animate = false
	block.action.click.Click()
	h.frame()
	if h.row.text.size.Y != collapsed {
		t.Fatal("reduced-motion quote did not collapse immediately")
	}
}

// The collapsed menu fits below the cursor, exactly at the window edge.
// Adding reactions must lift its top while keeping its bottom at that edge,
// rather than flipping the entire expanded menu above the original click.
func TestContextMenuExpansionAtWindowEdge(t *testing.T) {
	for _, x := range []int{20, 380} {
		for _, y := range []int{330, 392, 450} {
			var m contextMenu
			now := time.Unix(1000, 0)
			viewport, at := image.Pt(400, 500), image.Pt(x, y)
			height := 100
			frame := func() (image.Rectangle, menuCorner) {
				gtx := heightContext(now)
				gtx.Constraints.Max = viewport
				rect, corner := m.Place(gtx, at, viewport, image.Pt(200, height))
				m.Layout(gtx, true, rect, corner, 12, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Max} })
				return rect, corner
			}
			initial, corner := frame()
			now = now.Add(menuEnterDuration + time.Millisecond)
			frame()
			height = 220
			rect, grownCorner := frame()
			wantBottom := 492
			if y == 450 {
				wantBottom = y
			}
			if grownCorner != corner || rect.Max.Y != wantBottom {
				t.Fatalf("at %v: expanded from %v to %v, corner %v -> %v; want bottom %d", at, initial, rect, corner, grownCorner, wantBottom)
			}
			if m.bounds != initial {
				t.Fatalf("resize jumped on first frame: %v -> %v", initial, m.bounds)
			}
			now = now.Add(heightDuration / 2)
			frame()
			if m.bounds.Min.Y <= rect.Min.Y || m.bounds.Min.Y >= initial.Min.Y || m.bounds.Max.Y > 492 {
				t.Fatalf("at %v: invalid intermediate bounds %v", at, m.bounds)
			}
			if y == 392 && m.bounds.Max.Y != 492 {
				t.Fatal("bottom edge moved while growing upwards")
			}
			now = now.Add(heightDuration)
			frame()
			if m.bounds != rect {
				t.Fatal("resize did not settle")
			}
			height = 100
			frame()
			now = now.Add(heightDuration)
			back, _ := frame()
			if back != initial || m.bounds != initial {
				t.Fatal("collapse lost the original anchor")
			}
		}
	}
}

func TestContextMenuWindowResizeDoesNotAnimate(t *testing.T) {
	var m contextMenu
	now := time.Unix(1000, 0)
	viewport := image.Pt(600, 600)
	body := image.Pt(240, 200)
	at := image.Pt(300, 260)
	var drawn image.Point
	frame := func() image.Rectangle {
		gtx := heightContext(now)
		gtx.Constraints.Max = viewport
		rect, corner := m.Place(gtx, at, viewport, body)
		m.Layout(gtx, true, rect, corner, 12, func(gtx layout.Context) layout.Dimensions {
			drawn = gtx.Constraints.Max
			return layout.Dimensions{Size: drawn}
		})
		return rect
	}
	frame()
	now = now.Add(menuEnterDuration + time.Millisecond)
	frame()
	body.Y = 320
	frame() // resize the window in the middle of content expansion
	for _, size := range []image.Point{{600, 450}, {600, 420}, {600, 540}, {210, 380}, {600, 600}} {
		viewport = size
		now = now.Add(16 * time.Millisecond)
		target := frame()
		if m.bounds != target || drawn != target.Size() {
			t.Fatalf("window %v: menu floats at %v instead of %v, content %v", size, m.bounds, target, drawn)
		}
	}
	before := m.bounds
	body.Y = 180
	target := frame()
	if m.bounds != before {
		t.Fatal("window resize permanently disabled content animation")
	}
	now = now.Add(heightDuration)
	frame()
	if m.bounds != target {
		t.Fatal("content transition did not finish after window resize")
	}
}
