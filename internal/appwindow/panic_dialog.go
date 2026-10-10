// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"io"
	"os/exec"
	"runtime"
	"strings"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/token"

	"gioui.org/io/clipboard"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"komarugram/internal/crash"
)

const panicDialogWhere = "crash dialog"

// showPanic is called by crash.Report after the text file has been written.
// One incident opens one window; dismissing it silences that incident for the
// rest of this run, including a panic that repeats on every frame.
func (h *Host) showPanic(p *crash.Panic) {
	if p.Where == panicDialogWhere {
		return
	}
	key := p.Where
	if !h.beginPanicNotice(key) {
		return
	}
	h.Open(Spec{
		Options: Options{
			Title: "Ошибка приложения", Width: unit.Dp(620), Height: unit.Dp(320),
			TopMost: true, QuitOnEscape: true, panicDialog: true,
		},
		Build:  func(w *Window) Content { return newPanicDialog(w, p) },
		Closed: func() { h.ignorePanicNotice(key) },
	})
}

func (h *Host) beginPanicNotice(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.crashDialogs[key] || h.ignoredPanics[key] {
		return false
	}
	if h.crashDialogs == nil {
		h.crashDialogs = make(map[string]bool)
	}
	h.crashDialogs[key] = true
	return true
}

func (h *Host) ignorePanicNotice(key string) {
	h.mu.Lock()
	delete(h.crashDialogs, key)
	if h.ignoredPanics == nil {
		h.ignoredPanics = make(map[string]bool)
	}
	h.ignoredPanics[key] = true
	h.mu.Unlock()
}

type panicDialog struct {
	w                  *Window
	p                  *crash.Panic
	theme              *token.Theme
	material           *material.Theme
	copy, open, ignore widget.Clickable
	opening            bool
	opened             chan error
	status             string
}

func newPanicDialog(w *Window, p *crash.Panic) *panicDialog {
	return &panicDialog{w: w, p: p, material: material.NewTheme(), opened: make(chan error, 1)}
}

func (d *panicDialog) Theme(gtx layout.Context) *token.Theme {
	if d.theme == nil {
		d.theme = defaults.NewTheme(gtx, schemes.SchemeBaselineLight())
	}
	return d.theme
}

func (d *panicDialog) Update(gtx layout.Context) {
	if d.copy.Clicked(gtx) {
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(d.p.Text()))})
		d.status = "Отчёт скопирован"
	}
	if d.open.Clicked(gtx) && d.p.Path != "" && !d.opening {
		d.opening = true
		go func() { d.opened <- openPanicReport(d.p.Path); d.w.Invalidate() }()
	}
	select {
	case err := <-d.opened:
		d.opening = false
		if err != nil {
			d.status = "Не удалось открыть файл: " + err.Error()
		} else {
			d.status = "Файл открыт"
		}
	default:
	}
	if d.ignore.Clicked(gtx) {
		d.w.Perform(system.ActionClose)
	}
}

func openPanicReport(path string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin", "haiku":
		command = exec.Command("open", path)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		command = exec.Command("xdg-open", path)
	}
	return command.Run()
}

func (d *panicDialog) button(gtx layout.Context, click *widget.Clickable, text string) layout.Dimensions {
	button := material.Button(d.material, click, text)
	button.TextSize = unit.Sp(14)
	return button.Layout(gtx)
}

func (d *panicDialog) Layout(gtx layout.Context) {
	paint.Fill(gtx.Ops, d.material.Bg)
	layout.UniformInset(unit.Dp(24)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.H6(d.material, "В приложении произошла ошибка").Layout),
			layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				label := material.Body1(d.material, d.p.Error())
				label.MaxLines = 3
				return label.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				text := "Не удалось сохранить отчёт в файл. Его можно скопировать."
				if d.p.Path != "" {
					text = "Отчёт: " + d.p.Path
				}
				label := material.Body2(d.material, text)
				label.MaxLines = 2
				return label.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.button(gtx, &d.copy, "Скопировать") }),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if d.p.Path == "" || d.opening {
							gtx = gtx.Disabled()
						}
						return d.button(gtx, &d.open, "Открыть файл")
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return d.button(gtx, &d.ignore, "Игнорировать")
					}),
				)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				label := material.Caption(d.material, d.status)
				label.MaxLines = 2
				return label.Layout(gtx)
			}),
		)
	})
}
