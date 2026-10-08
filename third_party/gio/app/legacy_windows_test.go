// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"slices"
	"testing"

	"gioui.org/app/internal/windows"
	"gioui.org/gpu"
	"gioui.org/io/pointer"
)

// Before Windows 8 there is no pointer input: the window takes the mouse
// as it comes, and does not fail.
func TestWithoutPointerInput(t *testing.T) {
	if err := windows.EnableMouseInPointer(1); err != nil && err != windows.ErrNoPointerInput {
		t.Fatal(err)
	}
}

func TestMouseButtonMessages(t *testing.T) {
	for _, c := range []struct {
		msg   uint32
		w     uintptr
		btn   pointer.Buttons
		press bool
	}{
		{windows.WM_LBUTTONDOWN, 0, pointer.ButtonPrimary, true},
		{windows.WM_LBUTTONUP, 0, pointer.ButtonPrimary, false},
		{windows.WM_RBUTTONDOWN, 0, pointer.ButtonSecondary, true},
		{windows.WM_MBUTTONUP, 0, pointer.ButtonTertiary, false},
		{windows.WM_XBUTTONDOWN, windows.XBUTTON1 << 16, pointer.ButtonQuaternary, true},
		{windows.WM_XBUTTONUP, windows.XBUTTON2 << 16, pointer.ButtonQuinary, false},
	} {
		if btn, press := mouseButton(c.msg, c.w); btn != c.btn || press != c.press {
			t.Errorf("message %#x, wParam %#x: %v, %v; want %v, %v", c.msg, c.w, btn, press, c.btn, c.press)
		}
	}
}

func TestDeviceLostBeforePresentTurnsToWARP(t *testing.T) {
	defer useWARP.Store(useWARP.Load())
	for _, c := range []struct {
		name string
		ctx  d3d11Context
		err  error
		want bool
	}{
		{"lost before a frame", d3d11Context{}, gpu.ErrDeviceLost, true},
		{"lost after frames", d3d11Context{presented: true}, gpu.ErrDeviceLost, false},
		{"WARP lost", d3d11Context{warp: true}, gpu.ErrDeviceLost, false},
		{"not lost", d3d11Context{}, nil, false},
	} {
		useWARP.Store(false)
		if err := c.ctx.lost(c.err); err != c.err {
			t.Errorf("%s: error %v, want %v", c.name, err, c.err)
		}
		if got := useWARP.Load(); got != c.want {
			t.Errorf("%s: WARP %v, want %v", c.name, got, c.want)
		}
	}
}

// A character beyond the Basic Multilingual Plane comes as two WM_CHAR,
// a surrogate pair, as an emoji from SendInput or an on-screen keyboard.
func TestCharOfSurrogatePair(t *testing.T) {
	w := new(window)
	var got []string
	for _, c := range []uint16{'a', 0xd83d, 0xdc4b, 0x7, 0xdc4b, 0xd83d, 'b'} {
		if text := w.char(c); text != "" {
			got = append(got, text)
		}
	}
	// The wave, then nothing for the bell and a lone low half, and a high
	// half followed by no low one is dropped.
	if want := []string{"a", "👋", "b"}; !slices.Equal(got, want) {
		t.Errorf("text %q, want %q", got, want)
	}
}
