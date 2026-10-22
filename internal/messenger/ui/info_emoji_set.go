// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// emojiSetResult is the set an emoji status belongs to, as the store told it:
// nil when the emoji is in none.
type emojiSetResult struct {
	chat int64
	ref  *model.StickerSetRef
}

// openEmojiSet shows the set the emoji status emoji belongs to, over the
// info, as a click on a custom emoji in a message does: Telegram Desktop's
// profile does too.
func (p *chatInfo) openEmojiSet(emoji int64) {
	source, ok := p.renderer.source.(interface {
		CustomEmoji(context.Context, int64) (model.Message, error)
	})
	if !ok || !p.visible || emoji == 0 {
		return
	}
	ch := make(chan emojiSetResult, 1)
	p.emojiSet = ch
	chat, invalidate := p.chat.ID, p.invalidate
	go func() {
		defer crash.Recover("emoji set", func(*crash.Panic) { ch <- emojiSetResult{chat: chat} })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var ref *model.StickerSetRef
		if m, err := source.CustomEmoji(ctx, emoji); err == nil && m.Media != nil {
			ref = m.Media.StickerSet
		}
		ch <- emojiSetResult{chat: chat, ref: ref}
		invalidate()
	}()
}

// drainEmojiSet opens the set that came, if the info is still of the chat it
// was asked in.
func (p *chatInfo) drainEmojiSet() {
	select {
	case r := <-p.emojiSet:
		p.emojiSet = nil
		if r.chat == p.chat.ID && r.ref != nil {
			p.renderer.stickers.open(p.renderer, *r.ref)
		}
	default:
	}
}
