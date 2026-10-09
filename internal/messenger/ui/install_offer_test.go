// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"

	"gio-mw/exp/appearance"

	"komarugram/internal/messenger/install"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
)

// offeringInstall is a sign-in waiting at StepInstall, and what it ends
// with.
func offeringInstall(t *testing.T) (*login.Login, chan string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "")
	l := login.New(nil)
	done := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		exe, err := l.OfferInstall(ctx)
		if err == nil {
			done <- exe
		}
	}()
	for l.State().Step != login.StepInstall {
		time.Sleep(time.Millisecond)
	}
	return l, done
}

func TestInstallViewHandsTheProgramToTheSignIn(t *testing.T) {
	l, done := offeringInstall(t)
	v := newInstallView(nil)
	v.result <- installResult{exe: "/opt/x/komarugram"}
	v.Update(layout.Context{Ops: new(op.Ops), Now: time.Now()}, l, localization.For("ru"))
	select {
	case exe := <-done:
		if exe != "/opt/x/komarugram" {
			t.Fatalf("sign-in got %q", exe)
		}
	case <-time.After(time.Second):
		t.Fatal("the sign-in was not told of the installation")
	}
}

// A refused elevation is told, and the offer stays to be tried again.
func TestInstallViewTellsRefusedRights(t *testing.T) {
	l, done := offeringInstall(t)
	catalog := localization.For("ru")
	v := newInstallView(nil)
	v.running = true
	v.result <- installResult{err: errors.Join(errors.New("ShellExecuteEx"), install.ErrCancelled)}
	v.Update(layout.Context{Ops: new(op.Ops), Now: time.Now()}, l, catalog)
	if v.running || v.problem != catalog.T("install.cancelled") {
		t.Fatalf("running %v, problem %q", v.running, v.problem)
	}
	select {
	case exe := <-done:
		t.Fatalf("the offer ended: %q", exe)
	case <-time.After(20 * time.Millisecond):
	}
	if l.State().Step != login.StepInstall {
		t.Fatalf("step %v", l.State().Step)
	}
}

// The folder the chooser picked becomes the program's own folder in it.
func TestInstallViewTakesTheChosenFolder(t *testing.T) {
	l, _ := offeringInstall(t)
	v := newInstallView(nil)
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now()}
	v.Update(gtx, l, localization.For("ru"))
	chosen := filepath.Join(t.TempDir(), "Programs")
	v.chosen <- folderChoice{dir: chosen}
	v.Update(gtx, l, localization.For("ru"))
	if got, want := v.folder.Text(), filepath.Join(chosen, "KomaruGram"); got != want {
		t.Fatalf("folder %q, want %q", got, want)
	}
}

func TestUninstallerTellsWhatHappened(t *testing.T) {
	catalog := localization.For("ru")
	u := NewUninstaller(nil, catalog, func() {})
	u.found, u.running = true, true
	u.result <- errors.Join(install.ErrInUse, errors.New("sharing violation"))
	u.Update(layout.Context{Ops: new(op.Ops), Now: time.Now()})
	if u.done || u.problem != catalog.T("uninstall.running") {
		t.Fatalf("done %v, problem %q", u.done, u.problem)
	}
	u.running = true
	u.result <- nil
	u.Update(layout.Context{Ops: new(op.Ops), Now: time.Now()})
	if !u.done || u.problem != "" {
		t.Fatalf("done %v, problem %q", u.done, u.problem)
	}
}

// The offer to install and the uninstaller, in ACCOUNTS_PNG_DIR with the
// other screens of the sign-in.
func TestRenderInstallScreens(t *testing.T) {
	dir := os.Getenv("ACCOUNTS_PNG_DIR")
	if dir == "" {
		t.Skip("set ACCOUNTS_PNG_DIR to a directory")
	}
	l := localization.For("ru")
	size := image.Pt(900, 820)
	signIn, _ := offeringInstall(t)
	page := newLoginPage(signIn, nil, nil)
	renderPNG(t, filepath.Join(dir, "install.png"), size, 2, func(gtx layout.Context) {
		page.Update(gtx, l)
		page.Layout(gtx, l, false)
	})
	for name, set := range map[string]func(u *Uninstaller){
		"uninstall":         func(u *Uninstaller) {},
		"uninstall-done":    func(u *Uninstaller) { u.done = true },
		"uninstall-missing": func(u *Uninstaller) { u.found = false },
		"uninstall-running": func(u *Uninstaller) { u.problem = l.T("uninstall.running") },
	} {
		u := NewUninstaller(nil, l, func() {})
		u.found, u.inst = true, install.Installation{Dir: `C:\Users\komaru\AppData\Local\Programs\KomaruGram`}
		set(u)
		renderPNG(t, filepath.Join(dir, name+".png"), image.Pt(640, 480), 2, func(gtx layout.Context) {
			u.Layout(gtx)
		})
	}
}

