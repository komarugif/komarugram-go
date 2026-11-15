// SPDX-License-Identifier: Unlicense OR MIT

package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// A module embedded zstd-compressed compiles as itself; what is not zstd
// is an error, and compiles nothing.
func TestCompileZstdModule(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx, Limits{Memory: 1 << 20, Slow: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	// The smallest module: its magic and version, a function of no
	// arguments exported as "f".
	module := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: () -> ()
		0x03, 0x02, 0x01, 0x00, // function 0 of type 0
		0x07, 0x05, 0x01, 0x01, 'f', 0x00, 0x00, // export "f"
		0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b, // its body
	}
	zw, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := rt.CompileZstdModule(ctx, zw.EncodeAll(module, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := compiled.ExportedFunctions()["f"]; !ok {
		t.Fatalf("exports %v", compiled.ExportedFunctions())
	}
	if _, err := rt.CompileZstdModule(ctx, module); err == nil {
		t.Fatal("a module not compressed compiled")
	}
}

// An operation a frame waits for may take 300 ms, and 600 ms on a machine
// that draws its windows on the CPU, almost surely a virtual one, whose
// rasterizer takes the time the decoders need: there a sticker's frame took
// 300 to 600 ms. The limit is the one of when the runtime is made.
func TestPerFrameLimit(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { SetSoftwareRendered(false) })
	for _, c := range []struct {
		software bool
		want     time.Duration
	}{{false, 300 * time.Millisecond}, {true, 600 * time.Millisecond}} {
		SetSoftwareRendered(c.software)
		rt, err := NewRuntime(ctx, Limits{Memory: 1 << 20, Slow: time.Hour, PerFrame: true})
		if err != nil {
			t.Fatal(err)
		}
		if got := rt.limits.Slow; got != c.want {
			t.Errorf("software %v: a frame's operation may take %v, want %v", c.software, got, c.want)
		}
		rt.Close(ctx)
	}
}
