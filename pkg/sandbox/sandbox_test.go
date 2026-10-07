// SPDX-License-Identifier: Unlicense OR MIT

package sandbox

import (
	"bytes"
	"compress/gzip"
	"context"
	"testing"
	"time"
)

// A module embedded gzipped compiles as itself; what is not gzip is an
// error, and compiles nothing.
func TestCompileGzipModule(t *testing.T) {
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
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(module)
	zw.Close()
	compiled, err := rt.CompileGzipModule(ctx, gz.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := compiled.ExportedFunctions()["f"]; !ok {
		t.Fatalf("exports %v", compiled.ExportedFunctions())
	}
	if _, err := rt.CompileGzipModule(ctx, module); err == nil {
		t.Fatal("a module not gzipped compiled")
	}
}
