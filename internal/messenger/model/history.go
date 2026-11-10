// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"time"
)

// MessageID is Telegram's per-dialog message identifier.
type MessageID int

// MessageKey is globally unique inside the local database. Telegram message
// IDs alone are only unique within an account/dialog combination.
type MessageKey struct {
	AccountID string
	ChatID    int64
	MessageID MessageID
}

// MessageKind describes the payload needed by the renderer without retaining
// the complete MTProto update as the application's database model.
type MessageKind uint8

const (
	MessageText MessageKind = iota
	MessagePhoto
	MessageVideo
	MessageFile
	MessageVoice
	MessageService
	MessageSticker
	MessageGIF
	MessageMusic
	MessagePoll
	// MessageRound is a video message, the round one, which plays in the
	// client with its sound, as a voice message does.
	MessageRound
)

// MaxMediaBytes is the most of a file the client reads into memory whole,
// to draw it: a larger one is streamed, as a video or a file is to open it.
const MaxMediaBytes = 64 << 20

// ErrMediaTooLarge is media larger than MaxMediaBytes, asked for whole.
var ErrMediaTooLarge = fmt.Errorf("media exceeds %d MiB limit", MaxMediaBytes>>20)

// Whole tells whether m's media is small enough to be read whole.
func (m *MessageMedia) Whole() bool {
	return m != nil && m.Size <= MaxMediaBytes
}

// MessageMedia contains durable presentation metadata. The Telegram download
// location/file_reference belongs in the account database adapter because it
// can expire and must not leak into UI state.
type MessageMedia struct {
	Preview   []byte
	Thumbnail *MessageMedia
	// StickerSet identifies the pack of a sticker or custom emoji. It is nil
	// for documents without a usable pack reference.
	StickerSet *StickerSetRef `json:",omitempty"`
	// TextColor marks a custom emoji drawn in the color of the text around
	// it, as Telegram marks some: its shape, not its own colors.
	TextColor bool `json:",omitempty"`
	// Premium marks restricted stickers and non-free custom emoji.
	Premium bool `json:",omitempty"`
	// PremiumKnown distinguishes unrestricted media from old cached documents
	// that predate entitlement metadata.
	PremiumKnown bool `json:",omitempty"`
	// Emoji is the emoji a sticker stands for, which a list shows before
	// the word, as Telegram's clients do.
	Emoji     string `json:",omitempty"`
	ID        string
	FileName  string
	MIMEType  string
	Size      int64
	Width     int
	Height    int
	Duration  time.Duration
	Performer string
	Title     string
	// Waveform is a voice message's loudness in 5-bit bars, as Telegram
	// packs it (see voice.Bars); nil when the sender gave none.
	Waveform []byte `json:",omitempty"`
	// Motion is the video a profile photo plays, in its place, looped; nil for one
	// that is only a picture.
	Motion *MessageMedia `json:",omitempty"`
	// Variants are smaller renditions of the same picture that can be
	// downloaded on their own, smallest first. The media itself is the
	// largest. Messages cached before variants existed have none.
	Variants []MessageMedia `json:",omitempty"`
}

// StickerSetRef is the durable part of Telegram's inputStickerSet. Type is
// "id", "short_name", "dice", or one of the named built-in emoji sets.
// Keeping the reference on media also covers custom emoji documents fetched
// from text entities after their message was cached.
type StickerSetRef struct {
	Type       string
	ID         int64  `json:",omitempty"`
	AccessHash int64  `json:",omitempty"`
	ShortName  string `json:",omitempty"`
	Emoticon   string `json:",omitempty"`
}

// Variant returns the smallest rendition that covers w×h pixels, or the
// media itself when no smaller one does.
func (m *MessageMedia) Variant(w, h int) *MessageMedia {
	for i := range m.Variants {
		v := &m.Variants[i]
		if v.Width >= w && v.Height >= h {
			return v
		}
	}
	return m
}

