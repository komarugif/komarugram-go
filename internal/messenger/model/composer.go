package model

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf16"
)

type PickerTab int

const (
	PickerEmoji PickerTab = iota
	PickerStickers
	PickerGIF
)

// PickerItem contains display data only. Telegram access hashes stay in the store.
type PickerItem struct {
	Keywords   string
	ID, Emoji  string
	DocumentID int64
	Media      Message
	Custom     bool
	QueryID    int64
	ResultID   string
}
type PickerPack struct {
	ID    int64
	Title string
	Items []PickerItem
	// Complete is false while Items only contains featured cover documents.
	Complete bool
	// Ref opens the full set from a featured preview.
	Ref StickerSetRef
}
type PickerPage struct {
	Recent []PickerItem
	Packs  []PickerPack
	// Featured contains server-recommended sets not installed by this account.
	Featured []PickerPack
	Items    []PickerItem
	Next     string
	// Stale tells a page as the store last kept it, shown while it asks
	// Telegram what changed since: PickerRefresher brings the page after.
	Stale bool `json:",omitempty"`
}
type PickerRequest struct {
	Tab                     PickerTab
	Query, Offset, Language string
	ChatID                  int64
}
type OutgoingMessage struct {
	RandomID int64
	Text     string
	Entities []Entity
	Item     *PickerItem
	Path     string
	AsMedia  bool
	Tasks    []string
	// ReplyTo is the message of the same chat this one replies to, 0 for
	// none.
	ReplyTo MessageID
	// Voice makes the file at Path, Opus in OGG, MP3 or M4A, a voice
	// message.
	Voice *VoiceNote
	// FFmpeg is the FFmpeg the user set, beside which ffprobe inspects a
	// video sent as media; empty for the one on PATH.
	FFmpeg string
	// Files, when set, are the files sent, with Text as their caption.
	Files *OutgoingFiles
}

// OutgoingFiles are files sent together, as Telegram Desktop's box for
// sending files sends them: photos compressed or as documents, in albums
// or one by one.
type OutgoingFiles struct {
	// Paths are the files, in the order they are sent.
	Paths []string
	// Documents sends photos and videos as files. Group puts what can go in
	// an album in one. HighQuality lets photos keep up to 2560 pixels a
	// side instead of 1280.
	Documents, Group, HighQuality bool
}

// VoiceNote is what Telegram shows of a voice message before it is played.
type VoiceNote struct {
	Duration time.Duration
	// Waveform is its loudness in 5-bit bars, as Telegram packs it.
	Waveform []byte
}

func (m OutgoingMessage) Validate() error {
	if m.RandomID == 0 {
		return errors.New("missing message identity")
	}
	if m.Files != nil && len(m.Files.Paths) == 0 {
		return errors.New("no files to send")
	}
	if strings.TrimSpace(m.Text) == "" && m.Item == nil && m.Path == "" && m.Files == nil {
		return errors.New("empty message")
	}
	if len(utf16.Encode([]rune(m.Text))) > 4096 {
		return errors.New("message exceeds 4096 characters")
	}
	if len(m.Tasks) > 0 {
		if strings.TrimSpace(m.Text) == "" || len(m.Tasks) > 30 {
			return errors.New("a checklist needs a title and at most 30 tasks")
		}
		for _, t := range m.Tasks {
			if strings.TrimSpace(t) == "" {
				return errors.New("empty task")
			}
		}
	}
	return nil
}

type ComposerStore interface {
	Picker(context.Context, PickerRequest) (PickerPage, error)
	Send(context.Context, int64, OutgoingMessage) error
}

// OutboxStore accepts a message once its request and attachments are durable.
// Delivery continues independently of the composer and its window.
type OutboxStore interface {
	QueueMessage(context.Context, int64, OutgoingMessage) error
	RetryOutgoing(MessageKey)
	CancelOutgoing(MessageKey)
}

// LocalPremiumStore controls client-only presentation and collections. It never
// changes the account's actual Telegram subscription.
type LocalPremiumStore interface{ SetLocalPremium(bool) }

type LocalPremiumSource interface{ LocalPremiumEnabled() bool }

// PreferSaved deduplicates global search results while retaining saved-pack order.
func PreferSaved(saved, global []PickerItem) []PickerItem {
	out := make([]PickerItem, 0, len(saved)+len(global))
	seen := map[string]bool{}
	for _, items := range [][]PickerItem{saved, global} {
		for _, item := range items {
			if !seen[item.ID] {
				seen[item.ID] = true
				out = append(out, item)
			}
		}
	}
	return out
}

// PickerRefresher is a ComposerStore whose Picker may give a Stale page:
// RefreshPicker waits until it is brought up to date, and gives the page
// of req then.
type PickerRefresher interface {
	RefreshPicker(ctx context.Context, req PickerRequest) (PickerPage, error)
}

// PickerRecentStore optionally persists selections in the account's local cache.
type PickerRecentStore interface {
	RememberPicker(context.Context, PickerTab, PickerItem) error
}

// SavedGIFStore is a ComposerStore that keeps the account's saved GIFs, the
// GIF tab's list, as Telegram Desktop's panel does (messages.saveGif).
type SavedGIFStore interface {
	// SavedGIF reports whether the GIF of media id is among the saved
	// ones, as the store last knew them.
	SavedGIF(id string) bool
	// SaveGIF puts the GIF m shows first among the saved ones, or, with
	// unsave, removes it. A save over the limit drops the last one, and
	// reports it with ErrSavedGIFLimit, the save done.
	SaveGIF(ctx context.Context, m Message, unsave bool) error
}

// ErrSavedGIFLimit tells that a GIF saved took the place of the oldest, the
// saved ones being as many as the account may keep.
var ErrSavedGIFLimit = errors.New("saved GIFs limit reached")

// SavedGIFLimit is how many GIFs an account may keep, by its Premium, as
// Telegram Desktop counts them (saved_gifs_limit).
func SavedGIFLimit(p Premium) int {
	return p.Limit("saved_gifs_limit")
}

// RecentStickerStore is a ComposerStore whose recent stickers can be taken
// off the list (messages.saveRecentSticker with unsave).
type RecentStickerStore interface {
	RemoveRecentSticker(ctx context.Context, item PickerItem) error
}

// PickerItemOf is the item a picker shows of the media of m, a GIF or a
// sticker of a message.
func PickerItemOf(m Message) PickerItem {
	item := PickerItem{Media: Message{Kind: m.Kind, Media: m.Media}}
	if m.Media != nil {
		item.ID, item.Emoji = m.Media.ID, m.Media.Emoji
	}
	return item
}

// WithoutItem is items without the one of id, items itself when none is.
func WithoutItem(items []PickerItem, id string) []PickerItem {
	for i, item := range items {
		if item.ID == id {
			out := make([]PickerItem, 0, len(items)-1)
			out = append(out, items[:i]...)
			return append(out, WithoutItem(items[i+1:], id)...)
		}
	}
	return items
}
