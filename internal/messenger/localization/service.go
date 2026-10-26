// SPDX-License-Identifier: Unlicense OR MIT

package localization

import (
	"strings"
	"time"
	"unicode/utf16"

	"komarugram/internal/messenger/model"
)

// ServiceContext is what wording a service message needs besides it.
type ServiceContext struct {
	// Chat names the chat: who did what a message without a sender's
	// name tells, as the other side of a private chat.
	Chat string
	// Channel is set in a channel, where actions are the channel's own.
	Channel bool
	// Pinned is the message a pin action pinned, when it is loaded;
	// PinnedGone, when it is known to be deleted.
	Pinned     *model.Message
	PinnedGone bool
	// Now is the time a scheduled video chat's date is worded against.
	Now time.Time
}

// pinnedTextLimit is how many characters of a pinned message's text a pin
// action quotes, as in Telegram Desktop.
const pinnedTextLimit = 16

// Service words a service message, as Telegram Desktop does
// (HistoryItem::setServiceMessageByAction). An action it does not know is
// a service message only; a hidden one, as a group's migration, is empty.
func (c Catalog) Service(m model.Message, ctx ServiceContext) string {
	s := m.Service
	if s == nil || s.Kind == "" {
		return c.T("history.service")
	}
	from := m.SenderName
	if from == "" {
		if m.Outgoing {
			from = c.T("history.you")
		} else {
			from = ctx.Chat
		}
	}
	peer := func(i int) string {
		if i < len(s.Peers) {
			if s.Peers[i].Name != "" {
				return s.Peers[i].Name
			}
		}
		return c.T("service.somebody")
	}
	list := func(prefix string) string {
		var out string
		for i := range s.Peers {
			switch {
			case i == 0:
				out = peer(i)
			case i == len(s.Peers)-1:
				out = c.Format(prefix+"_and_last", map[string]string{"accumulated": out, "user": peer(i)})
			default:
				out = c.Format(prefix+"_and_one", map[string]string{"accumulated": out, "user": peer(i)})
			}
		}
		return out
	}
	args := map[string]string{"from": from}
	f := func(key string) string { return c.Format("service."+key, args) }
	channel := ctx.Channel || m.Post
	switch s.Kind {
	case model.ServiceHidden:
		return ""
	case model.ServiceAddUser:
		switch {
		case len(s.Peers) == 1 && s.Peers[0].ID == m.SenderID:
			return f("user_joined")
		case len(s.Peers) <= 1:
			args["user"] = peer(0)
			return f("add_user")
		}
		args["users"] = list("service.add_users")
		return f("add_users_many")
	case model.ServiceJoinedByLink:
		return f("user_joined_by_link")
	case model.ServiceJoinedByRequest:
		return f("user_joined_by_request")
	case model.ServiceDeleteUser:
		if len(s.Peers) == 0 || s.Peers[0].ID == m.SenderID {
			return f("user_left")
		}
		args["user"] = peer(0)
		return f("kick_user")
	case model.ServiceCreateChat:
		args["title"] = s.Title
		return f("created_chat")
	case model.ServiceCreateChannel:
		if channel {
			return f("created_channel")
		}
		args["title"] = s.Title
		return f("created_chat")
	case model.ServiceEditTitle:
		args["title"] = s.Title
		if channel {
			return f("changed_title_channel")
		}
		return f("changed_title")
	case model.ServiceEditPhoto:
		if channel {
			return f("changed_photo_channel")
		}
		return f("changed_photo")
	case model.ServiceDeletePhoto:
		if channel {
			return f("removed_photo_channel")
		}
		return f("removed_photo")
	case model.ServicePin:
		return c.pinned(args, ctx)
	case model.ServicePhoneCall:
		return c.call(m)
	case model.ServiceGroupCall:
		if s.Count == 0 {
			if channel {
				return f("group_call_started_channel")
			}
			return f("group_call_started_group")
		}
		args["duration"] = c.callDuration(s.Count)
		if channel {
			return f("group_call_finished")
		}
		return f("group_call_finished_group")
	case model.ServiceGroupCallScheduled:
		args["date"] = c.scheduled(s.Date, ctx.Now)
		if channel {
			return f("group_call_scheduled_channel")
		}
		return f("group_call_scheduled_group")
	case model.ServiceInviteToGroupCall:
		args["chat"] = c.T("service.invite_user_chat")
		if len(s.Peers) <= 1 {
			args["user"] = peer(0)
			return f("invite_user")
		}
		args["users"] = list("service.invite_users")
		return f("invite_users_many")
	case model.ServiceScreenshot:
		if m.Outgoing {
			return f("you_took_screenshot")
		}
		return f("took_screenshot")
	case model.ServiceStoryMention:
		// Who posted it mentioned the other: the chat's user, for the
		// account's own.
		key := "story_mention"
		args["user"] = from
		if m.Outgoing {
			key, args["user"] = "story_mention_me", ctx.Chat
		}
		if m.StoryOf != nil && m.StoryOf.Expired {
			key += "_unavailable"
		}
		return f(key)
	case model.ServiceCustom:
		return s.Title
	case model.ServiceContactSignUp:
		return f("user_registered")
	case model.ServiceTTL:
		args["duration"] = c.ttl(s.Count)
		switch {
		case s.Count != 0 && len(s.Peers) > 0 && m.Outgoing && s.Peers[0].ID == m.SenderID:
			return f("ttl_global_me")
		case s.Count != 0 && len(s.Peers) > 0:
			args["from"] = peer(0)
			return f("ttl_global")
		case channel && s.Count == 0:
			return f("ttl_removed_channel")
		case channel:
			return f("ttl_changed_channel")
		case m.Outgoing && s.Count == 0:
			return f("ttl_removed_you")
		case m.Outgoing:
			return f("ttl_changed_you")
		case s.Count == 0:
			return f("ttl_removed")
		}
		return f("ttl_changed")
	case model.ServiceChatTheme:
		args["emoji"] = s.Title
		switch {
		case s.Title == "" && m.Outgoing:
			return f("you_theme_disabled")
		case s.Title == "":
			return f("theme_disabled")
		case m.Outgoing:
			return f("you_theme_changed")
		}
		return f("theme_changed")
	case model.ServiceTopicCreate:
		args["topic"] = s.Title
		return f("topic_created")
	case model.ServiceTopicEdit:
		args["link"] = c.T("service.topic_placeholder")
		args["title"] = s.Title
		switch {
		case s.Reason == "closed":
			return f("topic_closed_inside_by")
		case s.Reason == "reopened":
			return f("topic_reopened_inside_by")
		case s.Title != "":
			return f("topic_renamed")
		}
		return c.T("history.service")
	case model.ServiceBoost:
		if m.Outgoing {
			return f("boost_apply_me")
		}
		return c.Count("service.boost_apply", int(s.Count), args)
	case model.ServiceWallpaper:
		args["user"] = from
		switch {
		case m.Outgoing && s.Reason == "both":
			args["user"] = ctx.Chat
			return f("set_wallpaper_both_me")
		case m.Outgoing && s.Reason == "same":
			return f("set_same_wallpaper_me")
		case m.Outgoing:
			return f("set_wallpaper_me")
		case s.Reason == "same":
			return f("set_same_wallpaper")
		}
		return f("set_wallpaper")
	case model.ServiceGameScore:
		if m.Outgoing {
			return c.Count("service.game_you_scored_no_game", int(s.Count), args)
		}
		return c.Count("service.game_score_no_game", int(s.Count), args)
	case model.ServiceStarGift:
		args["user"] = from
		if s.Count == 0 {
			if m.Outgoing {
				return f("gift_unique_sent")
			}
			return f("gift_unique_received")
		}
		args["cost"] = c.Count("service.gift_for_stars", int(s.Count), nil)
		if m.Outgoing {
			return f("gift_sent")
		}
		return f("gift_received")
	case model.ServiceNoForwards:
		switch {
		case s.Reason == "still":
			return f("no_forwards_still_disabled")
		case m.Outgoing && s.Reason == "disabled":
			return f("no_forwards_you_disabled")
		case m.Outgoing:
			return f("no_forwards_you_enabled")
		case s.Reason == "disabled":
			return f("no_forwards_disabled")
		}
		return f("no_forwards_enabled")
	case model.ServiceSuggestPhoto:
		if m.Outgoing {
			args["user"] = ctx.Chat
			return f("suggested_photo_me")
		}
		args["user"] = from
		return f("suggested_photo")
	case model.ServiceWebViewData:
		args["text"] = s.Title
		return f("webview_data_done")
	case model.ServiceCommunity:
		// As Telegram Desktop words it: what the chat did itself, or one of
		// its admins.
		self := m.SenderID == 0 || m.SenderID == m.Key.ChatID || channel
		args["community"] = s.Title
		key := "community_added"
		switch {
		case s.Reason == "removed":
			key = "community_removed"
		case s.Title == "":
			key = "community_added_unknown"
		}
		switch {
		case self && channel:
			key += "_channel"
		case self:
			key += "_chat"
		}
		return f(key)
	case model.ServicePaidMessages:
		// As Telegram Desktop words it: of a channel, its direct messages.
		switch {
		case channel && s.Count > 0:
			return c.Count("service.direct_messages_paid", int(s.Count), args)
		case channel && s.Reason == "allowed":
			return f("direct_messages_enabled")
		case channel:
			return f("direct_messages_disabled")
		case s.Count > 0:
			return c.Count("service.message_price_paid", int(s.Count), args)
		}
		return f("message_price_free")
	case model.ServiceJoinedViaCommunity:
		if s.Title == "" {
			return f("joined_via_community_unknown")
		}
		args["community"] = s.Title
		return f("joined_via_community")
	}
	return c.T("history.service")
}

