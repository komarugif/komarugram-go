package tgstore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gotd/td/tg"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
)

func bannedKinds(b tg.ChatBannedRights) model.SendKind {
	if b.ViewMessages || b.SendMessages {
		return model.SendAll
	}
	var k model.SendKind
	for _, v := range []struct {
		banned bool
		kind   model.SendKind
	}{
		{b.SendPlain, model.SendText}, {b.SendPhotos, model.SendPhoto}, {b.SendVideos, model.SendVideo},
		{b.SendAudios, model.SendMusic}, {b.SendDocs, model.SendFile}, {b.SendVoices, model.SendVoice},
		{b.SendRoundvideos, model.SendRoundVideo}, {b.SendStickers, model.SendSticker}, {b.SendGifs, model.SendGIF},
		{b.SendInline, model.SendInline}, {b.SendPolls, model.SendPoll}, {b.SendGames, model.SendGame}, {b.EmbedLinks, model.SendLinkPreview},
	} {
		if v.banned {
			k |= v.kind
		}
	}
	if b.SendMedia {
		k |= model.SendPhoto | model.SendVideo | model.SendMusic | model.SendFile | model.SendVoice | model.SendRoundVideo
	}
	return k
}
func groupRights(ch *tg.Chat) peerRights {
	_, admin := ch.GetAdminRights()
	r := peerRights{Known: true, Creator: ch.Creator, Left: ch.Left || ch.Deactivated, NoForwards: ch.Noforwards, Delete: ch.Creator || ch.AdminRights.DeleteMessages, Muted: ch.Left || ch.Deactivated, Admin: ch.Creator || admin}
	if !r.Admin {
		r.Default = bannedKinds(ch.DefaultBannedRights)
	}
	return r
}
func (s *Store) SendPermissions(chat int64) model.SendPermissions {
	thread := isThread(chat)
	chat = s.realChat(chat)
	s.history.mu.Lock()
	peer, known := s.history.peers[chat]
	s.history.mu.Unlock()
	r := peer.Rights
	p := model.SendPermissions{Unavailable: !known || !r.Known && peer.Kind != "user", Broadcast: r.Broadcast, Default: r.Default, Personal: r.Personal, Until: r.Until, DiscussionID: r.DiscussionID, DirectMessagesID: r.DirectMessagesID}
	if r.HasDiscussion && p.DiscussionID == 0 {
		p.DiscussionID = -1
	}
	if r.Until > 0 && r.Until != 2147483647 && r.Until <= time.Now().Unix() {
		p.Personal = 0
	}
	if r.Admin {
		p.Default, p.Personal = 0, 0
	}
	if r.Unrestricted {
		p.Default = 0
	}
	if r.Broadcast {
		p.Unavailable = p.Unavailable || !r.Post || r.Left
	} else if peer.Kind == "channel" {
		p.Unavailable = p.Unavailable || r.Left && (r.JoinToSend || !thread && !r.HasDiscussion) || r.Gigagroup && !r.Admin
	} else {
		p.Unavailable = p.Unavailable || r.Muted
	}
	if s.Freeze() != (model.Freeze{}) {
		p.Unavailable = true
	}
	return p
}

func (s *Store) checkSend(chat int64, msg model.OutgoingMessage) error {
	p := s.SendPermissions(chat)
	k := model.SendText
	switch {
	case msg.Voice != nil:
		k = model.SendVoice
	case msg.Item != nil:
		k = model.ItemSendKind(*msg.Item)
		if msg.Item.ResultID != "" {
			if err := p.Check(model.SendInline); err != nil {
				return err
			}
		}
	case msg.Files != nil:
		for _, path := range msg.Files.Paths {
			f, err := sendfiles.Inspect(path)
			if err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(path), err)
			}
			if err := p.Check(sendfiles.Permission(f, msg.Files.Documents)); err != nil {
				return err
			}
		}
		return nil // Text is a media caption, not a standalone text message.
	case msg.Path != "":
		f, err := sendfiles.Inspect(msg.Path)
		if err != nil {
			return err
		}
		k = sendfiles.Permission(f, !msg.AsMedia)
	}
	return p.Check(k)
}

func (s *Store) Discussion(ctx context.Context, chat int64) (model.Chat, error) {
	ctx, api, peer, _, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return model.Chat{}, err
	}
	defer done()
	if peer.Kind != "channel" {
		return model.Chat{}, errors.New("not a channel")
	}
	full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash})
	if err != nil {
		return model.Chat{}, err
	}
	s.rememberPeers(full.Users, full.Chats)
	c, ok := full.FullChat.(*tg.ChannelFull)
	if !ok || c.LinkedChatID == 0 {
		return model.Chat{}, errors.New("no discussion group")
	}
	id := peerID(&tg.PeerChannel{ChannelID: c.LinkedChatID})
	s.history.mu.Lock()
	group, ok := s.history.peers[id]
	s.history.mu.Unlock()
	if !ok {
		return model.Chat{}, errors.New("discussion group unavailable")
	}
	return group.withMetadata(model.Chat{ID: id}), nil
}

func (s *Store) RefreshSendPermissions(ctx context.Context, chat int64) error {
	chat = s.realChat(chat)
	ctx, api, peer, _, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return err
	}
	defer done()
	var full *tg.MessagesChatFull
	switch peer.Kind {
	case "channel":
		full, err = api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash})
	case "chat":
		full, err = api.MessagesGetFullChat(ctx, peer.ID)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	s.rememberPeers(full.Users, full.Chats)
	if c, ok := full.FullChat.(*tg.ChannelFull); ok {
		s.history.mu.Lock()
		p := s.history.peers[chat]
		if count, ok := c.GetParticipantsCount(); ok {
			p.Metadata.MembersKnown = true
			p.Metadata.Members = count
		}
		p.Rights.DiscussionID = 0
		if c.LinkedChatID != 0 {
			p.Rights.DiscussionID = peerID(&tg.PeerChannel{ChannelID: c.LinkedChatID})
		}
		p.Rights.HasDiscussion = c.LinkedChatID != 0
		p.Rights.Unrestricted = c.BoostsUnrestrict > 0 && c.BoostsApplied >= c.BoostsUnrestrict
		s.history.peers[chat] = p
		s.history.mu.Unlock()
	}
	s.refreshDialogMetadata(chat)
	s.changed()
	return nil
}
