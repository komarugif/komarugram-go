// SPDX-License-Identifier: Unlicense OR MIT

// Command kitchen is the MD3 widget kitchen: one page per widget of gio-mw
// and per media experiment.
package main

import (
	"gioui.org/io/system"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/kitchen/app_shell"
	"komarugram/internal/kitchen/services"
)

func main() {
	opts := appwindow.Options{
		Title:        "Material Example",
		Width:        unit.Dp(1280),
		Height:       unit.Dp(640),
		Locale:       system.Locale{Language: "en", Direction: system.LTR},
		QuitOnEscape: true,
	}
	appwindow.SetLanguage("en")
	appwindow.Main(opts, func(w *appwindow.Window) appwindow.Content {
		services.SetMotionService(w.Motion)
		return app_shell.NewAppShell(w.Window)
	})
}
