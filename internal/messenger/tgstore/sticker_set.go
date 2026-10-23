// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"komarugram/internal/messenger/model"

	"github.com/gotd/td/tg"
)

func inputStickerSet(ref model.StickerSetRef) (tg.InputStickerSetClass, error) {
	switch ref.Type {
	case "id":
		return &tg.InputStickerSetID{ID: ref.ID, AccessHash: ref.AccessHash}, nil
	case "short_name":
		return &tg.InputStickerSetShortName{ShortName: ref.ShortName}, nil
	case "dice":
		return &tg.InputStickerSetDice{Emoticon: ref.Emoticon}, nil
	case "premium_gifts":
		return &tg.InputStickerSetPremiumGifts{}, nil
	case "emoji_default_statuses":
		return &tg.InputStickerSetEmojiDefaultStatuses{}, nil
	case "emoji_default_topic_icons":
		return &tg.InputStickerSetEmojiDefaultTopicIcons{}, nil
	case "emoji_channel_default_statuses":
		return &tg.InputStickerSetEmojiChannelDefaultStatuses{}, nil
	default:
		return nil, errors.New("sticker set reference unavailable")
	}
}

// stickerSetKey is where a set of ref is cached, or "" for a set that is
// not, such as a dice.
func stickerSetKey(ref model.StickerSetRef) string {
	switch ref.Type {
	case "id":
		return fmt.Sprintf("stickerset/id/%d", ref.ID)
	case "short_name":
		return "stickerset/name/" + strings.ToLower(ref.ShortName)
	}
	return ""
}

// CachedStickerSet is the set of ref as StickerSet last returned it. The
// locations of its media were saved with it.
func (s *Store) CachedStickerSet(ctx context.Context, ref model.StickerSetRef) (model.StickerSet, bool) {
	cache, key := s.Cache(), stickerSetKey(ref)
	if cache == nil || key == "" {
		return model.StickerSet{}, false
	}
	var set model.StickerSet
	ok, err := cache.Get(ctx, key, &set)
	return set, ok && err == nil
}

// cacheStickerSet saves set as fetched by ref, and by its ID.
func (s *Store) cacheStickerSet(ctx context.Context, ref model.StickerSetRef, set model.StickerSet) {
	cache := s.Cache()
	if cache == nil {
		return
	}
	for _, key := range []string{stickerSetKey(ref), stickerSetKey(set.Ref)} {
		if key != "" {
			_ = cache.Put(ctx, key, set)
		}
	}
}

func (s *Store) StickerSet(ctx context.Context, ref model.StickerSetRef) (model.StickerSet, error) {
	input, err := inputStickerSet(ref)
	if err != nil {
		return model.StickerSet{}, err
	}
	s.history.mu.Lock()
	api := s.history.api
	s.history.mu.Unlock()
	if api == nil {
		return model.StickerSet{}, errors.New("sticker set unavailable offline")
	}
	raw, err := api.MessagesGetStickerSet(ctx, &tg.MessagesGetStickerSetRequest{Stickerset: input})
	if err != nil {
		return model.StickerSet{}, err
	}
	pack, ok := raw.(*tg.MessagesStickerSet)
	if !ok {
		return model.StickerSet{}, errors.New("sticker set unavailable")
	}
	_, installed := pack.Set.GetInstalledDate()
	author := model.StickerSetCreatorID(pack.Set.ID)
	if pack.Set.Creator {
		author = s.Me().ID
	}
	set := model.StickerSet{
		Ref:       model.StickerSetRef{Type: "id", ID: pack.Set.ID, AccessHash: pack.Set.AccessHash},
		Title:     pack.Set.Title,
		AuthorID:  author,
		Count:     pack.Set.Count,
		Items:     s.pickerItems(ctx, pack.Documents),
		Installed: installed && !pack.Set.Archived,
		Emoji:     pack.Set.Emojis,
	}
	s.cacheStickerSet(ctx, ref, set)
	return set, nil
}

func (s *Store) SetStickerSetInstalled(ctx context.Context, ref model.StickerSetRef, installed bool) error {
	input, err := inputStickerSet(ref)
	if err != nil {
		return err
	}
	s.history.mu.Lock()
	api := s.history.api
	s.history.mu.Unlock()
	if api == nil {
		return errors.New("sticker set unavailable offline")
	}
	if installed {
		_, err = api.MessagesInstallStickerSet(ctx, &tg.MessagesInstallStickerSetRequest{Stickerset: input})
	} else {
		var removed bool
		removed, err = api.MessagesUninstallStickerSet(ctx, input)
		if err == nil && !removed {
			err = errors.New("sticker set was not removed")
		}
	}
	if err == nil {
		s.picker.mu.Lock()
		delete(s.picker.at, model.PickerStickers)
		delete(s.picker.at, model.PickerEmoji)
		s.picker.mu.Unlock()
		if set, ok := s.CachedStickerSet(ctx, ref); ok {
			set.Installed = installed
			s.cacheStickerSet(ctx, ref, set)
		}
	}
	return err
}

// StickerSetAuthor finds the creator: known, by a message of theirs the
// cache has, or as the lookup bot tells (userFromLookupBot), which is
// asked the ID inline. It never sends a message, to the creator or the bot.
func (s *Store) StickerSetAuthor(ctx context.Context, id int64) (model.Chat, error) {
	if id <= 0 {
		return model.Chat{}, errors.New("sticker creator unavailable")
	}
	s.history.mu.Lock()
	peer := s.history.peers[id]
	api := s.history.api
	s.history.mu.Unlock()
	if peer.Kind == "user" && (peer.Hash != 0 || peer.Rights.Self) && !peer.Rights.Muted {
		return s.chatFor(id, nil), nil
	}
	if api == nil {
		return model.Chat{}, errors.New("sticker creator unavailable offline")
	}
	// Telegram gives a user it knows only by id for a message of theirs,
	// as it gives a group's member brought as a min user: one the cache
	// has of them in a group or a channel.
	if key, ok, err := s.senderMessage(ctx, id); err == nil && ok {
		if chat, err := s.SenderChat(ctx, model.Message{Key: key, SenderID: id}); err == nil {
			return chat, nil
		}
	}
	if chat, ok := s.userFromLookupBot(ctx, api, id); ok {
		return chat, nil
	}
	users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUser{UserID: id}})
	if err != nil {
		return model.Chat{}, err
	}
	for _, raw := range users {
		if user, ok := raw.(*tg.User); ok && user.ID == id && !user.Deleted && !user.Min && (user.AccessHash != 0 || user.Self) {
			s.rememberPeers([]tg.UserClass{user}, nil)
			return s.chatFor(id, nil), nil
		}
	}
	return model.Chat{}, errors.New("sticker creator unavailable")
}

// senderMessage is a message the cache has of sender in a group or a
// channel, none without a cache.
func (s *Store) senderMessage(ctx context.Context, sender int64) (model.MessageKey, bool, error) {
	if s.history.cache == nil {
		return model.MessageKey{}, false, nil
	}
	return s.history.cache.SenderMessage(ctx, sender)
}
