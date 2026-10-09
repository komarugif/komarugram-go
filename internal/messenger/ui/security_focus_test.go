// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"path/filepath"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
	"komarugram/internal/messenger/security"
)

// focusHarness draws a screen frame by frame through a real input router.
type focusHarness struct {
	router input.Router
	draw   func(gtx layout.Context)
}

func (h *focusHarness) frame() {
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: time.Now(), Constraints: layout.Exact(image.Pt(900, 820)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	h.draw(gtx)
	h.router.Frame(gtx.Ops)
}

func TestUnlockFocusesPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	tpm := &renderTPM{}
	protection, err := security.OpenPath(path, tpm)
	if err != nil {
		t.Fatal(err)
	}
	if err := protection.Enable(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	locked, err := security.OpenPath(path, tpm)
	if err != nil {
		t.Fatal(err)
	}
	v := newSecurityView(locked, func() {})
	h := &focusHarness{draw: func(gtx layout.Context) {
		v.UpdateUnlock(gtx)
		v.UnlockLayout(gtx, localization.For("ru"))
	}}
	h.frame()
	if !h.router.Source().Focused(&v.unlock.editor) {
		t.Fatal("the unlock password field is not focused")
	}
}

func TestProtectionOfferFocusesMasterPassword(t *testing.T) {
	protection, err := security.OpenPath(filepath.Join(t.TempDir(), "security.json"), &renderTPM{})
	if err != nil {
		t.Fatal(err)
	}
	signIn := login.New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go signIn.OfferProtection(ctx)
	for signIn.State().Step != login.StepProtect {
		time.Sleep(time.Millisecond)
	}
	v := newSecurityView(protection, func() {})
	page := newLoginPage(signIn, v, nil)
	h := &focusHarness{draw: func(gtx layout.Context) {
		page.Update(gtx, localization.For("ru"))
		page.Layout(gtx, localization.For("ru"), false)
	}}
	h.frame()
	if !h.router.Source().Focused(&v.master.editor) {
		t.Fatal("the master password field is not focused")
	}
	// Enter in the first field goes on to the second rather than failing.
	h.router.Queue(key.EditEvent{Text: "secret"})
	h.frame()
	h.router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.frame()
	h.frame()
	if !h.router.Source().Focused(&v.confirm.editor) || v.localProblem != "" {
		t.Fatalf("after Enter: confirm focused %v, problem %q", h.router.Source().Focused(&v.confirm.editor), v.localProblem)
	}
}

func TestSignInFocusesPhone(t *testing.T) {
	signIn := login.New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go signIn.Phone(ctx, nil)
	for signIn.State().Step != login.StepPhone {
		time.Sleep(time.Millisecond)
	}
	page := newLoginPage(signIn, nil, nil)
	h := &focusHarness{draw: func(gtx layout.Context) {
		page.Update(gtx, localization.For("ru"))
		page.Layout(gtx, localization.For("ru"), false)
	}}
	h.frame()
	if !h.router.Source().Focused(&page.phone.editor) {
		t.Fatal("the phone field is not focused")
	}
}
