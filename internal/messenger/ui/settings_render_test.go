// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"komarugram/internal/messenger/emojipacks"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/messenger/proxy"
	"komarugram/internal/messenger/shortcuts"
	"komarugram/internal/wallpaper"
	"komarugram/pkg/player"
)

// Exercise the controls through the same settings page and account store as the demo.
func TestFolderSettingsActions(t *testing.T) {
	h := newSettingsApp(t, newSettingsPrefs(t), Services{}, false, image.Pt(900, 700))
	p := h.app.settings
	p.items[settingsFolders].click.Click()
	h.frame(nil)
	if p.section != settingsFolders {
		t.Fatal("folder settings did not open")
	}
	v := p.folders
	v.create.click.Click()
	h.frame(nil)
	if !v.draft {
		t.Fatal("creation form did not open")
	}
	var router input.Router
	drawPage := func() {
		gtx := h.context()
		gtx.Source = router.Source()
		h.draw(gtx)
		router.Frame(gtx.Ops)
	}
	drawPage()
	toggle := func(label string) {
		t.Helper()
		var point f32.Point
		var find func([]input.SemanticNode) bool
		find = func(nodes []input.SemanticNode) bool {
			for _, n := range nodes {
				if n.Desc.Label == label {
					point = layout.FPt(n.Desc.Bounds.Min.Add(image.Pt(4, 4)))
					return true
				}
				if find(n.Children) {
					return true
				}
			}
			return false
		}
		if !find(router.AppendSemantics(nil)) {
			t.Fatalf("checkbox %q not found", label)
		}
		for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
			router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: point})
			drawPage()
		}
	}
	toggle("Группы")
	if v.name.Text() != "Группы" {
		t.Fatalf("automatic name: %q", v.name.Text())
	}
	toggle("Каналы")
	if v.name.Text() != "" {
		t.Fatalf("two types still suggest %q", v.name.Text())
	}
	toggle("Группы")
	if v.name.Text() != "Каналы" {
		t.Fatalf("changing the single type: %q", v.name.Text())
	}
	v.name.editor.SetText("Work")
	toggle("Каналы")
	v.leaveDraft(h.context(), localization.For("ru"), nil)
	v.save.click.Click()
	drawPage()
	if !v.draft || !v.invalidTypes || v.pending != nil {
		t.Fatal("empty selection was accepted")
	}
	toggle("Группы")
	toggle("Каналы")
	if v.name.Text() != "Work" || v.invalidTypes {
		t.Fatal("lost custom name or selection")
	}
	v.leaveDraft(h.context(), localization.For("ru"), nil)
	v.save.click.Click()
	h.frame(nil)
	h.until("created folder", func() bool { return v.pending == nil && !v.draft })
	folders := h.store.Folders()
	last := folders[len(folders)-1]
	if last.Title != "Work" || !last.Contains(model.Chat{Kind: model.KindGroup}) || !last.Contains(model.Chat{Kind: model.KindChannel}) || last.Contains(model.Chat{Kind: model.KindBot}) {
		t.Fatalf("created folder: %v", last)
	}
	// Feed the list a pointer drag in its own coordinates, just as the
	// settings scroll container does after transforming input to the card.
	l := localization.For("ru")
	frame := func() {
		gtx := h.context()
		gtx.Source = router.Source()
		v.update(gtx, p, l)
		v.layoutList(gtx, l, false)
		router.Frame(gtx.Ops)
	}
	frame()
	height := h.context().Dp(menuSettingsRowHeight)
	send := func(kind pointer.Kind, row int) {
		router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(12, float32(height*row+height/2))})
		frame()
	}
	send(pointer.Move, len(folders)-1)
	if !v.edits[last.ID].handleHover.Update(router.Source()) {
		t.Fatal("hover over the drag handle did not reach its row")
	}
	send(pointer.Press, len(folders)-1)
	send(pointer.Move, 0)
	send(pointer.Release, 0)
	h.until("reordered folders", func() bool { return v.pending == nil })
	folders = h.store.Folders()
	if v.draft || folders[0].ID != 0 || folders[1].ID != last.ID {
		t.Fatalf("drag across the fixed row: %v", model.FolderIDs(folders))
	}
}

