// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// Old catalogs did not keep the Premium bit. Read the document's set before
// deciding whether the sticker may leave the device.
func (s *Store) resolveOutgoingSticker(ctx context.Context, e *outgoingEntry) error {
	media := e.Request.Item.Media.Media
	if media.StickerSet == nil {
		return errors.New("sticker metadata unavailable; reopen the picker")
	}
	s.history.mu.Lock()
	api := s.history.api
	s.history.mu.Unlock()
	if api == nil {
		return errNotConnected
	}
	input, err := inputStickerSet(*media.StickerSet)
	if err != nil {
		return err
	}
	set, err := api.MessagesGetStickerSet(ctx, &tg.MessagesGetStickerSetRequest{Stickerset: input})
	if err != nil {
		return err
	}
	pack, ok := set.(*tg.MessagesStickerSet)
	if !ok {
		return errors.New("sticker set unavailable")
	}
	for _, item := range s.pickerItems(ctx, pack.Documents) {
		if item.ID == e.Request.Item.ID {
			e.Request.Item = &item
			e.LocalSticker = item.Media.Media.Premium
			e.ResolveSticker = false
			e.Messages[0].Media = item.Media.Media
			func() {
				s.history.mu.Lock()
				defer s.history.mu.Unlock()
				e.pending[0].msg.Media = item.Media.Media
				e.pending[0].msg.ContentRevision++
				s.history.touchPending(e.Chat)
			}()
			return s.Cache().PutOutgoing(ctx, e.Request.RandomID, e, nil)
		}
	}
	return errors.New("sticker is no longer in its set")
}

func (s *Store) SetLocalPremium(on bool) { s.localPremium.Store(on) }

func (s *Store) LocalPremiumEnabled() bool { return s.localPremium.Load() }

func (s *Store) localEmojiKinds(ctx context.Context, entities []model.Entity) map[int64]bool {
	kinds := map[int64]bool{}
	cache := s.Cache()
	for _, entity := range entities {
		if entity.Kind != model.EntityEmoji {
			continue
		}
		if _, seen := kinds[entity.DocumentID]; seen {
			continue
		}
		var doc model.Message
		known := false
		if cache != nil {
			known, _ = cache.Get(ctx, emojiKey(entity.DocumentID), &doc)
		}
		kinds[entity.DocumentID] = !known || doc.Media == nil || !doc.Media.PremiumKnown || doc.Media.Premium
	}
	return kinds
}

// A local appearance can be edited without changing its ordinary server text.
func (s *Store) editedLocalEmoji(ctx context.Context, chat int64, id int, text string, entities, local []model.Entity) error {
	cache := s.Cache()
	if err := cache.Put(ctx, localEmojiKey(chat, id), localEmoji{Text: text, Entities: local}); err != nil {
		return err
	}
	if m, ok, err := cache.Message(ctx, chat, id); err != nil {
		return err
	} else if ok {
		m.Text, m.Entities, m.LocalPremium = text, entities, len(local) > 0
		m.ContentRevision = model.Revision(m)
		if err := cache.SaveMessages(ctx, []model.Message{m}); err != nil {
			return err
		}
	}
	s.history.mu.Lock()
	defer s.history.mu.Unlock()
	for _, h := range s.history.histories {
		for i, m := range h.Messages {
			if m.Key.ChatID == chat && int(m.Key.MessageID) == id {
				m.Text, m.Entities, m.LocalPremium = text, entities, len(local) > 0
				m.ContentRevision = model.Revision(m)
				h.Messages[i] = m
				h.Revision++
			}
		}
	}
	return nil
}

