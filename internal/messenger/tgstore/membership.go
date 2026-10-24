// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"slices"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"komarugram/internal/messenger/model"
)

func (s *Store) Membership(chat int64) model.ChatMembership {
	s.history.mu.Lock()
	p, ok := s.history.peers[chat]
	s.history.mu.Unlock()
	return model.ChatMembership{
		Known: ok && p.Rights.Known && (p.Kind == "channel" || p.Kind == "chat"), Left: p.Rights.Left, Creator: p.Rights.Creator, Broadcast: p.Rights.Broadcast,
		Channel: p.Kind == "channel", Request: p.Rights.JoinRequest && !p.Rights.Creator && !p.Rights.Broadcast,
	}
}

// joinError words Telegram's refusals of a join, as Telegram Desktop does.
func joinError(err error) error {
	switch {
	case tgerr.Is(err, "INVITE_REQUEST_SENT"):
		return model.ErrJoinRequested
	case tgerr.Is(err, "USERS_TOO_MUCH"):
		return model.ErrJoinFull
	case tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_PUBLIC_GROUP_NA", "USER_BANNED_IN_CHANNEL"):
		return model.ErrChatInaccessible
	}
	return err
}

func (s *Store) JoinChat(ctx context.Context, chat int64) error {
	ctx, api, peer, _, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return err
	}
	defer done()
	if peer.Kind != "channel" || !peer.Rights.Known {
		return errors.New("channel is unavailable")
	}
	if !peer.Rights.Left {
		return nil
	}
	result, err := api.ChannelsJoinChannel(ctx, &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash})
	if err != nil {
		return joinError(err)
	}
	joined, ok := result.(*tg.MessagesChatInviteJoinResultOk)
	if !ok {
		return model.ErrJoinVerification
	}
	// The RPC succeeded: a local cache failure must not invite another join.
	_ = s.Handle(ctx, joined.Updates)
	s.setMembership(ctx, chat, false)
	return nil
}

func (s *Store) PrepareLeave(ctx context.Context, chat int64) (model.LeavePlan, error) {
	// Refresh creator status before deciding whether the ownership warning is needed.
	if err := s.RefreshSendPermissions(ctx, chat); err != nil {
		return model.LeavePlan{}, err
	}
	ctx, api, peer, _, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return model.LeavePlan{}, err
	}
	defer done()
	if !peer.Rights.Known || peer.Rights.Left || (peer.Kind != "chat" && peer.Kind != "channel") {
		return model.LeavePlan{}, errors.New("not a member of this chat")
	}
	plan := model.LeavePlan{ChatID: chat, Creator: peer.Rights.Creator, BasicGroup: peer.Kind == "chat"}
	if !plan.Creator {
		return plan, nil
	}
	user, err := api.MessagesGetFutureChatCreatorAfterLeave(ctx, peer.input())
	if err != nil {
		// Telegram documents RPC errors as permission to use the ordinary leave flow.
		// Transport failures are not such a response and must not hide the warning.
		if _, rpc := tgerr.As(err); rpc {
			return plan, nil
		}
		return model.LeavePlan{}, err
	}
	if u, ok := user.(*tg.User); ok && !u.Deleted {
		plan.SuccessorID, plan.SuccessorName = u.ID, personName(u)
	} else {
		return model.LeavePlan{}, errors.New("Telegram did not identify the future owner")
	}
	return plan, nil
}

func (s *Store) LeaveChat(ctx context.Context, chat int64, confirmed model.LeavePlan) error {
	// A modal may have been open while another device changed ownership.
	current, err := s.PrepareLeave(ctx, chat)
	if err != nil {
		return err
	}
	if confirmed.ChatID != chat || current.Creator != confirmed.Creator || current.BasicGroup != confirmed.BasicGroup || current.SuccessorID != confirmed.SuccessorID {
		return model.ErrLeavePlanChanged
	}
	ctx, api, peer, _, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return err
	}
	defer done()
	var result tg.UpdatesClass
	switch peer.Kind {
	case "channel":
		result, err = api.ChannelsLeaveChannel(ctx, &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash})
	case "chat":
		result, err = api.MessagesDeleteChatUser(ctx, &tg.MessagesDeleteChatUserRequest{ChatID: peer.ID, UserID: &tg.InputUserSelf{}})
	default:
		return errors.New("not a group or channel")
	}
	if tgerr.Is(err, "USER_CREATOR") {
		return model.ErrOwnerCannotLeave
	}
	if err != nil {
		return err
	}
	_ = s.Handle(ctx, result)
	s.setMembership(ctx, chat, true)
	return nil
}

// setMembership updates the local list only after server acceptance. History stays cached.
func (s *Store) setMembership(ctx context.Context, chat int64, left bool) {
	s.history.mu.Lock()
	peer := s.history.peers[chat]
	peer.Rights.Left = left
	peer.Rights.Muted = left || peer.Rights.Broadcast && !peer.Rights.Post
	s.history.peers[chat] = peer
	added := peer.withMetadata(model.Chat{ID: chat})
	if h := s.history.histories[chat]; h != nil && !h.HasNewer {
		if items := lastAlbum(h.Messages); items != nil {
			added.Last, added.LastTime = albumPreview(items, added.Kind == model.KindGroup), items[len(items)-1].Date
		}
	}
	s.history.mu.Unlock()
	s.publish(func() {
		if left {
			s.chats = slices.DeleteFunc(slices.Clone(s.chats), func(c model.Chat) bool { return c.ID == chat })
			return
		}
		for i, c := range s.chats {
			if c.ID == chat {
				s.chats = slices.Clone(s.chats)
				s.chats[i] = peer.withMetadata(c)
				return
			}
		}
		s.chats = append(slices.Clone(s.chats), added)
		model.SortChats(s.chats)
	})
	_ = s.persistDialogs(ctx)
}
