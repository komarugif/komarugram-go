// SPDX-License-Identifier: Unlicense OR MIT

package model

import "time"

// ServiceKind names what a service message tells. The values are stored in
// the cache, so they never change.
type ServiceKind string

const (
	// ServiceAddUser: Peers were added; the sender alone means it joined.
	ServiceAddUser ServiceKind = "add_user"
	// ServiceJoinedByLink and ServiceJoinedByRequest: the sender joined.
	ServiceJoinedByLink    ServiceKind = "joined_by_link"
	ServiceJoinedByRequest ServiceKind = "joined_by_request"
	// ServiceDeleteUser: Peers[0] was removed; the sender itself, it left.
	ServiceDeleteUser ServiceKind = "delete_user"
	// ServiceCreateChat and ServiceCreateChannel: the chat Title was
	// created, a group or a channel.
	ServiceCreateChat    ServiceKind = "create_chat"
	ServiceCreateChannel ServiceKind = "create_channel"
	ServiceEditTitle     ServiceKind = "edit_title"
	ServiceEditPhoto     ServiceKind = "edit_photo"
	ServiceDeletePhoto   ServiceKind = "delete_photo"
	// ServicePin: the message ReplyToMessageID was pinned.
	ServicePin ServiceKind = "pin"
	// ServiceHidden is an action Telegram Desktop shows nothing for, as a
	// group's migration to a supergroup.
	ServiceHidden ServiceKind = "hidden"
	// ServicePhoneCall is a call: Video, Reason and Count, its seconds.
	// Unlike the other actions it is drawn as a message.
	ServicePhoneCall ServiceKind = "phone_call"
	// ServiceGroupCall: a video chat started, or ended after Count
	// seconds; ServiceGroupCallScheduled, it is scheduled for Date.
	ServiceGroupCall          ServiceKind = "group_call"
	ServiceGroupCallScheduled ServiceKind = "group_call_scheduled"
	// ServiceInviteToGroupCall: Peers were invited to the video chat.
	ServiceInviteToGroupCall ServiceKind = "invite_to_group_call"
	ServiceScreenshot        ServiceKind = "screenshot"
	// ServiceStoryMention is a story the account was mentioned in, or one
	// it mentioned the chat's user in: see Message.StoryOf.
	ServiceStoryMention ServiceKind = "story_mention"
	// ServiceCustom shows Title as it is.
	ServiceCustom        ServiceKind = "custom"
	ServiceContactSignUp ServiceKind = "contact_sign_up"
	// ServiceTTL: messages auto-delete after Count seconds, 0 for never;
	// Peers[0], when set, set it for all its chats.
	ServiceTTL ServiceKind = "ttl"
	// ServiceChatTheme: the chat's theme is the emoji Title, or none.
	ServiceChatTheme ServiceKind = "chat_theme"
	// ServiceTopicCreate and ServiceTopicEdit: a forum topic named Title
	// was created, or renamed to Title, or closed or reopened (Reason is
	// "closed" or "reopened").
	ServiceTopicCreate ServiceKind = "topic_create"
	ServiceTopicEdit   ServiceKind = "topic_edit"
	// ServiceBoost: the sender boosted the group Count times.
	ServiceBoost ServiceKind = "boost"
	// ServiceWallpaper: a wallpaper was set; Reason is "same" or "both"
	// when it was.
	ServiceWallpaper ServiceKind = "wallpaper"
	// ServiceGameScore: the sender scored Count in the game Title.
	ServiceGameScore ServiceKind = "game_score"
	// ServiceStarGift: a gift worth Count Telegram Stars.
	ServiceStarGift ServiceKind = "star_gift"
	// ServiceNoForwards: sharing was disabled, Reason "disabled", or
	// enabled.
	ServiceNoForwards ServiceKind = "no_forwards"
	// ServiceSuggestPhoto: the sender suggested a profile photo.
	ServiceSuggestPhoto ServiceKind = "suggest_photo"
	// ServiceWebViewData: data from the button Title went to the bot.
	ServiceWebViewData ServiceKind = "web_view_data"
	// ServiceCommunity: the chat was added to the community Title, of chat
	// id Count, empty when not known; Reason "removed", taken out of one.
	ServiceCommunity ServiceKind = "community"
	// ServiceJoinedViaCommunity: the sender joined the group through the
	// community Title, of chat id Count.
	ServiceJoinedViaCommunity ServiceKind = "joined_via_community"
	// ServicePaidMessages: messages to the group cost Count Stars each, 0
	// for free; in a channel, its direct messages do, Reason "allowed" when
	// they are allowed for free.
	ServicePaidMessages ServiceKind = "paid_messages"
)

// ServicePeer is a user or chat an action names.
type ServicePeer struct {
	ID   int64
	Name string `json:",omitempty"`
}

// ServiceAction is what a service message tells, for the UI to word in its
// language. Which fields mean what depends on Kind.
type ServiceAction struct {
	Kind   ServiceKind
	Peers  []ServicePeer `json:",omitempty"`
	Title  string        `json:",omitempty"`
	Count  int64         `json:",omitempty"`
	Reason string        `json:",omitempty"`
	Video  bool          `json:",omitempty"`
	Date   time.Time     `json:",omitzero"`
}

// ServicePill reports whether m is drawn as a line across the history
// rather than as a message: every service message but a call.
func (m Message) ServicePill() bool {
	return m.Kind == MessageService && (m.Service == nil || m.Service.Kind != ServicePhoneCall)
}
