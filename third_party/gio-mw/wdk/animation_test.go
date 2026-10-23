// SPDX-License-Identifier: Unlicense OR MIT

package wdk

import (
	"testing"
	"time"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"
)

func tweenContext(now time.Time) layout.Context {
	return layout.Context{Ops: new(op.Ops), Now: now}
}

func TestFloatTween(t *testing.T) {
	start := time.Unix(1000, 0)
	var tw FloatTween
	tw.Easing = &token.EasingLinear
	tw.Duration = 100 * time.Millisecond

	if got := tw.Animate(tweenContext(start), 0); got != 0 {
		t.Fatalf("first frame: got %v, want 0 without animating", got)
	}
	if got := tw.Animate(tweenContext(start), 1); got != 0 {
		t.Errorf("target change at t=0: got %v, want 0", got)
	}
	if got := tw.Animate(tweenContext(start.Add(50*time.Millisecond)), 1); got != 0.5 {
		t.Errorf("t=50ms: got %v, want 0.5", got)
	}
	// Reverse half way: the animation continues from the current value.
	mid := start.Add(50 * time.Millisecond)
	if got := tw.Animate(tweenContext(mid), 0); got != 0.5 {
		t.Errorf("reverse at t=50ms: got %v, want 0.5", got)
	}
	if got := tw.Animate(tweenContext(mid.Add(50*time.Millisecond)), 0); got != 0.25 {
		t.Errorf("reverse +50ms: got %v, want 0.25", got)
	}
	if got := tw.Animate(tweenContext(mid.Add(200*time.Millisecond)), 0); got != 0 {
		t.Errorf("after the end: got %v, want 0", got)
	}
}

// A target that changes every frame, as the progress of work, is followed:
// the value moves every frame, and keeps near the target. It stood still
// while the target changed, restarted at each frame's own time.
func TestFloatTweenFollowsMovingTarget(t *testing.T) {
	start := time.Unix(1000, 0)
	var tw FloatTween
	tw.Duration = token.DurationMedium1
	tw.Animate(tweenContext(start), 0)
	last := float32(0)
	for f := 1; f <= 40; f++ {
		target := float32(f) / 40
		got := tw.Animate(tweenContext(start.Add(time.Duration(f)*16*time.Millisecond)), target)
		// From rest, the first frame shows where the value was.
		if f > 1 && got <= last {
			t.Fatalf("frame %d: %v after %v, toward %v", f, got, last, target)
		}
		last = got
	}
	if last < .8 {
		t.Errorf("at the end of the work: %v, toward 1", last)
	}
}

func TestFloatTweenWithoutClock(t *testing.T) {
	var tw FloatTween
	tw.Animate(layout.Context{Ops: new(op.Ops)}, 0)
	if got := tw.Animate(layout.Context{Ops: new(op.Ops)}, 1); got != 1 {
		t.Errorf("zero gtx.Now: got %v, want target 1", got)
	}
}

func TestColorTween(t *testing.T) {
	start := time.Unix(1000, 0)
	tw := ColorTween{Duration: 100 * time.Millisecond, Easing: &token.EasingLinear}
	red := token.MatColor{R: 255, A: 255}
	tw.Animate(tweenContext(start), red.SetOpacity(0))
	tw.Animate(tweenContext(start), red)
	got := tw.Animate(tweenContext(start.Add(50*time.Millisecond)), red)
	if got.R != 255 || got.A < 126 || got.A > 129 {
		t.Errorf("fade in at t=50ms: got %+v, want red at half opacity", got)
	}
}

func TestFloatTweenAnimationsDisabled(t *testing.T) {
	start := time.Unix(1000, 0)
	tw := FloatTween{Duration: 100 * time.Millisecond, Easing: &token.EasingLinear}
	tw.Animate(tweenContext(start), 0)
	tw.Animate(tweenContext(start), 1)
	if got := tw.Animate(tweenContext(start.Add(50*time.Millisecond)), 1); got != 0.5 {
		t.Fatalf("t=50ms: got %v, want 0.5", got)
	}
	// Turning animations off mid-way finishes the running animation.
	gtx := tweenContext(start.Add(60 * time.Millisecond))
	gtx.Values = map[string]any{}
	SetAnimationsEnabled(gtx, false)
	if got := tw.Animate(gtx, 1); got != 1 {
		t.Errorf("disabled mid-way: got %v, want 1", got)
	}
	if got := tw.Animate(gtx, 0); got != 0 {
		t.Errorf("disabled, new target: got %v, want 0 immediately", got)
	}
}

func TestRippleAlpha(t *testing.T) {
	start := time.Unix(1000, 0)
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }
	held := widget.Press{Start: start}
	if a, running := rippleAlpha(at(0), held); a != 0 || !running {
		t.Errorf("press start: alpha %v running %v, want 0 true", a, running)
	}
	if a, _ := rippleAlpha(at(100), held); a != 1 {
		t.Errorf("held 100ms: alpha %v, want 1", a)
	}
	if _, running := rippleAlpha(at(300), held); running {
		t.Errorf("held 300ms: still running, want settled")
	}
	// A quick tap still fades in fully before fading out.
	tap := widget.Press{Start: start, End: at(20)}
	if a, _ := rippleAlpha(at(75), tap); a != 1 {
		t.Errorf("tap at 75ms: alpha %v, want 1", a)
	}
	if a, _ := rippleAlpha(at(150), tap); a != 0.5 {
		t.Errorf("tap at 150ms: alpha %v, want 0.5", a)
	}
	if a, running := rippleAlpha(at(300), tap); a != 0 || running {
		t.Errorf("tap at 300ms: alpha %v running %v, want 0 false", a, running)
	}
	// A cancelled press fades out right away.
	cancelled := widget.Press{Start: start, End: at(20), Cancelled: true}
	if a, _ := rippleAlpha(at(95), cancelled); a >= 0.6 {
		t.Errorf("cancelled at 95ms: alpha %v, want below 0.6", a)
	}
}
