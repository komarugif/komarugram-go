// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
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

// holdGrid is a grid of 3 rows of 3 items 50 px square, in a list, as the
// picker lays out its items, with a preview over the window.
type holdGrid struct {
	router  input.Router
	list    layout.List
	clicks  [9]widget.Clickable
	preview holdPreview
	now     time.Time
	chosen  []int
}

func (g *holdGrid) frame() {
	gtx := layout.Context{Ops: new(op.Ops), Source: g.router.Source(), Now: g.now, Constraints: layout.Exact(image.Pt(150, 100)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	g.preview.Update(gtx)
	for i := range g.clicks {
		if g.clicks[i].Clicked(gtx) && !g.preview.Swallow() {
			g.chosen = append(g.chosen, i)
		}
	}
	g.list.Axis = layout.Vertical
	g.list.Layout(gtx, 3, func(gtx layout.Context, row int) layout.Dimensions {
		for col := range 3 {
			i := row*3 + col
			rect := image.Rect(col*50, 0, col*50+50, 50)
			g.preview.tile(row, rect, model.PickerItem{ID: fmt.Sprint(i), Emoji: "😀"}, false)
			inRect(gtx, rect, func(gtx layout.Context) layout.Dimensions {
				return g.clicks[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Max}
				})
			})
		}
		g.preview.row(row, 50)
		return layout.Dimensions{Size: image.Pt(150, 50)}
	})
	g.preview.area(gtx, image.Pt(150, 100), &g.list, image.Point{})
	// Over everything, as the window draws it.
	g.preview.Layout(gtx, nil)
	g.router.Frame(gtx.Ops)
	g.now = g.now.Add(16 * time.Millisecond)
}

func (g *holdGrid) event(kind pointer.Kind, at f32.Point) {
	e := pointer.Event{Kind: kind, Source: pointer.Mouse, Position: at}
	if kind != pointer.Release {
		e.Buttons = pointer.ButtonPrimary
	}
	g.router.Queue(e)
	g.frame()
}

// An item held shows large; moved over another with the button held, the
// preview shows that one, the preview taking nothing from the grid; the
// release closes it and chooses nothing. A click chooses, as before.
func TestHoldPreview(t *testing.T) {
	g := &holdGrid{now: time.Unix(1000, 0)}
	g.frame()
	g.event(pointer.Press, f32.Pt(25, 25))
	g.event(pointer.Release, f32.Pt(25, 25))
	if g.preview.Shown() || len(g.chosen) != 1 || g.chosen[0] != 0 {
		t.Fatalf("a click: shown %v, chosen %v", g.preview.Shown(), g.chosen)
	}
	// Scrolled by half a row: the second row starts at 25.
	g.list.Position = layout.Position{First: 0, Offset: 25}
	g.frame()
	g.event(pointer.Press, f32.Pt(75, 10)) // item 1
	g.now = g.now.Add(holdPreviewDelay / 2)
	g.frame()
	if g.preview.Shown() {
		t.Fatal("shown before its time")
	}
	g.now = g.now.Add(holdPreviewDelay)
	g.frame()
	if !g.preview.Shown() || g.preview.item.ID != "1" {
		t.Fatalf("held: shown %v, %q", g.preview.Shown(), g.preview.item.ID)
	}
	g.event(pointer.Move, f32.Pt(125, 40)) // item 5, of the second row
	if !g.preview.Shown() || g.preview.item.ID != "5" {
		t.Fatalf("moved: shown %v, %q", g.preview.Shown(), g.preview.item.ID)
	}
	g.event(pointer.Move, f32.Pt(80, 12)) // back to item 1, pressed first
	g.event(pointer.Release, f32.Pt(80, 12))
	if g.preview.Shown() || len(g.chosen) != 1 {
		t.Fatalf("released: shown %v, chosen %v", g.preview.Shown(), g.chosen)
	}
	g.event(pointer.Press, f32.Pt(25, 10))
	g.event(pointer.Release, f32.Pt(25, 10))
	if len(g.chosen) != 2 || g.chosen[1] != 0 {
		t.Fatalf("a click after: chosen %v", g.chosen)
	}
}