// clickAlong clicks down a column of the screen h draws until changed
// reports a change, and reports whether one came.
func clickAlong(h *focusHarness, x float32, changed func() bool) bool {
	return clickColumn(h, x, 0, 820, 4, changed)
}

// clickColumn is clickAlong from y to end, by step.
func clickColumn(h *focusHarness, x, y, end, step float32, changed func() bool) bool {
	h.frame()
	for ; step > 0 && y < end || step < 0 && y > end; y += step {
		for _, kind := range []pointer.Kind{pointer.Move, pointer.Press, pointer.Release} {
			e := pointer.Event{Kind: kind, Source: pointer.Mouse, Position: f32.Pt(x, y)}
			if kind == pointer.Press {
				e.Buttons = pointer.ButtonPrimary
			}
			h.router.Queue(e)
			h.frame()
		}
		if changed() {
			return true
		}
	}
	return false
}

// The switches turn when clicked: the toggle calls its onChange, which
// must be there.
func TestInstallSwitchesTurn(t *testing.T) {
	l, _ := offeringInstall(t)
	page := newLoginPage(l, nil, nil)
	page.install.installFn = func(context.Context, install.Options) (string, error) { return "", errors.New("not here") }
	page.install.chooseFn = func(context.Context, string) (string, error) { return "", nil }
	catalog := localization.For("ru")
	h := &focusHarness{draw: func(gtx layout.Context) {
		page.Update(gtx, catalog)
		page.Layout(gtx, catalog, false)
	}}
	if !clickAlong(h, 290, func() bool { return len(page.install.switches.GetValues()) < 2 }) {
		t.Fatal("no switch turned off")
	}
}

func TestUninstallSwitchTurns(t *testing.T) {
	u := NewUninstaller(nil, localization.For("ru"), func() {})
	u.found, u.inst = true, install.Installation{Dir: "/opt/KomaruGram"}
	u.uninstallFn = func(context.Context, install.Installation, bool) error { return errors.New("not here") }
	h := &focusHarness{draw: func(gtx layout.Context) {
		u.Update(gtx)
		u.Layout(gtx)
	}}
	if !clickAlong(h, 290, func() bool { return len(u.data.GetValues()) > 0 }) {
		t.Fatal("the switch of the data did not turn on")
	}
}

// The settings offer the uninstaller where the host gives it, under
// leaving the account, and nowhere else.
func TestSettingsOpenTheUninstaller(t *testing.T) {
	accounts := staticAccounts{{ID: "1", UserID: 1, Name: "Ада Лавлейс"}}
	for _, given := range []bool{true, false} {
		p := newSettingsPage(motion.New(func() {}), miniappprefs.New(miniapp.Ephemeral), nil, func() {}, accounts,
			func() string { return "1" }, themeAuto, "ru", func(themeMode) {}, func(string) {})
		opened := 0
		if given {
			p.uninstall = func() { opened++ }
		}
		// Tall enough for the whole page, which scrolls otherwise.
		h := &focusHarness{draw: func(gtx layout.Context) {
			gtx.Constraints = layout.Exact(image.Pt(900, 1400))
			p.Update(gtx, themeAuto, "ru")
			p.Layout(gtx, themeAuto, appearance.Light, false, localization.For("ru"))
		}}
		// Up from the bottom: the rows above it open sections in place
		// of the page.
		clicked := clickColumn(h, 450, 1400, 0, -4, func() bool { return opened > 0 })
		if clicked != given {
			t.Errorf("given %v: opened %d times", given, opened)
		}
	}
}
