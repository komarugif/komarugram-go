// SPDX-License-Identifier: Unlicense OR MIT

package toggle

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

// TestLabelClickToggles verifies that clicking a switch's label, not only its
// track, switches it, as a checkbox's label does.
func TestLabelClickToggles(t *testing.T) {
	var r input.Router
	options := []string{"a", "b"}
	labels := map[string]string{"a": "Option A", "b": "Option B"}
	toggle := NewToggle(options, nil, func([]string) {})

	var rowHeight, rowWidth int
	frame := func() {
		gtx := layout.Context{
			Ops:         new(op.Ops),
			Source:      r.Source(),
			Constraints: layout.Exact(image.Pt(400, 400)),
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Values:      map[string]any{},
		}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		dims := toggle.Layout(gtx, labels)
		rowHeight = dims.Size.Y / len(options)
		rowWidth = dims.Size.X
		r.Frame(gtx.Ops)
	}
	frame()

	// Click in the label area of the second row, away from the track.
	pos := f32.Pt(float32(rowWidth/2), float32(rowHeight)+float32(rowHeight)/2)
	r.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos},
	)
	frame()
	frame()

	if got := toggle.GetValues(); !slices.Equal(got, []string{"b"}) {
		t.Errorf("clicking label: values = %q, want [b]", got)
	}
}
