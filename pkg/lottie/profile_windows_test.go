// SPDX-License-Identifier: Unlicense OR MIT

package lottie_test

import (
	"bytes"
	"runtime/pprof"
	"testing"
	"time"

	"komarugram/pkg/sandbox"
)

// The CPU profiler of Go on Windows cannot walk a thread stopped in the
// machine code wazero compiles, which runs on a stack of its own: the whole
// process died (runtime.sigprof). Interpreted, the module runs as Go code,
// and frames rendered while the profile is written leave the process be.
func TestCPUProfileWhileInterpreting(t *testing.T) {
	sandbox.Interpret(true)
	defer sandbox.Interpret(false)
	runtime, ctx := newRuntime(t)
	animation, err := runtime.Open(ctx, "sample.json", sampleAnimation(t), 128)
	if err != nil {
		t.Fatal(err)
	}
	defer animation.Close(ctx)
	var profile bytes.Buffer
	if err := pprof.StartCPUProfile(&profile); err != nil {
		t.Skipf("a CPU profile is written already: %v", err)
	}
	defer pprof.StopCPUProfile()
	frame := time.Duration(float64(time.Second) / animation.FPS)
	for i, end := 0, time.Now().Add(3*time.Second); time.Now().Before(end); i++ {
		if _, _, err := animation.FrameAt(ctx, time.Duration(i%max(1, animation.FrameCount))*frame); err != nil {
			t.Fatal(err)
		}
	}
}