func (s *Store) restoreOutgoingEmoji(ctx context.Context, e *outgoingEntry) error {
	if !e.LocalEmoji {
		return nil
	}
	for i, m := range e.Messages {
		if id := e.Done[outgoingRandomID(e.Request.RandomID, i)]; id > 0 {
			if err := s.keepLocalEmoji(ctx, e.Peer.key(), id, m.Text, e.localEmojiEntities(m.Entities)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) removeLocalSticker(key model.MessageKey) bool {
	s.localMessagesMu.Lock()
	defer s.localMessagesMu.Unlock()
	s.history.mu.Lock()
	var messages []model.Message
	found := false
	for _, m := range s.history.localStickers {
		if m.Key == key {
			found = true
		} else {
			messages = append(messages, m)
		}
	}
	s.history.mu.Unlock()
	if !found {
		return false
	}
	if err := s.Cache().Put(context.Background(), "local-premium/stickers", messages); err != nil {
		return false
	}
	s.history.mu.Lock()
	s.history.localStickers = messages
	s.history.touchPending(key.ChatID)
	s.history.mu.Unlock()
	s.changed()
	return true
}

func (e *outgoingEntry) wireEntities(entities []model.Entity) []model.Entity {
	if !e.LocalEmoji {
		return entities
	}
	out := make([]model.Entity, 0, len(entities))
	for _, entity := range entities {
		if entity.Kind != model.EntityEmoji || e.EmojiLocal != nil && !e.EmojiLocal[entity.DocumentID] {
			out = append(out, entity)
		}
	}
	return out
}

func (e *outgoingEntry) localEmojiEntities(entities []model.Entity) []model.Entity {
	var local []model.Entity
	for _, entity := range entities {
		if entity.Kind == model.EntityEmoji && e.LocalEmoji && (e.EmojiLocal == nil || e.EmojiLocal[entity.DocumentID]) {
			local = append(local, entity)
		}
	}
	return local
}

func mergeLocalEmoji(entities, local []model.Entity) []model.Entity {
	var out []model.Entity
	for _, entity := range entities {
		replaced := false
		if entity.Kind == model.EntityEmoji {
			for _, emoji := range local {
				if entity.Offset < emoji.Offset+emoji.Length && entity.Offset+entity.Length > emoji.Offset {
					replaced = true
					break
				}
			}
		}
		if !replaced {
			out = append(out, entity)
		}
	}
	return append(out, local...)
}

type localEmoji struct {
	Text     string
	Entities []model.Entity
}

func localEmojiKey(chat int64, id int) string {
	return fmt.Sprintf("local-premium/emoji/%d/%d", chat, id)
}

func (s *Store) keepLocalEmoji(ctx context.Context, chat int64, id int, text string, entities []model.Entity) error {
	var emoji []model.Entity
	for _, e := range entities {
		if e.Kind == model.EntityEmoji {
			emoji = append(emoji, e)
		}
	}
	if len(emoji) == 0 {
		return nil
	}
	cache := s.Cache()
	if err := cache.Put(ctx, localEmojiKey(chat, id), localEmoji{Text: text, Entities: emoji}); err != nil {
		return err
	}
	for _, e := range emoji {
		var m model.Message
		id := fmt.Sprintf("document/%d", e.DocumentID)
		if ok, _ := cache.Get(ctx, emojiKey(e.DocumentID), &m); ok && m.Media != nil {
			id = m.Media.ID
		}
		if err := cache.PinLocalMedia(ctx, id); err != nil {
			return err
		}
		if b, _ := cache.Media(ctx, id); len(b) > 0 {
			if err := cache.SaveLocalMedia(ctx, id, b); err != nil {
				return err
			}
		}
	}
	// An update can arrive before its acknowledgement. Refresh loaded copies
	// now, as well as overlaying future pages read from Telegram.
	s.history.mu.Lock()
	for _, h := range s.history.histories {
		for i, m := range h.Messages {
			if m.Key.ChatID == chat && int(m.Key.MessageID) == id && m.Text == text {
				m.Entities = mergeLocalEmoji(m.Entities, emoji)
				m.LocalPremium = true
				m.ContentRevision = model.Revision(m)
				h.Messages[i] = m
				h.Revision++
			}
		}
	}
	s.history.mu.Unlock()
	return nil
}

func (s *Store) applyLocalEmoji(ctx context.Context, m model.Message) model.Message {
	if m.Key.MessageID <= 0 {
		return m
	}
	var local localEmoji
	if cache := s.Cache(); cache != nil {
		if ok, err := cache.Get(ctx, localEmojiKey(m.Key.ChatID, int(m.Key.MessageID)), &local); ok && err == nil && m.Text == local.Text {
			m.Entities = mergeLocalEmoji(m.Entities, local.Entities)
			m.LocalPremium = len(local.Entities) > 0
			m.ContentRevision = model.Revision(m)
		}
	}
	return m
}

func (s *Store) keepLocalSticker(ctx context.Context, e *outgoingEntry) error {
	s.localMessagesMu.Lock()
	defer s.localMessagesMu.Unlock()
	m := e.Messages[0]
	m.Sending = false
	m.LocalPremium = true
	m.ContentRevision = model.Revision(m)
	cache := s.Cache()
	if m.Media != nil {
		if err := cache.PinLocalMedia(ctx, m.Media.ID); err != nil {
			return err
		}
		if b, _ := cache.Media(ctx, m.Media.ID); len(b) > 0 {
			if err := cache.SaveLocalMedia(ctx, m.Media.ID, b); err != nil {
				return err
			}
		}
	}
	// A separate collection leaves Telegram's contiguous history spans intact.
	var messages []model.Message
	if _, err := cache.Get(ctx, "local-premium/stickers", &messages); err != nil {
		return err
	}
	found := false
	for _, prior := range messages {
		if prior.Key == m.Key {
			found = true
			break
		}
	}
	if !found {
		messages = append(messages, m)
	}
	if err := cache.Put(ctx, "local-premium/stickers", messages); err != nil {
		return err
	}
	s.history.mu.Lock()
	s.history.localStickers = messages
	s.history.touchPending(e.Chat)
	s.history.mu.Unlock()
	return nil
}
