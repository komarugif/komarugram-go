// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
)

// MessageEditor is a store whose messages the account may edit.
type MessageEditor interface {
	// EditMessage makes text, with entities, the text of the message key,
	// and blocks until Telegram has it. It returns ErrMessageNotModified
	// for a text the message has already, ErrEditForbidden for a message
	// that cannot be edited (any more), and ErrMessageEmpty for an empty
	// text where the message has nothing else.
	EditMessage(ctx context.Context, key MessageKey, text string, entities []Entity) error
	// EditTimeLimit is how long after it was sent a message may be edited,
	// Telegram's edit_time_limit.
	EditTimeLimit() time.Duration
}

// DefaultEditTimeLimit is Telegram's edit_time_limit until the store has
// read its own, as Telegram Desktop's (mtproto_config.h).
const DefaultEditTimeLimit = 48 * time.Hour

// MaxMessageLength is the most UTF-16 code units a message's text may have,
// Telegram's message_length_max for an account without Premium.
const MaxMessageLength = 4096

// MaxCaptionLength is the same for the caption of media.
const MaxCaptionLength = 1024

// Errors of an edit, by Telegram's errors, as Telegram Desktop tells them
// (history_widget.cpp, saveEditMessage).
var (
	// ErrMessageNotModified is MESSAGE_NOT_MODIFIED: nothing changed,
	// which ends the edit.
	ErrMessageNotModified = errors.New("message not modified")
	// ErrEditForbidden is MESSAGE_ID_INVALID, CHAT_ADMIN_REQUIRED and
	// MESSAGE_EDIT_TIME_EXPIRED: the message cannot be edited.
	ErrEditForbidden = errors.New("message cannot be edited")
	// ErrMessageEmpty is MESSAGE_EMPTY.
	ErrMessageEmpty = errors.New("message empty")
)

// EditTooLong is a text longer than its limit by Over code units.
type EditTooLong struct{ Over int }

func (e *EditTooLong) Error() string { return "message too long" }

// CheckEdit tells whether text may replace the text of m: an error of
// the kinds above, or an *EditTooLong.
func CheckEdit(m Message, text string) error {
	limit := MaxMessageLength
	if m.Media != nil || m.Kind != MessageText {
		limit = MaxCaptionLength
	}
	if over := len(utf16.Encode([]rune(text))) - limit; over > 0 {
		return &EditTooLong{Over: over}
	}
	if strings.TrimSpace(text) == "" && m.Media == nil && m.Kind == MessageText {
		return ErrMessageEmpty
	}
	return nil
}

// Editable tells whether the account may edit m, a message of a chat of
// kind, at now, with limit for Telegram's edit time limit, as Telegram
// Desktop's HistoryItem::allowsEdit: its own message, not a service one,
// not forwarded, not a rich message, not a sticker, a poll or a round
// video, and not older than the limit, which Saved Messages have none of.
func Editable(m Message, kind ChatKind, now time.Time, limit time.Duration) bool {
	if m.Key.MessageID <= 0 || m.Local() || m.Deleted || m.Service != nil || m.Rich != nil {
		return false
	}
	if !m.Outgoing && kind != KindSaved {
		return false
	}
	if m.ForwardFromID != 0 || !m.ForwardDate.IsZero() || m.ForwardName != "" {
		return false
	}
	switch m.Kind {
	case MessageService, MessageSticker, MessagePoll, MessageRound:
		return false
	}
	if m.Poll != nil || m.Gift != nil {
		return false
	}
	if kind == KindSaved {
		return true
	}
	return limit <= 0 || now.Sub(m.Date) < limit
}

// Formatting kinds of entities, which the message field applies to what is
// selected.
const (
	EntityBold      = "bold"
	EntityItalic    = "italic"
	EntityUnderline = "underline"
	EntityStrike    = "strike"
	EntityCode      = "code"
	EntityPre       = "pre"
	EntitySpoiler   = "spoiler"
	EntityURL       = "url"
	EntityQuote     = "quote"
	EntityEmoji     = "emoji"
)

// EditableEntities are the entities of m that its text keeps in the message
// field: what was written, not what Telegram found in it, links typed
// as they are, mentions and hashtags, which Telegram finds again.
func EditableEntities(m Message) []Entity {
	var out []Entity
	for _, e := range m.Entities {
		switch e.Kind {
		case EntityBold, EntityItalic, EntityUnderline, EntityStrike, EntityCode, EntityPre, EntitySpoiler, EntityQuote, EntityEmoji:
			out = append(out, e)
		case EntityURL:
			if e.URL != "" {
				out = append(out, e)
			}
		}
	}
	return out
}

// SameText tells whether text with entities is what m has, as Telegram
// would compare them for MESSAGE_NOT_MODIFIED.
func SameText(m Message, text string, entities []Entity) bool {
	if m.Text != text {
		return false
	}
	a, b := SortEntities(EditableEntities(m)), SortEntities(entities)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || a[i].Offset != b[i].Offset || a[i].Length != b[i].Length || a[i].URL != b[i].URL || a[i].DocumentID != b[i].DocumentID || a[i].Language != b[i].Language {
			return false
		}
	}
	return true
}

// SortEntities returns entities ordered by offset, then the longer first,
// as Telegram keeps them, without the empty ones.
func SortEntities(entities []Entity) []Entity {
	out := make([]Entity, 0, len(entities))
	for _, e := range entities {
		if e.Length > 0 {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Offset != out[j].Offset {
			return out[i].Offset < out[j].Offset
		}
		if out[i].Length != out[j].Length {
			return out[i].Length > out[j].Length
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}
