// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"gio-mw/exp"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/exp/appearance"
	"gio-mw/wdk"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/emojipacks"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
	"komarugram/pkg/player"
)

type staticAccounts []model.AccountInfo

func (a staticAccounts) All() []model.AccountInfo { return a }
func (staticAccounts) Open(string)                {}
func (staticAccounts) Add()                       {}
func (staticAccounts) LogOut(string)              {}
func (staticAccounts) Subscribe(func()) func()    { return func() {} }

// TestRenderSettingsAccounts draws the main settings page with a list of
// saved accounts and saves a screenshot, for looking at it. SETTINGS_SECTION=appearance,
// chats, notify, privacy or integrations draws that section instead; wallpapers,
// the chats' section under the gallery of wallpapers:
//
//	SETTINGS_PNG=/tmp/settings.png go test ./internal/messenger/ui -run RenderSettingsAccounts
func TestRenderSettingsAccounts(t *testing.T) {
	out := os.Getenv("SETTINGS_PNG")
	if out == "" {
		t.Skip("set SETTINGS_PNG to a file name")
	}
	photo := image.NewRGBA(image.Rect(0, 0, 160, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 160; x++ {
			photo.Set(x, y, color.RGBA{uint8(x), 90, uint8(y), 255})
		}
	}
	accounts := staticAccounts{
		{ID: "1", UserID: 1, Name: "Ада Лавлейс", Username: "ada", Avatar: photo},
		{ID: "2", UserID: 2, Name: "Чарльз Бэббидж", Phone: "+44 20 0000 0000", Open: true},
		{ID: "3", UserID: 3, Name: "Аккаунт 3"},
	}
	p := newSettingsPage(motion.New(func() {}), miniappprefs.New(miniapp.Ephemeral), nil, func() {}, accounts,
		func() string { return "1" }, themeAuto, "ru", func(themeMode) {}, func(string) {})
	var images imageOps
	p.images = &images
	p.composerStyle = func() preferences.ComposerStyle { return preferences.ComposerFloating }
	p.composerBlur = func() bool { return true }
	overlays := preferences.Overlays{Transparency: 30, MenusBlur: true}
	windowBlur := true
	p.windowBlur = func() bool { return windowBlur }
	p.setWindowBlur = func(on bool) { windowBlur = on }
	windowTransparency := 40
	p.windowTransparency = func() int { return windowTransparency }
	p.setWindowTransparency = func(value int) { windowTransparency = value }
	p.windowTransparencyAvailable = func() bool { return true }
	p.overlays = func() preferences.Overlays { return overlays }
	p.setOverlays = func(o preferences.Overlays) { overlays = o }
	p.uninstall = func() {}
	size := image.Pt(900, 1300)
	if os.Getenv("SETTINGS_SECTION") == "appearance" {
		p.section = settingsAppearance
		// One font is picked and gone since, the others are the system's.
		files := preferences.Fonts{Extra: filepath.Join(t.TempDir(), "NotoSansKR-Regular.ttf")}
		p.fontsView.files = func() preferences.Fonts { return files }
		p.fontsView.setFiles = func(f preferences.Fonts) { files = f }
		// The packs of a catalog: one installed and in use, one to download.
		catalog := emojiPackCatalog(t)
		p.emojiView.files, p.emojiView.setFiles = p.fontsView.files, p.fontsView.setFiles
		p.emojiView.store = emojipacks.Open(filepath.Join(t.TempDir(), "emoji"))
		p.emojiView.source = emojipacks.NewSource(catalog)
		if packs, err := emojipacks.ReadIndex(context.Background(), p.emojiView.source); err == nil {
			p.emojiView.catalog, p.emojiView.asked = packs, true
			for _, pack := range packs {
				if pack.ID == "sprites" {
					if err := p.emojiView.store.Install(context.Background(), p.emojiView.source, pack, nil); err != nil {
						t.Fatal(err)
					}
					files.EmojiPack = pack.ID
				}
			}
		}
		size.Y = 2700
	}
	var chats *preferences.Store
	if section := os.Getenv("SETTINGS_SECTION"); section == "chats" || section == "wallpapers" {
		t.Chdir("../../..")
		p.section = settingsChats
		chats = preferences.Memory()
		if err := chats.SetChats(preferences.ChatLook{Day: preferences.ChatMode{Theme: "day", Accent: 0xffd46c99}}); err != nil {
			t.Fatal(err)
		}
		store := mockstore.New(time.Now(), 0)
		c := p.chats
		c.chats = func() preferences.ChatLook { return chats.Global().Chats }
		c.setChats = chats.SetChats
		c.dark = func() bool { return false }
		c.store = chats
		c.wallpapers = store
		c.media = store
		c.images = &images
		c.thumbs = newWallpaperThumbs(store, func() {})
		defer c.thumbs.Close()
		p.confirmations = func() (bool, bool) { return true, false }
		p.setConfirmations = func(bool, bool) {}
		look := preferences.Look{BubbleRadius: 8, AvatarCorners: 10, Seconds: true}
		p.lookView.look = func() preferences.Look { return look }
		p.lookView.setLook = func(l preferences.Look) { look = l }
		size.Y = 1900
		if section == "wallpapers" {
			size.Y = 900
			c.gallery.open()
		}
		// The thumbnails are rendered in the background.
		ops := new(op.Ops)
		for range 60 {
			ops.Reset()
			gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}, Values: map[string]any{}}
			wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
			p.Update(gtx, themeAuto, "ru")
			p.Layout(gtx, themeAuto, appearance.Light, false, localization.For("ru"))
			c.layoutDialog(gtx, localization.For("ru"))
			time.Sleep(50 * time.Millisecond)
		}
	}
	// Sound and channels off, to show both states of a switch.
	notifyPrefs := preferences.Notify{Desktop: true, Name: true, Text: true, Private: true, Groups: true, AllAccounts: true}
	p.notifyView.get = func() preferences.Notify { return notifyPrefs }
	p.notifyView.set = func(n preferences.Notify) { notifyPrefs = n }
	if os.Getenv("SETTINGS_SECTION") == "notify" {
		p.section = settingsNotify
		p.shownAccounts = p.accounts.All()
		size.Y = 1100
	}
	if os.Getenv("SETTINGS_SECTION") == "privacy" {
		p.section = settingsPrivacy
		size.Y = 2300
		ghost := preferences.Ghost{ReadOnInteract: true}
		p.ghost = func() preferences.Ghost { return ghost }
		p.setGhost = func(g preferences.Ghost) { ghost = g }
		keep := preferences.Keep{Deleted: true, Edits: true}
		p.keep = func() preferences.Keep { return keep }
		p.setKeep = func(k preferences.Keep) { keep = k }
		filters := preferences.Filters{Enabled: true, Patterns: []preferences.FilterPattern{{Text: "реклама|промокод", CaseInsensitive: true}, {Text: "^#", Reversed: true, Chat: 5}}}
		p.filtersView.filters = func() preferences.Filters { return filters }
		p.filtersView.setFilters = func(f preferences.Filters) { filters = f }
		p.filtersView.remove = make([]surface, 2)
	}
	if os.Getenv("SETTINGS_SECTION") == "premium" {
		// An account without Premium that has Local Premium on.
		p.section = settingsPremium
		p.premium = plainPremium{}
		on := os.Getenv("LOCAL_PREMIUM") != "off"
		p.localPremium = func() bool { return on }
		p.setLocalPremium = func(v bool) { on = v }
		size.Y = 1500
	}
	if os.Getenv("SETTINGS_SECTION") == "integrations" {
		p.section = settingsIntegrations
		size.Y = 1300
		// A VLC the user pointed at, and a file that is not mpv.
		custom := map[player.Kind]string{player.VLC: "/var/lib/flatpak/exports/bin/org.videolan.VLC", player.MPV: "/usr/bin/ls"}
		p.players.paths = func() map[player.Kind]string { return custom }
		p.invalidate = func() {}
		p.refreshPrograms()
		// The programs answer in the background.
		ops := new(op.Ops)
		for range 100 {
			ops.Reset()
			gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}, Values: map[string]any{}}
			wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
			p.Update(gtx, themeAuto, "ru")
			if p.browser.detected != nil && p.players.programs[player.VLC].customAbout != nil && p.players.programs[player.MPV].customAbout != nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	ops := new(op.Ops)
	gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	images.BeginFrame()
	// The root surface, which sections drawn by exp widgets read.
	exp.Background(gtx)
	p.Update(gtx, themeAuto, "ru")
	p.Layout(gtx, themeAuto, appearance.Light, false, localization.For("ru"))
	if chats != nil {
		p.chats.layoutDialog(gtx, localization.For("ru"))
	}
	images.EndFrame()
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// plainPremium is an account without Premium.
type plainPremium struct{}

func (plainPremium) Premium() model.Premium {
	return model.PremiumFromConfig(false, map[string]any{"premium_purchase_blocked": false, "premium_bot_username": "PremiumBot"})
}
