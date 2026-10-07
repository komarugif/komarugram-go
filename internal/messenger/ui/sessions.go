// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"sort"
	"sync"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"golang.org/x/exp/shiny/materialdesign/icons"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// The Devices section of the settings: the sessions of the account, as
// Telegram Desktop's Active Sessions show them (settings_active_sessions.cpp):
// this device, then the others, the last active first, and the logins that
// stopped at the password apart. A click on one shows what Telegram knows of
// it. While the section is open the list is asked for again every minute.

// sessionsRefresh is how often the open section asks again
// (kShortPollTimeout).
const sessionsRefresh = time.Minute

var sessionIcons = map[model.SessionKind]wdk.IconWidget{
	model.SessionOther:   wdk.RequireIconWidget(icons.HardwareDevicesOther),
	model.SessionAndroid: wdk.RequireIconWidget(icons.HardwarePhoneAndroid),
	model.SessionIPhone:  wdk.RequireIconWidget(icons.HardwarePhoneIPhone),
	model.SessionIPad:    wdk.RequireIconWidget(icons.HardwareTabletMac),
	model.SessionWindows: wdk.RequireIconWidget(icons.HardwareDesktopWindows),
	model.SessionMac:     wdk.RequireIconWidget(icons.HardwareLaptopMac),
	model.SessionLinux:   wdk.RequireIconWidget(icons.HardwareComputer),
	model.SessionWeb:     wdk.RequireIconWidget(icons.AVWeb),
}

// sessionsView is the Devices section.
type sessionsView struct {
	source     model.SessionsSource
	invalidate func()

	mu      sync.Mutex
	list    []model.Session
	err     error
	loaded  bool
	loading bool
	asked   time.Time
	rows    map[int64]*surface
	retry   surface
	loader  loadingIndicator
	detail  model.Session
	dialog  modal
	done    surface
	// toast is the page's, where a failure to load is told; told is the
	// failure told last, so that one that repeats on every refresh is told
	// once.
	toast *toast
	told  string
	// private hides the sessions' IP addresses, in visual privacy mode.
	private func() bool
}

// shown is s as the view shows it: without its IP address in visual
// privacy mode.
func (v *sessionsView) shown(s model.Session) model.Session {
	if v.private != nil && v.private() {
		s.IP = ""
	}
	return s
}

func newSessionsView(source model.SessionsSource, invalidate func()) *sessionsView {
	return &sessionsView{source: source, invalidate: invalidate, rows: map[int64]*surface{}}
}

// refresh asks Telegram for the sessions, unless it is asking already.
func (v *sessionsView) refresh() {
	if v == nil || v.source == nil {
		return
	}
	v.mu.Lock()
	if v.loading {
		v.mu.Unlock()
		return
	}
	v.loading, v.asked = true, time.Now()
	v.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		list, err := v.source.Sessions(ctx)
		v.mu.Lock()
		v.loading = false
		if err == nil {
			v.list, v.loaded = list, true
		}
		v.err = err
		v.mu.Unlock()
		if v.invalidate != nil {
			v.invalidate()
		}
	}()
}

// sessions returns what is known: this device, the others and the
// incomplete logins, each the last active first.
func (v *sessionsView) sessions() (current *model.Session, others, incomplete []model.Session, loaded bool, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for i := range v.list {
		s := v.list[i]
		switch {
		case s.Current:
			current = &s
		case s.Incomplete:
			incomplete = append(incomplete, s)
		default:
			others = append(others, s)
		}
	}
	byActive := func(list []model.Session) {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Active.After(list[j].Active) })
	}
	byActive(others)
	byActive(incomplete)
	return current, others, incomplete, v.loaded, v.err
}

