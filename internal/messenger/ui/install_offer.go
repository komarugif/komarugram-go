// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"gio-mw/token"
	"gio-mw/widget/button"
	"gio-mw/widget/toggle"

	"gioui.org/layout"

	"komarugram/internal/messenger/install"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
)

// installView is the sign-in's offer to install the program into the
// system (login.StepInstall): the folder, which the system's chooser can
// pick, and switches for the shortcuts. Installed, the program starts
// the installed copy in place of this one.
type installView struct {
	folder                *textField
	browse, install, skip *button.Button
	switches              *toggle.Toggle[string]
	texts                 map[string]string
	language              localization.Language

	// checked is the folder admin and replace were found for.
	checked        string
	admin, replace bool

	running bool
	result  chan installResult
	chosen  chan folderChoice
	problem string
	// install and choose are install.Install and chooseFolder; tests
	// replace them.
	installFn func(context.Context, install.Options) (string, error)
	chooseFn  func(context.Context, string) (string, error)
	started   bool
	// invalidate redraws the window when a result comes.
	invalidate func()
}

type installResult struct {
	exe string
	err error
}

type folderChoice struct {
	dir string
	err error
}

func newInstallView(invalidate func()) *installView {
	if invalidate == nil {
		invalidate = func() {}
	}
	v := &installView{
		folder:    newTextField(0, ""),
		browse:    button.Text(),
		install:   button.Filled(),
		skip:      button.Text(),
		switches:  toggle.NewToggle([]string{"desktop", "menu"}, []string{"desktop", "menu"}, func([]string) {}),
		result:    make(chan installResult, 1),
		chosen:    make(chan folderChoice, 1),
		installFn: install.Install,
		chooseFn:  chooseFolder,

		invalidate: invalidate,
	}
	return v
}

// systemKey is key, or its text of Linux there.
func systemKey(key string) string {
	if runtime.GOOS == "linux" {
		return key + "_linux"
	}
	return key
}

// Update handles the view's input, and tells l when the program is
// installed or the offer declined.
func (v *installView) Update(gtx layout.Context, l *login.Login, catalog localization.Catalog) {
	if !v.started {
		v.started = true
		v.folder.editor.SetText(install.DefaultDir())
	}
	select {
	case r := <-v.result:
		v.running = false
		switch {
		case r.err == nil:
			l.Installed(r.exe)
		case errors.Is(r.err, install.ErrCancelled):
			v.problem = catalog.T("install.cancelled")
		case errors.Is(r.err, install.ErrNeedsAdmin):
			v.problem = catalog.T("install.admin_linux")
		default:
			v.problem = fmt.Sprintf(catalog.T("install.failed"), r.err)
		}
	default:
	}
	select {
	case c := <-v.chosen:
		switch {
		case errors.Is(c.err, errNoChooser):
			v.problem = catalog.T("install.no_chooser")
		case c.err != nil:
			v.problem = c.err.Error()
		case c.dir != "":
			v.folder.editor.SetText(install.FolderFor(c.dir))
			v.problem = ""
		}
	default:
	}
	if dir := v.folder.Text(); dir != v.checked {
		v.checked = dir
		v.admin = install.NeedsAdmin(dir)
		existing, ok := install.Find()
		v.replace = ok && sameFolder(existing.Dir, dir)
	}
	if v.running {
		return
	}
	if v.skip.Clicked(gtx) {
		l.Continue()
		return
	}
	if v.browse.Clicked(gtx) {
		start := v.folder.Text()
		go func() {
			dir, err := v.chooseFn(context.Background(), start)
			v.chosen <- folderChoice{dir, err}
			v.invalidate()
		}()
	}
	submitted := v.folder.Submitted(gtx)
	if (v.install.Clicked(gtx) || submitted) && v.folder.Text() != "" {
		values := v.switches.GetValues()
		o := install.Options{Dir: v.folder.Text(), Desktop: slices.Contains(values, "desktop"), Menu: slices.Contains(values, "menu")}
		v.running, v.problem = true, ""
		go func() {
			exe, err := v.installFn(context.Background(), o)
			v.result <- installResult{exe, err}
			v.invalidate()
		}()
	}
}

// sameFolder reports whether a and b name one folder, as far as their
// text tells.
func sameFolder(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (v *installView) Layout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	if v.texts == nil || v.language != l.Language() {
		v.language = l.Language()
		v.texts = map[string]string{"desktop": l.T("install.desktop"), "menu": l.T(systemKey("install.menu"))}
	}
	note := ""
	switch {
	case v.admin:
		note = l.T(systemKey("install.admin"))
	case v.replace:
		note = l.T("install.replace")
	}
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("install.title"), token.TypestyleHeadlineSmall, sc.Surface.OnColor, 0)
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T(systemKey("install.body")), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
		vspace(20),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return v.folder.Layout(gtx, l.T("install.folder"), v.problem != "" || v.admin && runtime.GOOS == "linux")
		}),
		vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if v.running {
				v.browse.Disable()
			} else {
				v.browse.Enable()
			}
			return layout.Flex{Spacing: layout.SpaceStart}.Layout(gtx, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return v.browse.Layout(gtx, l.T("install.browse"))
			}))
		}),
	}
	if note != "" {
		rows = append(rows, vspace(4), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, note, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
		}))
	}
	rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return v.switches.Layout(gtx, v.texts)
	}))
	if v.problem != "" {
		rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, v.problem, token.TypestyleBodyMedium, sc.Error.Color, 0)
		}))
	}
	if v.running || v.folder.Text() == "" {
		v.install.Disable()
	} else {
		v.install.Enable()
	}
	if v.running {
		v.skip.Disable()
	} else {
		v.skip.Enable()
	}
	text := l.T("install.install")
	if v.running {
		text = l.T("install.installing")
	}
	rows = append(rows, vspace(20), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return v.skip.Layout(gtx, l.T("install.skip"))
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return v.install.Layout(gtx, text)
			}),
		)
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}
