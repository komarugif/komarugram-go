// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"image"
	"testing"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/token"

	"gioui.org/app"
	"gioui.org/op"

	"komarugram/internal/crash"
)

type recoveringContent struct {
	Content
	recover func() bool
}

func (c recoveringContent) RecoverFrame() bool { return c.recover() }

func TestRecoverContent(t *testing.T) {
	if recoverContent(nil) {
		t.Fatal("content without recovery must keep the fallback")
	}
	called := false
	if !recoverContent(recoveringContent{recover: func() bool {
		called = true
		return true
	}}) || !called {
		t.Fatal("content recovery was not called")
	}
	if recoverContent(recoveringContent{recover: func() bool { return false }}) {
		t.Fatal("declined recovery must keep the fallback")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if recoverContent(recoveringContent{recover: func() bool { panic("broken recovery") }}) {
		t.Fatal("failed recovery must keep the fallback")
	}
}

// The frame of a panic asks nothing of the content, which may be what is
// broken: it draws the window's frame with the theme of the last frame
// that had one, and the panic's text alone before there was one.
func TestPanicFrameNeedsNoContent(t *testing.T) {
	ownFrames(t, true)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	var ops op.Ops
	theme := defaults.NewTheme(frameContext(&ops, image.Pt(400, 300), false), schemes.SchemeBaselineLight())
	w := &Window{Window: new(app.Window), host: new(Host)}
	w.frame.configure(app.Config{})
	p := &crash.Panic{Where: "window test", Value: "broken"}
	// The window's frame drawn around it does not panic in turn.
	defer crash.Subscribe(func(p *crash.Panic) { t.Errorf("the panic frame panicked: %v", p) })()
	for _, theme := range []*token.Theme{nil, theme} {
		submitted := false
		e := app.FrameEvent{Size: image.Pt(400, 300), Frame: func(*op.Ops) { submitted = true }}
		w.layoutPanic(&ops, e, theme, "KomaruGram Go", e.Size, p, &panicScreen{}, true)
		if !submitted {
			t.Fatalf("no frame of the panic (theme %v)", theme != nil)
		}
	}
}
