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
	Theme          Theme           `json:"theme"`
	Language       string          `json:"language"`
	LastAccountID  string          `json:"last_account_id,omitempty"`
	MotionMode     powersave.Mode  `json:"motion_mode"`
	LowBattery     int             `json:"low_battery"`
	MiniAppStorage miniapp.Storage `json:"mini_app_storage"`
	// VisualPrivacy hides phone numbers from the interface and covers the
	// account's identifiers in its profile with spoilers, for showing the
	// screen to others: streams, recordings, screenshots.
	VisualPrivacy bool `json:"visual_privacy,omitempty"`
	// StreamerMode hides the windows from screen capture, as AyuGram's
	// Streamer Mode, where the platform allows it.
	StreamerMode bool `json:"streamer_mode,omitempty"`
	// LocalPremium makes the accounts signed in here look Premium to
	// themselves, as AyuGram's Local Premium: the star beside their names,
	// and the Premium section of the settings. Telegram does not know of it,
	// so nothing else changes.
	LocalPremium bool `json:"local_premium,omitempty"`
	// Window locking only covers the UI; account connections stay running.
	AutoLockMinutes int           `json:"auto_lock_minutes,omitempty"`
	LockOnMinimize  bool          `json:"lock_on_minimize,omitempty"`
	LockOnClose     bool          `json:"lock_on_close,omitempty"`
	Composer        ComposerStyle `json:"composer,omitempty"`
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
	// VoiceSpeed is the speed voice messages, and music long enough to be
	// a podcast, play at: 0 for their own, as 1 is.
	VoiceSpeed float64 `json:"voice_speed,omitempty"`
	// AudioVolume is how loud voice messages and music play in the client,
	// in percent.
	AudioVolume int `json:"audio_volume"`
	// ArticleZoom is how big the article window draws articles, in
	// percent, from 25 to 400 as Telegram Desktop's: 0 for 100.
	ArticleZoom int `json:"article_zoom,omitempty"`
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
}

// TransparencyMax is the most transparent an overlay can be made: past it
// the text on the overlay could not be read.
const TransparencyMax = 70

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
// told by default, as Telegram does, and Ghost Mode is choosing not to:
// the fields are saved even when false, or a choice would come back as the
// default. A chat is also read on sending to it or reacting in it.
type Ghost struct {
	SendRead       bool `json:"send_read"`
	SendOnline     bool `json:"send_online"`
	SendTyping     bool `json:"send_typing"`
	ReadOnInteract bool `json:"read_on_interact"`
}

// Equal reports whether g and o are the same preferences, as saved.
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
	// Accounts reserves a stable namespace for preferences which will belong
	// to one Telegram account. There are no such preferences yet.
	Accounts map[string]json.RawMessage `json:"accounts"`
}

// Store is a process-wide, persistent and observable preference store.
type Store struct {
	mu          sync.Mutex
	path        string
	global      Global
	accounts    map[string]json.RawMessage
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
		ComposerBlur:   true,
		AudioVolume:    100,
		WindowBlur:     true,
		Overlays:       Overlays{Transparency: 30, MenusBlur: true, ToastsBlur: true},
		Ghost:          Ghost{SendRead: true, SendOnline: true, SendTyping: true, ReadOnInteract: true},
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

func validate(g Global) error {
	if g.Theme < ThemeAuto || g.Theme > ThemeDark {
		return errors.New("invalid theme")
	}
	if g.Language != "ru" && g.Language != "en" {
		return errors.New("invalid language")
	}
	if g.MotionMode < powersave.ModeAuto || g.MotionMode > powersave.ModeOff {
		return errors.New("invalid animation mode")
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
	if g.AudioPlayer != "" && g.AudioPlayer != "wasm" && g.AudioPlayer != "external" {
		return errors.New("invalid audio player")
	}
	if g.Player != "" && g.Player != player.MPV && g.Player != player.VLC && g.Player != player.Chromium {
		return errors.New("invalid external player")
	}
	if g.Overlays.Transparency < 0 || g.Overlays.Transparency > TransparencyMax {
		return errors.New("invalid overlay transparency")
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
	return s.global
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
		g.LowBattery = lowBattery
	})
}

// SetVisualPrivacy switches visual privacy mode for every window.
func (s *Store) SetVisualPrivacy(on bool) error {
	return s.change(func(g *Global) { g.VisualPrivacy = on })
}

// SetLocalPremium makes the accounts look Premium to themselves, or not.
func (s *Store) SetLocalPremium(on bool) error {
	return s.change(func(g *Global) { g.LocalPremium = on })
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

// SetConfirmations chooses whether stickers and GIFs are sent only once
// confirmed.
func (s *Store) SetConfirmations(sticker, gif bool) error {
	return s.change(func(g *Global) { g.ConfirmSticker, g.ConfirmGIF = sticker, gif })
}

// SetComposer changes the message composer style for all windows.
func (s *Store) SetComposer(value ComposerStyle) error {
	return s.change(func(g *Global) { g.Composer = value })
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
