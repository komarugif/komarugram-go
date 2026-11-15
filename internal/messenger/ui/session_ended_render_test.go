// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/account"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// endedStore is a store whose session Telegram ended for why, whose
// account it froze, or whose connection stopped with failed.
type endedStore struct {
	model.Store
	why    model.SessionEnd
	freeze model.Freeze
	failed error
}

func (s endedStore) SessionEnded() model.SessionEnd { return s.why }
func (s endedStore) Freeze() model.Freeze           { return s.freeze }
func (s endedStore) ConnectionFailed() error        { return s.failed }
func (s endedStore) Reconnect()                     {}

// renderFrames draws frame until animations settle and saves the last one.
func renderFrames(t *testing.T, size image.Point, path string, frame func(gtx layout.Context)) {
	t.Helper()
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	ops := new(op.Ops)
	now := time.Now()
	for range 30 {
		ops.Reset()
		gtx := layout.Context{Ops: ops, Now: now, Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}, Values: map[string]any{}}
		theme := testTheme(gtx, schemes.SchemeBaselineLight())
		wdk.InitMaterialThemeInContext(gtx, theme)
		fillRect(gtx, scheme(gtx).Surface.Color, size)
		frame(gtx)
		now = now.Add(50 * time.Millisecond)
	}
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// TestRenderSessionEnded draws the dialog for every reason the session
// ended, what a frozen account shows and a connection that stopped, in both
// languages, and saves the
// screenshots, for looking at them:
//
//	SESSION_PNG_DIR=/tmp/session go test ./internal/messenger/ui -run RenderSessionEnded
func TestRenderSessionEnded(t *testing.T) {
	dir := os.Getenv("SESSION_PNG_DIR")
	if dir == "" {
		t.Skip("set SESSION_PNG_DIR to a directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	frozen := endedStore{freeze: model.Freeze{
		Since:     time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local),
		Until:     time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local),
		AppealURL: "https://t.me/SpamBot",
	}}
	for _, lang := range []string{"ru", "en"} {
		l := localization.For(lang)
		for why := range sessionEndedText {
			d := newSessionEndedDialog(func() {})
			renderFrames(t, image.Pt(700, 420), filepath.Join(dir, fmt.Sprintf("%s-session-%d.png", lang, why)), func(gtx layout.Context) {
				d.Update(gtx, endedStore{why: why})
				d.Layout(gtx, l)
			})
		}
		for name, failed := range map[string]error{
			"failed": errors.New("history cache: open history.db: unable to open database file"),
			"in-use": account.ErrInUse,
		} {
			d := newConnectionFailedDialog()
			renderFrames(t, image.Pt(700, 420), filepath.Join(dir, lang+"-connection-"+name+".png"), func(gtx layout.Context) {
				d.Update(gtx, endedStore{failed: failed})
				d.Layout(gtx, l)
			})
		}
		f := newFrozenView(frozen)
		f.Show()
		renderFrames(t, image.Pt(700, 640), filepath.Join(dir, lang+"-frozen-dialog.png"), func(gtx layout.Context) {
			f.Update(gtx)
			f.Layout(gtx, l)
		})
		f = newFrozenView(frozen)
		renderFrames(t, image.Pt(360, 150), filepath.Join(dir, lang+"-frozen-bars.png"), func(gtx layout.Context) {
			f.layoutBar(gtx, l)
			offset(gtx, image.Pt(16, gtx.Dp(72)), func(gtx layout.Context) layout.Dimensions {
				size := image.Pt(gtx.Constraints.Max.X-32, gtx.Dp(56))
				fillRounded(gtx, scheme(gtx).SurfaceContainerHigh, size, size.Y/2)
				return f.layoutComposer(gtx, l, size, size.Y/2)
			})
		})
	}
}
