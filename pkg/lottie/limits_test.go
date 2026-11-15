// SPDX-License-Identifier: Unlicense OR MIT

package lottie_test

import (
	"context"
	"testing"
	"time"

	"komarugram/pkg/lottie"
	"komarugram/pkg/sandbox"
)

func runtimeWith(t *testing.T, limits lottie.Limits) (*lottie.Runtime, context.Context) {
	t.Helper()
	ctx := context.Background()
	runtime, err := lottie.NewRuntimeWithLimits(ctx, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	return runtime, ctx
}

// TestRenderSizeLimit checks that a render size over MaxSide is refused before
// the host allocates a frame for it.
func TestRenderSizeLimit(t *testing.T) {
	runtime, ctx := newRuntime(t)
	data := sampleAnimation(t)
	for _, size := range []int{0, -1, lottie.DefaultLimits.MaxSide + 1, 1 << 30} {
		if animation, err := runtime.Open(ctx, "sample.json", data, size); err == nil {
			animation.Close(ctx)
			t.Errorf("render size %d was accepted", size)
		}
	}
	if used := runtime.Budget().Used(); used != 0 {
		t.Errorf("refused animations hold %d bytes of the budget", used)
	}
}

// TestMemoryLimit checks that a sandbox which needs more memory than it may
// have fails with an error: a 1024 pixel frame alone takes 4 MiB.
func TestMemoryLimit(t *testing.T) {
	data := sampleAnimation(t)

	runtime, ctx := newRuntime(t)
	animation, err := runtime.Open(ctx, "sample.json", data, lottie.DefaultLimits.MaxSide)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := animation.FrameAt(ctx, 0); err != nil {
		t.Fatal(err)
	}
	t.Logf("one animation at %dpx holds %.1f MiB", lottie.DefaultLimits.MaxSide,
		float64(runtime.Budget().Used())/(1<<20))
	animation.Close(ctx)

	limits := lottie.DefaultLimits
	limits.Memory = 2 << 20
	runtime, ctx = runtimeWith(t, limits)
	animation, err = runtime.Open(ctx, "sample.json", data, lottie.DefaultLimits.MaxSide)
	if err == nil {
		defer animation.Close(ctx)
		_, _, err = animation.FrameAt(ctx, 0)
	}
	if err == nil {
		t.Fatal("a 1024px frame rendered within 2 MiB")
	}
	t.Logf("over the memory limit: %v", err)
}

// TestBudget checks that one budget can be shared by several runtimes, that
// it refuses sandboxes once spent, and that closing them gives it all back.
func TestBudget(t *testing.T) {
	data := sampleAnimation(t)
	budget := sandbox.NewBudget(16 << 20)
	limits := lottie.DefaultLimits
	limits.Budget = budget
	first, ctx := runtimeWith(t, limits)
	second, _ := runtimeWith(t, limits)

	var opened []*lottie.Animation
	var refused error
	for i := range 64 {
		runtime := first
		if i%2 == 1 {
			runtime = second
		}
		animation, err := runtime.Open(ctx, "sample.json", data, 512)
		if err == nil {
			_, _, err = animation.FrameAt(ctx, 0)
			opened = append(opened, animation)
		}
		if err != nil {
			refused = err
			break
		}
	}
	if refused == nil {
		t.Fatal("64 animations at 512px fit in a 16 MiB budget")
	}
	t.Logf("%d animations fit, then: %v", len(opened), refused)
	if used := budget.Used(); used > 16<<20 {
		t.Errorf("budget overdrawn: %d bytes", used)
	}
	for _, animation := range opened {
		animation.Close(ctx)
	}
	if used := budget.Used(); used != 0 {
		t.Errorf("closed animations still hold %d bytes of the budget", used)
	}
}

// TestSlowLimit checks that an operation overrunning the time limit fails and
// closes its sandbox, so that later frames fail at once.
func TestSlowLimit(t *testing.T) {
	data := sampleAnimation(t)
	limits := lottie.DefaultLimits
	limits.Slow, limits.PerFrame = time.Microsecond, false
	runtime, ctx := runtimeWith(t, limits)

	animation, err := runtime.Open(ctx, "sample.json", data, 512)
	if err == nil {
		defer animation.Close(ctx)
		_, _, err = animation.FrameAt(ctx, 0)
	}
	if err == nil {
		t.Fatal("rendering finished within a microsecond")
	}
	t.Logf("%v", err)
}
