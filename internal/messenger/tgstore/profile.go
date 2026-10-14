// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"komarugram/internal/messenger/model"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// profileColors are Telegram's colors of profiles, by their id, asked for
// once a session: help.getPeerProfileColors.
type profileColors struct {
	mu     sync.Mutex
	loaded bool
	colors map[int]profileColor
}

// profileColor is the background of a profile color, for a light theme and
// a dark one.
type profileColor struct {
	Light, Dark []uint32
}

const profileColorsKey = "profile-colors"

func profileKey(chat int64) string { return fmt.Sprintf("profile/%d", chat) }

// PeerProfile implements model.PeerProfileSource. What Telegram tells is
// kept, and what was kept is the answer offline or when Telegram fails.
func (s *Store) PeerProfile(ctx context.Context, chat int64) (model.PeerProfile, error) {
	profile, err := s.fetchProfile(ctx, chat)
	cache := s.historyCache()
	if err != nil {
		var cached model.PeerProfile
		if cache != nil {
			if ok, e := cache.Get(ctx, profileKey(chat), &cached); e == nil && ok {
				return cached, nil
			}
		}
		return profile, err
	}
	if cache != nil {
		if e := cache.Put(ctx, profileKey(chat), profile); e != nil {
			return profile, e
		}
	}
	return profile, nil
}

// CachedPeerProfile implements model.PeerProfileSource.
func (s *Store) CachedPeerProfile(chat int64) (model.PeerProfile, bool) {
	var profile model.PeerProfile
	cache := s.historyCache()
	if cache == nil {
		return profile, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ok, err := cache.Get(ctx, profileKey(chat), &profile)
	return profile, ok && err == nil
}

func (s *Store) historyCache() historyCache {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil {
		return nil
	}
	return c.cache
}

// historyCache is what the profile keeps of the cache.
type historyCache interface {
	Get(ctx context.Context, key string, v any) (bool, error)
	Put(ctx context.Context, key string, v any) error
}

func (s *Store) fetchProfile(ctx context.Context, chat int64) (model.PeerProfile, error) {
	var out model.PeerProfile
	ctx, api, peer, _, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return out, err
	}
	defer done()
	var (
		status     tg.EmojiStatusClass
		color      tg.PeerColorClass
		usernames  []tg.Username
		username   string
		channelID  int64
		channelMsg int
		chats      []tg.ChatClass
		users      []tg.UserClass
	)
	switch peer.Kind {
	case "user":
		full, e := api.UsersGetFullUser(ctx, &tg.InputUser{UserID: peer.ID, AccessHash: peer.Hash})
		if e != nil {
			return out, e
		}
		users, chats = full.Users, full.Chats
		for _, raw := range full.Users {
			if u, ok := raw.(*tg.User); ok && u.ID == peer.ID {
				status, color, usernames, username = u.EmojiStatus, u.ProfileColor, u.Usernames, u.Username
			}
		}
		channelID, channelMsg = full.FullUser.PersonalChannelID, full.FullUser.PersonalChannelMessage
	case "channel":
		full, e := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash})
		if e != nil {
			return out, e
		}
		users, chats = full.Users, full.Chats
		for _, raw := range full.Chats {
			if ch, ok := raw.(*tg.Channel); ok && ch.ID == peer.ID {
				status, color, usernames, username = ch.EmojiStatus, ch.ProfileColor, ch.Usernames, ch.Username
			}
		}
	default:
		// A basic group has none of a profile's own.
		return out, nil
	}
	l := s.peerList(users, chats)
	out.Usernames = profileUsernames(username, usernames)
	statusGift := int64(0)
	if c, ok := status.(*tg.EmojiStatusCollectible); ok && emojiStatus(c, time.Now()) != 0 {
		statusGift = c.CollectibleID
		out.Cover = model.ProfileCover{
			Gift:         true,
			CenterColor:  uint32(c.CenterColor) & 0xffffff,
			EdgeColor:    uint32(c.EdgeColor) & 0xffffff,
			PatternColor: uint32(c.PatternColor) & 0xffffff,
			TextColor:    uint32(c.TextColor) & 0xffffff,
			Pattern:      c.PatternDocumentID,
		}
	} else if c, ok := color.(*tg.PeerColor); ok {
		if id, ok := c.GetColor(); ok {
			set := s.profileColor(ctx, api, id)
			out.Cover.Colors, out.Cover.DarkColors = set.Light, set.Dark
		}
		out.Cover.Pattern, _ = c.GetBackgroundEmojiID()
	}
	// What follows is optional: a failure leaves it out of the profile.
	out.Gifts = s.coverGifts(ctx, api, peer, statusGift)
	if channelID != 0 {
		out.Channel = s.personalChannel(ctx, api, l, chats, channelID, channelMsg)
	}
	return out, nil
}

// profileUsernames are the active usernames of a user or channel: usernames
// when it has several, the editable ones not collectible; else main.
func profileUsernames(main string, usernames []tg.Username) []model.Username {
	var out []model.Username
	for _, u := range usernames {
		if u.Active {
			out = append(out, model.Username{Name: u.Username, Collectible: !u.Editable})
		}
	}
	if len(out) == 0 && main != "" {
		out = append(out, model.Username{Name: main})
	}
	return out
}

