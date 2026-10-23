// SPDX-License-Identifier: Unlicense OR MIT
package ui

import (
	"context"
	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"image"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/messenger/shortcuts"
	"testing"
	"time"
)

func TestShortcutRecordingConflictAndManualSystemCollision(t *testing.T) {
	prefs := preferences.Memory()
	catalog := append(shortcuts.Select("app.settings", "app.search", "history.copy"), shortcuts.Command{ID: "example.count", Label: "Stats", Scope: "history"})
	view := newShortcutSettings(func() shortcuts.Bindings { return prefs.Global().Shortcuts }, prefs.SetShortcuts, func() []shortcuts.Command { return catalog }, func() {}, nil)
	defer view.Close()
	occupied := shortcuts.Chord{Key: "T", Modifiers: key.ModCtrl | key.ModAlt}
	view.check = func(context.Context, []shortcuts.Chord) shortcuts.SystemReport {
		return shortcuts.SystemReport{KDE: true, Occupied: map[shortcuts.Chord]bool{occupied: true}}
	}
	var router input.Router
	p := &settingsPage{}
	now := time.Unix(1000, 0)
	frame := func() {
		gtx := layout.Context{Ops: new(op.Ops), Source: router.Source(), Now: now, Constraints: layout.Exact(image.Pt(900, 1800)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		view.Update(gtx, localization.For("en"))
		view.Layout(gtx, p, localization.For("en"))
		view.Overlay(gtx, localization.For("en"))
		router.Frame(gtx.Ops)
		now = now.Add(16 * time.Millisecond)
	}
	settle := func() {
		end := time.Now().Add(time.Second)
		for view.busy && time.Now().Before(end) {
			frame()
			time.Sleep(time.Millisecond)
		}
		if view.busy {
			t.Fatal("system probe did not settle")
		}
	}
	frame()
	settle()
	view.rows["app.search"].edit.click.Click()
	frame()
	frame()
	router.Queue(key.Event{Name: "Й", Physical: "Q", Modifiers: key.ModCtrl | key.ModAlt, State: key.Press})
	frame()
	settle()
	if view.candidate == nil || view.candidate.Key != "Q" {
		t.Fatal("recording depended on symbol", view.candidate)
	}
	generation := view.generation
	router.Queue(key.Event{Name: "Й", Physical: "Q", Modifiers: key.ModCtrl | key.ModAlt, State: key.Press})
	frame()
	settle()
	if view.generation != generation {
		t.Fatal("key repeat restarted the desktop probe")
	}
	view.save.click.Click()
	frame()
	if got := prefs.Global().Shortcuts["app.search"]; len(got) != 1 || got[0].Key != "Q" {
		t.Fatal("recording not saved", got)
	}
	for range 20 {
		frame()
	}
	view.rows["example.count"].edit.click.Click()
	frame()
	frame()
	router.Queue(key.Event{Name: "C", Physical: "C", Modifiers: key.ModCtrl, State: key.Press})
	frame()
	settle()
	view.save.click.Click()
	frame()
	if view.problem == "" || len(prefs.Global().Shortcuts["example.count"]) != 0 {
		t.Fatal("local conflict was silently saved")
	}
	view.manual.editor.SetText("Ctrl+Alt+T")
	frame()
	settle()
	if !view.report.Occupied[occupied] || view.candidate == nil || *view.candidate != occupied {
		t.Fatal("manual system collision was not checked")
	}
	view.save.click.Click()
	frame()
	if got := prefs.Global().Shortcuts["example.count"]; len(got) != 1 || got[0] != occupied {
		t.Fatal("advisory collision prevented explicit choice", got)
	}
}
func TestNavigationCancelsShortcutCapture(t *testing.T) {
	cancelled := false
	v := &shortcutSettings{stop: func() { cancelled = true }, candidate: &shortcuts.Chord{Key: "T", Modifiers: key.ModCtrl}, editing: "app.settings"}
	v.dialog.Open()
	a := &App{settings: &settingsPage{shortcuts: v}}
	a.openSection(layout.Context{}, section{kind: sectionAll})
	if v.Capturing() || v.candidate != nil || !cancelled {
		t.Fatal("navigation retained shortcut capture or its worker")
	}
}
