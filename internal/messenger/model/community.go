// SPDX-License-Identifier: Unlicense OR MIT

package model

import "context"

// A community gathers chats of a kind Telegram links to it: groups,
// channels and bots (communities.togglePeerLink), each a chat of its own.
// It is not a folder: its chats are the community's for everyone in them,
// and Telegram lists the community among the dialogs. Shown as one chat
// (Chat.Collapsed, "Show as One Chat"), it takes the place of its chats in
// the list of all chats; shown apart, its chats stay where they are and
// the community has no row, as Telegram Desktop and Telegram for Android
// show them.

// CommunityPeer is a chat of a community, as the community lists it.
type CommunityPeer struct {
	// Chat is the chat, without a last message unless the account has it
	// in its list.
	Chat Chat
	// Joined is set for a chat the account is in, or a bot it has a chat
	// with.
	Joined bool
	// CanView is set for one whose history the community lets everyone in
	// it read without joining.
	CanView bool
	// Hidden is set for one only its members and the community's admins
	// see listed.
	Hidden bool
	// Public is set for a group or channel with a username, which anyone
	// can read.
	Public bool
}

// CommunityChatType is what the account can do with a chat of a
// community, as Telegram for Android tells (CommunityChatType), and the
// section of the community's page it is listed in.
type CommunityChatType int

const (
	// CommunityJoined: the account is in the chat, or has a chat with the
	// user or bot.
	CommunityJoined CommunityChatType = iota
	// CommunityViewable: it can read the chat without joining: a user or
	// bot, a public group or channel, or one the community lets everyone
	// read.
	CommunityViewable
	// CommunityRequestable: it can ask to join.
	CommunityRequestable
	// CommunityHidden: it can neither join nor read the chat until an admin
	// invites it.
	CommunityHidden
	communityChatTypes
)

// Type is what the account can do with p, as Telegram for Android decides
// it (CommunityUtils.getCommunityChatType): a user or bot always opens,
// hidden or not; a group or channel is hidden only when it cannot be read.
func (p CommunityPeer) Type() CommunityChatType {
	switch {
	case p.Joined:
		return CommunityJoined
	case p.Chat.Kind == KindUser || p.Chat.Kind == KindBot:
		return CommunityViewable
	case p.Public || p.CanView:
		return CommunityViewable
	case p.Hidden:
		return CommunityHidden
	}
	return CommunityRequestable
}

// Community is what a community's own page shows.
type Community struct {
	ID int64
	// Title is its name, once known: a community shown apart has no chat
	// of its own in the list.
	Title string
	// Collapsed is set while it is shown as one chat, with or without a
	// row in the list yet.
	Collapsed bool
	// About is its description.
	About string
	// Peers are its chats, in Telegram's order.
	Peers []CommunityPeer
	// Loading is set while it is read; Err is why it could not be.
	Loading bool
	Err     error
}

// Sections divides the chats of c by their Type, in the order Telegram for
// Android's page of a community lists them (CommunityUtils.fillLinkedPeers).
func (c Community) Sections() (sections [communityChatTypes][]CommunityPeer) {
	for _, p := range c.Peers {
		t := p.Type()
		sections[t] = append(sections[t], p)
	}
	return sections
}

// CommunitySource reads communities and how the list shows them.
type CommunitySource interface {
	// OpenCommunity starts reading community, or reads it again; the store
	// tells of it as of any other change.
	OpenCommunity(community int64)
	// Community returns what was read of community.
	Community(community int64) Community
	// LinkedCommunity is the community a chat is one of, 0 for none: of a
	// chat in the list or not, as one opened from a search.
	LinkedCommunity(chat int64) int64
	// SetCommunityCollapsed shows the chats of community as one chat in the
	// list, or apart (communities.toggleCommunityCollapsedInDialogs).
	SetCommunityCollapsed(ctx context.Context, community int64, collapsed bool) error
}

// CommunityMembers are the chats of chats that are community's, in their
// order.
func CommunityMembers(chats []Chat, community int64) []Chat {
	var out []Chat
	for _, c := range chats {
		if c.Community == community && c.Kind != KindCommunity {
			out = append(out, c)
		}
	}
	return out
}

// WithCommunityShown is chats with the community of row shown as one chat,
// its row in the list, or apart, without one, as Telegram lists a
// community's dialog only while it is shown as one chat. row is the
// community's chat, which a list without it gets, pinned as it says.
func WithCommunityShown(chats []Chat, row Chat, collapsed bool) []Chat {
	out := make([]Chat, 0, len(chats)+1)
	found := false
	for _, c := range chats {
		if c.ID == row.ID && c.Kind == KindCommunity {
			found = true
			if !collapsed {
				continue
			}
			c.Collapsed = true
		}
		out = append(out, c)
	}
	if collapsed && !found {
		row.Kind, row.Collapsed = KindCommunity, true
		out = append(out, row)
	}
	return out
}
