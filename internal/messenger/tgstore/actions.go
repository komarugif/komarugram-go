// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// channelRights are the account's rights in a channel or supergroup.
func channelRights(ch *tg.Channel) peerRights {
	r := peerRights{Known: !ch.Min, Creator: ch.Creator, NoForwards: ch.Noforwards, Broadcast: ch.Broadcast, Delete: ch.Creator, Post: ch.Creator, Left: ch.Left, JoinToSend: ch.JoinToSend, JoinRequest: ch.JoinRequest, Gigagroup: ch.Gigagroup, HasDiscussion: ch.HasLink}
	_, admin := ch.GetAdminRights()
	r.Admin = ch.Creator || admin
	r.Delete = r.Delete || ch.AdminRights.DeleteMessages
	r.Post = r.Post || ch.AdminRights.PostMessages
	r.Default, r.Personal = bannedKinds(ch.DefaultBannedRights), bannedKinds(ch.BannedRights)
	r.Until = int64(ch.BannedRights.UntilDate)
	r.Muted = ch.Left || ch.Broadcast && !r.Post
	r.Banned = !r.Admin && (r.Default|r.Personal)&model.SendText != 0
	if ch.Broadcast && ch.BroadcastMessagesAllowed && ch.LinkedMonoforumID != 0 {
		r.DirectMessagesID = peerID(&tg.PeerChannel{ChannelID: ch.LinkedMonoforumID})
	}
	if ch.Monoforum {
		// A channel's direct messages are written to without joining
		// them, as Telegram Desktop's CanSendAnyOf allows.
		r.Monoforum, r.Left, r.Muted = true, false, false
	}
	return r
}

// MessageRights implements model.RightsSource, with Telegram Desktop's rules
// (HistoryItem::allowsForward, canDelete and canDeleteForEveryone).
func (s *Store) MessageRights(chat int64, msgs []model.Message) model.MessageRights {
	chat = s.realChat(chat)
	s.history.mu.Lock()
	peer, known := s.history.peers[chat]
	s.history.mu.Unlock()
	if len(msgs) == 0 {
		return model.MessageRights{}
	}
	r := model.MessageRights{Forward: !peer.Rights.NoForwards, Save: !peer.Rights.NoForwards, Delete: known, Revoke: known}
	channel := peer.Kind == "channel"
	r.Everyone = channel
	for _, m := range msgs {
		if m.Deleted {
			// Kept after Telegram deleted it: it is only this computer's,
			// which deletes it alone.
			r.Forward = false
			continue
		}
		service := m.Kind == model.MessageService
		if service || m.NoForwards {
			r.Forward = false
		}
		if m.NoForwards {
			r.Save = false
		}
		switch {
		case channel:
			// The first message of a channel is its creation.
			ok := m.Key.MessageID != 1 && (peer.Rights.Delete || (m.Outgoing && !service && (!m.Post || peer.Rights.Post)))
			r.Delete = r.Delete && ok
			r.Revoke = r.Delete
		case peer.Kind == "user":
			// Nothing sent to oneself or to a bot can be revoked.
			r.Revoke = r.Revoke && !peer.Rights.Self && !peer.Rights.Bot
		case peer.Kind == "chat":
			r.Revoke = r.Revoke && (m.Outgoing || peer.Rights.Delete)
		}
	}
	return r
}

// CanSend implements model.RightsSource.
func (s *Store) CanSend(chat int64) bool {
	return s.SendPermissions(chat).Any(model.SendAll &^ model.SendLinkPreview)
}

// userUsername is a user's username, or its first active one.
func userUsername(u *tg.User) string {
	if u.Username != "" {
		return u.Username
	}
	for _, name := range u.Usernames {
		if name.Active {
			return name.Username
		}
	}
	return ""
}

// Username implements model.UsernameSource.
func (s *Store) Username(chat int64) string {
	s.history.mu.Lock()
	defer s.history.mu.Unlock()
	return s.history.peers[chat].Username
}

// channelUsername is the username of a public channel or supergroup: its
// own, or the first active of its collectible ones.
func channelUsername(ch *tg.Channel) string {
	if ch.Username != "" {
		return ch.Username
	}
	for _, u := range ch.Usernames {
		if u.Active {
			return u.Username
		}
	}
	return ""
}

// MessageLink implements model.MessageLinker as Telegram Desktop's
// CopyPostLink does: t.me/username/id for a public channel or supergroup,
// t.me/c/channel/id, for members only, for a private one.
func (s *Store) MessageLink(chat int64, id model.MessageID) (string, bool, bool) {
	chat = s.realChat(chat)
	s.history.mu.Lock()
	peer, known := s.history.peers[chat]
	s.history.mu.Unlock()
	if !known || peer.Kind != "channel" || id <= 0 {
		return "", false, false
	}
	if peer.Username != "" {
		return fmt.Sprintf("https://t.me/%s/%d", peer.Username, id), true, true
	}
	return fmt.Sprintf("https://t.me/c/%d/%d", peer.ID, id), false, true
}

// ForwardMessages implements model.MessageForwarder.
func (s *Store) ForwardMessages(ctx context.Context, from int64, ids []model.MessageID, to int64) error {
	return s.queueForward(ctx, from, ids, to, false)
}

var (
	_ model.RightsSource     = (*Store)(nil)
	_ model.MessageForwarder = (*Store)(nil)
)
