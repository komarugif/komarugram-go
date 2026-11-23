// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strings"
	"time"
	"unicode/utf8"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/button"

	"gioui.org/layout"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// profileSaveTimeout bounds how long saving an edit waits for Telegram.
const profileSaveTimeout = 30 * time.Second

// profilePage shows the account's profile: the info of others' profiles,
// drawn as a page (chatInfo's LayoutPage), with what only the account's
// own has. With a store that can edit it, the pencil in its header puts a
// form of the name, username and bio in a card of the info, in place of
// the cards that show them, as the official clients edit them; the phone
// number, ID and data center are shown only. Its sections are the archive
// of its stories and its gifts.
type profilePage struct {
	// info draws the profile; nil without a store of conversations, where
	// the page is empty.
	info *chatInfo
	// me is the account's profile of this frame.
	me model.Profile
	// private covers the phone number, the username and the ID until
	// clicked, in visual privacy mode; animate uncovers them with the wave.
	private, animate bool
	// editor saves edits; nil when the store cannot.
	editor model.ProfileEditor
	// premium tells the bio's limit; nil when the store does not know.
	premium model.PremiumSource
	// frozen, when the account is frozen, is shown instead of the form.
	frozen *frozenView

	// edit is the pencil in the info's header; form, the card of the
	// fields, whose height follows them.
	edit                       surface
	form                       heightTransition
	save, cancel               *button.Button
	editing, saving            bool
	first, last, username, bio *textField
	result                     chan error
	problem                    error
	// The covers of the values of visual privacy mode, in the info's rows.
	phoneCover, handle, link, accountID spoiler
}

func newProfilePage(editor model.ProfileEditor) *profilePage {
	return &profilePage{
		editor: editor,
		save:   button.Filled(), cancel: button.Text(),
		first: newTextField(0, ""), last: newTextField(0, ""),
		username: newTextField(0, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"),
		bio:      newTextField(0, ""),
		result:   make(chan error, 1),
	}
}

// attach makes info, the info of others' profiles, draw this one.
func (p *profilePage) attach(info *chatInfo) {
	p.info, info.own = info, p
}

// open shows the profile of me as it is: covered, and not being edited,
// asking Telegram for what the info shows.
func (p *profilePage) open(me model.Profile) {
	for _, s := range []*spoiler{&p.phoneCover, &p.handle, &p.link, &p.accountID} {
		s.Hide()
	}
	if !p.saving {
		p.editing, p.problem = false, nil
	}
	p.me = me
	if p.info != nil && me.ID != 0 {
		p.info.OpenOwn(me)
	}
}

// close drops what the page shows, once another is shown in its place.
func (p *profilePage) close() {
	if p.info != nil && p.info.visible {
		p.info.Close()
	}
}

// canEdit tells whether the profile has a pencil: a store that edits it,
// and no form open.
func (p *profilePage) canEdit() bool { return p.editor != nil && !p.editing }

func (p *profilePage) Update(gtx layout.Context, me model.Profile, invalidate func()) {
	p.me = me
	if p.info != nil && me.ID != 0 && (!p.info.visible || p.info.chat.ID != me.ID) {
		// The profile came after the page was opened, or the page was
		// shown at once, as the window opened.
		p.info.OpenOwn(me)
	}
	select {
	case err := <-p.result:
		p.saving, p.problem = false, err
		if err == nil {
			p.editing = false
			if p.info != nil {
				// The cover and the usernames, as Telegram has them now.
				p.info.loadProfile()
			}
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
		defer crash.Recover("edit profile", func(e *crash.Panic) { p.result <- e; invalidate() })
		ctx, cancel := context.WithTimeout(context.Background(), profileSaveTimeout)
		defer cancel()
		p.result <- p.editor.EditProfile(ctx, e)
		invalidate()
	}()
}

// Layout draws the profile. With private set, phone numbers and the
// account's identifiers are covered until clicked.
func (p *profilePage) Layout(gtx layout.Context, me model.Profile, l localization.Catalog, private, animate bool) layout.Dimensions {
	p.me, p.private, p.animate = me, private, animate
	if p.info == nil {
		return layoutEmptyPage(gtx, l)
	}
	// The name and the marks beside it are the account's own, which follow
	// an edit at once.
	p.info.chat.Title, p.info.chat.Badges = me.Name(), me.Badges
	return p.info.LayoutPage(gtx, l, animate)
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

// ownChat is the account's profile as the chat whose info shows it.
func ownChat(me model.Profile) model.Chat {
	return model.Chat{ID: me.ID, Kind: model.KindUser, Title: me.Name(), Badges: me.Badges}
}

// OpenOwn shows the info of the account's own profile, me, on its page:
// without the counts of its messages, its chat with itself being Saved
// Messages, and with its own sections, the archive of its stories and its
// gifts.
func (p *chatInfo) OpenOwn(me model.Profile) {
	p.Open(ownChat(me))
	p.modal.Hide()
	p.resetMessages()
	p.kinds = nil
	if p.collection != nil {
		p.kinds = []model.SharedKind{model.SharedStoryArchive, model.SharedGifts}
	}
}

// editing tells whether the account's own form is open in the info.
func (p *chatInfo) editing() bool { return p.own != nil && p.own.editing }

// ownSpoiler is the cover the account's own page has for a value, which
// of picks; nil in the info of another, which covers nothing.
func (p *chatInfo) ownSpoiler(of func(*profilePage) *spoiler) *spoiler {
	if p.own == nil {
		return nil
	}
	return of(p.own)
}

// coveredRow is a row of the info, icon, title over subtitle, which a click
// copies, covered or not. On the account's own page in visual privacy mode
// cover hides its title, or with coverSubtitle its subtitle, until clicked.
func (p *chatInfo) coveredRow(gtx layout.Context, item *settingsItem, icon wdk.IconWidget, title, subtitle string, cover *spoiler, coverSubtitle bool) layout.Dimensions {
	if cover == nil || !p.own.private {
		return item.Layout(gtx, icon, title, subtitle)
	}
	sc := scheme(gtx)
	text := func(s string, style token.Typestyle, color token.MatColor, covered bool) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			w := func(gtx layout.Context) layout.Dimensions { return label(gtx, s, style, color, 1) }
			if !covered {
				return w(gtx)
			}
			return cover.Layout(gtx, true, p.own.animate, w)
		}
	}
	iconPx := gtx.Dp(24)
	return item.layoutWidgets(gtx, func(gtx layout.Context) layout.Dimensions {
		return exact(gtx, image.Pt(iconPx, iconPx), func(gtx layout.Context) layout.Dimensions { return icon(gtx, sc.Primary.Color) })
	}, iconPx, text(title, token.TypestyleBodyLarge, sc.Surface.OnColor, !coverSubtitle), text(subtitle, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, coverSubtitle))
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
