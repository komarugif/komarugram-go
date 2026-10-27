// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"time"

	"github.com/gotd/td/tg"
	"komarugram/internal/messenger/model"
)

// serviceAction keeps what a service message's action tells, for the UI to
// word as Telegram Desktop does (HistoryItem::setServiceMessageByAction).
// names are the known peers' names. An action it does not know yet is
// kept without a kind, and shows as a service message only.
func serviceAction(action tg.MessageActionClass, names map[int64]string) *model.ServiceAction {
	users := func(ids ...int64) []model.ServicePeer {
		var out []model.ServicePeer
		for _, id := range ids {
			if id != 0 {
				out = append(out, model.ServicePeer{ID: id, Name: names[id]})
			}
		}
		return out
	}
	a := &model.ServiceAction{}
	switch action := action.(type) {
	case *tg.MessageActionChatAddUser:
		a.Kind, a.Peers = model.ServiceAddUser, users(action.Users...)
	case *tg.MessageActionChatJoinedByLink:
		a.Kind = model.ServiceJoinedByLink
	case *tg.MessageActionChatJoinedByRequest:
		a.Kind = model.ServiceJoinedByRequest
	case *tg.MessageActionChatDeleteUser:
		a.Kind, a.Peers = model.ServiceDeleteUser, users(action.UserID)
	case *tg.MessageActionChatCreate:
		a.Kind, a.Title = model.ServiceCreateChat, action.Title
	case *tg.MessageActionChannelCreate:
		a.Kind, a.Title = model.ServiceCreateChannel, action.Title
	case *tg.MessageActionChatEditTitle:
		a.Kind, a.Title = model.ServiceEditTitle, action.Title
	case *tg.MessageActionChatEditPhoto:
		a.Kind = model.ServiceEditPhoto
	case *tg.MessageActionChatDeletePhoto:
		a.Kind = model.ServiceDeletePhoto
	case *tg.MessageActionPinMessage:
		a.Kind = model.ServicePin
	case *tg.MessageActionChatMigrateTo, *tg.MessageActionChannelMigrateFrom, *tg.MessageActionHistoryClear:
		a.Kind = model.ServiceHidden
	case *tg.MessageActionPhoneCall:
		a.Kind, a.Video, a.Count = model.ServicePhoneCall, action.Video, int64(action.Duration)
		switch action.Reason.(type) {
		case *tg.PhoneCallDiscardReasonMissed:
			a.Reason = "missed"
		case *tg.PhoneCallDiscardReasonBusy:
			a.Reason = "busy"
		}
	case *tg.MessageActionGroupCall:
		a.Kind, a.Count = model.ServiceGroupCall, int64(action.Duration)
	case *tg.MessageActionGroupCallScheduled:
		a.Kind, a.Date = model.ServiceGroupCallScheduled, time.Unix(int64(action.ScheduleDate), 0)
	case *tg.MessageActionInviteToGroupCall:
		a.Kind, a.Peers = model.ServiceInviteToGroupCall, users(action.Users...)
	case *tg.MessageActionScreenshotTaken:
		a.Kind = model.ServiceScreenshot
	case *tg.MessageActionCustomAction:
		a.Kind, a.Title = model.ServiceCustom, action.Message
	case *tg.MessageActionContactSignUp:
		a.Kind = model.ServiceContactSignUp
	case *tg.MessageActionSetMessagesTTL:
		a.Kind, a.Count = model.ServiceTTL, int64(action.Period)
		if action.Period != 0 {
			a.Peers = users(action.AutoSettingFrom)
		}
	case *tg.MessageActionSetChatTheme:
		a.Kind = model.ServiceChatTheme
		if theme, ok := action.Theme.(*tg.ChatTheme); ok {
			a.Title = theme.Emoticon
		} else if gift, ok := action.Theme.(*tg.ChatThemeUniqueGift); ok {
			if g, ok := gift.Gift.(*tg.StarGiftUnique); ok {
				a.Title = g.Title
			}
		}
	case *tg.MessageActionTopicCreate:
		a.Kind, a.Title = model.ServiceTopicCreate, action.Title
	case *tg.MessageActionTopicEdit:
		a.Kind, a.Title = model.ServiceTopicEdit, action.Title
		if closed, ok := action.GetClosed(); ok {
			a.Reason = "reopened"
			if closed {
				a.Reason = "closed"
			}
		}
	case *tg.MessageActionBoostApply:
		a.Kind, a.Count = model.ServiceBoost, int64(action.Boosts)
	case *tg.MessageActionSetChatWallPaper:
		a.Kind = model.ServiceWallpaper
		switch {
		case action.Same:
			a.Reason = "same"
		case action.ForBoth:
			a.Reason = "both"
		}
	case *tg.MessageActionGameScore:
		a.Kind, a.Count = model.ServiceGameScore, int64(action.Score)
	case *tg.MessageActionStarGift:
		a.Kind = model.ServiceStarGift
		if g, ok := action.Gift.(*tg.StarGift); ok {
			a.Count = g.Stars
		}
	case *tg.MessageActionStarGiftUnique:
		a.Kind = model.ServiceStarGift
	case *tg.MessageActionNoForwardsToggle:
		a.Kind = model.ServiceNoForwards
		switch {
		case action.NewValue == action.PrevValue:
			a.Reason = "still"
		case action.NewValue:
			a.Reason = "disabled"
		}
	case *tg.MessageActionSuggestProfilePhoto:
		a.Kind = model.ServiceSuggestPhoto
	case *tg.MessageActionWebViewDataSent:
		a.Kind, a.Title = model.ServiceWebViewData, action.Text
	case *tg.MessageActionChangeCommunity:
		a.Kind = model.ServiceCommunity
		if id, ok := action.GetCommunityID(); ok && id != 0 {
			a.Count = peerID(&tg.PeerChannel{ChannelID: id})
			a.Title = names[a.Count]
		} else {
			a.Reason = "removed"
		}
	case *tg.MessageActionPaidMessagesPrice:
		a.Kind, a.Count = model.ServicePaidMessages, action.Stars
		if action.BroadcastMessagesAllowed {
			a.Reason = "allowed"
		}
	case *tg.MessageActionChatJoinedViaCommunity:
		a.Kind = model.ServiceJoinedViaCommunity
		a.Count = peerID(&tg.PeerChannel{ChannelID: action.CommunityID})
		a.Title = names[a.Count]
	}
	return a
}

// serviceMessage is a service message as the chat list words it.
func (l *list) serviceMessage(m *tg.MessageService) *model.Message {
	names := map[int64]string{}
	for id, u := range l.users {
		names[id] = personName(u)
	}
	// The communities an action names.
	for id, c := range l.communities {
		names[peerID(&tg.PeerChannel{ChannelID: id})] = c.Title
	}
	msg, _ := convertMessage("", m, names)
	return &msg
}
