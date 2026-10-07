// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"image"
	"io"
	"log"
	"slices"
	"strings"

	"gio-mw/token"
	"gio-mw/widget/button"
	"gio-mw/widget/checkbox"
	"gio-mw/widget/slider"

	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/messenger/security"
)

type securityView struct {
	height         heightTransition
	manager        *security.Manager
	invalidate     func()
	preferences    *preferences.Store
	autoLock       *slider.Slider
	autoLockValues []int
	lockOptions    *checkbox.Checkboxes[string]

	master, confirm, unlock               *textField
	decryptPassword                       *textField
	enable, open                          *button.Button
	decrypt, decryptSubmit, decryptCancel *button.Button
	decryptPrompt                         bool
	recheck, copy                         *button.Button
	running                               bool
	result                                chan error
	localProblem                          string
	// copied is the command last put on the clipboard.
	copied string
	// focused is set once the first field of the unlock screen or of the
	// offer to protect has been focused, and cleared by a failure, so that
	// the password can be typed again at once.
	focused bool
}

func (v *securityView) SetPreferences(p *preferences.Store) {
	v.preferences = p
	v.setAutoLockSlider(p.Global().AutoLockMinutes)
	v.lockOptions = checkbox.NewCheckboxes([]string{"minimize", "close"}, nil, func(values []string) {
		g := p.Global()
		minimize, close := false, false
		for _, value := range values {
			if value == "minimize" {
				minimize = true
			}
			if value == "close" {
				close = true
			}
		}
		if err := p.SetWindowLock(g.AutoLockMinutes, minimize, close); err != nil {
			log.Printf("save window lock: %v", err)
		}
	})
}

var autoLockPresets = []int{0, 1, 5, 15, 30, 60, 120}

func autoLockOptions(current int) []int {
	options := slices.Clone(autoLockPresets)
	if !slices.Contains(options, current) {
		options = append(options, current)
		slices.Sort(options)
	}
	return options
}

// A saved custom value remains available until a preset is chosen.
func (v *securityView) setAutoLockSlider(minutes int) {
	options := autoLockOptions(minutes)
	if v.autoLock != nil && slices.Equal(options, v.autoLockValues) {
		v.autoLock.SetValue(minutes)
		return
	}
	v.autoLockValues = options
	v.autoLock = slider.StandardSlider(options, minutes, func(minutes int) {
		g := v.preferences.Global()
		if err := v.preferences.SetWindowLock(minutes, g.LockOnMinimize, g.LockOnClose); err != nil {
			log.Printf("save window lock: %v", err)
		}
	})
}

func newSecurityView(manager *security.Manager, invalidate func()) *securityView {
	return &securityView{
		manager: manager, invalidate: invalidate,
		master: newTextField('•', ""), confirm: newTextField('•', ""), unlock: newTextField('•', ""),
		decryptPassword: newTextField('•', ""),
		enable:          button.Filled(), open: button.Filled(), result: make(chan error, 1),
		decrypt: button.Outlined(), decryptSubmit: button.Filled(), decryptCancel: button.Text(),
		recheck: button.Outlined(), copy: button.Text(),
	}
}

