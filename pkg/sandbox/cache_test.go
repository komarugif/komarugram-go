// SPDX-License-Identifier: Unlicense OR MIT

package sandbox

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
)

// addModule is a module exporting add(i32, i32) i32, with a custom section
// named tag, so that different tags make different modules.
func addModule(tag string) []byte {
	m := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f, // type (i32, i32) -> i32
		0x03, 0x02, 0x01, 0x00, // one function of type 0
		0x07, 0x07, 0x01, 0x03, 'a', 'd', 'd', 0x00, 0x00, // export "add"
		0x0a, 0x09, 0x01, 0x07, 0x00, 0x20, 0x00, 0x20, 0x01, 0x6a, 0x0b, // local.get 0, local.get 1, i32.add
	}
	custom := append(append([]byte{0x00, byte(2 + len(tag)), byte(len(tag))}, tag...), 0x00)
	return append(m, custom...)
}

// compiles reports whether wazero compiles here, which is when a disk cache
// is used at all; the interpreter keeps nothing.
func compiles() bool { return runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" }

type cachePaths struct{ dir, key string }

func newPaths(t *testing.T) cachePaths {
	base := t.TempDir()
	return cachePaths{dir: filepath.Join(base, "cache", "wasm"), key: filepath.Join(base, "config", "wasm-cache.key")}
}

// run compiles module through a fresh disk cache at p, as a new process
// would, checks that it adds, and returns the cache's counters.
func run(t *testing.T, p cachePaths, module []byte) *diskCache {
	t.Helper()
	c, err := NewDiskCache(p.dir, p.key)
	if err != nil {
		t.Fatalf("NewDiskCache: %v", err)
	}
	SetCache(c)
	t.Cleanup(func() { SetCache(nil); _ = c.Close(context.Background()) })
	ctx := context.Background()
	rt, err := NewRuntime(ctx, Limits{Memory: 1 << 20, Slow: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	compiled, err := rt.CompileModule(ctx, module)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	mod, err := rt.Instantiate(ctx, compiled, wazero.NewModuleConfig())
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	res, err := mod.ExportedFunction("add").Call(ctx, 2, 3)
	if err != nil || res[0] != 5 {
		t.Fatalf("add(2, 3) = %v, %v", res, err)
	}
	return c.disk
}

func entries(t *testing.T, p cachePaths) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(p.dir, "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func counts(d *diskCache) [4]int64 {
	return [4]int64{d.hits.Load(), d.misses.Load(), d.rejected.Load(), d.added.Load()}
}

// A module compiled once is taken from disk by the next process.
func TestDiskCacheReused(t *testing.T) {
	if !compiles() {
		t.Skip("wazero interprets here")
	}
	p := newPaths(t)
	if got := counts(run(t, p, addModule("a"))); got != [4]int64{0, 1, 0, 1} {
		t.Fatalf("first run hits, misses, rejected, added = %v", got)
	}
	if got := counts(run(t, p, addModule("a"))); got != [4]int64{1, 0, 0, 0} {
		t.Fatalf("second run hits, misses, rejected, added = %v", got)
	}
	if n := len(entries(t, p)); n != 1 {
		t.Fatalf("%d entries on disk", n)
	}
}

// An entry changed on disk is not run: it is compiled again and replaced.
func TestDiskCacheRejectsChangedEntry(t *testing.T) {
	if !compiles() {
		t.Skip("wazero interprets here")
	}
	p := newPaths(t)
	run(t, p, addModule("a"))
	path := entries(t, p)[0]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := counts(run(t, p, addModule("a"))); got != [4]int64{0, 0, 1, 1} {
		t.Fatalf("hits, misses, rejected, added = %v", got)
	}
	if got := counts(run(t, p, addModule("a"))); got != [4]int64{1, 0, 0, 0} {
		t.Fatalf("after recompiling: hits, misses, rejected, added = %v", got)
	}
}

// An entry signed with another key is not run.
func TestDiskCacheRejectsOtherKey(t *testing.T) {
	if !compiles() {
		t.Skip("wazero interprets here")
	}
	p := newPaths(t)
	run(t, p, addModule("a"))
	if err := os.Remove(p.key); err != nil {
		t.Fatal(err)
	}
	if got := counts(run(t, p, addModule("a"))); got[0] != 0 || got[2] != 1 {
		t.Fatalf("hits, misses, rejected, added = %v", got)
	}
}

// One module's entry, renamed to another's key, is not run for it: the
// signature covers the key.
func TestDiskCacheRejectsMovedEntry(t *testing.T) {
	if !compiles() {
		t.Skip("wazero interprets here")
	}
	p := newPaths(t)
	run(t, p, addModule("a"))
	a := entries(t, p)[0]
	run(t, p, addModule("b"))
	var b string
	for _, e := range entries(t, p) {
		if e != a {
			b = e
		}
	}
	data, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := counts(run(t, p, addModule("b"))); got[0] != 0 || got[2] != 1 {
		t.Fatalf("hits, misses, rejected, added = %v", got)
	}
}

// An entry that is a link, even to a valid entry, is not read.
func TestDiskCacheRejectsLink(t *testing.T) {
	if !compiles() || runtime.GOOS == "windows" {
		t.Skip("needs compilation and symbolic links")
	}
	p := newPaths(t)
	run(t, p, addModule("a"))
	path := entries(t, p)[0]
	elsewhere := filepath.Join(t.TempDir(), "entry")
	if err := os.Rename(path, elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, path); err != nil {
		t.Fatal(err)
	}
	if got := counts(run(t, p, addModule("a"))); got[0] != 0 || got[2] != 1 {
		t.Fatalf("hits, misses, rejected, added = %v", got)
	}
}

// A key others can read is replaced, and what it signed is compiled again.
func TestCacheKeyOpenToOthersReplaced(t *testing.T) {
	if !compiles() || runtime.GOOS == "windows" {
		t.Skip("needs compilation and Unix permissions")
	}
	p := newPaths(t)
	run(t, p, addModule("a"))
	old, err := os.ReadFile(p.key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.key, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := counts(run(t, p, addModule("a"))); got[0] != 0 || got[2] != 1 {
		t.Fatalf("hits, misses, rejected, added = %v", got)
	}
	now, err := os.ReadFile(p.key)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p.key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(old, now) || st.Mode().Perm() != 0o600 {
		t.Fatalf("key kept (%v), mode %v", bytes.Equal(old, now), st.Mode().Perm())
	}
}

// A cache directory that is a link is refused; the caller keeps a memory
// cache.
func TestCacheDirLinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs symbolic links")
	}
	p := newPaths(t)
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(p.dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p.dir); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDiskCache(p.dir, p.key); err == nil {
		t.Fatal("a linked cache directory was taken")
	}
}

// A cache directory open to others is closed.
func TestCacheDirClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs Unix permissions")
	}
	p := newPaths(t)
	if err := os.MkdirAll(p.dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.dir, 0o777); err != nil {
		t.Fatal(err)
	}
	c, err := NewDiskCache(p.dir, p.key)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	st, err := os.Stat(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
}

// Runtimes share compiled code in memory, and a closed runtime lets its go.
func TestRuntimeCloseLetsCompiledGo(t *testing.T) {
	SetCache(NewMemoryCache())
	t.Cleanup(func() { SetCache(nil) })
	ctx := context.Background()
	rt, err := NewRuntime(ctx, Limits{Memory: 1 << 20, Slow: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.CompileModule(ctx, addModule("a")); err != nil {
		t.Fatal(err)
	}
	if len(rt.compiled) != 1 {
		t.Fatalf("%d compiled modules held", len(rt.compiled))
	}
	if err := rt.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rt.compiled) != 0 {
		t.Fatalf("%d compiled modules kept after Close", len(rt.compiled))
	}
}
