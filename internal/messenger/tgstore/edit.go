// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// EditMessage implements model.MessageEditor with messages.editMessage,
// whose updates bring the message edited into the history and the cache.
func (s *Store) EditMessage(ctx context.Context, key model.MessageKey, text string, entities []model.Entity) error {
	c := s.history
	c.mu.Lock()
	chat, _ := c.threadChat(key.ChatID)
	api := c.api
	peer, ok := c.peers[chat]
	c.mu.Unlock()
	if api == nil {
		return errors.New("cannot edit while offline")
	}
	if !ok {
		return errors.New("unknown chat")
	}
	req := &tg.MessagesEditMessageRequest{Peer: peer.input(), ID: int(key.MessageID), NoWebpage: !s.SendPermissions(chat).Allows(model.SendLinkPreview)}
	local := s.localPremium.Load()
	if cache := s.Cache(); cache != nil {
		var prior localEmoji
		if ok, _ := cache.Get(ctx, localEmojiKey(chat, int(key.MessageID)), &prior); ok && len(prior.Entities) > 0 {
			local = true
		}
	}
	projection := outgoingEntry{LocalEmoji: local && !s.Premium().Active}
	if projection.LocalEmoji {
		projection.EmojiLocal = s.localEmojiKinds(ctx, entities)
	}
	req.SetMessage(text)
	req.SetEntities(inputEntities(projection.wireEntities(entities)))
	res, err := api.MessagesEditMessage(ctx, req)
	if err != nil && !(projection.LocalEmoji && tgerr.Is(err, "MESSAGE_NOT_MODIFIED")) {
		return editError(err)
	}
	if err == nil {
		if err := s.Handle(ctx, res); err != nil {
			return err
		}
	}
	if projection.LocalEmoji {
		if err := s.editedLocalEmoji(ctx, chat, int(key.MessageID), text, entities, projection.localEmojiEntities(entities)); err != nil {
			return err
		}
		s.changed()
	}
	return nil
}

// editError is what Telegram's error of an edit means to the client, as
// Telegram Desktop reads it (api_editing.h, kDefaultEditMessagesErrors).
func editError(err error) error {
	switch {
	case tgerr.Is(err, "MESSAGE_NOT_MODIFIED"):
		return fmt.Errorf("edit: %w", model.ErrMessageNotModified)
	case tgerr.Is(err, "MESSAGE_ID_INVALID", "CHAT_ADMIN_REQUIRED", "MESSAGE_EDIT_TIME_EXPIRED"):
		return fmt.Errorf("edit: %w: %v", model.ErrEditForbidden, err)
	case tgerr.Is(err, "MESSAGE_EMPTY"):
		return fmt.Errorf("edit: %w", model.ErrMessageEmpty)
	}
	return fmt.Errorf("edit: %w", err)
}

// EditTimeLimit implements model.MessageEditor: Telegram's edit_time_limit,
// read once from help.getConfig, and the default until then.
func (s *Store) EditTimeLimit() time.Duration {
	if v := s.editLimit.Load(); v > 0 {
		return time.Duration(v) * time.Second
	}
	if s.editLimitAsked.CompareAndSwap(false, true) {
		c := s.history
		c.mu.Lock()
		api, ctx, closing := c.api, c.ctx, c.closing
		if !closing && api != nil {
			c.wg.Go(func() {
				defer crash.Recover("edit time limit", nil)
				ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				cfg, err := api.HelpGetConfig(ctx)
				if err != nil || cfg.EditTimeLimit <= 0 {
					// Asked again the next time.
					s.editLimitAsked.Store(false)
					return
				}
				s.editLimit.Store(int64(cfg.EditTimeLimit))
			})
		} else {
			s.editLimitAsked.Store(false)
		}
		c.mu.Unlock()
	}
	return model.DefaultEditTimeLimit
}

// inputEntities are entities as Telegram takes them with a message: the
// formatting the message field applies and custom emoji. What Telegram
// finds in a text by itself, as plain links and mentions, it finds again.
func inputEntities(entities []model.Entity) []tg.MessageEntityClass {
	var out []tg.MessageEntityClass
	for _, e := range model.SortEntities(entities) {
		o, n := e.Offset, e.Length
		switch e.Kind {
		case model.EntityBold:
			out = append(out, &tg.MessageEntityBold{Offset: o, Length: n})
		case model.EntityItalic:
			out = append(out, &tg.MessageEntityItalic{Offset: o, Length: n})
		case model.EntityUnderline:
			out = append(out, &tg.MessageEntityUnderline{Offset: o, Length: n})
		case model.EntityStrike:
			out = append(out, &tg.MessageEntityStrike{Offset: o, Length: n})
		case model.EntityCode:
			out = append(out, &tg.MessageEntityCode{Offset: o, Length: n})
		case model.EntityPre:
			out = append(out, &tg.MessageEntityPre{Offset: o, Length: n, Language: e.Language})
		case model.EntitySpoiler:
			out = append(out, &tg.MessageEntitySpoiler{Offset: o, Length: n})
		case model.EntityQuote:
			out = append(out, &tg.MessageEntityBlockquote{Offset: o, Length: n, Collapsed: e.Collapsed})
		case model.EntityURL:
			if e.URL != "" {
				out = append(out, &tg.MessageEntityTextURL{Offset: o, Length: n, URL: e.URL})
			}
		case model.EntityEmoji:
			out = append(out, &tg.MessageEntityCustomEmoji{Offset: o, Length: n, DocumentID: e.DocumentID})
		}
	}
	return out
}
