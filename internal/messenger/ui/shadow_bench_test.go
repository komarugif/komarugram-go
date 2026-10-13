// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// BenchmarkDialogShadowFrame draws and rasterizes a frame with a dialog's
// shadow, the one of the dialog's opening animation, against the same frame
// without it, on whatever the system rasterizes with (software, in a
// virtual machine):
//
//	go test ./internal/messenger/ui -run XXX -bench DialogShadowFrame
func BenchmarkDialogShadowFrame(b *testing.B) {
	size := image.Pt(1000, 900)
	for _, dark := range []bool{false, true} {
		for _, shadow := range []bool{false, true} {
			name := "light"
			if dark {
				name = "dark"
			}
			if shadow {
				name += "/shadow"
			} else {
				name += "/none"
			}
			b.Run(name, func(b *testing.B) {
				win, err := headless.NewWindow(size.X, size.Y)
				if err != nil {
					b.Skip(err)
				}
				defer win.Release()
				ops := new(op.Ops)
				sch := schemes.SchemeBaselineLight()
				if dark {
					sch = schemes.SchemeBaselineDark()
				}
				b.ResetTimer()
				for range b.N {
					ops.Reset()
					gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}, Values: map[string]any{}}
					wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, sch))
					fillRect(gtx, scheme(gtx).Surface.Color, size)
					card := image.Rect(180, 80, 820, 820)
					if shadow {
						drawShadow(gtx, card, gtx.Dp(16), dialogShadow)
					}
					offset(gtx, card.Min, func(gtx layout.Context) layout.Dimensions {
						fillRounded(gtx, dialogSurface(gtx), card.Size(), gtx.Dp(16))
						return layout.Dimensions{}
					})
					if err := win.Frame(ops); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
