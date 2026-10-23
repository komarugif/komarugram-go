// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"time"

	"gio-mw/token"

	"gioui.org/layout"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// tgLinks opens Telegram's links in the client, as Telegram Desktop's local
// URL handlers: chats, posts, sets of stickers and emoji, and invites.
type tgLinks struct {
	results chan tgLinkResult
	busy    bool
	// invite is the one the dialog offers to join.
	invite       model.ChatInvite
	hash         string
	modal        modal
	join, cancel surface
}

type tgLinkResult struct {
	link   model.TelegramLink
	chat   model.Chat
	invite *model.ChatInvite
	err    error
}

// openTelegramLink opens link in the client; false leaves it to the browser.
func (p *chatPage) openTelegramLink(link model.TelegramLink) bool {
	return p.resolveTelegramLink(link, false)
}

// resolveTelegramLink asks Telegram what link leads to, or with join joins
// the invite it is.
func (p *chatPage) resolveTelegramLink(link model.TelegramLink, join bool) bool {
	switch link.Kind {
	case model.LinkStickers, model.LinkEmoji:
		p.stickers.open(p, model.StickerSetRef{Type: "short_name", ShortName: link.Name})
		return true
	}
	source, ok := p.source.(model.TelegramLinkSource)
	if !ok {
		return false
	}
	d := &p.tgLinks
	if d.busy {
		return true
	}
	if d.results == nil {
		d.results = make(chan tgLinkResult, 1)
	}
	d.busy = true
	results, invalidate := d.results, p.invalidate
	go func() {
		defer crash.Recover("telegram link", func(e *crash.Panic) { results <- tgLinkResult{link: link, err: e}; invalidate() })
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		r := tgLinkResult{link: link}
		switch link.Kind {
		case model.LinkUsername:
			r.chat, r.err = source.ResolveUsername(ctx, link.Name)
		case model.LinkPrivatePost:
			r.chat, r.err = source.ChannelByID(ctx, link.Channel)
		case model.LinkUser:
			r.chat, r.err = source.UserByID(ctx, link.User)
		case model.LinkInvite:
			if join {
				r.chat, r.err = source.JoinInvite(ctx, link.Hash)
			} else {
				var inv model.ChatInvite
				inv, r.err = source.CheckInvite(ctx, link.Hash)
				r.invite = &inv
			}
		}
		results <- r
		invalidate()
	}()
	return true
}

func (p *chatPage) tgLinkDialog(gtx layout.Context, l localization.Catalog) {
	d := &p.tgLinks
	select {
	case r := <-d.results:
		d.busy = false
		p.takeTelegramLink(r, l)
	default:
	}
	if d.cancel.Clicked(gtx) {
		d.modal.Close()
	}
	if d.join.Clicked(gtx) && !d.busy {
		d.modal.Close()
		p.resolveTelegramLink(model.TelegramLink{Kind: model.LinkInvite, Hash: d.hash}, true)
	}
	if !d.modal.Shown() {
		return
	}
	d.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(400))
		sc := scheme(gtx)
		action, kind := l.T("links.join_group"), model.KindGroup
		if d.invite.Channel {
			action, kind = l.T("links.join_channel"), model.KindChannel
		}
		members := chatStatus(model.Chat{Kind: kind, Members: d.invite.Members}, l)
		if d.invite.Request {
			action = l.T("links.request")
		}
		return card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, d.invite.Title, token.TypestyleTitleMedium, sc.Surface.OnColor, 2)
				}),
				vspace(6),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, members, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &d.cancel, l.T("history.cancel")) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &d.join, action) }),
					)
				}),
			)
		}, 20)
	})
}

func (p *chatPage) takeTelegramLink(r tgLinkResult, l localization.Catalog) {
	switch {
	case errors.Is(r.err, model.ErrLinkNotFound):
		switch r.link.Kind {
		case model.LinkUsername:
			p.toast.Show(l.Format("links.username_not_found", map[string]string{"user": "@" + r.link.Name}))
		case model.LinkPrivatePost:
			p.toast.Show(l.T("links.post_invalid"))
		default:
			p.toast.Show(l.T("links.invite_bad"))
		}
	case errors.Is(r.err, model.ErrJoinRequested), errors.Is(r.err, model.ErrJoinVerification),
		errors.Is(r.err, model.ErrJoinFull), errors.Is(r.err, model.ErrChatInaccessible):
		// The dialog the join was asked in still has the invite.
		p.toast.Show(membershipError(r.err, p.tgLinks.invite.Channel, l))
	case r.err != nil:
		p.toast.Show(mediaErrorText(r.err))
	case r.invite != nil && r.invite.Chat != nil:
		p.openChatAt(*r.invite.Chat, 0)
	case r.invite != nil && r.invite.Paid:
		p.link = "https://t.me/+" + r.link.Hash
		p.linkModal.Open()
	case r.invite != nil:
		p.tgLinks.invite, p.tgLinks.hash = *r.invite, r.link.Hash
		p.tgLinks.modal.Open()
	default:
		p.openChatAt(r.chat, r.link.Post)
	}
}

func (p *chatPage) openChatAt(chat model.Chat, post model.MessageID) {
	if p.openChat != nil {
		p.openChat(chat, post)
	}
}