// Message is the durable, renderer-independent part of a history item.
// ContentRevision must change when anything affecting layout changes (an edit,
// media metadata becoming available, reply preview update, and so on).
type Message struct {
	WebPage *WebPreview `json:",omitempty"`
	Gift    *Gift       `json:",omitempty"`
	Poll    *Poll       `json:",omitempty"`
	// Todo is the message's checklist.
	Todo *Todo `json:",omitempty"`
	// Service is what a service message tells. Messages cached before
	// it existed have none, and show as a service message only.
	Service     *ServiceAction `json:",omitempty"`
	Attachments []Message      `json:"-"`
	Key         MessageKey
	SenderID    int64
	Kind        MessageKind
	Text        string
	SenderName  string
	Entities    []Entity
	Buttons     [][]MessageButton
	// Keyboard is the reply keyboard the message sets for the chat, and
	// KeyboardHide takes the last one away.
	Keyboard     *ReplyKeyboard `json:",omitempty"`
	KeyboardHide bool           `json:",omitempty"`
	Date         time.Time
	EditedAt     time.Time
	// HiddenEditAt is the date of an edit Telegram asks not to show
	// (edit_hide), which EditedAt leaves out: messages.getHistory's hash
	// folds it all the same (see tgstore's historyHash).
	HiddenEditAt     time.Time `json:",omitzero"`
	Outgoing         bool
	ReplyToMessageID MessageID
	ForwardFromID    int64
	ForwardDate      time.Time
	// ForwardPostID is the post of ForwardFromID, a channel, the message
	// was forwarded from (fwd_from.channel_post), 0 for none.
	ForwardPostID   MessageID `json:",omitempty"`
	GroupedID       int64
	Media           *MessageMedia
	ContentRevision uint64
	// NoForwards is set when the message protects its content: it cannot
	// be forwarded or saved.
	NoForwards bool `json:",omitempty"`
	// MediaSpoiler marks media its sender hid under a spoiler, or one that
	// destructs itself once seen.
	MediaSpoiler bool `json:",omitempty"`
	// Post is set for a message a channel posted.
	Post bool `json:",omitempty"`
	// ForwardName is who the forwarded message came from, as a name.
	ForwardName string `json:",omitempty"`
	// Views counts who saw a channel post; PostAuthor signs it.
	Views      int    `json:",omitempty"`
	PostAuthor string `json:",omitempty"`
	// Comments counts a channel post's comments when its channel has a
	// discussion group, which CommentsOpen marks, 0 comments included;
	// Commenters are the peers who commented last, newest first.
	Comments     int        `json:",omitempty"`
	CommentsOpen bool       `json:",omitempty"`
	Commenters   []int64    `json:",omitempty"`
	Reactions    []Reaction `json:",omitempty"`
	// ReactionsListed is set when the account may see who reacted.
	ReactionsListed bool `json:",omitempty"`
	// Deleted marks a message Telegram deleted that the cache kept.
	Deleted bool `json:",omitempty"`
	// ReplyToTopID is the root of the thread a reply is in, when it
	// replies to another reply there.
	ReplyToTopID MessageID `json:",omitempty"`
	// ForumTopic is set when the message's reply header marks it as part of
	// a forum topic: it replies to the topic's root, or to a message in it.
	ForumTopic bool `json:",omitempty"`
	// Rich is a rich message's article; Text is its summary then.
	Rich *RichPage `json:",omitempty"`
	// Story is what a story has beyond a message: see StoryMessage.
	Story *Story `json:",omitempty"`
	// StoryOf is the story a message of a chat carries, forwarded to it or
	// one the account was mentioned in: its media is the story's, a photo
	// or a video, its text the story's caption. ReplyToStory is the story
	// a message replies to.
	StoryOf      *StoryRef `json:",omitempty"`
	ReplyToStory *StoryRef `json:",omitempty"`
	// MediaUnread is Telegram's mark of a voice message nobody listened to
	// yet: the account, for one that came, or who it was sent to. Telegram
	// sets it on an unread mention as well; only voice messages show it.
	MediaUnread bool `json:",omitempty"`
	// Streaming marks a message a bot streams as it writes it, not one yet:
	// see StreamedDrafts. Stoppable is set when the account may stop it.
	Streaming bool `json:"-"`
	Stoppable bool `json:"-"`
	// Sending marks a message the account sent that Telegram has not taken
	// yet, which only the history shows: it has an id of its own, below
	// zero, until Telegram's comes.
	Sending bool `json:"-"`
	// SendError is a failed outgoing request, retained for retry or cancellation.
	SendError string `json:"-"`
	// LocalPremium marks content kept only on this device.
	LocalPremium bool `json:",omitempty"`
}

// Local reports whether m is not a message of Telegram's yet, but one the
// client shows on its own: a draft a bot streams, or a message on its way.
// It is not one to reply to, select or keep the layout of.
func (m Message) Local() bool {
	return m.Streaming || m.Sending || m.LocalPremium && m.Key.MessageID < 0
}

