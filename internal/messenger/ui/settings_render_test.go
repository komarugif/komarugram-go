// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/io/key"
	"komarugram/internal/messenger/emojipacks"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/messenger/proxy"
	"komarugram/internal/messenger/shortcuts"
	"komarugram/internal/wallpaper"
	"komarugram/pkg/player"
)

type staticAccounts []model.AccountInfo

func (a staticAccounts) All() []model.AccountInfo { return a }
func (staticAccounts) Open(string)                {}
func (staticAccounts) Add()                       {}
func (staticAccounts) LogOut(string)              {}
func (staticAccounts) Subscribe(func()) func()    { return func() {} }

// TestRenderSettingsAccounts draws the settings as a window of the client
// has them, the main page with a list of saved accounts, and saves a
// screenshot, for looking at it. SETTINGS_SECTION=appearance, menus,
// shortcuts, shortcut-record, chats, notify, privacy, visibility,
// premium, integrations, proxy, power or about draws that section
// instead; wallpapers, the chats' section under the gallery of
// wallpapers:
//
//	SETTINGS_PNG=/tmp/settings.png go test ./internal/messenger/ui -run RenderSettingsAccounts
//
// The window is the client's (newSettingsApp): what a section shows is
// set in the preferences, as the user sets it, and only what comes from
// the system or from Telegram is set on the page.
func TestRenderSettingsAccounts(t *testing.T) {
	out := os.Getenv("SETTINGS_PNG")
	if out == "" {
		t.Skip("set SETTINGS_PNG to a file name")
	}
	section := os.Getenv("SETTINGS_SECTION")
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
	prefs := newSettingsPrefs(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(prefs.SetComposer(preferences.ComposerFloating))
	must(prefs.SetComposerBlur(true))
	must(prefs.SetOverlays(preferences.Overlays{Transparency: 30, MenusBlur: true, Shadow: preferences.ShadowDefault}))
	must(prefs.SetWindowBlur(true))
	must(prefs.SetWindowTransparency(40))
	must(prefs.SetViewerBlur(true))
	// The window blurs the desktop by itself, where the compositor could too.
	must(prefs.SetWindowBlurOwn(true))
	// Sound and channels off, to show both states of a switch.
	must(prefs.SetNotify(preferences.Notify{Desktop: true, Name: true, Text: true, Private: true, Groups: true, AllAccounts: true}))
	// A key taken by the desktop, to show the warning.
	taken := shortcuts.Chord{Key: "T", Modifiers: key.ModCtrl | key.ModAlt}
	must(prefs.SetShortcuts(shortcuts.Bindings{"app.theme": {taken}}))
	must(prefs.SetMenus(preferences.Menus{menuMessage: {Order: []string{"delete", "reply"}, Hidden: []string{"repeat"}}, menuSidebar: {Hidden: []string{"profile"}}}))

	proxies := proxy.NewStore("", nil)
	proxies.Add(proxy.Proxy{Kind: proxy.SOCKS5, Host: "127.0.0.1", Port: 2080})
	proxies.Add(proxy.Proxy{Kind: proxy.MTProto, Host: "mt.example.org", Port: 443, Secret: "dddf83f4ddaee1f1171b2e7a6e7403c60d"})
	proxies.Select(proxy.Telegram().ID())
	services := Services{
		Accounts:  accounts,
		AccountID: "1",
		Uninstall: func() {},
		Proxies:   proxies,
		PingProxy: (&pingLog{fail: map[string]error{"mtproto|mt.example.org|443|": context.DeadlineExceeded}}).ping,
	}
	size := image.Pt(900, 1300)
	var show settingsSection
	switch section {
	case "appearance":
		show, size.Y = settingsAppearance, 2700
		// One font is picked and gone since, the others are the system's.
		must(prefs.SetFonts(preferences.Fonts{Extra: filepath.Join(t.TempDir(), "NotoSansKR-Regular.ttf")}))
		// The packs of a catalog: one to download, one installed and in use.
		t.Setenv(emojipacks.CatalogEnv, emojiPackCatalog(t))
	case "menus":
		show, size.Y = settingsMenuOrder, 5700
	case "shortcuts":
		show, size.Y = settingsShortcuts, 6300
	case "shortcut-record":
		show, size.Y = settingsShortcuts, 900
	case "chats", "wallpapers":
		show, size.Y = settingsChats, 1900
		// The demo's wallpapers and stickers are read from assets.
		t.Chdir("../../..")
		must(prefs.SetChats(preferences.ChatLook{Day: preferences.ChatMode{Theme: "day", Accent: 0xffd46c99}}))
		must(prefs.SetConfirmations(true, false))
		must(prefs.SetComposerInput(false, true))
		must(prefs.SetLook(preferences.Look{BubbleRadius: 8, AvatarCorners: 10, Seconds: true}))
		if section == "wallpapers" {
			size.Y = 900
		}
	case "notify":
		show, size.Y = settingsNotify, 1100
	case "privacy":
		show = settingsPrivacy
	case "visibility":
		show, size.Y = settingsVisibility, 2500
		must(prefs.PinVisibility("1", true))
		must(prefs.ChangeVisibility("1", func(v *preferences.Visibility) {
			v.Calm = preferences.Calm{On: true, KeepOnline: true, HidePaid: true}
			v.HidePeerBadges, v.KeepPeerArrows, v.KeepPeerFaces = true, false, true
			v.Ghost = preferences.Ghost{On: true, KeepOnline: true, ReadOnInteract: true}
			v.Keep = preferences.Keep{Deleted: true, Edits: true}
			v.Filters = preferences.Filters{Enabled: true, Patterns: []preferences.FilterPattern{{Text: "реклама|промокод", CaseInsensitive: true}, {Text: "^#", Reversed: true, Chat: 5}}}
		}))
	case "premium":
		// An account without Premium that has Local Premium on.
		show, size.Y = settingsPremium, 1500
		if os.Getenv("LOCAL_PREMIUM") != "off" {
			must(prefs.SetLocalPremiumOf("1", preferences.LocalPremium(model.LocalPremium{Star: true, Media: true, Pins: true})))
		}
		must(prefs.SetPremiumMediaOf("1", true))
	case "integrations":
		show, size.Y = settingsIntegrations, 1650
		// A VLC the user pointed at, and a file that is not mpv.
		must(prefs.SetPlayerPath(player.VLC, "/var/lib/flatpak/exports/bin/org.videolan.VLC"))
		must(prefs.SetPlayerPath(player.MPV, "/usr/bin/ls"))
	case "proxy":
		show, size.Y = settingsProxy, 900
	case "power":
		show = settingsPower
		must(prefs.SetPauseInactive(true))
	case "about":
		show = settingsAbout
	}

	h := newSettingsApp(t, prefs, services, true, size)
	p := h.app.settings
	// What Telegram says of the account: the premium section draws it
	// both ways.
	if section == "premium" {
		p.premium = plainPremium{}
		if os.Getenv("PREMIUM_ACTIVE") == "on" {
			p.premium = activePremium{}
		}
	}
	// What the desktop takes of the keys: KDE's, with one of them taken.
	report := shortcuts.SystemReport{KDE: true, PortalVersion: 2, Occupied: map[shortcuts.Chord]bool{taken: true}}
	p.shortcuts.check = func(context.Context, []shortcuts.Chord) shortcuts.SystemReport { return report }
	// Offline: the pictures GitHub gives stay the icons in their place.
	p.about.fetch = func(context.Context, string) ([]byte, error) { return nil, errors.New("offline") }
	// A system where both the window and the compositor can blur, and
	// where the window may draw its own frame: every choice of the
	// appearance shown.
	p.windowBlurChoice = func() bool { return true }
	p.windowOwnFrameSupported = func() bool { return true }
	p.windowTransparencyAvailable = func() bool { return true }
	// Windows 7 without Aero, its glass as the Basic theme left it: the
	// color of the windows is offered.
	p.windowColor.offered = func() bool { return true }
	p.windowColor.system = func() uint32 { return wallpaper.WindowColors[0] }
	if p.about.openCommunity == nil {
		p.about.openCommunity = func() {}
	}

	if show != settingsMain {
		h.show(show)
	}
	switch section {
	case "appearance":
		// The catalog is read, and the pack used is installed, as the
		// user installed it before.
		h.until("the catalog of emoji packs", func() bool { return !p.emojiView.reading })
		for _, pack := range p.emojiView.catalog {
			if pack.ID == "sprites" {
				must(p.emojiView.store.Install(context.Background(), p.emojiView.source, pack, nil))
				fonts := prefs.Global().Fonts
				fonts.EmojiPack = pack.ID
				must(prefs.SetFonts(fonts))
			}
		}
		// The settings read the packs installed again once a second.
		h.settle(1200 * time.Millisecond)
	case "shortcuts", "shortcut-record":
		h.until("the keys the desktop takes", func() bool { return !p.shortcuts.busy })
		if section == "shortcut-record" {
			// Pressing the taken key in the dialog that records one.
			s := p.shortcuts
			s.editing, s.candidate = "app.theme", &taken
			s.dialog.Open()
			h.until("the keys the desktop takes", func() bool { return !s.busy && s.report.Occupied[taken] })
		}
	case "chats", "wallpapers":
		if section == "wallpapers" {
			p.chats.gallery.open()
		}
		// The thumbnails are rendered in the background.
		for range 60 {
			h.frame(nil)
			time.Sleep(50 * time.Millisecond)
		}
	case "integrations":
		// The programs answer in the background.
		h.until("the programs' answers", func() bool {
			return p.browser.detected != nil && p.players.programs[player.VLC].customAbout != nil && p.players.programs[player.MPV].customAbout != nil
		})
	}
	h.render(out)
}

// activePremium is an account that has Premium.
type activePremium struct{}

func (activePremium) Premium() model.Premium {
	return model.PremiumFromConfig(true, map[string]any{})
}

// plainPremium is an account without Premium.
type plainPremium struct{}

func (plainPremium) Premium() model.Premium {
	return model.PremiumFromConfig(false, map[string]any{"premium_purchase_blocked": false, "premium_bot_username": "PremiumBot"})
}