// UpdateSettings handles the form that enables protection and the help
// about TPM access, wherever they are shown.
func (v *securityView) UpdateSettings(gtx layout.Context) {
	if v == nil || v.manager == nil {
		return
	}
	v.finishOperation()
	v.updateAccess(gtx)
	if v.preferences != nil {
		g := v.preferences.Global()
		v.setAutoLockSlider(g.AutoLockMinutes)
		values := []string{}
		if g.LockOnMinimize {
			values = append(values, "minimize")
		}
		if g.LockOnClose {
			values = append(values, "close")
		}
		v.lockOptions.SetValues(values)
		v.lockOptions.Update(gtx)
	}
	state := v.manager.State()
	if state.Enabled {
		if v.decrypt.Clicked(gtx) {
			v.decryptPrompt = true
			v.localProblem = ""
		}
		if v.decryptCancel.Clicked(gtx) {
			v.decryptPrompt = false
			v.decryptPassword.Clear()
			v.localProblem = ""
		}
		if v.decryptPrompt && !state.Busy && !v.running && (v.decryptSubmit.Clicked(gtx) || v.decryptPassword.Submitted(gtx)) {
			password := v.decryptPassword.Text()
			if password == "" {
				v.localProblem = "empty"
			} else {
				v.localProblem = ""
				v.start(func() error { return v.manager.Disable(context.Background(), password) })
			}
		}
		return
	}
	if state.Busy || v.running {
		return
	}
	submit := v.confirm.Submitted(gtx)
	if v.master.Submitted(gtx) {
		// Enter in the first field goes on to the second until it is filled.
		if v.confirm.Text() == "" {
			v.confirm.Focus(gtx)
		} else {
			submit = true
		}
	}
	if v.enable.Clicked(gtx) || submit {
		password := v.master.Text()
		switch {
		case password == "":
			v.localProblem = "empty"
		case password != v.confirm.Text():
			v.localProblem = "mismatch"
		default:
			v.localProblem = ""
			v.start(func() error { return v.manager.Enable(context.Background(), password) })
		}
	}
}

// FocusSetup focuses the master password field of the offer to protect,
// once it is shown.
func (v *securityView) FocusSetup(gtx layout.Context) {
	if v == nil || v.manager == nil || v.focused {
		return
	}
	if state := v.manager.State(); state.Available && !state.Enabled {
		v.master.Focus(gtx)
		v.focused = true
	}
}

// updateAccess handles the buttons of the help about TPM access.
func (v *securityView) updateAccess(gtx layout.Context) {
	if v.recheck.Clicked(gtx) {
		v.manager.Recheck()
	}
	if command := accessCommand(v.manager.Access()); command != "" && v.copy.Clicked(gtx) {
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(command))})
		v.copied = command
	}
}

func (v *securityView) UpdateUnlock(gtx layout.Context) {
	if v == nil || v.manager == nil {
		return
	}
	v.finishOperation()
	v.updateAccess(gtx)
	state := v.manager.State()
	if !state.Enabled || state.Unlocked || state.Busy || v.running {
		return
	}
	if !v.focused && state.Available {
		v.unlock.Focus(gtx)
		v.focused = true
	}
	if v.open.Clicked(gtx) || v.unlock.Submitted(gtx) {
		password := v.unlock.Text()
		if password == "" {
			v.localProblem = "empty"
			return
		}
		v.localProblem = ""
		v.start(func() error { return v.manager.Unlock(context.Background(), password) })
	}
}

func (v *securityView) start(operation func() error) {
	v.running = true
	go func() {
		v.result <- operation()
		if v.invalidate != nil {
			v.invalidate()
		}
	}()
}

func (v *securityView) finishOperation() {
	select {
	case err := <-v.result:
		v.running = false
		v.master.Clear()
		v.confirm.Clear()
		v.unlock.Clear()
		v.decryptPassword.Clear()
		if err != nil {
			v.localProblem = "failed"
			v.focused = false
			log.Printf("local data protection: %v", err)
		} else {
			v.decryptPrompt = false
			v.localProblem = ""
		}
	default:
	}
}

