// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import (
	"slices"
	"testing"
)

func TestCommandProgram(t *testing.T) {
	for command, want := range map[string]string{
		`"C:\Program Files\Supermium\chrome.exe"`:                                             `C:\Program Files\Supermium\chrome.exe`,
		`"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --single-argument %1`: `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Mozilla Firefox\firefox.exe -osint -url "%1"`:                       `C:\Program Files\Mozilla Firefox\firefox.exe`,
		`"C:\Program Files\Internet Explorer\iexplore.exe"`:                                   "",
		`"C:\Program Files\Pale Moon\palemoon.exe"`:                                           "",
		`"chrome.exe"`:            "",
		`"C:\unclosed\chrome.exe`: "",
		``:                        "",
	} {
		got, ok := commandProgram(command)
		if got != want || ok != (want != "") {
			t.Errorf("%s: %q, %v; want %q", command, got, ok, want)
		}
	}
}

// A Chromium-based browser runs without the GPU where the client's windows
// could not use it.
func TestChromiumWithoutGPU(t *testing.T) {
	defer func(f func() bool) { GPUFailed = f }(GPUFailed)
	has := func() bool {
		return slices.Contains(chromiumArgs("http://x/", "/p", Page{Width: 1, Height: 1}, false), "--disable-gpu")
	}
	GPUFailed = nil
	if has() {
		t.Error("--disable-gpu without a GPU failure")
	}
	GPUFailed = func() bool { return true }
	if !has() {
		t.Error("no --disable-gpu where the GPU failed")
	}
}