// count is how many devices are signed in, for the main settings page; 0
// while unknown.
func (v *sessionsView) count() int {
	if v == nil {
		return 0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	n := 0
	for _, s := range v.list {
		if !s.Incomplete {
			n++
		}
	}
	return n
}

func (v *sessionsView) row(hash int64) *surface {
	r := v.rows[hash]
	if r == nil {
		r = new(surface)
		v.rows[hash] = r
	}
	return r
}

// Update handles the rows, the retry and the dialog, and asks again when
// the list is old.
func (v *sessionsView) Update(gtx layout.Context) {
	v.mu.Lock()
	list := append([]model.Session(nil), v.list...)
	v.mu.Unlock()
	for _, s := range list {
		if v.row(s.Hash).Clicked(gtx) {
			v.detail = s
			v.dialog.Open()
		}
	}
	if v.retry.Clicked(gtx) {
		v.refresh()
	}
	if v.done.Clicked(gtx) {
		v.dialog.Close()
	}
	v.mu.Lock()
	next := v.asked.Add(sessionsRefresh)
	v.mu.Unlock()
	if time.Now().After(next) {
		v.refresh()
	} else {
		gtx.Execute(op.InvalidateCmd{At: next})
	}
}

func (v *sessionsView) Layout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	current, others, incomplete, loaded, err := v.sessions()
	told := ""
	if err != nil {
		told = l.T("sessions.failed") + ": " + mediaErrorText(err)
	}
	if told != "" && told != v.told && v.toast != nil {
		v.toast.Show(told)
	}
	v.told = told
	if !loaded {
		if err != nil {
			return card(gtx, func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &v.retry, l.T("history.retry")) }, defaultCardPadding)
		}
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(24).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return v.loader.sized(gtx, l, 40)
			})
		})
	}
	now := time.Now()
	group := func(title string, list []model.Session, about string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return card(gtx, func(gtx layout.Context) layout.Dimensions {
					children := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: 14, Top: 10, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return label(gtx, title, token.TypestyleTitleSmall, sc.Primary.Color, 1)
						})
					})}
					for _, s := range list {
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return v.layoutRow(gtx, s, now, l)
						}))
					}
					if about != "" {
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: 14, Right: 14, Top: 6, Bottom: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return label(gtx, about, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
							})
						}))
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
				}, 6)
			})
		})
	}
	var children []layout.FlexChild
	if current != nil {
		children = append(children, group(l.T("sessions.this"), []model.Session{*current}, ""))
	}
	if len(incomplete) > 0 {
		children = append(children, group(l.T("sessions.incomplete"), incomplete, l.T("sessions.incomplete_about")))
	}
	about := ""
	if len(others) == 0 {
		about = l.T("sessions.other_none")
	}
	children = append(children, group(l.T("sessions.other"), others, about))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// sessionPlace is the line under a session: where it is and when it was
// active, or that it is this one, online.
func sessionPlace(s model.Session, now time.Time, l localization.Catalog) string {
	place := s.Country
	if place == "" {
		place = s.IP
	}
	when := chatTime(s.Active, now, l)
	if s.Current {
		when = l.T("sessions.online")
	}
	if place == "" {
		return when
	}
	return place + " · " + when
}

// layoutRow draws a session: its kind's icon, the device, the app and where
// and when it was last seen.
func (v *sessionsView) layoutRow(gtx layout.Context, s model.Session, now time.Time, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(sessionsItemSize))
	style := surfaceStyle{radius: gtx.Dp(12), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor}
	return v.row(s.Hash).Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		pic := gtx.Dp(40)
		offset(gtx, image.Pt(gtx.Dp(14), (size.Y-pic)/2), func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, sc.PrimaryContainer.Color.AsNRGBA(), clip.Ellipse{Max: image.Pt(pic, pic)}.Op(gtx.Ops))
			icon := gtx.Dp(22)
			return offset(gtx, image.Pt((pic-icon)/2, (pic-icon)/2), func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(icon, icon), func(gtx layout.Context) layout.Dimensions {
					return sessionIcons[s.Kind()](gtx, sc.PrimaryContainer.OnColor)
				})
			})
		})
		x := gtx.Dp(14) + pic + gtx.Dp(14)
		tgtx := gtx
		tgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, size.X-x-gtx.Dp(14)), size.Y)}
		offset(tgtx, image.Pt(x, gtx.Dp(10)), func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, s.Device, token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, s.App, token.TypestyleBodySmall, sc.Surface.OnColor, 1)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, sessionPlace(v.shown(s), now, l), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
				}),
			)
		})
		return layout.Dimensions{Size: size}
	})
}

// layoutDialog shows what Telegram knows of the session clicked, over the
// window, as Telegram Desktop's session box does.
func (v *sessionsView) layoutDialog(gtx layout.Context, l localization.Catalog) {
	if v == nil || !v.dialog.Shown() {
		return
	}
	sc := scheme(gtx)
	s := v.shown(v.detail)
	when := l.T("sessions.online")
	if !s.Current {
		when = s.Active.Local().Format("02.01.2006 15:04")
	}
	yes := l.T("sessions.no")
	if s.Official {
		yes = l.T("sessions.yes")
	}
	v.dialog.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		return v.dialog.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, s.Device, token.TypestyleTitleLarge, sc.Surface.OnColor, 2)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, when, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("sessions.info"), token.TypestyleTitleSmall, sc.Primary.Color, 1)
				}),
				vspace(4),
			}
			for _, row := range [][2]string{
				{l.T("sessions.application"), s.App},
				{l.T("sessions.system"), s.System},
				{l.T("sessions.official"), yes},
				{l.T("sessions.ip"), s.IP},
				{l.T("sessions.location"), s.Country},
			} {
				if row[1] == "" {
					continue
				}
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: 6, Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return label(gtx, row[1], token.TypestyleBodyLarge, sc.Surface.OnColor, 2)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return label(gtx, row[0], token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
							}),
						)
					})
				}))
			}
			if s.Country != "" {
				children = append(children, vspace(4), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("sessions.location_about"), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
				}))
			}
			children = append(children, vspace(16), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Flex{Spacing: layout.SpaceStart}.Layout(gtx, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return textButton(gtx, &v.done, l.T("sessions.done"))
				}))
			}))
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		}, defaultCardPadding)
	})
}

// sessionsItemSize is the height of a session's row.
const sessionsItemSize = unit.Dp(76)
