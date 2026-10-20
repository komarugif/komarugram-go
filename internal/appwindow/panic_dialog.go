// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"image"
	"io"
	"strings"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/exp/appearance"
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/button"
	"gio-mw/widget/scroll"

	"gioui.org/io/clipboard"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"

	"komarugram/internal/buildinfo"
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
			Title: texts().T("window.error_title"), Width: unit.Dp(640), Height: unit.Dp(440),
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

// panicDialog shows a panic with its stack and offers to copy the report,
// or to open an issue of it on GitHub. The report file crash.Report wrote
// is not shown: it stays for whoever looks for it.
type panicDialog struct {
	w            *Window
	p            *crash.Panic
	theme        *token.Theme
	themeFonts   uint64
	dark         bool
	copy, ignore *button.Button
	issue        *button.Button
	// opened tells how opening the issue went, once the browser has it.
	opened chan error
	// issueFilled is whether the issue opened has the report in it.
	issueFilled bool
	opening     bool
	trace       scroll.List
	// stack is the panic's, a tab in front of a frame's file widened to
	// spaces, which the shaper draws narrow.
	stack  string
	status string
}

func newPanicDialog(w *Window, p *crash.Panic) *panicDialog {
	stack := strings.ReplaceAll(strings.TrimRight(string(p.Stack), "\n"), "\t", "    ")
	return &panicDialog{
		w: w, p: p, copy: button.Filled(), ignore: button.Text(), issue: button.Text(),
		opened: make(chan error, 1),
		trace:  scroll.List{List: layout.List{Axis: layout.Vertical}},
		stack:  stack,
	}
}

// Theme is the one the user picked in the application, as the host was
// last told it: the dialog depends on nothing from the content.
func (d *panicDialog) Theme(gtx layout.Context) *token.Theme {
	theme := d.w.host.Theme()
	dark := theme == ThemeDark || theme == ThemeAuto && d.w.Appearance != nil && d.w.Appearance.Scheme() == appearance.Dark
	if v := defaults.FontsVersion(); d.theme == nil || v != d.themeFonts || dark != d.dark {
		d.themeFonts, d.dark = v, dark
		scheme := schemes.SchemeBaselineLight()
		if dark {
			scheme = schemes.SchemeBaselineDark()
		}
		d.theme = defaults.NewTheme(gtx, scheme)
	}
	d.w.SetFrameDark(dark)
	d.w.SetFrameColor(d.theme.Scheme.SurfaceContainerLowest.AsNRGBA())
	return d.theme
}

func (d *panicDialog) Update(gtx layout.Context) {
	if d.copy.Clicked(gtx) {
		d.copyReport(gtx)
		d.status = texts().T("window.copied")
	}
	if d.issue.Clicked(gtx) && !d.opening {
		link, filled := panicIssue(d.p, buildinfo.Current())
		if !filled {
			// The form opens without the report, for it to be pasted.
			d.copyReport(gtx)
		}
		d.opening, d.issueFilled = true, filled
		go func() { d.opened <- openBrowser(link); d.w.Invalidate() }()
	}
	select {
	case err := <-d.opened:
		d.opening = false
		switch {
		case err != nil:
			d.status = texts().Format("window.issue_failed", map[string]string{"error": err.Error()})
		case d.issueFilled:
			d.status = texts().T("window.issue_opened")
		default:
			d.status = texts().T("window.issue_paste")
		}
	default:
	}
	if d.ignore.Clicked(gtx) {
		d.w.Perform(system.ActionClose)
	}
}

func (d *panicDialog) copyReport(gtx layout.Context) {
	gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(d.p.Text()))})
}

func panicLabel(txt string, style token.Typestyle, col token.MatColor, maxLines int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return wdk.LayoutLabel(gtx, wdk.LabelStyle{Typestyle: style, Color: col, MaxLines: maxLines}, txt)
	}
}

// Layout puts the stack in a box that takes what the window leaves it, and
// the buttons at the right, as the app's dialogs have them.
func (d *panicDialog) Layout(gtx layout.Context) {
	sc := wdk.GetMaterialTheme(gtx).Scheme
	paint.Fill(gtx.Ops, sc.SurfaceContainerLowest.AsNRGBA())
	layout.UniformInset(unit.Dp(24)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(panicLabel(texts().T("window.panic"), token.TypestyleTitleLarge, sc.Surface.OnColor, 2)),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(panicLabel(d.p.Error(), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 3)),
			layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
			layout.Flexed(1, d.layoutTrace),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				// What the last button did, in a line of its own: beside
				// three buttons there is no room for it.
				if d.status == "" {
					return layout.Spacer{Height: unit.Dp(16)}.Layout(gtx)
				}
				return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(12)}.Layout(gtx,
					panicLabel(d.status, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 2))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Spacing: layout.SpaceStart, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return d.ignore.Layout(gtx, texts().T("window.ignore"))
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if d.opening {
							gtx = gtx.Disabled()
						}
						return d.issue.Layout(gtx, texts().T("window.report_issue"))
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return d.copy.Layout(gtx, texts().T("window.copy"))
					}),
				)
			}),
		)
	})
}

// layoutTrace draws the stack in a rounded box that fills the constraints.
// It is one label, so that a wrapped line is as far from the next as two
// lines are.
func (d *panicDialog) layoutTrace(gtx layout.Context) layout.Dimensions {
	sc := wdk.GetMaterialTheme(gtx).Scheme
	size := gtx.Constraints.Max
	box := clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(12))
	paint.FillShape(gtx.Ops, sc.SurfaceContainer.AsNRGBA(), box.Op(gtx.Ops))
	defer box.Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(size)
	return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return d.trace.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
			gtx.Constraints.Min.X = 0
			// Paths and calls have no spaces to wrap at: they break anywhere.
			style := wdk.LabelStyle{Typestyle: token.TypestylePreformatted, Color: sc.Surface.OnColor, WrapPolicy: text.WrapGraphemes}
			return wdk.LayoutLabel(gtx, style, d.stack)
		})
	})
}