// Search and multiple selection share the same recipients list as forwarding.
func TestFolderEditorRecipients(t *testing.T) {
	h := newSettingsApp(t, newSettingsPrefs(t), Services{}, false, image.Pt(900, 1100))
	h.show(settingsFolders)
	v := h.app.settings.folders
	folder := h.store.Folders()[2]
	v.edits[folder.ID].click.Click()
	h.frame(nil)
	if v.base == nil || v.name.Text() != folder.Title {
		t.Fatal("did not open the chosen folder")
	}
	v.chooseIcon.click.Click()
	h.frame(nil)
	v.iconButtons["💼"].click.Click()
	h.frame(nil)
	h.until("chosen icon", func() bool { return !v.iconDialog.Shown() })
	v.includePeers.click.Click()
	h.frame(nil)
	p := &v.peers
	l := localization.For("ru")
	for _, picker := range []*chatPickerList{&h.app.history.forwarding.chatPickerList, &h.app.viewer.share.picker.chatPickerList, &p.chatPickerList} {
		picker.search.SetText("  @GoNeWs  ")
		hits := picker.targets(h.context(), h.store.Chats(), nil, l)
		if len(hits) != 1 || hits[0].ID != 4 {
			t.Fatalf("username search: %v", hits)
		}
	}
	pick := func(query string, id int64) {
		t.Helper()
		p.search.SetText(query)
		h.frame(nil)
		hits := p.targets(h.context(), p.chats, nil, l)
		if len(hits) != 1 || hits[0].ID != id || p.rows[id] == nil {
			t.Fatalf("search %q: %v", query, hits)
		}
		p.rows[id].click.Click()
		h.frame(nil)
	}
	p.chips.chip(3).click.Click() // Channels
	pick("GoNeWs", 4)
	p.search.SetText("@ANNA_S")
	h.frame(nil)
	if hits := p.targets(h.context(), p.chats, nil, l); len(hits) != 0 {
		t.Fatal("a name match escaped the channel filter")
	}
	p.chips.chip(1).click.Click() // Private chats
	pick("@ANNA_S", 2)
	p.apply.click.Click()
	h.frame(nil)
	h.until("picker closed", func() bool { return !p.modal.Shown() })
	if !slices.Equal(v.working.Included, []int64{4, 2}) {
		t.Fatal(v.working.Included)
	}
	v.excludePeers.click.Click()
	h.frame(nil)
	pick("@GoNews", 4)
	p.cancel.click.Click()
	h.frame(nil)
	h.until("cancelled selection", func() bool { return !p.modal.Shown() })
	if len(v.working.Excluded) != 0 || !slices.Equal(v.working.Included, []int64{4, 2}) {
		t.Fatal("cancel changed draft")
	}
	v.excludePeers.click.Click()
	h.frame(nil)
	pick("GoNews", 4)
	p.apply.click.Click()
	h.frame(nil)
	h.until("exclusions applied", func() bool { return !p.modal.Shown() })
	v.types.SetValues(nil)
	v.excludeTypes.SetValues([]string{"folders.read", "folders.archived"})
	// The network error leaves the draft in place so a retry uses the same selections.
	v.store = offlineFolderWriter{v.store}
	h.app.openSection(h.context(), section{kind: sectionDownloads})
	if h.app.section.kind != sectionSettings || !v.saveDialog.Shown() {
		t.Fatal("left the editor before confirming")
	}
	v.save.click.Click()
	h.frame(nil)
	h.until("save failure", func() bool { return v.pending == nil })
	if h.app.section.kind != sectionSettings || !v.draft || !slices.Equal(v.definition().Excluded, []int64{4}) || h.store.Folders()[2].ExcludeRead {
		t.Fatal("failed save lost or published the draft")
	}
	v.store = h.store
	h.app.openSection(h.context(), section{kind: sectionDownloads})
	if h.app.section.kind != sectionSettings || !v.saveDialog.Shown() {
		t.Fatal("left the editor before confirming")
	}
	v.save.click.Click()
	h.frame(nil)
	h.until("folder edits saved", func() bool { return v.pending == nil && !v.draft })
	if h.app.section.kind != sectionDownloads {
		t.Fatal("did not resume requested navigation")
	}
	got := h.store.Folders()[2]
	if got.Emoticon != "💼" || !slices.Equal(got.Included, []int64{2}) || !slices.Equal(got.Excluded, []int64{4}) || !got.ExcludeRead || !got.ExcludeArchived || got.FolderTypes != (model.FolderTypes{}) {
		t.Fatalf("saved %v", got)
	}
}

