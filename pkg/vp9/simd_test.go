// SPDX-License-Identifier: Unlicense OR MIT

package vp9_test

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"os"
	"testing"
)

// TestModuleUsesSIMD checks that vpxdec.wasm.gz was built with WebAssembly
// SIMD, as build.sh does: without it, decoding takes three times as long.
func TestModuleUsesSIMD(t *testing.T) {
	gz, err := os.ReadFile("vpxdec.wasm.gz")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	module, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	features, ok := customSection(module, "target_features")
	if !ok {
		t.Fatal("the module declares no target features")
	}
	if !bytes.Contains(features, []byte("\x2b\x07simd128")) {
		t.Fatalf("the module was built without SIMD: %q", features)
	}
}

// customSection returns the custom section of name in a WebAssembly module.
func customSection(module []byte, name string) ([]byte, bool) {
	if len(module) < 8 {
		return nil, false
	}
	r := bytes.NewReader(module[8:])
	for r.Len() > 0 {
		id, err := r.ReadByte()
		if err != nil {
			return nil, false
		}
		size, err := binary.ReadUvarint(r)
		if err != nil || size > uint64(r.Len()) {
			return nil, false
		}
		body := make([]byte, size)
		r.Read(body)
		if id != 0 {
			continue
		}
		b := bytes.NewReader(body)
		n, err := binary.ReadUvarint(b)
		if err != nil || n > uint64(b.Len()) {
			continue
		}
		got := make([]byte, n)
		b.Read(got)
		if string(got) == name {
			return body[len(body)-b.Len():], true
		}
	}
	return nil, false
}
