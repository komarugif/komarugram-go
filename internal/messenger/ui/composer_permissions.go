package ui

import (
	"context"
	"errors"
	"gio-mw/token"
	"gioui.org/layout"
	"gioui.org/op"
	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"time"
)

type discussionResult struct {
	from int64
	chat model.Chat
	err  error
}

func (c *messageComposer) permissions(chat int64) model.SendPermissions {
	if source, ok := c.source.(model.SendPermissionsSource); ok {
		return source.SendPermissions(chat)
	}
	if source, ok := c.source.(model.RightsSource); ok {
		return model.SendPermissions{Unavailable: !source.CanSend(chat)}
	}
	return model.SendPermissions{}
}
func restrictionText(p model.SendPermissions, k model.SendKind, l localization.Catalog) string {
	if p.Unavailable {
		return l.T("composer.unsupported")
	}
	key := "restriction." + k.Key()
	if until := p.Expiry(k); !until.IsZero() {
		return l.Format(key+".until", map[string]string{"date": until.Local().Format("02.01.2006"), "time": until.Local().Format("15:04")})
	}
	if p.Default&k != 0 {
		return l.T(key + ".all")
	}
	return l.T(key)
}
func composerErrorText(err error, l localization.Catalog) string {
	var ban *model.SendRestriction
	if errors.As(err, &ban) {
		return restrictionText(ban.Permissions, ban.Kind, l)
	}
	return mediaErrorText(err)
}
func (c *messageComposer) pickerAllowed(tab model.PickerTab) bool {
	return c.permissions(c.chat).Allows(model.PickerSendKind(tab))
}
func (c *messageComposer) checkMessage(chat int64, msg model.OutgoingMessage) error {
	p := c.permissions(chat)
	switch {
	case msg.Voice != nil:
		return p.Check(model.SendVoice)
	case msg.Item != nil:
		if msg.Item.ResultID != "" {
			if err := p.Check(model.SendInline); err != nil {
				return err
			}
		}
		return p.Check(model.ItemSendKind(*msg.Item))
	case msg.Path != "" || msg.Files != nil:
		if !p.Any(model.SendAttachments) {
			return p.Check(model.SendFile)
		}
		return nil
	default:
		return p.Check(model.SendText)
	}
}
func (c *messageComposer) enforcePermissions(gtx layout.Context) {
	p := c.permissions(c.chat)
	if !c.pickerAllowed(c.sendConfirm.tab) {
		c.sendConfirm.modal.Hide()
	}
	if !p.Allows(model.SendVoice) {
		c.cancelRecording()
	}
	if !p.Any(model.SendAttachments) {
		c.attachOpen = false
		c.form = 0
		c.files.close()
	}
	if !c.pickerAllowed(c.tab) {
		c.pickerOpen = false
		for _, tab := range []model.PickerTab{model.PickerEmoji, model.PickerStickers, model.PickerGIF} {
			if c.pickerAllowed(tab) {
				c.tab = tab
				c.page = model.PickerPage{}
				c.pageLoaded = false
				break
			}
		}
	}
	if until := time.Unix(p.Until, 0); p.Personal != 0 && p.Until != 2147483647 && until.After(gtx.Now) {
		gtx.Execute(op.InvalidateCmd{At: until})
	}
}
func (c *messageComposer) layoutReadOnly(gtx layout.Context, p *chatPage, l localization.Catalog) layout.Dimensions {
	rights := c.permissions(c.chat)
	membership := model.ChatMembership{}
	if source := p.membershipSource(); source != nil {
		membership = source.Membership(c.chat)
	}
	joinable := membership.Known && membership.Left && membership.Channel
	if joinable && c.join.Clicked(gtx) {
		p.startMembership(c.chat, membershipJoin)
	}
	if !rights.Broadcast {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if joinable {
				// A supergroup the account is not in is joined, or applied
				// to when its admins approve the members, as Telegram
				// Desktop's bottom bar does.
				if p.membership.busy || p.frozen.Frozen() {
					gtx = gtx.Disabled()
				}
				return textButton(gtx, &c.join, l.T(joinKey(membership)))
			}
			return label(gtx, restrictionText(rights, model.SendText, l), token.TypestyleBodySmall, scheme(gtx).SurfaceVariant.OnColor, 2)
		})
	}
	muter, _ := c.source.(model.NotificationSetter)
	muted := c.muted[c.chat]
	if muter != nil {
		muted = muter.ChatMuted(c.chat)
	}
	if !membership.Left && c.mute.Clicked(gtx) && !c.muting {
		if muter == nil {
			c.muted[c.chat] = !muted
			p.toast.Show(l.T("composer.notifications_stub"))
		} else {
			c.muting = true
			chat, mute := c.chat, !muted
			go func() {
				defer crash.Recover("mute chat", func(e *crash.Panic) {
					select {
					case c.mutes <- e:
						c.invalidate()
					case <-c.ctx.Done():
					}
				})
				ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
				defer cancel()
				select {
				case c.mutes <- muter.SetChatMuted(ctx, chat, mute):
					c.invalidate()
				case <-c.ctx.Done():
				}
			}()
		}
	}
	select {
	case err := <-c.mutes:
		c.muting = false
		if err != nil {
			p.toast.Show(mediaErrorText(err))
		}
	default:
	}
	if c.gift.Clicked(gtx) {
		p.toast.Show(l.T("composer.gift_stub"))
	}
	// The discussion and the direct messages are chats of their own,
	// found by asking Telegram; both go back to the channel.
	open := func(loading *bool, find func(context.Context, int64) (model.Chat, error)) {
		*loading = true
		chat := c.chat
		go func() {
			defer crash.Recover("discussion", func(e *crash.Panic) {
				select {
				case c.discussions <- discussionResult{from: chat, err: e}:
					c.invalidate()
				case <-c.ctx.Done():
				}
			})
			ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
			defer cancel()
			found, err := find(ctx, chat)
			select {
			case c.discussions <- discussionResult{chat, found, err}:
				c.invalidate()
			case <-c.ctx.Done():
			}
		}()
	}
	if c.discussion.Clicked(gtx) && !c.discussionLoading {
		if source, ok := c.source.(model.DiscussionSource); ok {
			open(&c.discussionLoading, source.Discussion)
		}
	}
	direct, _ := c.source.(model.DirectMessagesSource)
	if c.directMessages.Clicked(gtx) && !c.directLoading && direct != nil {
		open(&c.directLoading, direct.DirectMessages)
	}
	for {
		select {
		case result := <-c.discussions:
			c.discussionLoading, c.directLoading = false, false
			if result.from == c.chat {
				if result.err != nil {
					p.toast.Show(mediaErrorText(result.err))
				} else if p.openReturning != nil {
					p.openReturning(result.chat)
				} else if p.openChat != nil {
					p.openChat(result.chat, 0)
				}
			}
		default:
			goto drained
		}
	}
