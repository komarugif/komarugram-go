// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"image/color"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"komarugram/internal/crash"
)

// retryAfter paces redraw attempts after a panicked frame. Input still
// triggers frames immediately, so a transient failure clears on the next one.
const retryAfter = time.Second

// recoverContent lets content discard a failed view before the next frame.
// Implementations must only return true after leaving or rebuilding the
// failed state. A failure in recovery itself still gets a report and fallback.
func recoverContent(content Content) (recovered bool) {
	if r, ok := content.(interface{ RecoverFrame() bool }); ok {
		crash.Guard("window recovery", func() { recovered = r.RecoverFrame() })
	}
	return recovered
}

// panicScreen replaces a frame whose layout panicked. It depends on nothing
// from the content, which may be what is broken.
type panicScreen struct {
	theme *material.Theme
}

func (s *panicScreen) layout(gtx layout.Context, p *crash.Panic) {
	if s.theme == nil {
		s.theme = material.NewTheme()
		s.theme.Palette.Bg = color.NRGBA{R: 32, G: 26, B: 26, A: 255}
		s.theme.Palette.Fg = color.NRGBA{R: 240, G: 226, B: 226, A: 255}
	}
	paint.Fill(gtx.Ops, s.theme.Bg)
	l := texts()
	text := l.T("window.redrawing") + "\n\n" + p.Error()
	if dir, err := crash.Dir(); err == nil {
		text += "\n\n" + l.Format("window.reports", map[string]string{"path": dir})
	}
	layout.UniformInset(unit.Dp(24)).Layout(gtx, material.Body1(s.theme, text).Layout)
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(retryAfter)})
}