// PinnedSummary words a pin whose message is not at hand, as in the chat
// list: who pinned a message, in chat when the message does not say.
func (c Catalog) PinnedSummary(m model.Message, chat string) string {
	from := m.SenderName
	switch {
	case from == "" && m.Outgoing:
		from = c.T("history.you")
	case from == "":
		from = chat
	}
	return c.Format("service.pinned_summary", map[string]string{"from": from})
}

// pinned words a pin: a few words of the text pinned, or what its media
// is, as Telegram Desktop's preparePinnedText does.
func (c Catalog) pinned(args map[string]string, ctx ServiceContext) string {
	p := ctx.Pinned
	switch {
	case p == nil && ctx.PinnedGone:
		args["media"] = c.T("service.deleted_message")
		return c.Format("service.pinned_media", args)
	case p == nil:
		args["media"] = c.T("service.loading")
		return c.Format("service.pinned_media", args)
	}
	if media := c.pinnedMedia(*p); media != "" {
		args["media"] = media
		return c.Format("service.pinned_media", args)
	}
	args["text"] = PinnedQuote(p.Text)
	return c.Format("service.pinned_message", args)
}

// PinnedQuote is the start of a pinned message's text that a pin action
// quotes: its first characters, cut with an ellipsis when it is longer.
func PinnedQuote(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	units := 0
	for i, r := range text {
		if units >= pinnedTextLimit {
			// Five more characters are kept rather than cut.
			if len(utf16.Encode([]rune(text[i:]))) > 5 {
				return text[:i] + "…"
			}
			break
		}
		units += len(utf16.Encode([]rune{r}))
	}
	return text
}