drained:
	col := scheme(gtx).Primary.Color
	// Notifications (or joining), the discussion, the direct messages and a
	// gift, in this order.
	var actions []layout.FlexChild
	action := func(w layout.Widget) {
		if len(actions) > 0 {
			actions = append(actions, layout.Rigid(layout.Spacer{Width: 8}.Layout))
		}
		actions = append(actions, layout.Rigid(w))
	}
	if membership.Known && membership.Left {
		action(func(gtx layout.Context) layout.Dimensions {
			if p.membership.busy || p.frozen.Frozen() {
				gtx = gtx.Disabled()
			}
			return textButton(gtx, &c.join, l.T(joinKey(membership)))
		})
	} else {
		action(func(gtx layout.Context) layout.Dimensions {
			icon, key := iconNotificationsOn, "composer.mute"
			if muted {
				icon, key = iconNotificationsOff, "composer.unmute"
			}
			if c.muting {
				gtx = gtx.Disabled()
			}
			return c.mute.Layout(gtx, icon, l.T(key), col)
		})
		if rights.DiscussionID != 0 {
			action(func(gtx layout.Context) layout.Dimensions {
				if c.discussionLoading {
					gtx = gtx.Disabled()
				}
				return c.discussion.Layout(gtx, iconDiscussion, l.T("composer.discussion"), col)
			})
		}
	}
	if rights.DirectMessagesID != 0 && direct != nil {
		action(func(gtx layout.Context) layout.Dimensions {
			if c.directLoading {
				gtx = gtx.Disabled()
			}
			return c.directMessages.Layout(gtx, iconDirectMessages, l.T("composer.direct_messages"), col)
		})
	}
	action(func(gtx layout.Context) layout.Dimensions {
		return c.gift.Layout(gtx, iconGift, l.T("composer.gift"), col)
	})
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, actions...)
	})
}
