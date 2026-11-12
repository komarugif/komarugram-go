// SPDX-License-Identifier: Unlicense OR MIT

// Package preferences persists settings shared by all messenger windows.
// Account-specific settings deliberately live in a separate namespace in the
// file format so they can be added without changing the meaning of globals.
package preferences

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"gio-mw/exp/powersave"

	"komarugram/internal/messenger/shortcuts"
	"komarugram/pkg/miniapp"
	"komarugram/pkg/player"
)

const version = 1

// Theme is the global color-theme preference.
type Theme int

const (
	ThemeAuto Theme = iota
	ThemeLight
	ThemeDark
)

// ComposerStyle is how the message composer sits in a chat.
type ComposerStyle int

const (
	// ComposerFloating is a rounded capsule floating over the history.
	ComposerFloating ComposerStyle = iota
	// ComposerClassic is a full-width bar below the history.
	ComposerClassic
)

// Global contains preferences which currently apply to every account and
// every window.
type Global struct {
	Shortcuts     shortcuts.Bindings `json:"shortcuts,omitempty"`
	Menus         Menus              `json:"menus,omitempty"`
	Theme         Theme              `json:"theme"`
	Language      string             `json:"language"`
	LastAccountID string             `json:"last_account_id,omitempty"`
	MotionMode    powersave.Mode     `json:"motion_mode"`
	// MotionSet is true once MotionMode was set: by the user, or to off for
	// windows drawn by a software rasterizer (DefaultMotionOff).
	MotionSet      bool            `json:"motion_set,omitempty"`
	LowBattery     int             `json:"low_battery"`
	MiniAppStorage miniapp.Storage `json:"mini_app_storage"`
	// PauseInactive stops stickers, GIFs, animated emoji and video avatars
	// while the window is not active, as Telegram Desktop does.
	PauseInactive bool `json:"pause_inactive"`
	// Graphics is how windows are drawn where there is a choice, on
	// Wayland: the graphics API and the Vulkan device.
	Graphics Graphics `json:"graphics,omitzero"`
	// VisualPrivacy hides phone numbers from the interface and covers the
	// account's identifiers in its profile with spoilers, for showing the
	// screen to others: streams, recordings, screenshots.
	VisualPrivacy bool `json:"visual_privacy,omitempty"`
	// StreamerMode hides the windows from screen capture, as AyuGram's
	// Streamer Mode, where the platform allows it.
	StreamerMode bool `json:"streamer_mode,omitempty"`
	// LocalPremium is what the client does on its own of what Premium gives.
	// It does not change the accounts' actual Telegram subscription or
	// server-side limits.
	LocalPremium LocalPremium `json:"local_premium,omitzero"`
	// HidePeerBadges hides the marks of exteraGram, AyuGram and KomaruGram
	// beside names (package peerbadges), and keeps the client from reading
	// their lists.
	HidePeerBadges bool `json:"hide_peer_badges,omitempty"`
	// KeepPeerArrows keeps the arrows of exteraGram and AyuGram, and
	// KeepPeerFaces the Komaru faces, while HidePeerBadges hides the rest.
	KeepPeerArrows bool `json:"keep_peer_arrows,omitempty"`
	KeepPeerFaces  bool `json:"keep_peer_faces,omitempty"`
	// Calm hides what others do that draws the eye.
	Calm Calm `json:"calm"`
	// PremiumMedia has the WebSocket connections for files go to the address
	// Telegram's web clients take them to for Premium accounts, which no other
	// account is known to need: a lever for those who have Premium to try.
	PremiumMedia bool `json:"premium_media,omitempty"`
	// Window locking only covers the UI; account connections stay running.
	AutoLockMinutes int           `json:"auto_lock_minutes,omitempty"`
	LockOnMinimize  bool          `json:"lock_on_minimize,omitempty"`
	LockOnClose     bool          `json:"lock_on_close,omitempty"`
	Composer        ComposerStyle `json:"composer,omitempty"`
	// SilentVideoAsVideo sends a video without sound as a video: left to itself,
	// Telegram makes a GIF of one, as it does by default here.
	SilentVideoAsVideo bool `json:"silent_video_as_video,omitempty"`
	// ComposerBlur blurs the history behind the floating composer, while
	// animations are on.
	ComposerBlur bool `json:"composer_blur"`
	// Overlays is how the panels drawn over the messenger look: the
	// floating composer, the context menus and the toasts.
	Overlays Overlays `json:"overlays"`
	// WindowTransparency lets the desktop show through the main window
	// surfaces on Wayland and Windows, in percent. Text and media stay opaque.
	WindowTransparency int `json:"window_transparency,omitempty"`
	// WindowBlur asks the compositor, of Wayland or Windows, to blur behind translucent
	// main window surfaces; transparency can also be used on its own.
	WindowBlur bool `json:"window_blur"`
	// Player is the external player videos open in; empty until the user
	// chooses one, which is asked only when more than one is installed.
	Player player.Kind `json:"player,omitempty"`
	// MPVPath and VLCPath are the players the user pointed at, which
	// player.Check accepted; empty for the ones found on the system.
	MPVPath string `json:"mpv_path,omitempty"`
	VLCPath string `json:"vlc_path,omitempty"`
	// BrowserPath is the browser for Mini Apps the user pointed at, which
	// miniapp.CheckBrowser accepted; empty for the one found.
	BrowserPath string `json:"browser_path,omitempty"`
	FFmpegPath  string `json:"ffmpeg_path,omitempty"`
	// StickerPlayer is empty for automatic selection, or ffmpeg/wasm.
	StickerPlayer string `json:"sticker_player,omitempty"`
	// AnimationPlayer plays GIFs and animated avatars, which are MP4: empty
	// for automatic selection, or ffmpeg/wasm.
	AnimationPlayer string `json:"animation_player,omitempty"`
	// AudioPlayer plays voice messages and music: empty or wasm for the
	// client's own, external for mpv or VLC.
	AudioPlayer string `json:"audio_player,omitempty"`
	// VideoPlayer plays videos: empty or internal for the viewer, which
	// plays them in the sandbox or with FFmpeg as AnimationPlayer chooses,
	// external for the external player.
	VideoPlayer string `json:"video_player,omitempty"`
	// VoiceSpeed is the speed voice messages, and music long enough to be
	// a podcast, play at: 0 for their own, as 1 is.
	VoiceSpeed float64 `json:"voice_speed,omitempty"`
	// AudioVolume is how loud voice messages and music play in the client,
	// in percent.
	AudioVolume int `json:"audio_volume"`
	// ArticleZoom is how big the article window draws articles, in
	// percent, from 25 to 400 as Telegram Desktop's: 0 for 100.
	ArticleZoom int `json:"article_zoom,omitempty"`
	// SharedSelection is how a drag selects the files of a chat's info:
	// empty for the range from the file pressed to the one under the
	// pointer, as Telegram Desktop's; SharedSelectionCursor for the files
	// the pointer passes over.
	SharedSelection string `json:"shared_selection,omitempty"`
	// SharedColumns is how many thumbnails a row of a chat's info's grid
	// has, from SharedColumnsMin to SharedColumnsMax: 0 for
	// SharedColumnsDefault.
	SharedColumns int `json:"shared_columns,omitempty"`
	// Ghost is what the accounts tell others of themselves, as AyuGram's
	// Ghost Mode, the same for all of them.
	Ghost Ghost `json:"ghost"`
	// Notify is how new messages are told of, as Telegram Desktop's
	// Notifications and Sounds.
	Notify Notify `json:"notify"`
	// Keep is what the cache keeps that Telegram takes back, as AyuGram's
	// saved deleted messages and edits history.
	Keep Keep `json:"keep"`
	// Filters hide messages, as AyuGram's message filters.
	Filters Filters `json:"filters"`
	// Look is how messages and avatars are drawn, as AyuGram's
	// customization.
	Look Look `json:"look"`
	// Fonts are the font files the user pointed at.
	Fonts Fonts `json:"fonts"`
	// ConfirmSticker and ConfirmGIF ask before a sticker or a GIF chosen
	// in the composer is sent, as AyuGram's confirmations.
	ConfirmSticker bool `json:"confirm_sticker,omitempty"`
	ConfirmGIF     bool `json:"confirm_gif,omitempty"`
	// ManualFocus keeps the keyboard from the message field until it is
	// clicked, against messages sent by accident; by default the field
	// holds it whenever nothing else wants it, as Telegram Desktop's.
	ManualFocus bool `json:"manual_focus,omitempty"`
	// PickerOnClick opens the composer's picker of emoji, stickers and GIFs
	// only by a click on its button; by default the pointer over the button
	// opens it, as Telegram Desktop's, whose option is "Show tabbed panel
	// by click".
	PickerOnClick bool `json:"picker_on_click,omitempty"`
	// Chats is how every chat is drawn, unless the chat has a theme of its
	// own, as Telegram Desktop's chat settings.
	Chats ChatLook `json:"chats"`
}

