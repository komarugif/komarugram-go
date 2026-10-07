// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"gio-mw/token"
	"gio-mw/widget/button"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// sessionEndedDialog tells that Telegram ended the account's session and
// offers to log out. Ignoring it keeps the account and what it has saved
// here, to read without a connection; the dialog does not come back until
// the window opens again.
type sessionEndedDialog struct {
	modal          modal
	dismissed      bool
	logOut, ignore *button.Button
	why            model.SessionEnd
	// leave logs the window's account out; nil when there is no one to ask.
	leave func()
}

// sessionEndedText is the localization keys of the title and the text that
// tell each reason.
var sessionEndedText = map[model.SessionEnd][2]string{
	model.SessionDuplicated:   {"session.title_ended", "session.ended_duplicated"},
	model.SessionRevoked:      {"session.title_ended", "session.ended_revoked"},
	model.SessionExpired:      {"session.title_ended", "session.ended_expired"},
	model.SessionUnregistered: {"session.title_ended", "session.ended_unregistered"},
	model.SessionDeleted:      {"session.title_deleted", "session.ended_deleted"},
	model.SessionBanned:       {"session.title_banned", "session.ended_banned"},
}

func newSessionEndedDialog(leave func()) *sessionEndedDialog {
	return &sessionEndedDialog{logOut: button.Filled(), ignore: button.Text(), leave: leave}
}

// Update opens the dialog once the store reports the session ended.
func (d *sessionEndedDialog) Update(gtx layout.Context, store model.Store) {
	source, ok := store.(model.SessionSource)
	if !ok || d.dismissed {
		return
	}
	d.why = source.SessionEnded()
	if d.why == model.SessionAlive {
		return
	}
	if !d.modal.Shown() {
		d.modal.Open()
	}
	if d.ignore.Clicked(gtx) {
		d.dismissed = true
		d.modal.Close()
	}
	if d.logOut.Clicked(gtx) && d.leave != nil {
		d.dismissed = true
		d.modal.Close()
		d.leave()
	}
}

func (d *sessionEndedDialog) Layout(gtx layout.Context, l localization.Catalog) {
	if !d.modal.Shown() {
		return
	}
	sc := scheme(gtx)
	shown := d.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(440))
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T(sessionEndedText[d.why][0]), token.TypestyleTitleMedium, sc.Surface.OnColor, 2)
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T(sessionEndedText[d.why][1]), token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Spacing: layout.SpaceStart, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return d.ignore.Layout(gtx, l.T("session.ignore"))
						}),
						layout.Rigid(layout.Spacer{Width: 8}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if d.leave == nil {
								return layout.Dimensions{}
							}
							return d.logOut.Layout(gtx, l.T("account.logout"))
						}),
					)
				}),
			)
		}, defaultCardPadding)
	})
	if !shown {
		// Escape or a click beside the dialog ignores it too.
		d.dismissed = true
	}
}
