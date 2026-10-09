// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gioui.org/app"
	"gioui.org/layout"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/messenger/security"
	"komarugram/internal/motion"
)

func TestVisualLockTriggersAreIndependent(t *testing.T) {
	protection, err := security.OpenPath(filepath.Join(t.TempDir(), "security.json"), &renderTPM{})
	if err != nil {
		t.Fatal(err)
	}
	if err := protection.Enable(context.Background(), "secret"); err != nil {
		t.Fatal(err)
	}
	prefs := preferences.Memory()
	if err := prefs.SetWindowLock(1, false, true); err != nil {
		t.Fatal(err)
	}
	locked := new(atomic.Bool)
	a := &App{window: &appwindow.Window{Window: new(app.Window)}, preferences: prefs,
		security: newSecurityView(protection, nil), windowLocked: locked,
		lastInput: time.Now().Add(-2 * time.Minute)}
	if !a.checkWindowLock(layout.Context{Now: time.Now()}) || !locked.Load() {
		t.Fatal("idle timeout did not lock with close option enabled")
	}
	locked.Store(false)
	a.lastInput = time.Now()
	a.SetMinimized(true)
	if locked.Load() {
		t.Fatal("minimize locked while its option was off")
	}
	if err := prefs.SetWindowLock(0, true, true); err != nil {
		t.Fatal(err)
	}
	a.SetMinimized(true)
	if !locked.Load() {
		t.Fatal("minimize did not lock when enabled")
	}
	locked.Store(false)
	a.SetSuspended(true) // Linux can report minimize without Mode=Minimized.
	if !locked.Load() {
		t.Fatal("suspended window did not lock")
	}
}

func TestVisualUnlockUpdatesOverlayBeforeLayout(t *testing.T) {
	protection, err := security.OpenPath(filepath.Join(t.TempDir(), "security.json"), &renderTPM{})
	if err != nil {
		t.Fatal(err)
	}
	if err := protection.Enable(context.Background(), "secret"); err != nil {
		t.Fatal(err)
	}
	locked := new(atomic.Bool)
	locked.Store(true)
	w := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
	defer w.Motion.Close()
	a := New(w, mockstore.New(time.Now(), 0), Services{Security: protection, WindowLocked: locked})
	defer a.Close()
	h := &focusHarness{draw: func(gtx layout.Context) { a.Update(gtx); a.Layout(gtx) }}
	h.frame()
	a.visualLock.result <- nil
	h.frame() // Must not call Overlay.Layout without Overlay.Update.
	if locked.Load() {
		t.Fatal("window remained locked after a valid password")
	}
}

func TestAutoLockSliderPreservesOlderDelay(t *testing.T) {
	prefs := preferences.Memory()
	if err := prefs.SetWindowLock(17, true, false); err != nil {
		t.Fatal(err)
	}
	v := newSecurityView(nil, nil)
	v.SetPreferences(prefs)
	if v.autoLock.GetValue() != 17 || len(v.autoLockValues) != len(autoLockPresets)+1 {
		t.Fatalf("old delay not available: %v", v.autoLockValues)
	}
	if err := prefs.SetWindowLock(15, true, false); err != nil {
		t.Fatal(err)
	}
	v.setAutoLockSlider(prefs.Global().AutoLockMinutes)
	if v.autoLock.GetValue() != 15 || len(v.autoLockValues) != len(autoLockPresets) {
		t.Fatalf("too many stops after selecting a preset: %v", v.autoLockValues)
	}
}

// A wrong password over a key sealed by the password alone says no word
// of a TPM; over one sealed by the TPM, it does.
func TestVisualLockProblemWithoutTPM(t *testing.T) {
	for _, c := range []struct {
		access security.Access
		want   string
	}{
		{security.Access{Kind: security.AccessFailed}, "security.failed_password"},
		{security.Access{Kind: security.AccessReady}, "security.failed"},
	} {
		protection, err := security.OpenPath(filepath.Join(t.TempDir(), "security.json"), &renderTPM{access: c.access})
		if err != nil {
			t.Fatal(err)
		}
		if err := protection.Enable(context.Background(), "secret"); err != nil {
			t.Fatal(err)
		}
		v := newVisualLockView(protection, func() {})
		v.failed = true
		if got := v.problem(); got != c.want {
			t.Errorf("access %v: %q, want %q", c.access.Kind, got, c.want)
		}
	}
}
