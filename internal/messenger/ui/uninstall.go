// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"fmt"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/exp/appearance"
	"gio-mw/token"
	"gio-mw/widget/button"
	"gio-mw/widget/toggle"

	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/install"
	"komarugram/internal/messenger/localization"
)

// Uninstaller is the window of the program started with -uninstall, which
// the system's list of programs does to remove it: what is removed, the
// choice to remove the user's data too, and the result.
type Uninstaller struct {
	window  *appwindow.Window
	catalog localization.Catalog
	quit    func()

	inst  install.Installation
	found bool

	data                      *toggle.Toggle[string]
	texts                     map[string]string
	uninstall, cancel, finish *button.Button
	height                    heightTransition

	running, done bool
	problem       string
	result        chan error
	// uninstallFn is install.Uninstall; tests replace it.
	uninstallFn func(context.Context, install.Installation, bool) error

	themeFonts       uint64
	light, darkTheme *token.Theme
}

// NewUninstaller makes the window's content; quit closes the window.
func NewUninstaller(w *appwindow.Window, catalog localization.Catalog, quit func()) *Uninstaller {
	inst, found := install.Find()
	return &Uninstaller{
		window: w, catalog: catalog, quit: quit,
		inst: inst, found: found,
		data:        toggle.NewToggle([]string{"data"}, nil, func([]string) {}),
		texts:       map[string]string{"data": catalog.T("uninstall.data")},
		uninstall:   button.Filled(),
		cancel:      button.Text(),
		finish:      button.Filled(),
		result:      make(chan error, 1),
		uninstallFn: install.Uninstall,
	}
}

// Theme implements appwindow.Content, following the system's scheme.
func (u *Uninstaller) Theme(gtx layout.Context) *token.Theme {
	if v := defaults.FontsVersion(); v != u.themeFonts {
		u.themeFonts, u.light, u.darkTheme = v, nil, nil
	}
	dark := u.window.Appearance != nil && u.window.Appearance.Scheme() == appearance.Dark
	theme := u.light
	if dark {
		if u.darkTheme == nil {
			u.darkTheme = defaults.NewTheme(gtx, schemes.SchemeBaselineDark())
		}
		theme = u.darkTheme
	} else {
		if u.light == nil {
			u.light = defaults.NewTheme(gtx, schemes.SchemeBaselineLight())
		}
		theme = u.light
	}
	u.window.SetFrameDark(dark)
	u.window.SetFrameColor(theme.Scheme.Surface.Color.AsNRGBA())
	return theme
}

// Update implements appwindow.Content.
func (u *Uninstaller) Update(gtx layout.Context) {
	select {
	case err := <-u.result:
		u.running = false
		switch {
		case err == nil:
			u.done, u.problem = true, ""
		case errors.Is(err, install.ErrCancelled):
			u.problem = u.catalog.T("uninstall.cancelled")
		case errors.Is(err, install.ErrInUse):
			u.problem = u.catalog.T("uninstall.running")
		default:
			u.problem = fmt.Sprintf(u.catalog.T("uninstall.failed"), err)
		}
	default:
	}
	if u.running {
		return
	}
	if u.cancel.Clicked(gtx) || u.finish.Clicked(gtx) {
		u.quit()
		return
	}
	if u.found && !u.done && u.uninstall.Clicked(gtx) {
		u.running, u.problem = true, ""
		data := len(u.data.GetValues()) > 0
		go func() {
			u.result <- u.uninstallFn(context.Background(), u.inst, data)
			u.window.Invalidate()
		}()
	}
}

// Layout implements appwindow.Content.
func (u *Uninstaller) Layout(gtx layout.Context) {
	l := u.catalog
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	fillRect(gtx, sc.SurfaceContainerLow, size)
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = layout.Constraints{}.Min
		return layout.UniformInset(16).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(unit.Dp(440)))
			return u.height.Card(gtx, func(gtx layout.Context) layout.Dimensions {
				rows := []layout.FlexChild{
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, l.T("uninstall.title"), token.TypestyleHeadlineSmall, sc.Surface.OnColor, 0)
					}),
					vspace(8),
				}
				body := fmt.Sprintf(l.T(systemKey("uninstall.body")), u.inst.Dir)
				switch {
				case u.done:
					body = l.T("uninstall.done")
				case !u.found:
					body = l.T("uninstall.missing")
				}
				rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, body, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
				}))
				if u.found && !u.done {
					rows = append(rows, vspace(16),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return u.data.Layout(gtx, u.texts)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return label(gtx, l.T("uninstall.data_hint"), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
						}))
				}
				if u.problem != "" {
					rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, u.problem, token.TypestyleBodyMedium, sc.Error.Color, 0)
					}))
				}
				rows = append(rows, vspace(20), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if u.done || !u.found {
						return layout.Flex{Spacing: layout.SpaceStart}.Layout(gtx, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return u.finish.Layout(gtx, l.T("uninstall.close"))
						}))
					}
					text := l.T("uninstall.uninstall")
					if u.running {
						text = l.T("uninstall.uninstalling")
						u.uninstall.Disable()
						u.cancel.Disable()
					} else {
						u.uninstall.Enable()
						u.cancel.Enable()
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return u.cancel.Layout(gtx, l.T("uninstall.cancel"))
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: gtx.Constraints.Min}
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return u.uninstall.Layout(gtx, text)
						}),
					)
				}))
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
			}, defaultCardPadding)
		})
	})
}
