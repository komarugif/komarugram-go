// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testELF() []byte {
	data := make([]byte, 64)
	copy(data, "\x7fELF")
	data[4], data[5] = 2, 1
	binary.LittleEndian.PutUint16(data[18:20], 62)
	return data
}

func TestToolDownloadReusesCacheOffline(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the cached tool is a Linux executable")
	}
	data := testELF()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write(data)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "tools", "appimagetool.AppImage")
	if err := downloadTool(server.Client(), server.URL, path, "x86_64"); err != nil {
		t.Fatal(err)
	}
	server.Close()
	// No server is available now. A cached tool must still work.
	if err := downloadTool(server.Client(), server.URL, path, "x86_64"); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requested the tool %d times, want once", requests)
	}
	if err := validTool(path, "aarch64"); err == nil {
		t.Fatal("accepted an x86_64 tool for ARM64")
	}
}

func TestFailedToolDownloadKeepsCache(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the cached tool is a Linux executable")
	}
	for _, mode := range []string{"HTTP error", "HTML", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "HTTP error":
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
				case "HTML":
					w.Write([]byte("<html>not an executable</html>"))
				case "truncated":
					w.Header().Set("Content-Length", "256")
					w.Write(testELF())
				}
			}))
			defer server.Close()
			dir := t.TempDir()
			path := filepath.Join(dir, "tool.AppImage")
			const previous = "old incomplete cache"
			if err := os.WriteFile(path, []byte(previous), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := downloadTool(server.Client(), server.URL, path, "x86_64"); err == nil {
				t.Fatal("accepted a failed download")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != previous {
				t.Fatalf("replaced cache with %q, error %v", data, err)
			}
			left, err := filepath.Glob(filepath.Join(dir, ".appimagetool-*"))
			if err != nil || len(left) != 0 {
				t.Fatalf("temporary downloads left: %v, %v", left, err)
			}
		})
	}
}
