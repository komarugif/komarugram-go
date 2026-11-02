// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
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

// SetLocalPremium implements model.LocalPremiumStore. It is told again with
// every focus of a window; only a change does anything.
func (s *Store) SetLocalPremium(parts model.LocalPremium) {
	was := s.local()
	first := s.localPremium.Load() == nil
	if was == parts && !first {
		return
	}
	s.localPremium.Store(&parts)
	// The first time too: the dialogs kept may have been shown otherwise.
	if was.Media != parts.Media || first {
		s.localMediaChanged(parts.Media)
	}
	if parts.Sync && (!was.Sync || parts.GIFs && !was.GIFs || parts.Pins && !was.Pins) {
		s.storageResumed()
	}
	s.changed()
	s.syncPins()
	s.wakeStorage()
}

// localMediaChanged shows what is loaded as the setting now says: local stickers
// and local emoji with it, and without it what Telegram has, what the other side
// sees. The overlays and the stickers stay kept, for when it is on again.
func (s *Store) localMediaChanged(on bool) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	c.showLocalStickers = on
	for _, m := range c.localStickers {
		c.touchPending(m.Key.ChatID)
	}
	if c.closing || c.ctx == nil {
		return
	}
	ctx := c.ctx
	c.wg.Go(func() {
		defer crash.Recover("local emoji", nil)
		s.reapplyLocalEmoji(ctx)
		s.reapplyLocalPreviews(ctx)
	})
}

// savedChat reports whether chat is the account's Saved Messages. Telegram keeps
// paid custom emoji there for anyone, so that they are nothing local.
func (s *Store) savedChat(chat int64) bool {
	me := s.Me().ID
	return me != 0 && chat == me
}

// reapplyLocalPreviews has the overlays on the last messages of the chats, or
// off them, as they are in the histories. A list of dialogs tells the previews
// without them.
func (s *Store) reapplyLocalPreviews(ctx context.Context) {
	cache := s.Cache()
	if cache == nil {
		return
	}
	s.mu.RLock()
	chats := s.chats
	s.mu.RUnlock()
	redone := map[int64]model.Preview{}
	for _, c := range chats {
		if c.Last.ID <= 0 {
			continue
		}
		var local localEmoji
		if ok, err := cache.Get(ctx, localEmojiKey(c.ID, int(c.Last.ID)), &local); !ok || err != nil {
			continue
		}
		text, overlay := model.PreviewText(local.Text, local.Entities)
		if text != c.Last.Text {
			continue
		}
		p := c.Last
		if s.local().Media || s.savedChat(c.ID) {
			p.Entities = mergeLocalEmoji(p.Entities, overlay)
		} else {
			p.Entities = slices.DeleteFunc(slices.Clone(p.Entities), func(e model.Entity) bool { return slices.Contains(overlay, e) })
		}
		p.Entities = model.SortEntities(p.Entities)
		if !slices.Equal(p.Entities, model.SortEntities(c.Last.Entities)) {
			redone[c.ID] = p
		}
	}
	if len(redone) == 0 {
		return
	}
	s.publish(func() {
		s.chats = slices.Clone(s.chats)
		for i, c := range s.chats {
			if p, ok := redone[c.ID]; ok && p.ID == c.Last.ID {
				s.chats[i].Last = p
			}
		}
	})
	s.goPersistDialogs()
}

// reapplyLocalEmoji applies the overlays anew to the messages in memory.
func (s *Store) reapplyLocalEmoji(ctx context.Context) {
	c := s.history
	c.mu.Lock()
	loaded := make(map[int64][]model.Message, len(c.histories))
	for chat, h := range c.histories {
		loaded[chat] = slices.Clone(h.Messages)
	}
	c.mu.Unlock()
	changed := false
	for chat, messages := range loaded {
		redone := make(map[model.MessageKey]model.Message)
		for _, m := range messages {
			if n := s.applyLocalEmoji(ctx, m); n.ContentRevision != m.ContentRevision {
				redone[m.Key] = n
			}
		}
		if len(redone) == 0 {
			continue
		}
		changed = true
		c.mu.Lock()
		if h := c.histories[chat]; h != nil {
			for i, m := range h.Messages {
				if n, ok := redone[m.Key]; ok && n.Text == m.Text {
					h.Messages[i] = n
				}
			}
			h.Revision++
		}
		c.mu.Unlock()
	}
	if changed {
		s.changed()
	}
}

// HidesCustomEmoji implements model.LocalEmojiFilter: without Local Premium's
// media or a subscription, the field shows the ordinary emoji in place of a paid
// one. It is asked in frames: what is known is kept, and what is not asked again
// after a while, and counts as paid, as localEmojiKinds does.
func (s *Store) HidesCustomEmoji(chat, id int64) bool {
	if s.local().Media || s.Premium().Active || s.savedChat(chat) {
		return false
	}
	if kept, ok := s.emojiPaid.Load(id); ok {
		if k := kept.(emojiPaid); k.known || time.Since(k.at) < emojiPaidAgain {
			return k.paid
		}
	}
	k := emojiPaid{paid: true, at: time.Now()}
	if cache := s.Cache(); cache != nil {
		var doc model.Message
		if found, _ := cache.Get(context.Background(), emojiKey(id), &doc); found && doc.Media != nil && doc.Media.PremiumKnown {
			k.known, k.paid = true, doc.Media.Premium
		}
	}
	s.emojiPaid.Store(id, k)
	return k.paid
}

// emojiPaid is what HidesCustomEmoji keeps of a custom emoji.
type emojiPaid struct {
	known, paid bool
	at          time.Time
}

const emojiPaidAgain = 5 * time.Second

// local is what Local Premium has on.
func (s *Store) local() model.LocalPremium {
	if parts := s.localPremium.Load(); parts != nil {
		return *parts
	}
	return model.LocalPremium{}
}

// LocalPinsEnabled implements model.LocalPremiumSource.
func (s *Store) LocalPinsEnabled() bool { return s.local().Pins }

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
	if !s.local().Media && !s.savedChat(chat) {
		// Delivered after it was switched off: shown when it is on again.
		return nil
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
			if s.local().Media || s.savedChat(m.Key.ChatID) {
				m.Entities = mergeLocalEmoji(m.Entities, local.Entities)
				m.LocalPremium = len(local.Entities) > 0
			} else {
				// The cache keeps messages as they were shown.
				m.Entities = slices.DeleteFunc(slices.Clone(m.Entities), func(e model.Entity) bool { return slices.Contains(local.Entities, e) })
				m.LocalPremium = false
			}
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
