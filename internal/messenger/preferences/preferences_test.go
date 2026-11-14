package preferences

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gio-mw/exp/powersave"

	"komarugram/pkg/miniapp"
	"komarugram/pkg/player"
)

// A video without sound goes as a GIF until chosen otherwise, which stays.
func TestSilentVideoChoice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Global().SilentVideoAsVideo {
		t.Error("a video without sound goes as a video by default")
	}
	if err := s.SetSilentVideoAsVideo(true); err != nil {
		t.Fatal(err)
	}
	again, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Global().SilentVideoAsVideo {
		t.Error("the choice was not kept")
	}
}

func TestPersistsAndNotifies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	unsubscribe := s.Subscribe(func() { called++ })
	if err := s.SetTheme(ThemeDark); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMotion(powersave.ModeOff, 25); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMiniAppStorage(miniapp.PerApp); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLanguage("en"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLastAccount("account-b"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetComposer(ComposerClassic); err != nil {
		t.Fatal(err)
	}
	if err := s.SetComposerBlur(false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPauseInactive(false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPlayer(player.VLC); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPlayerPath(player.VLC, "/opt/vlc/vlc"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBrowserPath("/opt/chromium/chrome"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPlayer("totem"); err == nil {
		t.Fatal("an unknown player was accepted")
	}
	unsubscribe()
	if err := s.SetTheme(ThemeLight); err != nil {
		t.Fatal(err)
	}
	if called != 11 {
		t.Fatalf("notifications = %d, want 11", called)
	}

	loaded, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Global{WindowBlur: true, ViewerBlur: true, Theme: ThemeLight, Language: "en", LastAccountID: "account-b", MotionMode: powersave.ModeOff, MotionSet: true, LowBattery: 25, MiniAppStorage: miniapp.PerApp, Composer: ComposerClassic, AudioVolume: 100, Player: player.VLC, VLCPath: "/opt/vlc/vlc", BrowserPath: "/opt/chromium/chrome", Ghost: Ghost{ReadOnInteract: true}, Overlays: Overlays{Transparency: 30, MenusBlur: true, ToastsBlur: true, Shadow: ShadowDefault}, Keep: Keep{Deleted: true, Edits: true}, Notify: Notify{Desktop: true, Sound: true, Name: true, Text: true, Private: true, Groups: true, Channels: true, AllAccounts: true}, Look: Look{BubbleRadius: BubbleRadiusMax, AvatarCorners: AvatarRound}}
	if got := loaded.Global(); !got.Equal(want) {
		t.Fatalf("loaded %+v, want %+v", got, want)
	}
}

func TestWindowLockOptionsPersistIndependently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetWindowLock(17, true, false); err != nil {
		t.Fatal(err)
	}
	loaded, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	g := loaded.Global()
	if g.AutoLockMinutes != 17 || !g.LockOnMinimize || g.LockOnClose {
		t.Fatalf("settings: %+v", g)
	}
	if err := loaded.SetWindowLock(0, true, true); err != nil {
		t.Fatal(err)
	}
	g = loaded.Global()
	if g.AutoLockMinutes != 0 || !g.LockOnMinimize || !g.LockOnClose {
		t.Fatalf("independent triggers: %+v", g)
	}
	if err := loaded.SetWindowLock(121, false, false); err == nil {
		t.Fatal("invalid timeout accepted")
	}
}

func TestGlobalChangePreservesAccountNamespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := `{"version":1,"global":{"theme":0,"language":"ru","motion_mode":0,"low_battery":20,"mini_app_storage":2},"accounts":{"a":{"compact":true}}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTheme(ThemeDark); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved fileData
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	var accountSettings map[string]bool
	if err := json.Unmarshal(saved.Accounts["a"], &accountSettings); err != nil {
		t.Fatal(err)
	}
	if !accountSettings["compact"] || len(accountSettings) != 1 {
		t.Fatalf("account settings were changed: %s", saved.Accounts["a"])
	}
}

// Local Premium is kept between runs, and switched off again.
func TestLocalPremiumPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Global().LocalPremium != (LocalPremium{}) {
		t.Fatal("Local Premium is on by default")
	}
	parts := LocalPremium{Star: true, Pins: true, Sync: true}
	if err := s.SetLocalPremium(parts); err != nil {
		t.Fatal(err)
	}
	loaded, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Global().LocalPremium != parts {
		t.Fatalf("%+v", loaded.Global().LocalPremium)
	}
}

// The one switch it was means all but the channel, which asks first.
func TestLocalPremiumReadsTheOldSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	data := []byte(`{"version":` + strconv.Itoa(version) + `,"global":{"local_premium":true}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Global().LocalPremium; got != (LocalPremium{Star: true, Media: true, GIFs: true, Pins: true}) {
		t.Fatalf("%+v", got)
	}
}

func TestGhostDefaultsToTelling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if told := s.Global().Ghost.Tells(); told != (Told{true, true, true, true, true, true, true}) {
		t.Fatalf("by default %+v, want everything told", s.Global().Ghost)
	}
	if err := s.SetGhost(Ghost{On: true, KeepTyping: true}); err != nil {
		t.Fatal(err)
	}
	loaded, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if g := loaded.Global().Ghost; g != (Ghost{On: true, KeepTyping: true}) {
		t.Fatalf("Ghost Mode came back as %+v", g)
	}
}

// Settings saved when Ghost Mode was switches of what to tell keep what
// they hid.
func TestGhostFromSwitchesOfTelling(t *testing.T) {
	for _, c := range []struct {
		saved string
		want  Ghost
	}{
		{`{"send_read":true,"send_online":true,"send_typing":true,"read_on_interact":true}`, Ghost{ReadOnInteract: true}},
		{`{"send_read":false,"send_online":true,"send_typing":false,"read_on_interact":false}`, Ghost{On: true, KeepOnline: true}},
		{`{"on":true,"keep_online":true,"read_on_interact":true}`, Ghost{On: true, KeepOnline: true, ReadOnInteract: true}},
	} {
		var g Ghost
		if err := json.Unmarshal([]byte(c.saved), &g); err != nil {
			t.Fatal(err)
		}
		if g != c.want {
			t.Errorf("%s: %+v, want %+v", c.saved, g, c.want)
		}
		if told := g.Tells(); told.Read != (c.want.KeepRead || !c.want.On) || told.Stories != !c.want.On {
			t.Errorf("%s: told %+v", c.saved, told)
		}
	}
}

// Settings saved before notifications existed tell of everything; a switch
// turned off stays off.
func TestNotifyDefaultsOnAndKeepsChoices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"global":{"language":"en"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	all := Notify{Desktop: true, Sound: true, Name: true, Text: true, Private: true, Groups: true, Channels: true, AllAccounts: true}
	if n := s.Global().Notify; n != all {
		t.Fatalf("old settings: %+v", n)
	}
	off := all
	off.Sound, off.Channels = false, false
	if err := s.SetNotify(off); err != nil {
		t.Fatal(err)
	}
	loaded, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := loaded.Global().Notify; n != off {
		t.Fatalf("came back as %+v", n)
	}
}

// The overlays are transparent by 30% and blur by default, as the composer
// always did; a choice, unchecked boxes included, survives a restart, and
// one out of bounds is refused and never loaded.
func TestOverlays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	o := s.Global().Overlays
	if o.Transparency != 30 || !o.MenusBlur || !o.ToastsBlur || o.Opacity() != 0.7 || o.Shadow != ShadowDefault {
		t.Fatalf("by default %+v", o)
	}
	if err := s.SetOverlays(Overlays{Transparency: TransparencyMax + 1}); err == nil {
		t.Fatal("a transparency past the bound was accepted")
	}
	if err := s.SetOverlays(Overlays{Shadow: ShadowMax + 1}); err == nil {
		t.Fatal("a shadow past the bound was accepted")
	}
	if err := s.SetOverlays(Overlays{Transparency: 55}); err != nil {
		t.Fatal(err)
	}
	loaded, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Global().Overlays; got != (Overlays{Transparency: 55}) {
		t.Fatalf("came back as %+v", got)
	}
	// A file from before the overlays has the defaults.
	old := `{"version":1,"global":{"theme":0,"language":"ru","motion_mode":0,"low_battery":20,"mini_app_storage":2},"accounts":{}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Global().Overlays; got != (Overlays{Transparency: 30, MenusBlur: true, ToastsBlur: true, Shadow: ShadowDefault}) {
		t.Fatalf("an old file has %+v", got)
	}
	// Overlays saved before the shadow's setting keep the default shadows, and
	// shadows turned off stay off.
	for body, want := range map[string]int{`"transparency":40`: ShadowDefault, `"transparency":40,"shadow":0`: 0, `"shadow":150`: 150} {
		file := `{"version":1,"global":{"theme":0,"language":"ru","motion_mode":0,"low_battery":20,"mini_app_storage":2,"overlays":{` + body + `}},"accounts":{}}`
		if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err = OpenPath(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := loaded.Global().Overlays.Shadow; got != want {
			t.Errorf("overlays %s: shadow %d, want %d", body, got, want)
		}
	}
	bad := `{"version":1,"global":{"theme":0,"language":"ru","motion_mode":0,"low_battery":20,"mini_app_storage":2,"overlays":{"transparency":99}},"accounts":{}}`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPath(path); err == nil {
		t.Fatal("a file with a transparency past the bound was loaded")
	}
}

func TestWindowTransparency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Global().WindowTransparency != 0 {
		t.Fatal("new windows should be opaque by default")
	}
	overlays := s.Global().Overlays
	for _, value := range []int{40, TransparencyMax, 0} {
		if err := s.SetWindowTransparency(value); err != nil {
			t.Fatal(err)
		}
		loaded, err := OpenPath(path)
		if err != nil {
			t.Fatal(err)
		}
		if g := loaded.Global(); g.WindowTransparency != value || g.Overlays != overlays {
			t.Fatalf("transparency %d: loaded window %d, overlays %+v", value, g.WindowTransparency, g.Overlays)
		}
	}
	for _, value := range []int{-1, TransparencyMax + 1} {
		if err := s.SetWindowTransparency(value); err == nil {
			t.Fatalf("accepted %d", value)
		}
		if s.Global().WindowTransparency != 0 {
			t.Fatal("invalid value changed preferences")
		}
		g := defaults()
		g.WindowTransparency = value
		if err := validate(g); err == nil {
			t.Fatalf("would load invalid value %d", value)
		}
	}
}

func TestWindowBlurIndependent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Global().WindowBlur {
		t.Fatal("compositor blur should be enabled by default")
	}
	if err := s.SetWindowTransparency(40); err != nil {
		t.Fatal(err)
	}
	for _, on := range []bool{false, true, false} {
		if err := s.SetWindowBlur(on); err != nil {
			t.Fatal(err)
		}
		loaded, err := OpenPath(path)
		if err != nil {
			t.Fatal(err)
		}
		g := loaded.Global()
		if g.WindowBlur != on || g.WindowTransparency != 40 || g.Overlays != defaults().Overlays {
			t.Fatalf("blur %t: window blur %t, transparency %d, overlays %+v", on, g.WindowBlur, g.WindowTransparency, g.Overlays)
		}
	}
}

func TestChatWallpapersArePrunedWhenUnused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.SaveWallpaper([]byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SaveWallpaper([]byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	look := ChatLook{Day: ChatMode{Theme: "classic", Wallpaper: &Wallpaper{File: first, Blur: true}}, Night: ChatMode{Theme: "night", Wallpaper: &Wallpaper{File: second}}}
	if err := s.SetChats(look); err != nil {
		t.Fatal(err)
	}
	look.Night.Wallpaper = nil
	if err := s.SetChats(look); err != nil {
		t.Fatal(err)
	}
	if data, err := s.LoadWallpaper(first); err != nil || string(data) != "first" {
		t.Fatal(string(data), err)
	}
	if _, err := s.LoadWallpaper(second); !os.IsNotExist(err) {
		t.Fatal("unused wallpaper kept", err)
	}
	reopened, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if w := reopened.Global().Chats.Day.Wallpaper; w == nil || w.File != first || !w.Blur {
		t.Fatal(reopened.Global().Chats)
	}
	for _, bad := range []ChatLook{{Day: ChatMode{Theme: "sepia"}}, {Night: ChatMode{Wallpaper: &Wallpaper{File: "../settings.json"}}}} {
		if err := s.SetChats(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if _, err := s.LoadWallpaper("../settings.json"); err == nil {
		t.Fatal("read outside the wallpapers")
	}
}

func TestFontsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Global().Fonts != (Fonts{}) {
		t.Fatalf("fonts by default: %+v", s.Global().Fonts)
	}
	want := Fonts{Text: "/fonts/Text.ttf", Emoji: "/fonts/Emoji.ttf"}
	if err := s.SetFonts(want); err != nil {
		t.Fatal(err)
	}
	again, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Global().Fonts; got != want {
		t.Errorf("fonts read back: %+v, want %+v", got, want)
	}
}

func TestDefaultMotionOffKeepsModeSet(t *testing.T) {
	s := Memory()
	if err := s.DefaultMotionOff(); err != nil {
		t.Fatal(err)
	}
	if g := s.Global(); g.MotionMode != powersave.ModeOff || !g.MotionSet {
		t.Fatalf("software default: mode %v, set %v; want off, set", g.MotionMode, g.MotionSet)
	}
	if err := s.SetMotion(powersave.ModeAuto, powersave.DefaultLowBattery); err != nil {
		t.Fatal(err)
	}
	if err := s.DefaultMotionOff(); err != nil {
		t.Fatal(err)
	}
	if g := s.Global(); g.MotionMode != powersave.ModeAuto {
		t.Fatalf("mode chosen by the user: %v after DefaultMotionOff, want auto", g.MotionMode)
	}
}

// Drawn by a software rasterizer, videos open in the external player,
// unless the user chose their player.
func TestDefaultVideoExternalKeepsChoice(t *testing.T) {
	s := Memory()
	if err := s.DefaultVideoExternal(); err != nil {
		t.Fatal(err)
	}
	if got := s.Global().VideoPlayer; got != "external" {
		t.Fatalf("software default: %q, want external", got)
	}
	if err := s.SetVideoPlayer("internal"); err != nil {
		t.Fatal(err)
	}
	if err := s.DefaultVideoExternal(); err != nil {
		t.Fatal(err)
	}
	if got := s.Global().VideoPlayer; got != "internal" {
		t.Fatalf("player chosen by the user: %q after DefaultVideoExternal, want internal", got)
	}
}

func TestPremiumMediaIsKeptAndOffByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Global().PremiumMedia {
		t.Fatal("the Premium address is on before it is chosen")
	}
	if err := s.SetPremiumMedia(true); err != nil {
		t.Fatal(err)
	}
	again, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Global().PremiumMedia {
		t.Error("the Premium address was not kept")
	}
}

// Kept on with nothing to share, the box could not be unticked.
func TestLocalPremiumReadsSyncAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	data := []byte(`{"version":` + strconv.Itoa(version) + `,"global":{"local_premium":{"star":true,"sync":true}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Global().LocalPremium; got != (LocalPremium{Star: true}) {
		t.Fatalf("%+v", got)
	}
}

// Local Premium is each account's: one's change leaves the other, and it outlives
// a restart. Until it changes, an account follows what the one switch said, but
// for the channel, which asks first.
func TestLocalPremiumIsEachAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	old := LocalPremium{Star: true, GIFs: true, Sync: true}
	if err := s.SetLocalPremium(old); err != nil {
		t.Fatal(err)
	}
	if got := s.LocalPremiumOf("a"); got != (LocalPremium{Star: true, GIFs: true}) {
		t.Fatalf("follows: %+v", got)
	}
	mine := LocalPremium{Pins: true, Sync: true}
	if err := s.SetLocalPremiumOf("a", mine); err != nil {
		t.Fatal(err)
	}
	if got := s.LocalPremiumOf("b"); got != (LocalPremium{Star: true, GIFs: true}) {
		t.Fatalf("the other changed: %+v", got)
	}
	if err := s.SetPremiumMediaOf("a", true); err != nil {
		t.Fatal(err)
	}
	if s.PremiumMediaOf("b") {
		t.Fatal("one account's lever moved the other")
	}
	again, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.LocalPremiumOf("a") != mine || !again.PremiumMediaOf("a") {
		t.Fatalf("after a restart: %+v", again.LocalPremiumOf("a"))
	}
}

// What the account's entry has beyond what this version knows stays.
func TestAccountKeepsWhatItDoesNotKnow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	file := `{"version":1,"global":{"theme":0,"language":"ru","motion_mode":0,"low_battery":20,"mini_app_storage":2},"accounts":{"a":{"compact":true}}}`
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPremiumMediaOf("a", true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "compact") {
		t.Fatalf("lost: %s", b)
	}
}

// The section is shared until an account pins it, which starts from what is shared;
// its changes then stay with it. Unpinned, it follows again.
func TestPinVisibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	hide := func(id string, on bool) {
		t.Helper()
		if err := s.ChangeVisibility(id, func(v *Visibility) { v.HidePeerBadges = on }); err != nil {
			t.Fatal(err)
		}
	}
	hide("a", true)
	if !s.VisibilityOf("b").HidePeerBadges || !s.Global().HidePeerBadges {
		t.Fatal("not shared before pinning")
	}
	if err := s.PinVisibility("a", true); err != nil {
		t.Fatal(err)
	}
	if !s.VisibilityPinned("a") || s.VisibilityPinned("b") || !s.VisibilityOf("a").HidePeerBadges {
		t.Fatal("pinning did not inherit")
	}
	hide("a", false)
	if s.VisibilityOf("a").HidePeerBadges || !s.VisibilityOf("b").HidePeerBadges || !s.Global().HidePeerBadges {
		t.Fatal("a pinned account's change spread")
	}
	hide("b", false)
	hide("b", true)
	if s.VisibilityOf("a").HidePeerBadges {
		t.Fatal("what is shared reached a pinned account")
	}
	// Pinning again changes nothing it has.
	if err := s.PinVisibility("a", true); err != nil {
		t.Fatal(err)
	}
	if s.VisibilityOf("a").HidePeerBadges {
		t.Fatal("pinned again, it took what is shared")
	}
	reopened, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.VisibilityPinned("a") || reopened.VisibilityOf("a").HidePeerBadges {
		t.Fatal("not kept over a restart")
	}
	if err := reopened.PinVisibility("a", false); err != nil {
		t.Fatal(err)
	}
	if reopened.VisibilityPinned("a") || !reopened.VisibilityOf("a").HidePeerBadges {
		t.Fatal("unpinned, it does not follow what is shared")
	}
	// Without an account there is nothing to pin it to.
	if err := reopened.PinVisibility("", true); err != nil || reopened.VisibilityPinned("") {
		t.Fatal("pinned to no account")
	}
}

// The lists are read while any account shows the marks.
func TestPeerMarksWanted(t *testing.T) {
	s := Memory()
	if err := s.ChangeVisibility("", func(v *Visibility) { v.HidePeerBadges = true }); err != nil {
		t.Fatal(err)
	}
	if a, f := s.PeerMarksWanted(nil); a || f {
		t.Fatal("wanted, all hidden")
	}
	if err := s.PinVisibility("a", true); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeVisibility("a", func(v *Visibility) { v.KeepPeerFaces = true }); err != nil {
		t.Fatal(err)
	}
	if a, f := s.PeerMarksWanted([]string{"a", "b"}); a || !f {
		t.Fatalf("arrows %v, faces %v", a, f)
	}
	if a, f := s.PeerMarksWanted([]string{"b"}); a || f {
		t.Fatal("wanted for an account that hides them")
	}
	// Whichever account comes last.
	if a, f := s.PeerMarksWanted([]string{"b", "a"}); a || !f {
		t.Fatalf("arrows %v, faces %v", a, f)
	}
	if err := s.ChangeVisibility("a", func(v *Visibility) { v.KeepPeerFaces, v.KeepPeerArrows = false, true }); err != nil {
		t.Fatal(err)
	}
	if a, f := s.PeerMarksWanted([]string{"a", "b"}); !a || f {
		t.Fatalf("arrows %v, faces %v", a, f)
	}
}

// The channel is kept with the settings, for the account alone, and goes when zero.
func TestStorageChannelIsEachAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	ch := StorageChannel{ChannelID: 5, Hash: -7, Title: "x"}
	if err := s.SetStorageChannelOf("a", ch); err != nil {
		t.Fatal(err)
	}
	again, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.StorageChannelOf("a") != ch || again.StorageChannelOf("b") != (StorageChannel{}) {
		t.Fatal("not kept for the account alone")
	}
	if err := again.SetStorageChannelOf("a", StorageChannel{}); err != nil {
		t.Fatal(err)
	}
	if again.StorageChannelOf("a") != (StorageChannel{}) {
		t.Fatal("not forgotten")
	}
}

// The viewer's blur is on for settings saved before it was there, and stays
// off once the user takes it off.
func TestViewerBlurIsOnUntilTakenOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version": 1, "global": {"window_blur": false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Global().ViewerBlur || s.Global().WindowBlur {
		t.Fatalf("old settings: viewer blur %t, window blur %t", s.Global().ViewerBlur, s.Global().WindowBlur)
	}
	if err := s.SetViewerBlur(false); err != nil {
		t.Fatal(err)
	}
	s, err = OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Global().ViewerBlur {
		t.Error("the viewer's blur taken off came back")
	}
}
