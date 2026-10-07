// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gio-mw/token"
	"gio-mw/widget/button"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// profileSaveTimeout bounds how long saving an edit waits for Telegram.
const profileSaveTimeout = 30 * time.Second

// profilePage shows the account's profile. With a store that can edit it,
// the pencil makes the name, username and bio editable in place, as the
// official clients do; the phone number, ID and data center are shown only.
type profilePage struct {
	infoHeight heightTransition
	scrollPage
	// editor saves edits; nil when the store cannot.
	editor model.ProfileEditor
	// premium tells the bio's limit; nil when the store does not know.
	premium model.PremiumSource
	// badges draw the marks around the name: see App.badges.
	badges badgesLayout
	// frozen, when the account is frozen, is shown instead of the form.
	frozen *frozenView
	// openAvatar shows the photos of the account's profile; avatar is the
	// click on its avatar.
	openAvatar func(chat int64)
	avatar     widget.Clickable

	edit, save, cancel               *button.Button
	editing, saving                  bool
	first, last, username, bio       *textField
	result                           chan error
	problem                          error
	status, phone, handle, accountID spoiler
}

func newProfilePage(editor model.ProfileEditor) *profilePage {
	return &profilePage{
		editor: editor,
		edit:   button.Text(), save: button.Filled(), cancel: button.Text(),
		first: newTextField(0, ""), last: newTextField(0, ""),
		username: newTextField(0, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"),
		bio:      newTextField(0, ""),
		result:   make(chan error, 1),
	}
}

// open shows the profile as it is: covered, and not being edited.
func (p *profilePage) open() {
	for _, s := range []*spoiler{&p.status, &p.phone, &p.handle, &p.accountID} {
		s.Hide()
	}
	if !p.saving {
		p.editing, p.problem = false, nil
	}
}

func (p *profilePage) Update(gtx layout.Context, me model.Profile, invalidate func()) {
	if p.avatar.Clicked(gtx) && p.openAvatar != nil {
		p.openAvatar(me.ID)
	}
	select {
	case err := <-p.result:
		p.saving, p.problem = false, err
		if err == nil {
			p.editing = false
		}
	default:
	}
	if p.editor == nil || p.saving {
		return
	}
	if !p.editing && p.edit.Clicked(gtx) {
		if p.frozen.Frozen() {
			p.frozen.Show()
			return
		}
		p.editing, p.problem = true, nil
		p.first.editor.SetText(me.FirstName)
		p.last.editor.SetText(me.LastName)
		p.username.editor.SetText(me.Username)
		p.bio.editor.SetText(me.Bio)
		p.first.Focus(gtx)
		return
	}
	if !p.editing {
		return
	}
	if p.cancel.Clicked(gtx) {
		p.editing, p.problem = false, nil
		return
	}
	// Enter goes from field to field and saves from the last one.
	fields := []*textField{p.first, p.last, p.username, p.bio}
	submit := p.save.Clicked(gtx)
	for i, f := range fields {
		if f.Submitted(gtx) {
			if i == len(fields)-1 {
				submit = true
			} else {
				fields[i+1].Focus(gtx)
			}
		}
	}
	if !submit {
		return
	}
	e := model.ProfileEdit{
		FirstName: strings.TrimSpace(p.first.Text()),
		LastName:  strings.TrimSpace(p.last.Text()),
		Username:  strings.TrimPrefix(strings.TrimSpace(p.username.Text()), "@"),
		Bio:       strings.TrimSpace(p.bio.Text()),
	}
	if err := e.ValidateWith(p.bioLimit()); err != nil {
		p.problem = err
		return
	}
	if e == (model.ProfileEdit{FirstName: me.FirstName, LastName: me.LastName, Username: me.Username, Bio: me.Bio}) {
		p.editing, p.problem = false, nil
		return
	}
	p.saving, p.problem = true, nil
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), profileSaveTimeout)
		defer cancel()
		p.result <- p.editor.EditProfile(ctx, e)
		invalidate()
	}()
}

// Layout draws the profile. With private set, phone numbers and the
// account's identifiers are covered until clicked.
func (p *profilePage) Layout(gtx layout.Context, me model.Profile, l localization.Catalog, drawAvatar avatarLayout, private, animate bool) layout.Dimensions {
	sc := scheme(gtx)
	return p.layout(gtx, func(gtx layout.Context) layout.Dimensions {
		header := layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return p.avatar.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return drawAvatar(gtx, me.ID, model.KindUser, me.Name(), pageAvatarSize)
				})
			}),
			vspace(12),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				var before, after layout.Widget
				if p.badges != nil {
					// The profile shows the check mark and the status both.
					before, after = p.badges(me.Badges, 24, true)
				}
				if before == nil && after == nil {
					return centeredLabel(gtx, me.Name(), token.TypestyleHeadlineSmall, sc.Surface.OnColor, 2)
				}
				return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return withBadges(gtx, before, func(gtx layout.Context) layout.Dimensions {
						return label(gtx, me.Name(), token.TypestyleHeadlineSmall, sc.Surface.OnColor, 2)
					}, after)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if me.Username == "" {
					return centeredLabel(gtx, l.T("page.telegram_account"), token.TypestyleBodyMedium, sc.Primary.Color, 1)
				}
				if !private {
					return centeredLabel(gtx, "@"+me.Username, token.TypestyleBodyMedium, sc.Primary.Color, 1)
				}
				return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return p.status.Layout(gtx, true, animate, func(gtx layout.Context) layout.Dimensions {
						return label(gtx, "@"+me.Username, token.TypestyleBodyMedium, sc.Primary.Color, 1)
					})
				})
			}),
			vspace(20),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if p.editing {
					return p.infoHeight.Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.layoutForm(gtx, l) }, defaultCardPadding)
				}
				return p.infoHeight.Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.layoutInfo(gtx, me, l, private, animate) }, defaultCardPadding)
			}),
		)
		gtx.Constraints.Min = header.Size
		if p.editor != nil && !p.editing {
			layout.NE.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.edit.LayoutIconOnly(gtx, l.T("page.edit"), iconEdit)
			})
		}
		return header
	})
}

