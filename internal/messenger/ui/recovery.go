// SPDX-License-Identifier: Unlicense OR MIT

package ui

import "komarugram/internal/messenger/model"

// RecoverFrame runs after appwindow has discarded a frame whose Update or
// Layout panicked, and reports whether to draw again at once. It closes the
// photo viewer, or leaves the settings subsection, whose layout panicked.
// A panic elsewhere is tried once more as it was, then the window leaves
// for its list of chats: what panics in every frame cannot keep the window
// on its panic screen.
func (a *App) RecoverFrame() bool {
	if a.viewer != nil && a.viewer.recoverFrame() {
		a.chats.toast.Show(a.catalog().T("viewer.recovered"))
		return true
	}
	if a.recoverSettings() {
		return true
	}
	a.panics++
	switch a.panics {
	case 1:
		// What panicked may have been passing, as a store changing under
		// the frame.
		return true
	case 2:
		a.leaveToChats()
		a.chats.toast.Show(a.catalog().T("window.recovered"))
		return true
	}
	// The list of chats panics too: the panic screen stays, and the next
	// event tries again.
	return false
}

// recoverSettings leaves a settings subsection whose layout panicked.
func (a *App) recoverSettings() bool {
	p := a.settings
	if p == nil || !p.layingOut {
		return false
	}
	p.layingOut = false
	if p.section == settingsMain {
		return false
	}
	p.section = settingsMain
	// The main page opens at its top, not where the subsection was scrolled.
	p.scrollPage = scrollPage{}
	p.confirmingLogOut = false
	if p.sessions != nil {
		p.sessions.dialog = modal{}
	}
	p.toast.Show(a.catalog().T("settings.recovered"))
	return true
}

// leaveToChats closes all the window shows over its list of chats: the
// open chat with what is open over it, its comments and info, and a
// section other than the chats.
func (a *App) leaveToChats() {
	if a.info != nil {
		a.info.Close()
	}
	a.closeComments()
	for _, p := range []*chatPage{a.history, a.comments} {
		if p != nil {
			p.abandon()
		}
	}
	a.selected, a.found, a.returns = 0, model.Chat{}, nil
	a.section = section{kind: sectionAll}
}