// GeneralTopic is the id of the topic every forum has, whose messages have
// no reply header.
const GeneralTopic = 1

// TopicID is the forum topic the message is in: the topic it names, the one
// its creation opened, or else the General one. It means something only for
// a message of a forum.
func (m Message) TopicID() int {
	switch {
	case m.Service != nil && m.Service.Kind == ServiceTopicCreate:
		return int(m.Key.MessageID)
	case m.ForumTopic && m.ReplyToTopID != 0:
		return int(m.ReplyToTopID)
	case m.ForumTopic:
		return int(m.ReplyToMessageID)
	}
	return GeneralTopic
}

// CommentsStore opens the comments of channel posts.
type CommentsStore interface {
	// OpenComments returns the chat that shows the comments to post, a
	// channel post with CommentsOpen. The chat's history loads as any
	// chat's does; its first message is the post as the discussion group
	// has it.
	OpenComments(post Message) Chat
}

// Revision returns a ContentRevision for m that changes with anything stored
// in it: an edit, a reaction, a view. A store without its own counter sets it
// from this.
func Revision(m Message) uint64 {
	m.ContentRevision = 0
	b, err := json.Marshal(m)
	if err != nil {
		return 1
	}
	h := fnv.New64a()
	h.Write(b)
	// 0 is reserved for "not set".
	return h.Sum64()>>1 | 1
}

// Reaction is one kind of reaction to a message and how many chose it.
type Reaction struct {
	// Emoji is the reaction's emoji; DocumentID, a custom emoji instead;
	// Paid, the Telegram Stars reaction.
	Emoji      string `json:",omitempty"`
	DocumentID int64  `json:",omitempty"`
	Paid       bool   `json:",omitempty"`
	Count      int
	// Chosen is set when the account chose it.
	Chosen bool `json:",omitempty"`
}

// WithMedia returns a copy of m that shows media, such as one of the
// variants of its own, in place of m.Media.
func (m Message) WithMedia(media *MessageMedia) Message {
	m.Media = media
	return m
}

// HistoryCursor is opaque synchronization state for paging in either
// direction. The database may encode Telegram offsets or a local row key.
type HistoryCursor string

// HistorySlice is one ordered page returned from the local cache or network.
type HistorySlice struct {
	Messages    []Message
	Older       HistoryCursor
	Newer       HistoryCursor
	HasOlder    bool
	HasNewer    bool
	FromNetwork bool
}

// History is the state a chat view needs while pages are loaded around an
// anchor. It is deliberately separate from Chat, which stays a cheap list row.
type History struct {
	Revision     uint64
	Offline      bool
	Messages     []Message
	LoadingOlder bool
	LoadingNewer bool
	HasOlder     bool
	HasNewer     bool
	Err          error
	// ThreadRoot is the root of a thread chat, such as a post's comments:
	// replies to it quote nothing, since every message there replies to it.
	ThreadRoot MessageID
	// Count is how many messages Telegram counts in a thread chat, a
	// topic's first message included, once Counted is set: a topic's
	// header tells it, as Telegram Desktop's does.
	Count   int
	Counted bool
}

// RenderEnvironment identifies every external input that can change a
// message's measured height. It is comparable and suitable for a database
// compound key. ScaleMilli is device scale multiplied by 1000.
type RenderEnvironment struct {
	// WidthPx is the width of the history, up to the width past which no
	// row is laid out wider: wider histories keep their heights under it.
	WidthPx        int
	ScaleMilli     int
	TextScaleMilli int
	Locale         string
	FontRevision   uint32
	// LookRevision tells the parts of the look that change how wide a
	// message's time is, and so whether it fits on the text's last line:
	// its seconds and the marks of edited and deleted messages. Colors
	// change no height and are not in it.
	LookRevision     uint32
	RendererRevision uint32
}

// MessageLayout is a disposable cached measurement. It must never be treated
// as message data: a different environment or content revision is a cache miss.
type MessageLayout struct {
	Key             MessageKey
	Environment     RenderEnvironment
	ContentRevision uint64
	HeightPx        int
	MeasuredAt      time.Time
	// Profile, when set, is what the row's layout tells of its layout at
	// any width (the UI's row profile), kept under Environment without its
	// width (RowProfiles).
	Profile []byte
}

