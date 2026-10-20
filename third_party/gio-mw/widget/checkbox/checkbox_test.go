// SPDX-License-Identifier: Unlicense OR MIT

package checkbox

import (
	"image"
	"slices"
	"testing"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// TestClickWithoutOnChange clicks a checkbox made with no function to tell
// its changes to: it is checked, and nothing panics.
func TestClickWithoutOnChange(t *testing.T) {
	var r input.Router
	boxes := NewCheckboxes([]string{"a"}, nil, nil)
	var size image.Point
	frame := func() {
		gtx := layout.Context{
			Ops:         new(op.Ops),
			Source:      r.Source(),
			Constraints: layout.Exact(image.Pt(400, 100)),
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Values:      map[string]any{},
		}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		boxes.Update(gtx)
		size = boxes.Layout(gtx, map[string]string{"a": "Option A"}).Size
		r.Frame(gtx.Ops)
	}
	frame()
	pos := f32.Pt(float32(size.X/2), float32(size.Y/2))
	r.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos},
	)
	frame()
	if got := boxes.GetValues(); !slices.Equal(got, []string{"a"}) {
		t.Errorf("values %q, want [a]", got)
	}
}