// coverGifts are the unique gifts pinned to the top of peer's profile,
// without the one its emoji status shows, as Telegram Desktop's cover:
// pinned gifts come first among saved ones.
func (s *Store) coverGifts(ctx context.Context, api *tg.Client, peer peerRecord, statusGift int64) []model.Message {
	res, err := api.PaymentsGetSavedStarGifts(ctx, &tg.PaymentsGetSavedStarGiftsRequest{Peer: peer.input(), Limit: model.MaxCoverGifts})
	if err != nil {
		return nil
	}
	s.rememberPeers(res.Users, res.Chats)
	var out []model.Message
	for i, saved := range res.Gifts {
		unique, ok := saved.Gift.(*tg.StarGiftUnique)
		if !saved.PinnedToTop || !ok || unique.ID == statusGift {
			continue
		}
		m, err := s.savedGiftMessage(ctx, peer, saved, "", i)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

// personalChannel is the channel id a user put in the profile, with its
// post msg, from l, which the full user brought.
func (s *Store) personalChannel(ctx context.Context, api *tg.Client, l *list, chats []tg.ChatClass, id int64, msg int) *model.PersonalChannel {
	var channel *tg.Channel
	for _, raw := range chats {
		if ch, ok := raw.(*tg.Channel); ok && ch.ID == id {
			channel = ch
		}
	}
	if channel == nil {
		return nil
	}
	chat := peerID(&tg.PeerChannel{ChannelID: id})
	input := &tg.InputChannel{ChannelID: id, AccessHash: channel.AccessHash}
	out := &model.PersonalChannel{Chat: s.chatFor(chat, l)}
	if _, known := channel.GetParticipantsCount(); !known {
		if full, err := api.ChannelsGetFullChannel(ctx, input); err == nil {
			s.rememberPeers(full.Users, full.Chats)
			if f, ok := full.FullChat.(*tg.ChannelFull); ok {
				if n, ok := f.GetParticipantsCount(); ok {
					out.Chat.Members = n
				}
			}
		}
	}
	if msg != 0 {
		res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: input, ID: []tg.InputMessageClass{&tg.InputMessageID{ID: msg}}})
		if err == nil {
			if box, ok := res.(interface{ GetMessages() []tg.MessageClass }); ok {
				for _, m := range box.GetMessages() {
					if _, empty := m.(*tg.MessageEmpty); empty {
						continue
					}
					out.Post, out.Date = preview(m)
					if _, plain := m.(*tg.Message); plain && out.Post.Text != "" {
						s.history.mu.Lock()
						account := s.history.account
						s.history.mu.Unlock()
						whole, _ := convertMessage(account, m, nil)
						out.Post.Text, out.Entities = whole.Text, whole.Entities
					}
				}
			}
		}
	}
	return out
}

// profileColor is the profile color id, loading Telegram's colors the
// first time; the kept ones offline.
func (s *Store) profileColor(ctx context.Context, api *tg.Client, id int) profileColor {
	st := &s.profiles
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.loaded {
		cache := s.historyCache()
		if st.colors == nil && cache != nil {
			_, _ = cache.Get(ctx, profileColorsKey, &st.colors)
		}
		res, err := api.HelpGetPeerProfileColors(ctx, 0)
		if colors, ok := res.(*tg.HelpPeerColors); ok && err == nil {
			st.loaded = true
			st.colors = map[int]profileColor{}
			for _, option := range colors.Colors {
				st.colors[option.ColorID] = profileColor{Light: profileBackground(option.Colors), Dark: profileBackground(option.DarkColors)}
			}
			if cache != nil {
				_ = cache.Put(ctx, profileColorsKey, st.colors)
			}
		}
	}
	c := st.colors[id]
	if len(c.Dark) == 0 {
		c.Dark = c.Light
	}
	return c
}

func profileBackground(set tg.HelpPeerColorSetClass) []uint32 {
	p, ok := set.(*tg.HelpPeerColorProfileSet)
	if !ok {
		return nil
	}
	out := make([]uint32, len(p.BgColors))
	for i, c := range p.BgColors {
		out[i] = uint32(c) & 0xffffff
	}
	return out
}

// CollectibleUsername implements model.PeerProfileSource.
func (s *Store) CollectibleUsername(ctx context.Context, username string) (model.CollectibleUsername, error) {
	out := model.CollectibleUsername{Username: username}
	c := s.history
	c.mu.Lock()
	api := c.api
	c.mu.Unlock()
	if api == nil {
		return out, errors.New("Telegram is offline")
	}
	info, err := api.FragmentGetCollectibleInfo(ctx, &tg.InputCollectibleUsername{Username: username})
	if tgerr.Is(err, "USERNAME_NOT_OCCUPIED", "USERNAME_INVALID", "COLLECTIBLE_NOT_FOUND") {
		return out, model.ErrLinkNotFound
	}
	if err != nil {
		return out, err
	}
	out.Bought = time.Unix(int64(info.PurchaseDate), 0)
	out.Currency, out.Amount = info.Currency, info.Amount
	out.CryptoCurrency, out.CryptoAmount = info.CryptoCurrency, info.CryptoAmount
	out.URL = info.URL
	return out, nil
}
