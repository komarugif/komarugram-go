// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"

	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// TestSurfaceStates checks that a surface shows hover and, without ripples,
// press as state layers over its background, and the pointer cursor.
func TestSurfaceStates(t *testing.T) {
	win, err := headless.NewWindow(40, 40)
	if err != nil {
		t.Skip("no GPU:", err)
	}
	defer win.Release()
	var s surface
	var router input.Router
	// A zero gtx.Now disables ripples and makes the colors jump to their
	// targets, so every state shows in the frame after it starts.
	center := func() uint8 {
		ops := new(op.Ops)
		gtx := layout.Context{Ops: ops, Source: router.Source(), Constraints: layout.Exact(image.Pt(40, 40)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
		sc := scheme(gtx)
		fillRect(gtx, sc.Surface.Color, image.Pt(40, 40))
		style := surfaceStyle{area: image.Rect(10, 10, 30, 30), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor}
		s.Layout(gtx, image.Pt(40, 40), style, nil)
		router.Frame(ops)
		if err := win.Frame(ops); err != nil {
			t.Fatal(err)
		}
		im := image.NewRGBA(image.Rect(0, 0, 40, 40))
		if err := win.Screenshot(im); err != nil {
			t.Fatal(err)
		}
		return im.RGBAAt(20, 20).R
	}
	idle := center()
	router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(20, 20)})
	center()
	hovered := center()
	if hovered >= idle {
		t.Errorf("hovered surface %d is not darker than idle %d", hovered, idle)
	}
	if got := router.Cursor(); got != pointer.CursorPointer {
		t.Errorf("cursor over surface = %v, want pointer", got)
	}
	router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(20, 20)})
	center()
	if pressed := center(); pressed >= hovered {
		t.Errorf("pressed surface %d is not darker than hovered %d", pressed, hovered)
	}
}