// layoutInfo draws the profile's fields, each a value over its name.
func (p *profilePage) layoutInfo(gtx layout.Context, me model.Profile, l localization.Catalog, private, animate bool) layout.Dimensions {
	sc := scheme(gtx)
	field := func(name, value string, cover *spoiler) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 6, Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						text := func(gtx layout.Context) layout.Dimensions {
							return label(gtx, value, token.TypestyleBodyLarge, sc.Surface.OnColor, 0)
						}
						if cover == nil {
							return text(gtx)
						}
						return cover.Layout(gtx, private, animate, text)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, name, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
					}),
				)
			})
		})
	}
	var rows []layout.FlexChild
	if me.Phone != "" {
		rows = append(rows, field(l.T("page.phone"), me.Phone, &p.phone))
	}
	if me.Username != "" {
		rows = append(rows, field(l.T("page.username"), "@"+me.Username, &p.handle))
	} else {
		rows = append(rows, field(l.T("page.username"), l.T("page.not_set"), nil))
	}
	bio := me.Bio
	if bio == "" {
		bio = l.T("page.bio_empty")
	}
	rows = append(rows, field(l.T("page.bio"), bio, nil))
	if me.ID != 0 {
		rows = append(rows, field(l.T("page.id"), strconv.FormatInt(me.ID, 10), &p.accountID))
	}
	if me.DC != 0 {
		dc := fmt.Sprintf("DC %d", me.DC)
		if city := l.T(fmt.Sprintf("page.dc_%d", me.DC)); !strings.HasPrefix(city, "page.") {
			dc += " · " + city
		}
		rows = append(rows, field(l.T("page.dc"), dc, nil))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// layoutForm draws the editable fields in place of the profile's.
func (p *profilePage) layoutForm(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	problem := p.problemText(l)
	hint := func(text string, color token.MatColor) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, text, token.TypestyleBodySmall, color, 0)
			})
		})
	}
	bioCount := fmt.Sprintf("%d/%d", utf8.RuneCountInString(p.bio.Text()), p.bioLimit())
	bioColor := sc.SurfaceVariant.OnColor
	if utf8.RuneCountInString(p.bio.Text()) > p.bioLimit() {
		bioColor = sc.Error.Color
	}
	if p.saving {
		p.save.Disable()
		p.cancel.Disable()
	} else {
		p.save.Enable()
		p.cancel.Enable()
	}
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.first.Layout(gtx, l.T("page.first_name"), errors.Is(p.problem, model.ErrNameInvalid))
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.last.Layout(gtx, l.T("page.last_name"), false)
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			invalid := errors.Is(p.problem, model.ErrUsernameInvalid) || errors.Is(p.problem, model.ErrUsernameTaken)
			return p.username.Layout(gtx, l.T("page.username"), invalid)
		}),
		hint(l.T("page.username_hint"), sc.SurfaceVariant.OnColor),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.bio.Layout(gtx, l.T("page.bio"), errors.Is(p.problem, model.ErrBioTooLong))
		}),
		hint(bioCount, bioColor),
	}
	if problem != "" {
		rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, problem, token.TypestyleBodyMedium, sc.Error.Color, 0)
		}))
	}
	rows = append(rows, vspace(16), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return buttonRow(gtx, func(gtx layout.Context) layout.Dimensions {
			return p.cancel.Layout(gtx, l.T("page.cancel"))
		}, func(gtx layout.Context) layout.Dimensions {
			text := l.T("page.save")
			if p.saving {
				text = l.T("page.saving")
			}
			return p.save.Layout(gtx, text)
		})
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// bioLimit is how many characters the bio may have: more with Premium.
func (p *profilePage) bioLimit() int {
	if p.premium != nil {
		if limit := p.premium.Premium().Limit("about_length_limit"); limit > 0 {
			return limit
		}
	}
	return model.MaxBioLength
}

func (p *profilePage) problemText(l localization.Catalog) string {
	switch {
	case p.problem == nil:
		return ""
	case errors.Is(p.problem, model.ErrNameInvalid):
		return l.T("page.err_name")
	case errors.Is(p.problem, model.ErrUsernameInvalid):
		return l.T("page.err_username")
	case errors.Is(p.problem, model.ErrUsernameTaken):
		return l.T("page.err_taken")
	case errors.Is(p.problem, model.ErrBioTooLong):
		return fmt.Sprintf(l.T("page.err_bio"), p.bioLimit())
	case errors.Is(p.problem, model.ErrProfileOffline):
		return l.T("page.err_offline")
	}
	return fmt.Sprintf(l.T("page.err_failed"), p.problem)
}