// ReserveRow is a message the cache keeps after where a history is, as
// HistoryReserve tells it: its revision, whether it is the account's, the
// album it is a part of, whether it has media or is a service message;
// how high its row was laid out in the environment asked, at the revision
// told (Height -1 where it was not), and its row's profile, at the
// revision told (MessageLayout.Profile; nil for none).
type ReserveRow struct {
	ID              MessageID
	Revision        uint64
	Outgoing        bool
	GroupedID       int64
	Media, Service  bool
	Height          int
	HeightRevision  uint64
	Profile         []byte
	ProfileRevision uint64
}

// HistoryWindow is a store whose loaded history can move to the messages
// its cache keeps around a message at once (LoadAround), rather than page
// by page to them: the view went far into the rows whose room the history
// keeps. LoadAround tells whether it started; the history comes as pages
// do.
type HistoryWindow interface {
	LoadAround(chat int64, id MessageID) bool
}

// RowShapes is a store that reads the profiles its cache keeps of the
// rows of a chat's messages low to high, laid out in env at any width, in
// the encoding of version, without the messages: the history draws the
// skeletons of rows it let go of by them. It reads the cache: it is not
// called from a frame.
type RowShapes interface {
	ProfilesBetween(chat int64, env RenderEnvironment, version byte, low, high MessageID) map[MessageID][]byte
}

// HistoryReserve is a store that tells what its cache keeps of a chat's
// history after a message: the messages of the run it keeps whole after
// it, oldest first, at most limit of them, with their heights in env and
// their rows' profiles of the version of their encoding asked. The
// history reserves the room of the rows below what it has loaded by them,
// and draws them as skeletons until their pages come. It reads the cache:
// it is not called from a frame.
type HistoryReserve interface {
	Reserve(chat int64, after MessageID, env RenderEnvironment, version byte, limit int) []ReserveRow
}

// CachedHistory is a store that reads the messages of a chat's history
// its cache keeps, without Telegram: from message from on, in the run it
// keeps whole with it, oldest first, at most limit of them, as a page of
// the history would bring them. The history measures by them the rows of
// the reserve the scroll went past without laying them out. It reads the
// cache: it is not called from a frame.
//
// CachedLookup is message id of chat as the store has it without
// Telegram, for a reply that names it: in the cache's history, or looked
// up on its own before; known is false when it has none.
type CachedHistory interface {
	CachedFrom(chat int64, from MessageID, limit int) []Message
	CachedLookup(chat int64, id MessageID) (m Message, state LookupState, known bool)
}

// RowProfiles is a store that keeps the profiles of rows that come with
// their layouts (MessageLayout.Profile): Profiled tells the revisions of
// the messages of chat it has one of, laid out in env at any width, in the
// version of their encoding, the first byte of each.
type RowProfiles interface {
	Profiled(chat int64, env RenderEnvironment, version byte) map[MessageID]uint64
}

// LayoutChunk stores a prefix-sum segment. Chunk totals let a scrollbar map a
// large pixel offset to a small range without loading or summing every message.
type LayoutChunk struct {
	AccountID     string
	ChatID        int64
	FirstMessage  MessageID
	LastMessage   MessageID
	Environment   RenderEnvironment
	MessageCount  int
	TotalHeightPx int64
	// Revision changes whenever a member measurement changes.
	Revision uint64
}

// Viewport is the durable scroll position. The logical anchor is authoritative;
// AbsoluteOffsetPx is only an acceleration hint for a matching environment.
type Viewport struct {
	AccountID        string
	ChatID           int64
	AnchorMessageID  MessageID
	AnchorOffsetPx   int
	AbsoluteOffsetPx int64
	AtEnd            bool
	Environment      RenderEnvironment
	UpdatedAt        time.Time
}

// HistoryStore is the boundary a future SQLite implementation can satisfy.
// Keeping it separate from Store avoids forcing chat-list-only/demo stores to
// implement history before the history UI exists.
type HistoryStore interface {
	LoadHistory(context.Context, string, int64, HistoryCursor, int) (HistorySlice, error)
	SaveMessages(context.Context, []Message) error
	LoadViewport(context.Context, string, int64) (Viewport, bool, error)
	SaveViewport(context.Context, Viewport) error
	LoadMessageLayouts(context.Context, string, int64, RenderEnvironment) ([]MessageLayout, []LayoutChunk, error)
	SaveMessageLayouts(context.Context, []MessageLayout, []LayoutChunk) error
}

