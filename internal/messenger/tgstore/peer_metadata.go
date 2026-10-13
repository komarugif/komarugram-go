// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"slices"
	"time"

	"github.com/gotd/td/tg"
	"komarugram/internal/messenger/model"
)

// peerMetadata survives dialog removal and partial Telegram responses. In
// particular, an absent participants_count is unknown, not a new count of zero.
type peerMetadata struct {
	Known        bool         `json:",omitempty"`
	MembersKnown bool         `json:",omitempty"`
	Members      int          `json:",omitempty"`
	Forum        bool         `json:",omitempty"`
	Badges       model.Badges `json:",omitempty"`
}

func (p *peerRecord) rememberChannelMetadata(ch *tg.Channel) {
	p.Name = ch.Title
	p.Metadata.Forum = ch.Forum
	if !ch.Min {
		p.Metadata.Known = true
		p.Metadata.Badges = channelBadges(ch, time.Now())
	} else {
		// A min channel may update these flags, but cannot revoke verification.
		p.Metadata.Badges.Scam = ch.Scam
		p.Metadata.Badges.Fake = ch.Fake
		p.Metadata.Badges.EmojiStatus = emojiStatus(ch.EmojiStatus, time.Now())
	}
	if count, ok := ch.GetParticipantsCount(); ok {
		p.Metadata.MembersKnown = true
		p.Metadata.Members = count
	}
}

// withMetadata updates peer facts while preserving the dialog's unread count,
// notification settings, pin rank and last-message preview.
func (p peerRecord) withMetadata(chat model.Chat) model.Chat {
	if p.Name != "" {
		chat.Title = p.Name
	}
	switch p.Kind {
	case "channel":
		chat.Kind = model.KindGroup
		if p.Rights.Broadcast {
			chat.Kind = model.KindChannel
		}
	case "chat":
		chat.Kind = model.KindGroup
	case "user":
		chat.Kind = model.KindUser
		if p.Rights.Bot {
			chat.Kind = model.KindBot
		}
		if p.Rights.Self {
			chat.Kind = model.KindSaved
			chat.Title = ""
		}
	}
	if p.Metadata.Known {
		chat.Badges = p.Metadata.Badges
		chat.Forum = p.Metadata.Forum
	}
	if p.Metadata.MembersKnown {
		chat.Members = p.Metadata.Members
	}
	if p.Rights.Self {
		chat.Badges = model.Badges{}
	}
	return chat
}

// refreshDialogMetadata applies full peer information without adding a dialog
// for a channel the account merely viewed through search.
func (s *Store) refreshDialogMetadata(chat int64) {
	s.history.mu.Lock()
	peer := s.history.peers[chat]
	s.history.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.chats {
		if c.ID != chat {
			continue
		}
		next := peer.withMetadata(c)
		if next != c {
			s.chats = slices.Clone(s.chats)
			s.chats[i] = next
		}
		return
	}
}