// ChatLook is how every chat is drawn: one look for the light theme and
// one for the dark, as Telegram Desktop keeps a theme for the day and one
// for the night.
type ChatLook struct {
	Day   ChatMode `json:"day"`
	Night ChatMode `json:"night"`
}

// ChatMode is the look of the chats in one of the application's themes.
type ChatMode struct {
	// Theme is one of ChatPresets: "" for the application's own colors.
	Theme string `json:"theme,omitempty"`
	// Accent is the accent chosen, 0xffRRGGBB, so that black is one; zero
	// keeps the theme's.
	Accent uint32 `json:"accent,omitempty"`
	// Wallpaper is the wallpaper chosen; nil for the theme's.
	Wallpaper *Wallpaper `json:"wallpaper,omitempty"`
}

// ChatPresets are the themes of chats, as chattheme's presets.
var ChatPresets = []string{"", "classic", "day", "tinted", "night"}

// Wallpaper is Telegram's wallPaperSettings with the picture, if any, kept
// by SaveWallpaper.
type Wallpaper struct {
	// ID is Telegram's, to tell it among the ones offered.
	ID int64 `json:"id,omitempty"`
	// File is the picture's name in the store's wallpapers; empty for a
	// wallpaper of colors alone.
	File      string   `json:"file,omitempty"`
	Colors    []uint32 `json:"colors,omitempty"`
	Rotation  int      `json:"rotation,omitempty"`
	Intensity int      `json:"intensity,omitempty"`
	Blur      bool     `json:"blur,omitempty"`
	Pattern   bool     `json:"pattern,omitempty"`
	Tile      bool     `json:"tile,omitempty"`
	Dark      bool     `json:"dark,omitempty"`
}

// Overlays are the preferences of the panels the messenger draws over its
// content. An overlay that blurs what is behind it lets some of it show
// through, as much as Transparency says; one that does not is opaque. The
// composer's own switch is ComposerBlur. Blur costs power, so it is off
// while animations are.
type Overlays struct {
	// Transparency is how much of the blurred content shows through the
	// overlays that blur it, in percent, from 0 to TransparencyMax.
	Transparency int `json:"transparency"`
	// MenusBlur blurs behind the context menus, and ToastsBlur behind the
	// toasts.
	MenusBlur  bool `json:"menus_blur"`
	ToastsBlur bool `json:"toasts_blur"`
	// Shadow is how dark the shadows under dialogs and menus are, in
	// percent of the full ones, from 0, none, to ShadowMax.
	Shadow int `json:"shadow"`
}

// TransparencyMax is the most transparent an overlay can be made: past it
// the text on the overlay could not be read.
const TransparencyMax = 70

// ShadowDefault is the intensity of shadows until the user changes it, and
// ShadowMax the strongest one, in percent of the full shadow.
const (
	ShadowDefault = 75
	ShadowMax     = 200
)

// Opacity is how opaque the overlays that blur are, from 0.3 to 1.
func (o Overlays) Opacity() float32 { return 1 - float32(o.Transparency)/100 }

// Look is how messages and avatars are drawn.
type Look struct {
	// BubbleRadius rounds the corners of bubbles, in dp, up to 16.
	BubbleRadius int `json:"bubble_radius"`
	// AvatarCorners rounds avatars, from 0, square, to AvatarRound, a
	// circle.
	AvatarCorners int `json:"avatar_corners"`
	// Seconds shows the seconds of a message's time.
	Seconds bool `json:"seconds,omitempty"`
	// EditedMark and DeletedMark take the place of the marks of edited and
	// deleted messages; empty for the default ones.
	EditedMark  string `json:"edited_mark,omitempty"`
	DeletedMark string `json:"deleted_mark,omitempty"`
}