// pinnedMedia names what a pinned message's media is, or nothing for a
// message of text.
func (c Catalog) pinnedMedia(m model.Message) string {
	switch m.Kind {
	case model.MessagePhoto:
		return c.T("service.pinned_media_photo")
	case model.MessageVideo:
		return c.T("service.pinned_media_video")
	case model.MessageGIF:
		return c.T("service.pinned_media_gif")
	case model.MessageVoice:
		return c.T("service.pinned_media_voice")
	case model.MessageRound:
		return c.T("service.pinned_media_round")
	case model.MessageMusic:
		return c.T("service.pinned_media_audio")
	case model.MessageFile:
		return c.T("service.pinned_media_file")
	case model.MessageSticker:
		return c.T("service.pinned_media_sticker")
	case model.MessagePoll:
		if m.Poll != nil {
			return "«" + m.Poll.Question + "»"
		}
	}
	return ""
}

// call words a call as its message says it: outgoing, missed, declined,
// and how long it was.
func (c Catalog) call(m model.Message) string {
	s := m.Service
	video := ""
	if s.Video {
		video = "video_"
	}
	var key string
	switch {
	case m.Outgoing && s.Reason == "missed":
		key = "cancelled"
	case m.Outgoing:
		key = "outgoing"
	case s.Reason == "missed":
		key = "missed"
	case s.Reason == "busy":
		key = "declined"
	default:
		key = "incoming"
	}
	text := c.T("call." + video + key)
	if s.Count > 0 {
		text += " (" + c.callDuration(s.Count) + ")"
	}
	return text
}

// callDuration words how long a call went, in its largest unit.
func (c Catalog) callDuration(seconds int64) string {
	switch {
	case seconds/86400 > 1:
		return c.Count("duration.days", int(seconds/86400), nil)
	case seconds/3600 > 1:
		return c.Count("duration.hours", int(seconds/3600), nil)
	case seconds/60 > 1:
		return c.Count("duration.minutes", int(seconds/60), nil)
	}
	return c.Count("duration.seconds", int(seconds), nil)
}

// ttl words an auto-delete period as Telegram Desktop's FormatTTL does.
func (c Catalog) ttl(seconds int64) string {
	const day = 86400
	switch {
	case seconds == 5:
		return c.Count("duration.seconds", 5, nil)
	case seconds < day:
		return c.Count("duration.hours", int(seconds/3600), nil)
	case seconds < 7*day:
		return c.Count("duration.days", int(seconds/day), nil)
	case seconds < 31*day:
		days := int(seconds / day)
		text := c.Count("duration.weeks", days/7, nil)
		if days%7 != 0 {
			text += " " + c.Count("duration.days", days%7, nil)
		}
		return text
	case seconds <= 31*day*11:
		return c.Count("duration.months", int(seconds/(31*day)), nil)
	}
	return c.Count("duration.years", int((seconds+365*day/2)/(365*day)), nil)
}

// scheduled words when a video chat is scheduled: today, tomorrow or a
// date, and the time.
func (c Catalog) scheduled(at, now time.Time) string {
	at = at.Local()
	if now.IsZero() {
		now = time.Now()
	}
	now = now.Local()
	args := map[string]string{"time": at.Format("15:04")}
	y, m, d := at.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	ny, nm, nd := now.Date()
	today := time.Date(ny, nm, nd, 0, 0, 0, 0, time.Local)
	switch {
	case day.Equal(today):
		return c.Format("service.call_starts_today", args)
	case day.Equal(today.AddDate(0, 0, 1)):
		return c.Format("service.call_starts_tomorrow", args)
	}
	args["date"] = at.Format("02.01.2006")
	return c.Format("service.call_starts_date", args)
}
