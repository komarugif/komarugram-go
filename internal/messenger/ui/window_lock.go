// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"sync/atomic"

	"gio-mw/token"
	"gio-mw/widget/button"

	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/security"
)

// visualLockView covers only the window. It does not clear the root key or
// interrupt the account's Telegram connection.
type visualLockView struct {
	height     heightTransition
	security   *security.Manager
	invalidate func()
	password   *textField
	submit     *button.Button
	result     chan error
	running    bool
	failed     bool
	focused    bool
}

func newVisualLockView(manager *security.Manager, invalidate func()) *visualLockView {
	return &visualLockView{
		security: manager, invalidate: invalidate,
		password: newTextField('•', ""), submit: button.Filled(),
		result: make(chan error, 1),
	}
}

func (v *visualLockView) Update(gtx layout.Context, locked *atomic.Bool) {
	select {
	case err := <-v.result:
		v.running = false
		v.password.Clear()
		v.focused = false
		v.failed = err != nil
		if err == nil {
			locked.Store(false)
			return
		}
	default:
	}
	if !v.focused {
		v.password.Focus(gtx)
		v.focused = true
	}
	if v.running {
		return
	}
	if v.submit.Clicked(gtx) || v.password.Submitted(gtx) {
		password := v.password.Text()
		if password == "" {
			v.failed = true
			return
		}
		v.failed = false
		v.running = true
		go func() {
			v.result <- v.security.VerifyPassword(context.Background(), password)
			v.invalidate()
		}()
	}
}

func (v *visualLockView) Layout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	fillRect(gtx, sc.SurfaceContainerLow, size)
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = layout.Constraints{}.Min
		return layout.UniformInset(16).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(unit.Dp(420)))
			return v.height.Card(gtx, func(gtx layout.Context) layout.Dimensions {
				rows := []layout.FlexChild{
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, l.T("security.window_locked"), token.TypestyleHeadlineSmall, sc.Surface.OnColor, 0)
					}),
					vspace(12),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, l.T("security.window_lock_body"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
					}),
					vspace(20),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return v.password.Layout(gtx, l.T("security.password"), v.failed)
					}),
				}
				if v.failed {
					rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, l.T("security.failed"), token.TypestyleBodyMedium, sc.Error.Color, 0)
					}))
				}
				if v.running || v.password.Text() == "" {
					v.submit.Disable()
				} else {
					v.submit.Enable()
				}
				rows = append(rows, vspace(20), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return v.submit.Layout(gtx, l.T("security.unlock"))
				}))
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
			}, defaultCardPadding)
		})
	})
	return layout.Dimensions{Size: image.Pt(size.X, size.Y)}
}