// Fonts are the paths of font files the user pointed at, by what they
// draw; "" for the system's fonts. The files stay where they are: one that
// is gone, or does not load, is passed over.
type Fonts struct {
	// Text draws the text, before every other font.
	Text string `json:"text,omitempty"`
	// Extra draws what Text has no glyph for, before the system's fonts:
	// a font of another script, such as a CJK one.
	Extra string `json:"extra,omitempty"`
	// Mono draws preformatted text.
	Mono string `json:"mono,omitempty"`
	// Emoji draws emoji.
	Emoji string `json:"emoji,omitempty"`
	// EmojiPack is the installed emoji pack that draws emoji, before the
	// font of Emoji: a font of the catalog, or sprites. "" for none.
	EmojiPack string `json:"emoji_pack,omitempty"`
	// EmojiOffered is set once the offer of an emoji pack, made where no
	// font of the system has emoji, was answered.
	EmojiOffered bool `json:"emoji_offered,omitempty"`
}

// The bounds of Look, as AyuGram's.
const (
	BubbleRadiusMax = 16
	AvatarRound     = 23
)

// Filters hide others' messages that match a pattern, or that blocked
// users sent; all are off by default, as in AyuGram.
type Filters struct {
	Enabled bool `json:"enabled,omitempty"`
	// InChats applies the filters in groups and private chats too; without
	// it they apply in channels only.
	InChats bool `json:"in_chats,omitempty"`
	// HideBlocked hides what blocked users sent, in every chat.
	HideBlocked bool            `json:"hide_blocked,omitempty"`
	Patterns    []FilterPattern `json:"patterns,omitempty"`
}

