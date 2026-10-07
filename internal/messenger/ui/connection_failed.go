// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"errors"

	"gio-mw/token"
	"gio-mw/widget/button"

	"gioui.org/layout"

	"komarugram/internal/messenger/account"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// connectionFailedDialog tells that the account's connection stopped and
// does not come back on its own, and offers to make it again. Ignoring it
// leaves what is saved here to read; the dialog does not come back until
// the window opens again or another attempt fails.
type connectionFailedDialog struct {
	modal         modal
	dismissed     bool
	retry, ignore *button.Button
	// retrying is set while the dialog closes for another attempt.
	retrying bool
	// err is the failure shown, kept while the dialog closes.
	err error
}

func newConnectionFailedDialog() *connectionFailedDialog {
	return &connectionFailedDialog{retry: button.Filled(), ignore: button.Text()}
}

// Update opens the dialog once the store reports the connection failed.
func (d *connectionFailedDialog) Update(gtx layout.Context, store model.Store) {
	source, ok := store.(model.ConnectionSource)
	if !ok || d.dismissed {
		return
	}
	err := source.ConnectionFailed()
	if err == nil {
		return
	}
	d.err = err
	if !d.modal.Shown() {
		d.modal.Open()
	}
	if d.ignore.Clicked(gtx) {
		d.dismissed = true
		d.modal.Close()
	}
	if d.retry.Clicked(gtx) {
		d.retrying = true
		d.modal.Close()
		source.Reconnect()
	}
}

// text says what went wrong: in words where the reason is known, and with
// the error itself otherwise.
func (d *connectionFailedDialog) text(l localization.Catalog) string {
	if errors.Is(d.err, account.ErrInUse) {
		return l.T("connection.in_use")
	}
	return l.T("connection.failed") + "\n\n" + d.err.Error()
}

func (d *connectionFailedDialog) Layout(gtx layout.Context, l localization.Catalog) {
	if !d.modal.Shown() || d.err == nil {
		return
	}
	sc := scheme(gtx)
	shown := d.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(440))
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("connection.title"), token.TypestyleTitleMedium, sc.Surface.OnColor, 2)
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, d.text(l), token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Spacing: layout.SpaceStart, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return d.ignore.Layout(gtx, l.T("session.ignore"))
						}),
						layout.Rigid(layout.Spacer{Width: 8}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return d.retry.Layout(gtx, l.T("connection.retry"))
						}),
					)
				}),
			)
		}, defaultCardPadding)
	})
	if !shown {
		// Escape or a click beside the dialog ignores it too; closed for
		// another attempt, it comes back if that fails.
		d.dismissed = !d.retrying
		d.retrying = false
	}
}
