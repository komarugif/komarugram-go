// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/wdk"

	"gioui.org/app"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
)

// testWindow is a window without an OS window behind it: Perform is kept
// for later, which is enough to see that closing was asked for.
func testWindow(t *testing.T) *appwindow.Window {
	w := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
	t.Cleanup(w.Motion.Close)
	return w
}

// The window button opens the viewer in a window of the process; the window
// knows its account window, which closes it on the way out.
func TestViewerWindowOpensAndClosesWithItsAccount(t *testing.T) {
	var specs []appwindow.Spec
	w := testWindow(t)
	store := mockstore.New(time.Now(), 0)
	a := New(w, store, Services{MiniApps: miniappprefs.New(miniapp.Shared), OpenWindow: func(s appwindow.Spec) { specs = append(specs, s) }})
	if a.viewer.popout == nil {
		t.Fatal("no window button with a host that opens windows")
	}
	page, _ := store.ChatPhotos(t.Context(), 2, 1<<30, -1, 100)
	photos := page.Messages
	a.history.chat = 2
	a.viewer.popout(2, photos[3], mediaList{items: photos})
	if len(specs) != 1 || specs[0].Options.Title != "Фото — Анна Смирнова" {
		t.Fatalf("opened %+v", specs)
	}

	// What the host does with the spec, on the new window's goroutine.
	pw := testWindow(t)
	content := specs[0].Build(pw).(*viewerWindow)
	if !content.viewer.open || !content.viewer.standalone || content.viewer.current != photos[3].Key.MessageID {
		t.Fatal("the window does not show the photo it was opened for")
	}
	if n := len(a.viewerWindows.open); n != 1 {
		t.Fatalf("%d windows known to the account window", n)
	}

	var router input.Router
	frame := func() {
		gtx := layout.Context{Ops: new(op.Ops), Source: router.Source(), Now: time.Now(), Constraints: layout.Exact(image.Pt(900, 700)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, content.Theme(gtx))
		content.Layout(gtx)
		router.Frame(gtx.Ops)
	}
	frame()
	frame()
	router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	frame()
	if content.viewer.open || !content.closing {
		t.Fatal("Escape did not close the photo window")
	}
	specs[0].Closed()
	content.Close()
	if n := len(a.viewerWindows.open); n != 0 {
		t.Fatalf("a closed window is still known: %d", n)
	}

	// Closing the account window asks its photo windows to close; without
	// an OS window that is only recorded, but it must not block.
	specs[0].Build(testWindow(t))
	a.Close()
}

// Without a host that opens windows, the viewer has no such button.
func TestMediaViewerWithoutWindowHost(t *testing.T) {
	a := New(testWindow(t), mockstore.New(time.Now(), 0), Services{MiniApps: miniappprefs.New(miniapp.Shared)})
	defer a.Close()
	if a.viewer.popout != nil {
		t.Fatal("window button without a host")
	}
}
