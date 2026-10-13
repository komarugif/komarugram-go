// SPDX-License-Identifier: Unlicense OR MIT

package slider

import (
	"image"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// TestHandleSlides verifies that the handle slides to a value set from
// outside, and jumps there with animations off.
func TestHandleSlides(t *testing.T) {
	for _, animate := range []bool{true, false} {
		s := StandardSlider([]int{0, 1, 2, 3}, 0, func(int) {})
		start := time.Unix(1000, 0)
		frame := func(now time.Time) float32 {
			gtx := layout.Context{
				Ops:         new(op.Ops),
				Now:         now,
				Constraints: layout.Exact(image.Pt(400, 48)),
				Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
				Values:      map[string]any{},
			}
			wdk.SetAnimationsEnabled(gtx, animate)
			wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
			s.Layout(gtx)
			// The tween already holds this frame's value: asking for the
			// stop it is heading to returns it.
			return s.animation.position.Animate(gtx, 2)
		}
		frame(start)
		s.SetValue(2)
		frame(start)
		mid := frame(start.Add(60 * time.Millisecond))
		if animate && (mid <= 0 || mid >= 2) {
			t.Errorf("animations on: position after 60ms = %v, want between the stops", mid)
		}
		if !animate && mid != 2 {
			t.Errorf("animations off: position = %v, want 2 at once", mid)
		}
		if end := frame(start.Add(time.Second)); end != 2 {
			t.Errorf("animate %v: position after 1s = %v, want 2", animate, end)
		}
	}
}