func (v *securityView) SettingsLayout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	state := security.State{}
	if v != nil && v.manager != nil {
		state = v.manager.State()
	}
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("security.local_title"), token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
		}),
		vspace(8),
	}
	status, color := l.T("security.off"), sc.SurfaceVariant.OnColor
	switch {
	case state.Enabled && state.Unlocked:
		status, color = l.T("security.on"), sc.Primary.Color
	case state.Enabled:
		status, color = l.T("security.locked"), sc.Error.Color
	case !state.Available:
		status, color = l.T("security.unavailable"), sc.Error.Color
	}
	rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return label(gtx, status, token.TypestyleBodyLarge, color, 0)
	}), vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return label(gtx, l.T("security.explanation"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
	}))
	if !state.Enabled {
		rows = append(rows, vspace(16), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return v.SetupLayout(gtx, l, nil)
		}))
	} else {
		rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("security.recovery_warning"), token.TypestyleBodyMedium, sc.Error.Color, 0)
		}))
		rows = append(rows, vspace(16), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("security.decrypt_body"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}))
		if !v.decryptPrompt {
			rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return v.decrypt.Layout(gtx, l.T("security.decrypt"))
			}))
		} else {
			rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return v.decryptPassword.Layout(gtx, l.T("security.password"), v.localProblem != "" || state.Problem != "")
			}))
			if problem := v.problem(l, state); problem != "" {
				rows = append(rows, vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, problem, token.TypestyleBodyMedium, sc.Error.Color, 0)
				}))
			}
			if state.Busy || v.running || v.decryptPassword.Text() == "" {
				v.decryptSubmit.Disable()
			} else {
				v.decryptSubmit.Enable()
			}
			rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return buttonRow(gtx,
					func(gtx layout.Context) layout.Dimensions { return v.decryptCancel.Layout(gtx, l.T("security.cancel")) },
					func(gtx layout.Context) layout.Dimensions {
						text := l.T("security.decrypt")
						if state.Busy || v.running {
							text = l.T("security.decrypting")
						}
						return v.decryptSubmit.Layout(gtx, text)
					})
			}))
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

func (v *securityView) WindowLockLayout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("security.window_lock_title"), token.TypestyleTitleMedium, sc.Surface.OnColor, 0)
		}),
		vspace(8),
	}
	if v == nil || v.manager == nil || !v.manager.State().Enabled || v.preferences == nil {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("security.window_lock_unavailable"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	}
	rows = append(rows,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			minutes := v.autoLock.GetValue()
			text := l.T("security.auto_lock_off")
			if minutes > 0 {
				text = fmt.Sprintf(l.T("security.auto_lock_minutes"), minutes)
			}
			return label(gtx, text, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.autoLock.Layout(gtx) }),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return v.lockOptions.Layout(gtx, map[string]string{
				"minimize": l.T("security.lock_on_minimize"), "close": l.T("security.lock_on_close"),
			})
		}),
	)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// SetupLayout draws what enabling protection takes: the master password
// form when the TPM can be used, and how to make it usable when it cannot.
// other, if set, is a button put at the start of the row of its buttons.
func (v *securityView) SetupLayout(gtx layout.Context, l localization.Catalog, other layout.Widget) layout.Dimensions {
	if v == nil || v.manager == nil {
		return layout.Dimensions{}
	}
	state := v.manager.State()
	if !state.Available {
		return v.accessLayout(gtx, l, other)
	}
	sc := scheme(gtx)
	invalid := v.localProblem != "" || state.Problem != ""
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return v.master.Layout(gtx, l.T("security.password"), invalid)
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return v.confirm.Layout(gtx, l.T("security.confirm"), invalid)
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("security.forgotten"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
		vspace(12),
	}
	if problem := v.problem(l, state); problem != "" {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, problem, token.TypestyleBodyMedium, sc.Error.Color, 0)
		}), vspace(12))
	}
	if state.Busy || v.running || strings.TrimSpace(v.master.Text()) == "" || v.master.Text() != v.confirm.Text() {
		v.enable.Disable()
	} else {
		v.enable.Enable()
	}
	rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return buttonRow(gtx, other, func(gtx layout.Context) layout.Dimensions {
			text := l.T("security.enable")
			if state.Busy || v.running {
				text = l.T("security.enabling")
			}
			return v.enable.Layout(gtx, text)
		})
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// buttonRow puts main at the end of a row and other, if set, at its start.
func buttonRow(gtx layout.Context, other, main layout.Widget) layout.Dimensions {
	if other == nil {
		return main(gtx)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(other),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
		}),
		layout.Rigid(main),
	)
}

// accessCommand is what the user can run to give this process the TPM, empty
// when there is nothing to run.
func accessCommand(access security.Access) string {
	switch access.Kind {
	case security.AccessNoRule:
		return "sudo apt install tpm-udev\nsudo usermod -aG tss $USER"
	case security.AccessNoGroup:
		return "sudo usermod -aG " + access.Group + " $USER"
	}
	return ""
}

