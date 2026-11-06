// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"time"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/widget"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// viewerAvatar deletes the photo of a profile shown, after asking, or makes it the
// profile's main one, as Telegram Desktop's viewer does; the viewer closes on what
// was done, as there.
type viewerAvatar struct {
	main, remove widget.Clickable
	// modal asks whether to delete asked.
	modal           modal
	asked           model.Message
	confirm, cancel surface
	// done takes what came of the request under way; nil for none.
	done chan avatarOutcome
	stop context.CancelFunc
}

type avatarOutcome struct {
	deleted bool
	err     error
}

// reset stops what was asked and forgets the dialog.
func (a *viewerAvatar) reset() {
	if a.stop != nil {
		a.stop()
	}
	*a = viewerAvatar{}
}

// avatarEditor is the store's way to change the photos of a profile, when the
// viewer shows them.
func (v *mediaViewer) avatarEditor() (model.ProfilePhotoEditor, bool) {
	if v.gallery != galleryProfile || v.avatar.done != nil {
		return nil, false
	}
	editor, ok := v.source.(model.ProfilePhotoEditor)
	return editor, ok
}

// canDeleteAvatar reports whether photo m of the profile shown may be deleted.
func (v *mediaViewer) canDeleteAvatar(m model.Message) bool {
	editor, ok := v.avatarEditor()
	return ok && editor.CanDeleteProfilePhoto(m)
}

// canMakeAvatarMain reports whether photo m may become the profile's main one.
func (v *mediaViewer) canMakeAvatarMain(m model.Message) bool {
	editor, ok := v.avatarEditor()
	return ok && editor.CanMakeProfilePhotoMain(m)
}

// updateAvatar takes the clicks of the buttons and the dialog, and what came of
// the request, for photo m shown.
func (v *mediaViewer) updateAvatar(gtx layout.Context, m model.Message, l localization.Catalog) {
	a := &v.avatar
	if a.done != nil {
		select {
		case o := <-a.done:
			a.done, a.stop = nil, nil
			v.avatarOutcome(o, l)
			return
		default:
		}
	}
	if a.main.Clicked(gtx) && v.canMakeAvatarMain(m) {
		v.startAvatar(m, false)
	}
	if a.remove.Clicked(gtx) && v.canDeleteAvatar(m) {
		a.asked = m
		a.modal.Open()
	}
	if a.cancel.Clicked(gtx) {
		a.modal.Close()
	}
	if a.confirm.Clicked(gtx) && a.modal.Shown() {
		a.modal.Close()
		if v.canDeleteAvatar(a.asked) {
			v.startAvatar(a.asked, true)
		}
	}
}

// startAvatar deletes photo m, or makes it the main one, off the frame.
func (v *mediaViewer) startAvatar(m model.Message, remove bool) {
	editor, ok := v.avatarEditor()
	if !ok {
		return
	}
	a := &v.avatar
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	done := make(chan avatarOutcome, 1)
	a.done, a.stop = done, cancel
	invalidate := v.invalidate
	go func() {
		defer cancel()
		defer invalidate()
		defer crash.Recover("change a profile's photo", func(p *crash.Panic) { done <- avatarOutcome{deleted: remove, err: p} })
		var err error
		if remove {
			err = editor.DeleteProfilePhoto(ctx, m)
		} else {
			err = editor.MakeProfilePhotoMain(ctx, m)
		}
		done <- avatarOutcome{deleted: remove, err: err}
	}()
}

// avatarOutcome closes the viewer on what was done, as Telegram Desktop's does,
// and tells it; what failed is told in the viewer, which stays.
func (v *mediaViewer) avatarOutcome(o avatarOutcome, l localization.Catalog) {
	if o.err != nil {
		v.toast.Show(editFailureText(o.err, l))
		return
	}
	text := l.T("viewer.avatar_made_main")
	if o.deleted {
		text = l.T("viewer.avatar_deleted")
	}
	if v.told == nil {
		v.toast.Show(text)
		return
	}
	v.Close()
	v.told(text)
}

// layoutAvatar draws the dialog that asks whether to delete the photo.
func (v *mediaViewer) layoutAvatar(gtx layout.Context, l localization.Catalog) {
	a := &v.avatar
	if !a.modal.Shown() {
		return
	}
	a.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return a.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("viewer.avatar_delete_sure"), token.TypestyleBodyLarge, sc.Surface.OnColor, 0)
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Spacing: layout.SpaceStart}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &a.cancel, l.T("editor.cancel"))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &a.confirm, l.T("viewer.avatar_delete_confirm"))
						}),
					)
				}),
			)
		}, defaultCardPadding)
	})
}