// Leaving through either navigation menu honours the folder editor's decision.
func TestFolderEditorMenuExit(t *testing.T) {
	h := newSettingsApp(t, newSettingsPrefs(t), Services{}, false, image.Pt(1400, 1200))
	h.show(settingsFolders)
	a, v := h.app, h.app.settings.folders
	f := h.store.Folders()[1]
	v.edits[f.ID].click.Click()
	h.frame(nil)
	v.includePeers.click.Click()
	h.frame(nil)
	v.peers.rows[2].click.Click()
	h.frame(nil)
	dest := drawerKey{section: section{kind: sectionAll}}
	gtx := h.context()
	a.sidebar.Layout(gtx, a.section, navigationEntries(navigationSlots(false, a.catalog()), h.store.Folders(), h.store.Chats(), false, a.catalog()), false, a.catalog())
	a.sidebar.buttons[dest].click.Click()
	a.Update(h.context())
	if a.section.kind != sectionSettings || !v.saveDialog.Shown() {
		t.Fatal("sidebar bypassed the folder editor")
	}
	v.stay.click.Click()
	h.frame(nil)
	h.until("returned to editor", func() bool { return !v.saveDialog.Shown() })
	if !v.draft || !v.peers.modal.Shown() || !v.peers.selection[2] || v.afterLeave != nil {
		t.Fatal("stay lost draft or queued a transition")
	}
	dest = drawerKey{section: section{kind: sectionFolder, folder: f.ID}}
	a.drawer.row(dest).click.Click()
	a.layoutDrawer(h.context())
	if a.section.kind != sectionSettings || !v.saveDialog.Shown() {
		t.Fatal("drawer bypassed the folder editor")
	}
	v.discard.click.Click()
	h.frame(nil)
	if a.section != dest.section || v.draft || !h.store.Folders()[1].SameRules(f) {
		t.Fatal("discard did not resume the chosen navigation")
	}
}

type offlineFolderWriter struct{ model.FolderManager }

func (offlineFolderWriter) SaveFolder(context.Context, *model.Folder, model.Folder) error {
	return errors.New("offline")
}

type emptyFolderList struct{ model.FolderManager }

func (emptyFolderList) Folders() []model.Folder { return []model.Folder{{}} }

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
	case "folders", "folders-empty", "folders-hover":
		show, size.Y = settingsFolders, 700
	case "folder-create", "folder-edit", "folder-icons", "folder-peers", "folder-save":
		show, size.Y = settingsFolders, 1100
		if section == "folder-peers" {
			size = image.Pt(550, 750)
		}
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
		show, size.Y = settingsPower, 2140
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
	case "folders-hover":
		var router input.Router
		frame := func() {
			gtx := h.context()
			gtx.Source = router.Source()
			h.draw(gtx)
			router.Frame(gtx.Ops)
		}
		frame()
		f := h.store.Folders()[2]
		// The full-width navigation button exposes the row, including its handle.
		var rowBounds func([]input.SemanticNode) image.Rectangle
		rowBounds = func(nodes []input.SemanticNode) image.Rectangle {
			for _, n := range nodes {
				if n.Desc.Label == f.Title && n.Desc.Class == semantic.Button {
					return n.Desc.Bounds
				}
				if b := rowBounds(n.Children); !b.Empty() {
					return b
				}
			}
			return image.Rectangle{}
		}
		b := rowBounds(router.AppendSemantics(nil))
		if b.Empty() {
			t.Fatal("folder row missing from semantics")
		}
		router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: layout.FPt(image.Pt(b.Min.X+10, (b.Min.Y+b.Max.Y)/2))})
		frame()
		if !p.folders.edits[f.ID].handleHover.Update(router.Source()) {
			t.Fatal("handle did not receive hover")
		}
	case "folders-empty":
		p.folders.store = emptyFolderList{p.folders.store}
	case "folder-create":
		p.folders.create.click.Click()
		h.frame(nil)
		p.folders.types.SetValues([]string{"folders.groups"})
	case "folder-edit", "folder-icons", "folder-peers", "folder-save":
		f := h.store.Folders()[1]
		p.folders.edits[f.ID].click.Click()
		h.frame(nil)
		if section == "folder-save" {
			p.folders.name.editor.SetText("Work")
			p.folders.leaveDraft(h.context(), h.app.catalog(), nil)
			h.frame(nil)
		}
		if section == "folder-icons" {
			p.folders.chooseIcon.click.Click()
			h.frame(nil)
		}
		if section == "folder-peers" {
			p.folders.includePeers.click.Click()
			h.frame(nil)
			peer := &p.folders.peers
			peer.rows[2].click.Click()
			h.frame(nil)
			peer.rows[4].click.Click()
			h.frame(nil)
			peer.chips.chip(3).click.Click()
			peer.search.SetText("@GoN")
			h.frame(nil)
		}
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