// accessLayout explains why the TPM cannot be used and how to change that.
func (v *securityView) accessLayout(gtx layout.Context, l localization.Catalog, other layout.Widget) layout.Dimensions {
	sc := scheme(gtx)
	access := v.manager.Access()
	var text string
	switch access.Kind {
	case security.AccessMissing:
		text = l.T("security.tpm_missing")
	case security.AccessNoRule:
		text = fmt.Sprintf(l.T("security.tpm_no_rule"), access.Device)
	case security.AccessNoGroup:
		text = fmt.Sprintf(l.T("security.tpm_no_group"), access.Device, access.Group)
	case security.AccessRelogin:
		text = fmt.Sprintf(l.T("security.tpm_relogin"), access.Group)
	default:
		text = fmt.Sprintf(l.T("security.tpm_failed"), access.Detail)
	}
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, text, token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
		}),
	}
	if command := accessCommand(access); command != "" {
		rows = append(rows, vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return v.layoutCommand(gtx, l, command)
		}), vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("security.tpm_then_relogin"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}))
	}
	if !v.manager.State().Enabled {
		rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("security.tpm_later"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}))
	}
	rows = append(rows,
		vspace(16),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return buttonRow(gtx, other, func(gtx layout.Context) layout.Dimensions {
				return v.recheck.Layout(gtx, l.T("security.check_again"))
			})
		}),
	)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// layoutCommand draws a shell command on a panel of its own, with a button
// that copies it.
func (v *securityView) layoutCommand(gtx layout.Context, l localization.Catalog, command string) layout.Dimensions {
	sc := scheme(gtx)
	macro := op.Record(gtx.Ops)
	dims := layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, command, token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				text := l.T("security.copy")
				if v.copied == command {
					text = l.T("security.copied")
				}
				return v.copy.Layout(gtx, text)
			}),
		)
	})
	call := macro.Stop()
	fillRounded(gtx, sc.SurfaceContainerHighest, dims.Size, gtx.Dp(8))
	call.Add(gtx.Ops)
	return dims
}

func (v *securityView) UnlockLayout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	fillRect(gtx, sc.SurfaceContainerLow, size)
	state := v.manager.State()
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = layout.Constraints{}.Min
		return layout.UniformInset(16).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(unit.Dp(420)))
			return v.height.Card(gtx, func(gtx layout.Context) layout.Dimensions {
				rows := []layout.FlexChild{
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, l.T("security.unlock_title"), token.TypestyleHeadlineSmall, sc.Surface.OnColor, 0)
					}),
					vspace(8),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, l.T("security.unlock_body"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
					}),
				}
				if !state.Available {
					// The key cannot be unsealed without the TPM, whatever
					// the password.
					rows = append(rows, vspace(16), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return v.accessLayout(gtx, l, nil)
					}))
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
				}
				rows = append(rows, vspace(20), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return v.unlock.Layout(gtx, l.T("security.password"), state.Problem != "" || v.localProblem != "")
				}))
				if problem := v.problem(l, state); problem != "" {
					rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, problem, token.TypestyleBodyMedium, sc.Error.Color, 0)
					}))
				}
				if state.Busy || v.running || v.unlock.Text() == "" {
					v.open.Disable()
				} else {
					v.open.Enable()
				}
				rows = append(rows, vspace(20), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					text := l.T("security.unlock")
					if state.Busy || v.running {
						text = l.T("security.unlocking")
					}
					return v.open.Layout(gtx, text)
				}))
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
			}, defaultCardPadding)
		})
	})
	return layout.Dimensions{Size: size}
}

func (v *securityView) problem(l localization.Catalog, state security.State) string {
	switch v.localProblem {
	case "empty":
		return l.T("security.empty")
	case "mismatch":
		return l.T("security.mismatch")
	case "failed":
		return l.T("security.failed")
	}
	if state.Problem != "" {
		return l.T("security.failed")
	}
	return ""
}
