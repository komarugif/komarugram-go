// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// TelegramLinkKind is what a link of Telegram's opens in the client.
type TelegramLinkKind uint8

const (
	LinkUsername TelegramLinkKind = iota + 1
	LinkPrivatePost
	LinkStickers
	LinkEmoji
	LinkInvite
	LinkUser
)

// TelegramLink is a t.me or tg:// link the client opens itself, as
// Telegram Desktop's local URL handlers.
type TelegramLink struct {
	Kind TelegramLinkKind
	// Name is the username, or the short name of a sticker or emoji set.
	Name    string
	Channel int64
	Post    MessageID
	// Hash is an invite's.
	Hash  string
	Start string
	User  int64
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{2,31}$`)

// reservedPaths are t.me paths that are not usernames.
var reservedPaths = map[string]bool{"share": true, "proxy": true, "socks": true, "login": true, "addtheme": true, "setlanguage": true, "bg": true, "invoice": true, "boost": true, "addlist": true, "confirmphone": true, "iv": true, "msg": true, "contact": true, "nft": true, "m": true, "addstickers": true, "addemoji": true, "joinchat": true, "c": true, "s": true}

// ParseTelegramLink reads raw as a link the client opens itself.
func ParseTelegramLink(raw string) (TelegramLink, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return TelegramLink{}, false
	}
	q := u.Query()
	if strings.EqualFold(u.Scheme, "tg") {
		switch strings.ToLower(u.Host) {
		case "resolve":
			return usernameLink(q.Get("domain"), q.Get("post"), q.Get("start"))
		case "privatepost":
			return privatePost(q.Get("channel"), q.Get("post"))
		case "addstickers":
			return setLink(LinkStickers, q.Get("set"))
		case "addemoji":
			return setLink(LinkEmoji, q.Get("set"))
		case "join":
			return inviteLink(q.Get("invite"))
		case "user":
			if id, err := strconv.ParseInt(q.Get("id"), 10, 64); err == nil && id > 0 {
				return TelegramLink{Kind: LinkUser, User: id}, true
			}
		}
		return TelegramLink{}, false
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return TelegramLink{}, false
	}
	host := strings.ToLower(u.Hostname())
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case host == "t.me" || host == "telegram.me" || host == "telegram.dog":
	case strings.HasSuffix(host, ".t.me"):
		// username.t.me
		parts = append([]string{strings.TrimSuffix(host, ".t.me")}, parts...)
		if parts[len(parts)-1] == "" {
			parts = parts[:len(parts)-1]
		}
	default:
		return TelegramLink{}, false
	}
	if len(parts) == 0 || parts[0] == "" {
		return TelegramLink{}, false
	}
	switch first := parts[0]; {
	case strings.HasPrefix(first, "+") && len(parts) == 1:
		return inviteLink(first[1:])
	case first == "joinchat" && len(parts) == 2:
		return inviteLink(parts[1])
	case first == "addstickers" && len(parts) == 2:
		return setLink(LinkStickers, parts[1])
	case first == "addemoji" && len(parts) == 2:
		return setLink(LinkEmoji, parts[1])
	case first == "c" && len(parts) >= 3:
		// c/channel/post, or c/channel/topic/post.
		return privatePost(parts[1], parts[len(parts)-1])
	case first == "s" && len(parts) >= 2:
		return usernameLink(parts[1], "", "")
	case reservedPaths[strings.ToLower(first)]:
		return TelegramLink{}, false
	case len(parts) == 1:
		return usernameLink(first, "", q.Get("start"))
	case len(parts) == 2:
		return usernameLink(first, parts[1], "")
	case len(parts) == 3:
		// username/topic/post
		return usernameLink(first, parts[2], "")
	}
	return TelegramLink{}, false
}

func usernameLink(name, post, start string) (TelegramLink, bool) {
	if !usernamePattern.MatchString(name) {
		return TelegramLink{}, false
	}
	l := TelegramLink{Kind: LinkUsername, Name: name, Start: start}
	if post != "" {
		id, err := strconv.Atoi(post)
		if err != nil || id <= 0 {
			return TelegramLink{}, false
		}
		l.Post = MessageID(id)
	}
	return l, true
}

func privatePost(channel, post string) (TelegramLink, bool) {
	c, err := strconv.ParseInt(channel, 10, 64)
	p, err2 := strconv.Atoi(post)
	if err != nil || err2 != nil || c <= 0 || p <= 0 {
		return TelegramLink{}, false
	}
	return TelegramLink{Kind: LinkPrivatePost, Channel: c, Post: MessageID(p)}, true
}

func setLink(kind TelegramLinkKind, name string) (TelegramLink, bool) {
	if name == "" || strings.ContainsAny(name, "/?#") {
		return TelegramLink{}, false
	}
	return TelegramLink{Kind: kind, Name: name}, true
}

func inviteLink(hash string) (TelegramLink, bool) {
	if hash == "" || strings.ContainsAny(hash, "/?#") {
		return TelegramLink{}, false
	}
	return TelegramLink{Kind: LinkInvite, Hash: hash}, true
}

// ErrLinkNotFound is a link to a chat or post that does not exist, or that
// the account cannot see.
var ErrLinkNotFound = errors.New("link target not found")

// ChatInvite is what an invite link leads to: Chat, when the account may
// see it already, or what it is.
type ChatInvite struct {
	Chat    *Chat
	Title   string
	Members int
	Channel bool
	// Request: joining sends a request to the admins; Paid: joining is a
	// paid subscription, which the client does not take.
	Request, Paid bool
}

// TelegramLinkSource opens Telegram's links in the client.
type TelegramLinkSource interface {
	ResolveUsername(ctx context.Context, name string) (Chat, error)
	ChannelByID(ctx context.Context, id int64) (Chat, error)
	CheckInvite(ctx context.Context, hash string) (ChatInvite, error)
	JoinInvite(ctx context.Context, hash string) (Chat, error)
	UserByID(ctx context.Context, id int64) (Chat, error)
}

// ErrChatInaccessible is a chat the account may not read, as a channel it
// is banned from, of which nothing is cached either.
var ErrChatInaccessible = errors.New("chat not accessible")

// IsChannelID tells whether id, a chat's, is a channel's or a supergroup's.
func IsChannelID(id int64) bool { return id <= -1000000000000 }

// SenderChatSource opens the chat of a group message's sender, whom the
// account may know only from the group, as a click on the sender's avatar
// does: the private chat with a user, or the channel that posted it.
type SenderChatSource interface {
	// SenderChat is the chat of m's sender; ErrLinkNotFound when there is
	// none to open, and ErrChatInaccessible, with the chat still telling
	// what it is, for a channel the account may not read.
	SenderChat(ctx context.Context, m Message) (Chat, error)
}
