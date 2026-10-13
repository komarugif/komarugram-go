// SPDX-License-Identifier: Unlicense OR MIT

package wasmmodule

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

var testModule = Module{Name: "test", Env: "KOMARUGRAM_TEST_MODULE"}

func TestModuleFromPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "own.wasm")
	if err := os.WriteFile(path, []byte("own build"), 0o600); err != nil {
		t.Fatal(err)
	}
	module, err := testModule.load(context.Background(), path)
	if err != nil || string(module) != "own build" {
		t.Fatalf("got %q, %v", module, err)
	}
	if _, err := testModule.load(context.Background(), path+".missing"); err == nil {
		t.Fatal("a missing module loaded")
	}
}

func TestModuleDownload(t *testing.T) {
	body := []byte("module")
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write(body)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "avcdec.wasm")
	if _, err := testModule.download(context.Background(), srv.URL, sha256Hex([]byte("other")), path); err == nil {
		t.Fatal("a module with another checksum was accepted")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a module with another checksum was kept")
	}
	module, err := testModule.download(context.Background(), srv.URL, sha256Hex(body), path)
	if err != nil || !bytes.Equal(module, body) {
		t.Fatalf("got %q, %v", module, err)
	}
	if kept, _ := os.ReadFile(path); !bytes.Equal(kept, body) {
		t.Fatalf("kept %q", kept)
	}

	// A module of the user's at a URL is fetched once per process.
	isolateCache(t, t.TempDir())
	requests = 0
	for range 2 {
		if module, err := testModule.load(context.Background(), srv.URL); err != nil || !bytes.Equal(module, body) {
			t.Fatalf("got %q, %v", module, err)
		}
	}
	if requests != 1 {
		t.Fatalf("%d requests", requests)
	}
}

// A module that comes zstd-compressed is unpacked as it is loaded, and one
// that does not is as it came.
func TestModuleUnpacked(t *testing.T) {
	module := []byte("\x00asm\x01\x00\x00\x00 a module")
	e, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{"plain.wasm": module, "packed.wasm.zst": e.EncodeAll(module, nil)} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(testModule.Env, path)
		got, err := testModule.Load(context.Background())
		if err != nil || !bytes.Equal(got, module) {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
	}
}

// The tested build comes zstd-compressed, is kept as it came and loaded
// unpacked; the builds an older client kept go, the user's own stay.
func TestTestedBuildReplacesOlder(t *testing.T) {
	module := []byte("\x00asm\x01\x00\x00\x00 the tested build")
	e, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	packed := e.EncodeAll(module, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(packed) }))
	defer srv.Close()
	cache := t.TempDir()
	isolateCache(t, cache)
	t.Setenv("KOMARUGRAM_TEST_TESTED", "")
	m := Module{Name: "test", Env: "KOMARUGRAM_TEST_TESTED", URL: srv.URL + "/pinned/test.wasm.zst", SHA256: sha256Hex(packed)}
	dir := filepath.Join(cache, "komarugram-go")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(dir, "test-0123456789abcdef.wasm")
	own := filepath.Join(dir, "test-custom-0123456789abcdef.wasm")
	other := filepath.Join(dir, "testing-0123456789abcdef.wasm")
	for _, path := range []string{older, own, other} {
		if err := os.WriteFile(path, []byte("kept"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := m.Load(context.Background())
	if err != nil || !bytes.Equal(got, module) {
		t.Fatalf("got %q, %v", got, err)
	}
	if kept, _ := os.ReadFile(filepath.Join(dir, "test-"+m.SHA256[:16]+".wasm.zst")); !bytes.Equal(kept, packed) {
		t.Errorf("kept %q", kept)
	}
	if _, err := os.Stat(older); err == nil {
		t.Error("the older build stays")
	}
	for _, path := range []string{own, other} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s went: %v", filepath.Base(path), err)
		}
	}
}

// isolateCache points os.UserCacheDir at dir: Windows reads LocalAppData,
// the others XDG_CACHE_HOME.
func isolateCache(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("LocalAppData", dir)
}