// Entity offsets and lengths use Telegram's UTF-16 code units.
type Entity struct {
	Kind           string
	Offset, Length int
	URL            string
	DocumentID     int64
	Language       string `json:",omitempty"`
	Collapsed      bool   `json:",omitempty"`
	// Date and DateFormat are a formatted date's: Unix seconds, and how it
	// is written.
	Date       int64      `json:",omitempty"`
	DateFormat DateFormat `json:",omitempty"`
	// Button is an inline button's, in a rich message's text.
	Button *MessageButton `json:",omitempty"`
}

// DateFormat is how a formatted date entity is written, as Telegram's
// flags say.
type DateFormat uint8

const (
	DateRelative DateFormat = 1 << iota
	DateShortTime
	DateLongTime
	DateShortDate
	DateLongDate
	DateDayOfWeek
)

// MessageButton is a button of a bot's keyboard, under a message or in a
// reply keyboard. Kind is "url" (opens URL), "callback" (asks the bot,
// with Data, and shows its answer), "copy" (copies Copy), "text" (a reply
// keyboard's button, which sends its own Text) or "action" for what this
// client does not do: it shows the button disabled.
type MessageButton struct {
	Text, URL, Kind string
	Data            []byte `json:",omitempty"`
	Copy            string `json:",omitempty"`
}

// PhotoGallery pages a chat's photos independently of its visible history.
// Sources may use Telegram search or an offline cache. A page is oldest
// first: before it when dir is negative, after it otherwise.
type PhotoGallery interface {
	ChatPhotos(ctx context.Context, chat int64, anchor MessageID, dir, limit int) (GalleryPage, error)
}

// GIFGallery pages a chat's GIFs, as PhotoGallery its photos and videos: a
// GIF opened goes on to the chat's other GIFs, as in Telegram Desktop, and
// a photo to its photos and videos, not to the GIFs.
type GIFGallery interface {
	ChatGIFs(ctx context.Context, chat int64, anchor MessageID, dir, limit int) (GalleryPage, error)
}

// GalleryPage is a page of a chat's photos, of its GIFs or of the photos of
// its profile.
type GalleryPage struct {
	Messages []Message
	// Total counts the photos of the whole gallery, 0 when it is not known,
	// as for a page read from the offline cache.
	Total int
	// More tells whether there are photos past the page, in its direction.
	More bool
	// Next, for the photos of a profile, is where the page after this one
	// starts; empty after the last one.
	Next string
	// Current, for the photos of a profile, is the key of the one among Messages the
	// profile shows now, where the viewer opens; zero when none of them is.
	Current MessageID
}

// ChatWatcher learns which chats are on screen, so that a store can keep
// up to date the ones Telegram pushes no updates for, such as a channel the
// account has not joined. viewer is whatever shows the chat, such as a
// window's history; chat 0 means it shows none any more.
type ChatWatcher interface {
	WatchChat(viewer any, chat int64)
}

// ConversationStore exposes asynchronous history without blocking a Gio frame.
type ConversationStore interface {
	OpenChat(int64)
	History(int64) History
	LoadOlder(int64)
	LoadNewer(int64)
	Viewport(int64) (Viewport, bool)
	SaveView(Viewport, []MessageLayout)
	Layouts(int64, RenderEnvironment) []MessageLayout
	Media(context.Context, Message) ([]byte, error)
}

// LookupState tells how far finding a single message has gone.
type LookupState uint8

const (
	// LookupLoading: the message is on its way.
	LookupLoading LookupState = iota
	// LookupFound: the message is at hand.
	LookupFound
	// LookupGone: Telegram has no such message, as a deleted one.
	LookupGone
)

// MessageLookup finds single messages that a chat's loaded history does
// not hold, such as the one a reply quotes or a pin names. It never
// blocks: the first ask starts the search, and the store tells of its end
// as of any change.
type MessageLookup interface {
	LookupMessage(chat int64, id MessageID) (Message, LookupState)
}

// PinnedSource knows the pinned messages of chats.
type PinnedSource interface {
	// PinnedMessages returns the ids of chat's pinned messages, oldest
	// first, as far as they are known, and none while the account hid
	// them. The first ask starts loading them; the store tells of changes
	// as of any other.
	PinnedMessages(chat int64) []MessageID
	// HidePinned hides chat's pinned messages until another one is pinned,
	// as Telegram Desktop does for a member who may not unpin them.
	HidePinned(chat int64)
}
