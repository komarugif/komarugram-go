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

// UploadStatus is how far a file on its way to Telegram went: Done bytes of
// Total, both 0 before its upload starts.
type UploadStatus struct {
	Done, Total int64
}

// UploadSource tells of the files on their way, one by one: each file of an
// album is a message of its own.
type UploadSource interface {
	// UploadStatus is key's file's; false for what is no file on its way, or
	// no longer is, Telegram having taken it.
	UploadStatus(key MessageKey) (UploadStatus, bool)
	// CancelUpload takes key's file out of its message, which goes without
	// it; the others go on as they were. It does nothing once the request
	// that carries the file was made.
	CancelUpload(key MessageKey)
}

// OutboxStore accepts a message once its request and attachments are durable.
// Delivery continues independently of the composer and its window.
type OutboxStore interface {
	QueueMessage(context.Context, int64, OutgoingMessage) error
	RetryOutgoing(MessageKey)
	CancelOutgoing(MessageKey)
}

// ErrNoFFprobe is why a video cannot be sent as one here: nothing can tell its
// size and length.
var ErrNoFFprobe = errors.New("could not inspect video; install ffprobe or attach it as a file")

// SendFailure is a message Telegram will not be asked for again.
type SendFailure struct {
	// Text is what went wrong, as it was told; NoFFprobe says it was ErrNoFFprobe.
	Text      string
	NoFFprobe bool
}

// SendFailureSource tells of messages that failed, which the queue accepted long
// before. Without it they would only have a mark, wherever they are.
type SendFailureSource interface {
	// TakeSendFailure is the last failure not told yet. It is asked in frames.
	TakeSendFailure() (SendFailure, bool)
}

// LocalPremium is what the client does on its own of what Premium gives. Each
// part is of use with a subscription too, past its higher limits.
type LocalPremium struct {
	// Star shows the Premium star beside the accounts signed in here.
	Star bool
	// Media shows Premium stickers and custom emoji, kept on this device.
	Media bool
	// GIFs and Pins keep the saved GIFs and pinned chats past Telegram's limits.
	GIFs, Pins bool
	// Sync shares what GIFs and Pins keep through a private channel.
	Sync bool
}

// CanSync reports whether Sync has anything to share.
func (l LocalPremium) CanSync() bool { return l.GIFs || l.Pins }

// Consistent is l without Sync when it has nothing to share.
func (l LocalPremium) Consistent() LocalPremium {
	l.Sync = l.Sync && l.CanSync()
	return l
}

// AsksForChannel reports whether going from was to l has the user asked about
// the channel: Sync just ticked, or GIFs ticked, which can then go there.
func (l LocalPremium) AsksForChannel(was LocalPremium) bool {
	return l.Sync && (!was.Sync || l.GIFs && !was.GIFs)
}

// Any reports whether any part is on.
func (l LocalPremium) Any() bool { return l != LocalPremium{} }

// AllLocalPremium has every part on.
var AllLocalPremium = LocalPremium{Star: true, Media: true, GIFs: true, Pins: true, Sync: true}

// LocalPremiumStore controls client-only presentation and collections. It never
// changes the account's actual Telegram subscription.
type LocalPremiumStore interface{ SetLocalPremium(LocalPremium) }

// LocalEmojiFilter tells which custom emoji the message field of chat shows as
// ordinary ones: those the account could not really send there. It is asked in
// frames.
type LocalEmojiFilter interface {
	HidesCustomEmoji(chat, document int64) bool
	// CannotSendPaid reports whether paid stickers, or with emoji paid custom
	// emoji, could not be sent to chat, really or locally.
	CannotSendPaid(chat int64, emoji bool) bool
}

// WithoutPaid is items without the paid stickers and custom emoji.
func WithoutPaid(items []PickerItem) []PickerItem {
	var out []PickerItem
	for _, item := range items {
		if m := item.Media.Media; m == nil || !m.Premium {
			out = append(out, item)
		}
	}
	return out
}

// WithoutPaid is the page without the paid stickers and custom emoji, and the
// packs that were nothing else.
func (p PickerPage) WithoutPaid() PickerPage {
	packs := func(packs []PickerPack) []PickerPack {
		var out []PickerPack
		for _, pack := range packs {
			if pack.Items = WithoutPaid(pack.Items); len(pack.Items) > 0 {
				out = append(out, pack)
			}
		}
		return out
	}
	p.Recent, p.Items = WithoutPaid(p.Recent), WithoutPaid(p.Items)
	p.Packs, p.Featured = packs(p.Packs), packs(p.Featured)
	return p
}

type LocalPremiumSource interface{ LocalPinsEnabled() bool }

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
