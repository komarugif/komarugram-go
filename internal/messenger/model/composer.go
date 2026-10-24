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
