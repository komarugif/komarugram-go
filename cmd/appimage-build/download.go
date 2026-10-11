// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"komarugram/pkg/program"
)

const toolRelease = "https://github.com/AppImage/appimagetool/releases/download/continuous/"
const maxToolSize = 128 << 20

func resolveTool(explicit string) (string, error) {
	if explicit != "" {
		tool, err := program.LookPath(explicit)
		if err != nil {
			return "", fmt.Errorf("appimagetool %q: %w", explicit, err)
		}
		return filepath.Abs(tool)
	}
	if tool, err := program.LookPath("appimagetool"); err == nil {
		return filepath.Abs(tool)
	}
	// appimagetool runs on the build host, even when Go targets another CPU.
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if arch == "" {
		return "", fmt.Errorf("no automatic appimagetool download for host %q; use -appimagetool", runtime.GOARCH)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("appimagetool cache: %w; use -appimagetool for a local tool", err)
	}
	name := "appimagetool-" + arch + ".AppImage"
	path := filepath.Join(cache, "komarugram-go", "appimage-build", name)
	client := &http.Client{Timeout: 5 * time.Minute}
	if err := downloadTool(client, toolRelease+name, path, arch); err != nil {
		return "", fmt.Errorf("appimagetool download: %w; use -appimagetool for a local tool", err)
	}
	return path, nil
}

// downloadTool reuses a complete cached executable. A failed request leaves
// the cache unchanged; only a fully received, validated file is published.
func downloadTool(client *http.Client, url, path, arch string) error {
	if validTool(path, arch) == nil {
		log.Printf("using cached appimagetool: %s", path)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	log.Printf("downloading appimagetool: %s", url)
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	if resp.ContentLength > maxToolSize {
		return fmt.Errorf("appimagetool exceeds %d MiB", maxToolSize>>20)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".appimagetool-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, maxToolSize+1))
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxToolSize {
		return fmt.Errorf("appimagetool exceeds %d MiB", maxToolSize>>20)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	if err := validTool(tmp, arch); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	log.Printf("cached appimagetool: %s (%.1f MiB)", path, float64(n)/(1<<20))
	return nil
}

// Reject error pages and a tool for a different CPU before executing it.
// This checks the file format, not the authenticity of its contents: the
// executable is fetched over HTTPS from the official GitHub release.
func validTool(path, arch string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("appimagetool is not a regular executable file")
	}
	var header [64]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return fmt.Errorf("incomplete appimagetool ELF header: %w", err)
	}
	machine := map[string]uint16{"x86_64": 62, "aarch64": 183}[arch]
	if machine == 0 || string(header[:4]) != "\x7fELF" || header[4] != 2 || header[5] != 1 || binary.LittleEndian.Uint16(header[18:20]) != machine {
		return fmt.Errorf("downloaded file is not a 64-bit Linux ELF executable for %s", arch)
	}
	return nil
}
