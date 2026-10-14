// SPDX-License-Identifier: Unlicense OR MIT
package ui

import (
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/shortcuts"
)

type shortcutPreferences func() shortcuts.Bindings

func (get shortcutPreferences) bindings() shortcuts.Bindings {
	if get == nil {
		return nil
	}
	return get()
}
func (get shortcutPreferences) next(gtx layout.Context, focus event.Tag, ids ...string) (string, key.Event, bool) {
	return shortcuts.Next(gtx, focus, get.bindings(), shortcuts.Select(ids...))
}
func (a *App) shortcutPrefs() shortcuts.Bindings { return a.preferences.Global().Shortcuts }
func (a *App) shortcutCatalog() []shortcuts.Command {
	return shortcuts.Commands()
}
func (a *App) shortcutEvents(gtx layout.Context) {
	if a.section.kind == sectionSettings && a.settings.shortcuts.Capturing() {
		a.settings.shortcuts.UpdateCapture(gtx)
		return
	}
	for {
		id, e, ok := shortcutPreferences(a.shortcutPrefs).next(gtx, nil, "app.settings", "app.search", "app.saved", "app.profile", "app.theme")
		if !ok {
			break
		}
		if e.State != key.Press {
			continue
		}
		if a.viewer != nil && a.viewer.open {
			a.viewer.Close()
		}
		switch id {
		case "app.settings":
			a.openSection(gtx, section{kind: sectionSettings})
		case "app.search":
			a.openSection(gtx, section{kind: sectionSearch})
		case "app.saved":
			a.openSection(gtx, section{kind: sectionSaved})
		case "app.profile":
			a.openSection(gtx, section{kind: sectionProfile})
		case "app.theme":
			a.toggleTheme()
		}
	}
}
func (p *chatPage) selectionShortcutEvents(gtx layout.Context, l localization.Catalog) {
	for {
		id, e, ok := p.shortcuts.next(gtx, &p.keyboard, "history.copy", "history.select_all", "chat.search")
		if !ok {
			break
		}
		if e.State != key.Press {
			continue
		}
		switch id {
		case "chat.search":
			if p.canSearchChat() {
				p.openChatSearch(gtx)
			}
		case "history.copy":
			if p.activeText != nil {
				copySelection(gtx, p.activeText)
			}
		case "history.select_all":
			if r := p.activeText; r != nil {
				r.text.anchor, r.text.caret = 0, 0
				for _, run := range r.runs {
					r.text.caret += len([]rune(run.Text))
				}
			}
		}
	}
}