// FilterPattern is a regular expression, in Go's syntax, that hides the
// messages it matches, or, Reversed, the ones it does not; in Chat alone,
// or in every chat for 0.
type FilterPattern struct {
	Text            string `json:"text"`
	Reversed        bool   `json:"reversed,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
	Chat            int64  `json:"chat,omitempty"`
}

// Keep is what the cache keeps: see model.Keep. Both are on by default,
// as in AyuGram.
type Keep struct {
	Deleted bool `json:"deleted"`
	Edits   bool `json:"edits"`
}

// Notify is how new messages are told of. All is on by default, as in
// Telegram Desktop; the fields are saved even when false. The kinds of
// chat are this client's own: Telegram Desktop keeps them in the cloud.
type Notify struct {
	Desktop bool `json:"desktop"`
	Sound   bool `json:"sound"`
	// Name and Text are what a notification shows of the message.
	Name bool `json:"name"`
	Text bool `json:"text"`
	// Private (users and bots), Groups and Channels are the chats told of.
	Private  bool `json:"private"`
	Groups   bool `json:"groups"`
	Channels bool `json:"channels"`
	// AllAccounts tells of every account's messages, not only of the one
	// used last.
	AllAccounts bool `json:"all_accounts"`
}

// Ghost is what the accounts tell others: see model.Ghost. Everything is
// told by default, as Telegram does; Ghost Mode, On, hides what the Keep
// switches leave, all of it at first, as AyuGram's does. ReadOnInteract
// still reads a chat on sending to it or reacting in it while reads are
// hidden; it is saved even when false, or a choice would come back as the
// default.
type Ghost struct {
	On bool `json:"on"`
	// KeepRead keeps telling that messages are read; KeepOnline, that the
	// account is online; KeepTyping, that it types, chooses a place or a
	// contact; KeepUploads, that it sends a file, a photo, a video or a
	// voice message; KeepRecording, that it records one; KeepStickers,
	// that it chooses a sticker; KeepStories, the stories it saw.
	KeepRead      bool `json:"keep_read,omitempty"`
	KeepOnline    bool `json:"keep_online,omitempty"`
	KeepTyping    bool `json:"keep_typing,omitempty"`
	KeepUploads   bool `json:"keep_uploads,omitempty"`
	KeepRecording bool `json:"keep_recording,omitempty"`
	KeepStickers  bool `json:"keep_stickers,omitempty"`
	KeepStories   bool `json:"keep_stories,omitempty"`
	// ReadOnInteract marks a chat read on sending to it or reacting in
	// it, while reads are hidden.
	ReadOnInteract bool `json:"read_on_interact"`
}

// Told is what the accounts tell others: see Ghost's Keep switches.
type Told struct {
	Read, Online, Typing, Uploads, Recording, Stickers, Stories bool
}

// Tells reports what the accounts tell with g.
func (g Ghost) Tells() Told {
	tells := func(keep bool) bool { return !g.On || keep }
	return Told{tells(g.KeepRead), tells(g.KeepOnline), tells(g.KeepTyping), tells(g.KeepUploads), tells(g.KeepRecording), tells(g.KeepStickers), tells(g.KeepStories)}
}

// UnmarshalJSON reads g, and the settings saved before Ghost Mode was one
// switch: a switch of what to tell, turned off, is Ghost Mode on, hiding
// it; typing then stood for every action the account told.
func (g *Ghost) UnmarshalJSON(b []byte) error {
	type plain Ghost
	var old struct {
		plain
		SendRead   *bool `json:"send_read"`
		SendOnline *bool `json:"send_online"`
		SendTyping *bool `json:"send_typing"`
	}
	old.plain = plain(*g)
	if err := json.Unmarshal(b, &old); err != nil {
		return err
	}
	*g = Ghost(old.plain)
	if old.SendRead == nil && old.SendOnline == nil && old.SendTyping == nil {
		return nil
	}
	told := func(p *bool) bool { return p == nil || *p }
	read, online, typing := told(old.SendRead), told(old.SendOnline), told(old.SendTyping)
	if read && online && typing {
		// Nothing was hidden: turned on, Ghost Mode hides everything.
		return nil
	}
	g.On = true
	g.KeepRead, g.KeepOnline = read, online
	g.KeepTyping, g.KeepUploads, g.KeepRecording, g.KeepStickers = typing, typing, typing, typing
	return nil
}

// Equal reports whether g and o are the same preferences, as saved.
// LocalPremium is model.LocalPremium as it is kept.
type LocalPremium struct {
	Star  bool `json:"star,omitempty"`
	Media bool `json:"media,omitempty"`
	GIFs  bool `json:"gifs,omitempty"`
	Pins  bool `json:"pins,omitempty"`
	Sync  bool `json:"sync,omitempty"`
}

// UnmarshalJSON reads also the one switch this was. On, it meant all but the
// channel, which asks first when switched on.
func (l *LocalPremium) UnmarshalJSON(b []byte) error {
	var old bool
	if json.Unmarshal(b, &old) == nil {
		*l = LocalPremium{Star: old, Media: old, GIFs: old, Pins: old}
		return nil
	}
	type plain LocalPremium
	if err := json.Unmarshal(b, (*plain)(l)); err != nil {
		return err
	}
	// Written before it needed either: the box could not be unticked now.
	l.Sync = l.Sync && (l.GIFs || l.Pins)
	return nil
}

// Account is what belongs to one Telegram account. What is nil follows Global.
type Account struct {
	LocalPremium *LocalPremium `json:"local_premium,omitempty"`
	PremiumMedia *bool         `json:"premium_media,omitempty"`
	// Visibility, when set, is the Visibility section of this account alone:
	// see PinVisibility.
	Visibility *Visibility `json:"visibility,omitempty"`
	// StorageChannel is where Local Premium keeps its lists: model.StorageBinding.
	StorageChannel *StorageChannel `json:"storage_channel,omitempty"`
}

// StorageChannel is model.StorageBinding as it is kept.
type StorageChannel struct {
	ChannelID int64  `json:"channel_id"`
	Hash      int64  `json:"hash"`
	Title     string `json:"title,omitempty"`
}

// Visibility is the settings of the Visibility section, shared by the accounts
// in Global until one pins its own.
type Visibility struct {
	Calm           Calm    `json:"calm"`
	HidePeerBadges bool    `json:"hide_peer_badges,omitempty"`
	KeepPeerArrows bool    `json:"keep_peer_arrows,omitempty"`
	KeepPeerFaces  bool    `json:"keep_peer_faces,omitempty"`
	Ghost          Ghost   `json:"ghost"`
	Filters        Filters `json:"filters"`
	Keep           Keep    `json:"keep"`
}

// HidesPeerArrows reports whether the arrows of exteraGram and AyuGram are hidden.
func (v Visibility) HidesPeerArrows() bool { return v.HidePeerBadges && !v.KeepPeerArrows }

// HidesPeerFaces reports whether the Komaru faces are hidden.
func (v Visibility) HidesPeerFaces() bool { return v.HidePeerBadges && !v.KeepPeerFaces }

func (g Global) visibility() Visibility {
	return Visibility{Calm: g.Calm, HidePeerBadges: g.HidePeerBadges, KeepPeerArrows: g.KeepPeerArrows, KeepPeerFaces: g.KeepPeerFaces, Ghost: g.Ghost, Filters: g.Filters, Keep: g.Keep}
}

func (v Visibility) applyTo(g *Global) {
	g.Calm, g.HidePeerBadges, g.KeepPeerArrows, g.KeepPeerFaces = v.Calm, v.HidePeerBadges, v.KeepPeerArrows, v.KeepPeerFaces
	g.Ghost, g.Filters, g.Keep = v.Ghost, v.Filters, v.Keep
}

// accountLocked is what id has of its own.
func (s *Store) accountLocked(id string) Account {
	if a, ok := s.parsed[id]; ok {
		return a
	}
	var a Account
	if raw, ok := s.accounts[id]; ok {
		// One that cannot be read has nothing of its own; it stays in the file.
		_ = json.Unmarshal(raw, &a)
	}
	if s.parsed == nil {
		s.parsed = map[string]Account{}
	}
	s.parsed[id] = a
	return a
}

// changeAccount changes what id has of its own, and leaves the rest of its entry.
func (s *Store) changeAccount(id string, update func(*Account)) error {
	s.mu.Lock()
	a := s.accountLocked(id)
	update(&a)
	fields := map[string]json.RawMessage{}
	old, had := s.accounts[id]
	if had {
		_ = json.Unmarshal(old, &fields)
	}
	set := func(key string, present bool, value any) error {
		delete(fields, key)
		if !present {
			return nil
		}
		b, err := json.Marshal(value)
		fields[key] = b
		return err
	}
	err := errors.Join(set("local_premium", a.LocalPremium != nil, a.LocalPremium),
		set("premium_media", a.PremiumMedia != nil, a.PremiumMedia),
		set("visibility", a.Visibility != nil, a.Visibility),
		set("storage_channel", a.StorageChannel != nil, a.StorageChannel))
	var entry []byte
	if err == nil {
		entry, err = json.Marshal(fields)
	}
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if had && string(old) == string(entry) {
		s.mu.Unlock()
		return nil
	}
	s.accounts[id] = entry
	if err := s.writeLocked(s.global); err != nil {
		if had {
			s.accounts[id] = old
		} else {
			delete(s.accounts, id)
		}
		s.mu.Unlock()
		return err
	}
	s.parsed[id] = a
	callbacks := make([]func(), 0, len(s.subscribers))
	for _, callback := range s.subscribers {
		callbacks = append(callbacks, callback)
	}
	s.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
	return nil
}

// LocalPremiumOf is the Local Premium of account id. One that never changed it
// has what the one switch for all of them said, but for the channel: that is its
// own, and asks first when ticked.
func (s *Store) LocalPremiumOf(id string) LocalPremium {
	s.mu.Lock()
	defer s.mu.Unlock()
	if parts := s.accountLocked(id).LocalPremium; parts != nil {
		return *parts
	}
	parts := s.global.LocalPremium
	if id != "" {
		parts.Sync = false
	}
	return parts
}

// SetLocalPremiumOf changes it; a window of no account yet changes what is shared.
func (s *Store) SetLocalPremiumOf(id string, parts LocalPremium) error {
	if id == "" {
		return s.SetLocalPremium(parts)
	}
	return s.changeAccount(id, func(a *Account) { a.LocalPremium = &parts })
}

// PremiumMediaOf reports whether files over WebSocket of account id go to the
// address of Premium accounts.
func (s *Store) PremiumMediaOf(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on := s.accountLocked(id).PremiumMedia; on != nil {
		return *on
	}
	return s.global.PremiumMedia
}

// SetPremiumMediaOf changes it.
func (s *Store) SetPremiumMediaOf(id string, on bool) error {
	if id == "" {
		return s.SetPremiumMedia(on)
	}
	return s.changeAccount(id, func(a *Account) { a.PremiumMedia = &on })
}

// StorageChannelOf is the channel account id keeps its lists in.
func (s *Store) StorageChannelOf(id string) StorageChannel {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.accountLocked(id).StorageChannel; c != nil {
		return *c
	}
	return StorageChannel{}
}

// SetStorageChannelOf changes it; zero forgets it.
func (s *Store) SetStorageChannelOf(id string, c StorageChannel) error {
	return s.changeAccount(id, func(a *Account) {
		a.StorageChannel = nil
		if c != (StorageChannel{}) {
			a.StorageChannel = &c
		}
	})
}

// VisibilityOf is the Visibility section as account id has it.
func (s *Store) VisibilityOf(id string) Visibility {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.accountLocked(id).Visibility; v != nil {
		return *v
	}
	return s.global.visibility()
}

// VisibilityPinned reports whether account id has the section to itself.
func (s *Store) VisibilityPinned(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accountLocked(id).Visibility != nil
}

// PinVisibility gives account id the section to itself, starting from what is
// shared now, or takes it back to what is shared, forgetting its own.
func (s *Store) PinVisibility(id string, on bool) error {
	if id == "" {
		return nil
	}
	shared := s.Global().visibility()
	return s.changeAccount(id, func(a *Account) {
		switch {
		case !on:
			a.Visibility = nil
		case a.Visibility == nil:
			shared.Filters.Patterns = slices.Clone(shared.Filters.Patterns)
			a.Visibility = &shared
		}
	})
}

// ChangeVisibility changes the section as account id has it: its own when pinned,
// and otherwise what every account without its own shares.
func (s *Store) ChangeVisibility(id string, update func(*Visibility)) error {
	s.mu.Lock()
	pinned := s.accountLocked(id).Visibility
	next := s.global
	s.mu.Unlock()
	if pinned == nil {
		return s.change(func(g *Global) {
			v := g.visibility()
			update(&v)
			v.applyTo(g)
		})
	}
	v := *pinned
	update(&v)
	v.applyTo(&next)
	if err := validate(next); err != nil {
		return err
	}
	return s.changeAccount(id, func(a *Account) { a.Visibility = &v })
}

// PeerMarksWanted reports whether any of the accounts ids shows the arrows and
// the faces, for which their lists are read; with none yet, what is shared says.
func (s *Store) PeerMarksWanted(ids []string) (arrows, faces bool) {
	if len(ids) == 0 {
		ids = []string{""}
	}
	for _, id := range ids {
		v := s.VisibilityOf(id)
		arrows = arrows || !v.HidesPeerArrows()
		faces = faces || !v.HidesPeerFaces()
	}
	return arrows, faces
}

func (g Global) Equal(o Global) bool {
	a, errA := json.Marshal(g)
	b, errB := json.Marshal(o)
	return errA == nil && errB == nil && string(a) == string(b)
}

// PlayerPaths are the players the user pointed at, by kind.
func (g Global) PlayerPaths() map[player.Kind]string {
	return map[player.Kind]string{player.MPV: g.MPVPath, player.VLC: g.VLCPath}
}

type fileData struct {
	Version int    `json:"version"`
	Global  Global `json:"global"`
	// Accounts are the preferences of one Telegram account, by its id: see
	// Account. What this version does not know of them stays as it is.
	Accounts map[string]json.RawMessage `json:"accounts"`
}

// Store is a process-wide, persistent and observable preference store.
type Store struct {
	mu       sync.Mutex
	path     string
	global   Global
	accounts map[string]json.RawMessage
	// parsed is accounts as Account, asked in frames: read once, and made
	// again when one changes.
	parsed      map[string]Account
	subscribers map[uint64]func()
	nextID      uint64
	// wallpapers are the pictures SaveWallpaper keeps in a store in memory.
	wallpapers map[string][]byte
}

func defaults() Global {
	return Global{
		Theme:          ThemeAuto,
		Language:       "ru",
		MotionMode:     powersave.ModeAuto,
		LowBattery:     powersave.DefaultLowBattery,
		MiniAppStorage: miniapp.Shared,
		PauseInactive:  true,
		ComposerBlur:   true,
		AudioVolume:    100,
		WindowBlur:     true,
		Overlays:       Overlays{Transparency: 30, MenusBlur: true, ToastsBlur: true, Shadow: ShadowDefault},
		Ghost:          Ghost{ReadOnInteract: true},
		Keep:           Keep{Deleted: true, Edits: true},
		Notify:         Notify{Desktop: true, Sound: true, Name: true, Text: true, Private: true, Groups: true, Channels: true, AllAccounts: true},
		Look:           Look{BubbleRadius: BubbleRadiusMax, AvatarCorners: AvatarRound},
	}
}

// Open loads the process-wide settings from the application config directory.
func Open() (*Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return OpenPath(filepath.Join(dir, "komarugram-go", "settings.json"))
}

// OpenPath loads settings from path. It is also useful to isolate tests.
func OpenPath(path string) (*Store, error) {
	s := &Store{path: path, global: defaults(), accounts: make(map[string]json.RawMessage), subscribers: make(map[uint64]func())}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	data := fileData{Global: defaults()}
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	if data.Version != version {
		return nil, fmt.Errorf("unsupported settings version %d", data.Version)
	}
	if err := validate(data.Global); err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	s.global = data.Global
	if data.Accounts != nil {
		s.accounts = data.Accounts
	}
	return s, nil
}

// Memory returns a non-persistent store, primarily for demos and tests.
func Memory() *Store {
	return &Store{global: defaults(), accounts: make(map[string]json.RawMessage), subscribers: make(map[uint64]func())}
}

var ErrSettingsAccess = errors.New("keep a menu entry or shortcut for settings")

func validate(g Global) error {
	if err := shortcuts.ValidateBindings(g.Shortcuts); err != nil {
		return err
	}
	command, _ := shortcuts.Find("app.settings")
	if len(g.Shortcuts.For(command)) == 0 && slices.Contains(g.Menus["sidebar"].Hidden, "settings") && slices.Contains(g.Menus["drawer"].Hidden, "settings") {
		return ErrSettingsAccess
	}
	if g.Theme < ThemeAuto || g.Theme > ThemeDark {
		return errors.New("invalid theme")
	}
	if g.Language != "ru" && g.Language != "en" {
		return errors.New("invalid language")
	}
	if g.MotionMode < powersave.ModeAuto || g.MotionMode > powersave.ModeOff {
		return errors.New("invalid animation mode")
	}
	if g.Graphics.API != GraphicsAuto && g.Graphics.API != GraphicsOpenGL && g.Graphics.API != GraphicsVulkan {
		return errors.New("invalid graphics API")
	}
	if g.LowBattery < 0 || g.LowBattery > 100 {
		return errors.New("invalid low-battery threshold")
	}
	if g.MiniAppStorage < miniapp.Ephemeral || g.MiniAppStorage > miniapp.Shared {
		return errors.New("invalid Mini App storage")
	}
	if g.Composer < ComposerFloating || g.Composer > ComposerClassic {
		return errors.New("invalid composer style")
	}
	if g.Look.BubbleRadius < 0 || g.Look.BubbleRadius > BubbleRadiusMax || g.Look.AvatarCorners < 0 || g.Look.AvatarCorners > AvatarRound {
		return errors.New("invalid look")
	}
	if g.StickerPlayer != "" && g.StickerPlayer != "ffmpeg" && g.StickerPlayer != "wasm" {
		return errors.New("invalid sticker player")
	}
	if g.AnimationPlayer != "" && g.AnimationPlayer != "ffmpeg" && g.AnimationPlayer != "wasm" {
		return errors.New("invalid animation player")
	}
	if g.AudioVolume < 0 || g.AudioVolume > 100 {
		return errors.New("invalid audio volume")
	}
	if g.ArticleZoom != 0 && (g.ArticleZoom < 25 || g.ArticleZoom > 400) {
		return errors.New("invalid article zoom")
	}
	if g.VoiceSpeed != 0 && (g.VoiceSpeed < 0.5 || g.VoiceSpeed > 3) {
		return errors.New("invalid voice speed")
	}
	if g.SharedSelection != "" && g.SharedSelection != SharedSelectionCursor {
		return errors.New("invalid shared selection")
	}
	if g.SharedColumns != 0 && (g.SharedColumns < SharedColumnsMin || g.SharedColumns > SharedColumnsMax) {
		return errors.New("invalid shared columns")
	}
	if g.AudioPlayer != "" && g.AudioPlayer != "wasm" && g.AudioPlayer != "external" {
		return errors.New("invalid audio player")
	}
	if g.VideoPlayer != "" && g.VideoPlayer != "internal" && g.VideoPlayer != "external" {
		return errors.New("invalid video player")
	}
	if g.Player != "" && g.Player != player.MPV && g.Player != player.VLC && g.Player != player.Chromium {
		return errors.New("invalid external player")
	}
	if g.Overlays.Transparency < 0 || g.Overlays.Transparency > TransparencyMax {
		return errors.New("invalid overlay transparency")
	}
	if g.Overlays.Shadow < 0 || g.Overlays.Shadow > ShadowMax {
		return errors.New("invalid overlay shadow")
	}
	if g.WindowTransparency < 0 || g.WindowTransparency > TransparencyMax {
		return errors.New("invalid window transparency")
	}
	if g.AutoLockMinutes < 0 || g.AutoLockMinutes > 120 {
		return errors.New("invalid automatic lock delay")
	}
	for _, m := range []ChatMode{g.Chats.Day, g.Chats.Night} {
		if !slices.Contains(ChatPresets, m.Theme) {
			return errors.New("invalid chat theme")
		}
		if w := m.Wallpaper; w != nil {
			if w.File != "" && !validWallpaperName(w.File) {
				return errors.New("invalid wallpaper file")
			}
			if len(w.Colors) > 4 || w.Intensity < -100 || w.Intensity > 100 || w.Rotation < 0 || w.Rotation >= 360 {
				return errors.New("invalid wallpaper")
			}
		}
	}
	return nil
}

// Global returns a consistent snapshot.
func (s *Store) Global() Global {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.global
	g.Menus = g.Menus.Clone()
	g.Shortcuts = g.Shortcuts.Clone()
	return g
}

func (s *Store) SetFFmpegPath(path string) error {
	return s.change(func(g *Global) { g.FFmpegPath = path })
}

func (s *Store) SetStickerPlayer(value string) error {
	return s.change(func(g *Global) { g.StickerPlayer = value })
}

func (s *Store) SetAnimationPlayer(value string) error {
	return s.change(func(g *Global) { g.AnimationPlayer = value })
}

func (s *Store) SetAudioPlayer(value string) error {
	return s.change(func(g *Global) { g.AudioPlayer = value })
}

func (s *Store) SetVideoPlayer(value string) error {
	return s.change(func(g *Global) { g.VideoPlayer = value })
}

func (s *Store) SetAudioVolume(percent int) error {
	return s.change(func(g *Global) { g.AudioVolume = percent })
}

// SetArticleZoom sets how big the article window draws articles, in
// percent; 100 is kept as 0.
func (s *Store) SetArticleZoom(percent int) error {
	if percent == 100 {
		percent = 0
	}
	return s.change(func(g *Global) { g.ArticleZoom = percent })
}

func (s *Store) SetVoiceSpeed(value float64) error {
	return s.change(func(g *Global) { g.VoiceSpeed = value })
}

// SharedSelectionCursor is the SharedSelection of the files the pointer
// passes over.
const SharedSelectionCursor = "cursor"

// SetSharedSelection chooses how a drag selects the files of a chat's
// info: empty or SharedSelectionCursor.
func (s *Store) SetSharedSelection(value string) error {
	return s.change(func(g *Global) { g.SharedSelection = value })
}

// The thumbnails a row of a chat's info's grid may have, and has unless
// chosen.
const (
	SharedColumnsMin     = 3
	SharedColumnsMax     = 6
	SharedColumnsDefault = 4
)

// SharedGridColumns is how many thumbnails a row of a chat's info's grid has.
func (g Global) SharedGridColumns() int {
	if g.SharedColumns == 0 {
		return SharedColumnsDefault
	}
	return g.SharedColumns
}

// SetSharedColumns chooses how many thumbnails a row of a chat's info's
// grid has; the default is kept as 0.
func (s *Store) SetSharedColumns(n int) error {
	if n == SharedColumnsDefault {
		n = 0
	}
	return s.change(func(g *Global) { g.SharedColumns = n })
}

func (s *Store) SetTheme(value Theme) error {
	return s.change(func(g *Global) { g.Theme = value })
}

// SetLanguage changes the UI language for all windows.
func (s *Store) SetLanguage(value string) error {
	return s.change(func(g *Global) { g.Language = value })
}

// SetLastAccount records which account should be restored on the next start.
// It is updated when an account window gains focus or closes.
func (s *Store) SetLastAccount(id string) error {
	return s.change(func(g *Global) { g.LastAccountID = id })
}

func (s *Store) SetMotion(mode powersave.Mode, lowBattery int) error {
	return s.change(func(g *Global) {
		g.MotionMode = mode
		g.MotionSet = true
		g.LowBattery = lowBattery
	})
}

// DefaultMotionOff turns animations off unless their mode was set before:
// for windows drawn by a software rasterizer (llvmpipe, WARP), where nothing
// animates smoothly and every frame of an animation costs the CPU.
func (s *Store) DefaultMotionOff() error {
	return s.change(func(g *Global) {
		if !g.MotionSet {
			g.MotionMode = powersave.ModeOff
			g.MotionSet = true
		}
	})
}

// DefaultVideoExternal makes videos open in the external player unless
// their player was chosen: for windows drawn by a software rasterizer, as
// in a virtual machine, where the client's own player has no GPU to draw
// with and its pictures cost the CPU the decoders need.
func (s *Store) DefaultVideoExternal() error {
	return s.change(func(g *Global) {
		if g.VideoPlayer == "" {
			g.VideoPlayer = "external"
		}
	})
}

// SetPauseInactive switches whether media animations stop while the window
// is not active.
func (s *Store) SetPauseInactive(on bool) error {
	return s.change(func(g *Global) { g.PauseInactive = on })
}

// The graphics APIs of Graphics.API.
const (
	GraphicsAuto   = ""
	GraphicsOpenGL = "opengl"
	GraphicsVulkan = "vulkan"
)

// Graphics is how windows are drawn where there is a choice (Gio's
// app.Graphics).
type Graphics struct {
	// API is GraphicsAuto, GraphicsOpenGL or GraphicsVulkan.
	API string `json:"api,omitempty"`
	// Vulkan is the device drawn with by Vulkan; the zero value is the
	// first that can draw, of any driver.
	Vulkan VulkanDevice `json:"vulkan,omitzero"`
}

// VulkanDevice is a Vulkan device, by the manifest of its driver and its
// ids. Name is kept to show it before the devices are listed.
type VulkanDevice struct {
	Driver   string `json:"driver,omitempty"`
	VendorID uint32 `json:"vendor_id,omitempty"`
	DeviceID uint32 `json:"device_id,omitempty"`
	Name     string `json:"name,omitempty"`
}

// SetGraphics sets how windows are drawn.
func (s *Store) SetGraphics(g Graphics) error {
	return s.change(func(global *Global) { global.Graphics = g })
}

// SetVisualPrivacy switches visual privacy mode for every window.
func (s *Store) SetVisualPrivacy(on bool) error {
	return s.change(func(g *Global) { g.VisualPrivacy = on })
}

// SetLocalPremium changes what the client does on its own of what Premium gives.
func (s *Store) SetLocalPremium(parts LocalPremium) error {
	return s.change(func(g *Global) { g.LocalPremium = parts })
}

// SetHidePeerBadges hides the marks of exteraGram, AyuGram and KomaruGram
// beside names, or shows them.
func (s *Store) SetHidePeerBadges(on bool) error {
	return s.change(func(g *Global) { g.HidePeerBadges = on })
}

// SetKeepPeerBadges keeps the arrows of exteraGram and AyuGram, and the
// Komaru faces, while the marks are hidden, or hides them too.
func (s *Store) SetKeepPeerBadges(arrows, faces bool) error {
	return s.change(func(g *Global) { g.KeepPeerArrows, g.KeepPeerFaces = arrows, faces })
}

// HidesPeerArrows reports whether the arrows of exteraGram and AyuGram are
// hidden, and their list not read.
func (g Global) HidesPeerArrows() bool { return g.HidePeerBadges && !g.KeepPeerArrows }

// HidesPeerFaces reports whether the Komaru faces are hidden, and their
// lists not read.
func (g Global) HidesPeerFaces() bool { return g.HidePeerBadges && !g.KeepPeerFaces }

// Calm is the hiding of what others do that draws the eye: who is online
// and when they were last, and whether messages sent were delivered and
// read. The clock of a message not delivered yet always shows.
type Calm struct {
	// On hides what the Keep switches leave.
	On bool `json:"on,omitempty"`
	// KeepOnline keeps showing who is online; KeepRead, the checks of
	// messages delivered and read.
	KeepOnline bool `json:"keep_online,omitempty"`
	KeepRead   bool `json:"keep_read,omitempty"`
	// HidePaid takes out of the panel the paid stickers and custom emoji that
	// cannot be sent. Unlike what it keeps above, off by default: those who had
	// this on before it came keep their panel.
	HidePaid bool `json:"hide_paid,omitempty"`
}

// HidesOnline reports whether who is online is hidden.
func (c Calm) HidesOnline() bool { return c.On && !c.KeepOnline }

// HidesRead reports whether the checks of messages are hidden.
func (c Calm) HidesRead() bool { return c.On && !c.KeepRead }

// HidesPaid reports whether paid stickers and emoji that cannot be sent are out
// of the panel.
func (c Calm) HidesPaid() bool { return c.On && c.HidePaid }

// SetCalm changes what of others is hidden.
func (s *Store) SetCalm(c Calm) error {
	return s.change(func(g *Global) { g.Calm = c })
}

// SetPremiumMedia switches the Premium address for files over WebSocket.
func (s *Store) SetPremiumMedia(on bool) error {
	return s.change(func(g *Global) { g.PremiumMedia = on })
}

// SetStreamerMode hides the windows from screen capture, or shows them.
func (s *Store) SetStreamerMode(on bool) error {
	return s.change(func(g *Global) { g.StreamerMode = on })
}

func (s *Store) SetWindowLock(minutes int, minimize, close bool) error {
	return s.change(func(g *Global) {
		g.AutoLockMinutes = minutes
		g.LockOnMinimize = minimize
		g.LockOnClose = close
	})
}

// SetGhost changes what the accounts tell others of themselves.
func (s *Store) SetNotify(n Notify) error {
	return s.change(func(global *Global) { global.Notify = n })
}

func (s *Store) SetGhost(g Ghost) error {
	return s.change(func(global *Global) { global.Ghost = g })
}

// SetKeep changes what the cache keeps that Telegram takes back.
func (s *Store) SetKeep(k Keep) error {
	return s.change(func(global *Global) { global.Keep = k })
}

// SetFilters changes the message filters.
func (s *Store) SetFilters(f Filters) error {
	f.Patterns = slices.Clone(f.Patterns)
	return s.change(func(global *Global) { global.Filters = f })
}

// SetLook changes how messages and avatars are drawn.
func (s *Store) SetLook(l Look) error {
	return s.change(func(global *Global) { global.Look = l })
}

// SetChats changes how every chat is drawn, and removes the wallpapers'
// pictures it no longer uses.
func (s *Store) SetChats(c ChatLook) error {
	for _, w := range []**Wallpaper{&c.Day.Wallpaper, &c.Night.Wallpaper} {
		if *w != nil {
			copy := **w
			copy.Colors = slices.Clone(copy.Colors)
			*w = &copy
		}
	}
	if err := s.change(func(g *Global) { g.Chats = c }); err != nil {
		return err
	}
	return s.pruneWallpapers(c)
}

// DataDir is a directory of the given name beside the settings, for what
// is kept with them; empty for a store in memory.
func (s *Store) DataDir(name string) string {
	if s.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.path), name)
}

// wallpaperDir is where the wallpapers' pictures are, beside the settings;
// empty for a store in memory.
func (s *Store) wallpaperDir() string {
	if s.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.path), "wallpapers")
}

func validWallpaperName(name string) bool {
	if len(name) != 64 {
		return false
	}
	for _, c := range name {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// SaveWallpaper keeps a wallpaper's picture for every window and account,
// and returns its name for Wallpaper.File. The name is the picture's hash,
// so the same picture is kept once.
func (s *Store) SaveWallpaper(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.wallpaperDir()
	if dir == "" {
		if s.wallpapers == nil {
			s.wallpapers = map[string][]byte{}
		}
		s.wallpapers[name] = slices.Clone(data)
		return name, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return name, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", err
	}
	return name, os.Rename(tmp, path)
}

// LoadWallpaper returns a picture SaveWallpaper kept.
func (s *Store) LoadWallpaper(name string) ([]byte, error) {
	if !validWallpaperName(name) {
		return nil, errors.New("invalid wallpaper file")
	}
	s.mu.Lock()
	dir := s.wallpaperDir()
	data, ok := s.wallpapers[name]
	s.mu.Unlock()
	if dir == "" {
		if !ok {
			return nil, os.ErrNotExist
		}
		return data, nil
	}
	return os.ReadFile(filepath.Join(dir, name))
}

// pruneWallpapers removes the pictures c does not use.
func (s *Store) pruneWallpapers(c ChatLook) error {
	used := map[string]bool{}
	for _, m := range []ChatMode{c.Day, c.Night} {
		if m.Wallpaper != nil && m.Wallpaper.File != "" {
			used[m.Wallpaper.File] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.wallpaperDir()
	if dir == "" {
		for name := range s.wallpapers {
			if !used[name] {
				delete(s.wallpapers, name)
			}
		}
		return nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if validWallpaperName(e.Name()) && !used[e.Name()] {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetFonts chooses the font files.
func (s *Store) SetFonts(f Fonts) error {
	return s.change(func(g *Global) { g.Fonts = f })
}

// SetComposerInput chooses whether the message field takes the keyboard
// only when clicked, and the picker opens only by a click.
func (s *Store) SetComposerInput(manualFocus, pickerOnClick bool) error {
	return s.change(func(g *Global) { g.ManualFocus, g.PickerOnClick = manualFocus, pickerOnClick })
}

// SetConfirmations chooses whether stickers and GIFs are sent only once
// confirmed.
func (s *Store) SetConfirmations(sticker, gif bool) error {
	return s.change(func(g *Global) { g.ConfirmSticker, g.ConfirmGIF = sticker, gif })
}

// SetComposer changes the message composer style for all windows.
func (s *Store) SetComposer(value ComposerStyle) error {
	return s.change(func(g *Global) { g.Composer = value })
}

// SetSilentVideoAsVideo chooses how a video without sound goes: as a video, or
// as Telegram takes it, a GIF.
func (s *Store) SetSilentVideoAsVideo(on bool) error {
	return s.change(func(g *Global) { g.SilentVideoAsVideo = on })
}

// SetComposerBlur switches the blur behind the floating composer.
func (s *Store) SetComposerBlur(on bool) error {
	return s.change(func(g *Global) { g.ComposerBlur = on })
}

// SetOverlays changes how the overlays look for all windows.
func (s *Store) SetOverlays(o Overlays) error {
	if o.Transparency < 0 || o.Transparency > TransparencyMax {
		return errors.New("invalid overlay transparency")
	}
	if o.Shadow < 0 || o.Shadow > ShadowMax {
		return errors.New("invalid overlay shadow")
	}
	return s.change(func(g *Global) { g.Overlays = o })
}

// SetWindowBlur changes the compositor blur independently of transparency.
func (s *Store) SetWindowBlur(on bool) error {
	return s.change(func(g *Global) { g.WindowBlur = on })
}

// SetWindowTransparency changes the window surfaces independently
// of the overlays. Zero keeps the surfaces opaque.
func (s *Store) SetWindowTransparency(value int) error {
	if value < 0 || value > TransparencyMax {
		return errors.New("invalid window transparency")
	}
	return s.change(func(g *Global) { g.WindowTransparency = value })
}

// SetPlayer chooses the external player for videos.
func (s *Store) SetPlayer(kind player.Kind) error {
	return s.change(func(g *Global) { g.Player = kind })
}

// SetPlayerPath points the player kind at path; "" goes back to the one
// found on the system. The caller checks the path with player.Check.
func (s *Store) SetPlayerPath(kind player.Kind, path string) error {
	return s.change(func(g *Global) {
		switch kind {
		case player.MPV:
			g.MPVPath = path
		case player.VLC:
			g.VLCPath = path
		}
	})
}

// SetBrowserPath points Mini Apps at the browser at path; "" goes back to the
// one found. The caller checks the path with miniapp.CheckBrowser.
func (s *Store) SetBrowserPath(path string) error {
	return s.change(func(g *Global) { g.BrowserPath = path })
}

func (s *Store) SetMiniAppStorage(value miniapp.Storage) error {
	return s.change(func(g *Global) { g.MiniAppStorage = value })
}

func (s *Store) change(update func(*Global)) error {
	s.mu.Lock()
	next := s.global
	update(&next)
	if next.Equal(s.global) {
		s.mu.Unlock()
		return nil
	}
	if err := validate(next); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := s.writeLocked(next); err != nil {
		s.mu.Unlock()
		return err
	}
	s.global = next
	callbacks := make([]func(), 0, len(s.subscribers))
	for _, callback := range s.subscribers {
		callbacks = append(callbacks, callback)
	}
	s.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
	return nil
}

func (s *Store) writeLocked(global Global) error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(fileData{
		Version:  version,
		Global:   global,
		Accounts: s.accounts,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Subscribe registers a callback invoked after a successful change. The
// returned function removes it.
func (s *Store) Subscribe(callback func()) func() {
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.subscribers[id] = callback
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.subscribers, id)
		s.mu.Unlock()
	}
}

func (s *Store) SetShortcuts(b shortcuts.Bindings) error {
	return s.change(func(g *Global) { g.Shortcuts = b.Clone() })
}
