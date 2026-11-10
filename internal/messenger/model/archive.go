// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"time"
)

// ArchiveProgress is how far saving a chat's messages to the cache has come.
type ArchiveProgress struct {
	// Cached is how many messages of the chat the cache holds now, those it
	// held before included; Total how many Telegram has, below 0 when not
	// known.
	Cached, Total int
	// Wait is how long Telegram asked the saving to wait for, when it is
	// waiting.
	Wait time.Duration
}

// Share is how much of the chat the cache holds, 0..1, below 0 when the
// total is not known.
func (p ArchiveProgress) Share() float32 {
	if p.Total <= 0 {
		return -1
	}
	return float32(min(p.Cached, p.Total)) / float32(p.Total)
}

// ChatArchiver counts a chat's messages and saves those the cache lacks.
type ChatArchiver interface {
	// CachedMessages is how many messages of chat the cache holds: it asks
	// nothing of Telegram.
	CachedMessages(ctx context.Context, chat int64) (int, error)
	// MessageTotal is how many messages chat has, as Telegram counts them.
	MessageTotal(ctx context.Context, chat int64) (int, error)
	// ArchiveChat saves the messages of chat that the cache lacks, newest
	// first, telling how far it has come, until ctx ends: those saved stay
	// in the cache, and a next call goes on from them. It runs as long as
	// it takes, at the pace of scrolling the history by hand. The messages
	// go to the cache only: the history on screen, and the room it reserves
	// for the rows not loaded, are the same.
	ArchiveChat(ctx context.Context, chat int64, report func(ArchiveProgress)) error
}
