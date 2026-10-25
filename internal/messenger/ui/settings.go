// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"math"
	"slices"
	"strconv"
	"strings"

	"gio-mw/exp/appearance"
	"gio-mw/exp/powersave"
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/button"
	"gio-mw/widget/checkbox"
	"gio-mw/widget/radio"
	"gio-mw/widget/slider"
	"gio-mw/widget/toggle"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/localization/viewtexts"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/messenger/security"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/deviceinfo"
	"komarugram/pkg/miniapp"
)

// themeMode is the theme the user picked.
type themeMode = preferences.Theme

const (
	themeAuto  = preferences.ThemeAuto
	themeLight = preferences.ThemeLight
	themeDark  = preferences.ThemeDark
)

func themeLabels(l localization.Catalog) map[themeMode]string {
	return map[themeMode]string{
		themeAuto: l.T("settings.theme_auto"), themeLight: l.T("settings.theme_light"), themeDark: l.T("settings.theme_dark"),
	}
}

// settingsSection is a page of the settings.
type settingsSection int

const (
	settingsMain settingsSection = iota
	settingsAppearance
	settingsPrivacy
	settingsPower
	settingsPremium
	settingsIntegrations
	settingsDevices
	// settingsChats is the theme and the wallpaper of the chats, the composer
	// and how messages look, as Telegram Desktop's Chat Settings.
	settingsChats
	// settingsNotify is how new messages are told of, as Telegram Desktop's
	// Notifications and Sounds.
	settingsNotify
	// settingsProxy is the proxies Telegram is connected through.
	settingsProxy
	// settingsAbout is the program, the system it runs on and its links.
	settingsAbout
	settingsMenuOrder
	settingsShortcuts
	// settingsVisibility is what others see of the account and what the
	// client shows of them: ghost mode, the message filters, what the cache
	// keeps, and the hiding of what distracts.
	settingsVisibility
)

func settingsTitles(l localization.Catalog) map[settingsSection]string {
	return map[settingsSection]string{
		settingsMain: l.T("settings.title"), settingsAppearance: l.T("settings.appearance"),
		settingsPrivacy: l.T("settings.privacy"), settingsPower: l.T("settings.power"),
		settingsPremium: l.T("premium.title"), settingsIntegrations: l.T("settings.integrations"),
		settingsDevices: l.T("settings.devices"), settingsChats: l.T("settings.chats"),
		settingsNotify: l.T("settings.notify"), settingsAbout: l.T("settings.about"),
		settingsProxy:      l.T("settings.proxy"),
		settingsMenuOrder:  l.T("menus.title"),
		settingsShortcuts:  l.T("keys.title"),
		settingsVisibility: l.T("settings.visibility"),
	}
}

var settingsIcons = map[settingsSection]wdk.IconWidget{
	settingsAppearance: iconPalette,
	settingsPrivacy:    iconPrivacy,
	settingsPower:      iconPower,
	settingsPremium:    iconPremium,
	// The section holds what the app hands over to other programs.
	settingsIntegrations: iconIntegrations,
	settingsDevices:      iconDevices,
	settingsChats:        iconChats,
	settingsNotify:       iconNotifications,
	settingsProxy:        iconProxy,
	settingsAbout:        iconInfo,
	settingsMenuOrder:    iconReorder,
	settingsShortcuts:    iconKeyboard,
	settingsVisibility:   iconVisibility,
}

func storageShort(l localization.Catalog) map[miniapp.Storage]string {
	return map[miniapp.Storage]string{
		miniapp.Ephemeral: l.T("settings.storage_none"), miniapp.PerApp: l.T("settings.storage_per_app"),
		miniapp.Shared: l.T("settings.storage_shared"),
	}
}

func motionShort(l localization.Catalog) map[powersave.Mode]string {
	return map[powersave.Mode]string{
		powersave.ModeAuto: l.T("settings.motion_auto"), powersave.ModeOn: l.T("settings.motion_on"),
		powersave.ModeOff: l.T("settings.motion_off"),
	}
}

// settingsPage shows the main settings page or one of its sections.
type settingsPage struct {
	shortcuts   *shortcutSettings
	menus       *menuSettings
	cardHeights map[string]*heightTransition
	scrollPage
	section settingsSection
	// layingOut stays set on panic, identifying the failed page to RecoverFrame.
	layingOut bool
	items     map[settingsSection]*settingsItem
	back      *button.Button

	motion   *motion.Settings
	miniapps *miniappprefs.Settings
	theme    *radio.Radios[themeMode]
	language *radio.Radios[string]
	// worded is the catalog animations and privacy took their texts from.
	worded     localization.Catalog
	animations *motion.View
	privacy    *miniappprefs.View
	security   *securityView
	// proxy is the proxies; nil hides their section.
	proxy       *proxyView
	accounts    model.Accounts
	accountID   func() string
	accountRows map[string]*settingsItem
	addAccount  settingsItem
	// logOut asks to leave the window's account; confirmLogOut does it.
	logOut           settingsItem
	confirmingLogOut bool
	// revealLogOut is set while the confirmation opens out of the row,
	// which the page scrolls with so that its buttons are in view: the row
	// is at the end of a page scrolled to it, and the card grows below.
	// belowLogOut is how tall what follows it was laid out last.
	revealLogOut bool
	belowLogOut  int
	// uninstallItem opens the uninstaller through uninstall, which is nil
	// where the program does not install itself.
	uninstallItem settingsItem
	uninstall     func()
	about         *aboutView
	confirmLogOut *button.Button
	cancelLogOut  *button.Button
	loggingOut    bool
	shownAccounts []model.AccountInfo
	images        *imageOps
	// private and setPrivate read and switch visual privacy mode; without
	// them the mode is off and the switch hidden.
	private    func() bool
	setPrivate func(bool)
	visual     *toggle.Toggle[string]
	// streamer and setStreamer read and switch Streamer Mode, where the
	// platform has it.
	streamer     func() bool
	setStreamer  func(bool)
	streamerMode *toggle.Toggle[string]
	// ghost and setGhost read and change what the accounts tell others;
	// without them the section is hidden. ghostToggle turns Ghost Mode on,
	// ghostBoxes choose what it hides, and ghostInteract still reads a
	// chat on an action in it.
	ghost         func() preferences.Ghost
	setGhost      func(preferences.Ghost)
	ghostToggle   *toggle.Toggle[string]
	ghostBoxes    *checkbox.Checkboxes[string]
	ghostInteract *checkbox.Checkboxes[string]
	// keep and setKeep read and change what the cache keeps.
	keep     func() preferences.Keep
	setKeep  func(preferences.Keep)
	keepOpts *toggle.Toggle[string]
	// notifyView changes notifications; hidden without its functions.
	notifyView *notifySettings
	// filtersView edits the message filters; hidden without its functions.
	filtersView *filterSettings
	// lookView changes how messages and avatars are drawn.
	lookView *lookSettings
	// fontsView points the client at font files; hidden without its
	// functions.
	fontsView *fontSettings
	// emojiView chooses the emoji pack; hidden without its functions.
	emojiView *emojiSettings
	// composerStyle and setComposerStyle read and switch the composer
	// style; without them the choice is hidden.
	composerStyle    func() preferences.ComposerStyle
	setComposerStyle func(preferences.ComposerStyle)
	composer         *radio.Radios[preferences.ComposerStyle]
	// composerBlur and setComposerBlur read and switch the blur behind the
	// floating composer; without them the checkbox is hidden.
	composerBlur    func() bool
	setComposerBlur func(bool)
	// overlays and setOverlays read and change how the menus and toasts are
	// drawn, and the transparency of all the overlays; blur is the switches
	// of the composer, the menus and the toasts, and transparency the
	// slider.
	overlays     func() preferences.Overlays
	setOverlays  func(preferences.Overlays)
	blur         *checkbox.Checkboxes[string]
	transparency *slider.Slider
	// shadow is how dark the shadows of dialogs and menus are.
	shadow *slider.Slider
	// pauseInactive and setPauseInactive read and switch whether media
	// animations stop in an inactive window; without them the checkbox is
	// hidden.
	pauseInactive         func() bool
	setPauseInactive      func(bool)
	pauseInactiveCheckbox *checkbox.Checkboxes[string]
	// Compositor blur is independent of in-app animation and overlay blur.
	windowBlur                  func() bool
	setWindowBlur               func(bool)
	windowBlurCheckbox          *checkbox.Checkboxes[string]
	windowTransparency          func() int
	setWindowTransparency       func(int)
	windowTransparencyAvailable func() bool
	windowTransparencySlider    *slider.Slider
	// confirmations and setConfirmations read and change whether stickers
	// and GIFs are sent only once confirmed.
	confirmations    func() (sticker, gif bool)
	setConfirmations func(sticker, gif bool)
	confirmBoxes     *checkbox.Checkboxes[string]
	// input and setInput read and change whether the message field takes
	// the keyboard only when clicked, and the picker opens only by a click.
	input      func() (manualFocus, pickerOnClick bool)
	setInput   func(manualFocus, pickerOnClick bool)
	inputBoxes *checkbox.Checkboxes[string]
	// premium is the account's Premium; nil hides the section.
	premium model.PremiumSource
	// localPremium and setLocalPremium read and switch Local Premium; the
	// toggle shows it.
	openPremiumBot     func(string)
	localPremium       func() bool
	setLocalPremium    func(bool)
	localPremiumToggle *toggle.Toggle[string]
	// hidePeerBadges and setHidePeerBadges read and switch the hiding of the
	// marks of exteraGram, AyuGram and KomaruGram beside names; keepPeerBadges
	// and setKeepPeerBadges, whether the arrows and the faces stay, which
	// peerBadgeBoxes show.
	// premiumMark draws the Premium star beside a name; without it the star
	// is drawn by the client.
	premiumMark          func(gtx layout.Context, size unit.Dp) layout.Dimensions
	hidePeerBadges       func() bool
	setHidePeerBadges    func(bool)
	hidePeerBadgesToggle *toggle.Toggle[string]
	keepPeerBadges       func() (arrows, faces bool)
	setKeepPeerBadges    func(arrows, faces bool)
	peerBadgeBoxes       *checkbox.Checkboxes[string]
	// calm and setCalm read and change the hiding of what distracts: who
	// is online, and whether messages were read; without them the switch is
	// hidden. calmBoxes choose which of the two it hides.
	calm       func() preferences.Calm
	setCalm    func(preferences.Calm)
	calmToggle *toggle.Toggle[string]
	calmBoxes  *checkbox.Checkboxes[string]
	// premiumMedia and setPremiumMedia read and switch the Premium address for
	// files over WebSocket, which the Premium page offers to who has Premium.
	premiumMedia       func() bool
	setPremiumMedia    func(bool)
	premiumMediaToggle *toggle.Toggle[string]
	subscribe          *button.Button
	// players chooses the external player for videos and audio; browser,
	// the browser Mini Apps run in.
	players  *playerSettings
	decoders *decoderSettings
	// sessions are the account's devices; nil hides the section.
	sessions *sessionsView
	// chats is the theme and the wallpaper of every chat; hidden without
	// its functions.
	chats      *chatsSettings
	browser    *programSetting
	invalidate func()
}

func newSettingsPage(m *motion.Settings, miniapps *miniappprefs.Settings, protection *security.Manager, invalidate func(), accounts model.Accounts, accountID func() string, mode themeMode, language string, setMode func(themeMode), setLanguage func(string)) *settingsPage {
	l := localization.For(language)
	animations := motion.NewView(m, viewtexts.Motion(l))
	animations.TitleStyle = token.TypestyleTitleMedium
	privacy := miniappprefs.NewView(miniapps, viewtexts.MiniApps(l))
	privacy.TitleStyle = token.TypestyleTitleMedium
	p := &settingsPage{
		about: newAboutView(invalidate),
		items: map[settingsSection]*settingsItem{
			settingsAppearance:   new(settingsItem),
			settingsPrivacy:      new(settingsItem),
			settingsPower:        new(settingsItem),
			settingsPremium:      new(settingsItem),
			settingsIntegrations: new(settingsItem),
			settingsAbout:        new(settingsItem),
			settingsDevices:      new(settingsItem),
			settingsChats:        new(settingsItem),
			settingsNotify:       new(settingsItem),
			settingsProxy:        new(settingsItem),
			settingsMenuOrder:    new(settingsItem),
			settingsShortcuts:    new(settingsItem),
			settingsVisibility:   new(settingsItem),
		},
		back:          button.Text(),
		motion:        m,
		miniapps:      miniapps,
		theme:         radio.NewRadios([]themeMode{themeAuto, themeLight, themeDark}, mode, setMode),
		language:      radio.NewRadios(languageCodes(), language, setLanguage),
		animations:    animations,
		privacy:       privacy,
		security:      newSecurityView(protection, invalidate),
		accounts:      accounts,
		accountID:     accountID,
		accountRows:   make(map[string]*settingsItem),
		subscribe:     button.Filled(),
		confirmLogOut: button.Filled(),
		cancelLogOut:  button.Text(),
		players:       newPlayerSettings(),
		decoders:      newDecoderSettings(),
		browser:       newBrowserSetting(),
		invalidate:    invalidate,
	}
	// A click saves the mode; layoutVisualPrivacy shows it as saved.
	p.visual = toggle.NewToggle([]string{"visual"}, nil, func(values []string) {
		if p.setPrivate != nil {
			p.setPrivate(len(values) == 1)
		}
	})
	p.streamerMode = toggle.NewToggle([]string{"streamer"}, nil, func(values []string) {
		if p.setStreamer != nil {
			p.setStreamer(len(values) == 1)
		}
	})
	p.localPremiumToggle = toggle.NewToggle([]string{"local"}, nil, func(values []string) {
		if p.setLocalPremium != nil {
			p.setLocalPremium(len(values) == 1)
		}
	})
	p.hidePeerBadgesToggle = toggle.NewToggle([]string{"hide"}, nil, func(values []string) {
		if p.setHidePeerBadges != nil {
			p.setHidePeerBadges(len(values) == 1)
		}
	})
	p.peerBadgeBoxes = checkbox.NewCheckboxes([]string{"arrows", "faces"}, nil, func(values []string) {
		if p.setKeepPeerBadges != nil {
			p.setKeepPeerBadges(!slices.Contains(values, "arrows"), !slices.Contains(values, "faces"))
		}
	})
	p.calmToggle = toggle.NewToggle([]string{"calm"}, nil, func(values []string) {
		if p.setCalm != nil {
			c := p.calm()
			c.On = len(values) == 1
			p.setCalm(c)
		}
	})
	p.calmBoxes = checkbox.NewCheckboxes([]string{"online", "read"}, nil, func(values []string) {
		if p.setCalm != nil {
			c := p.calm()
			c.KeepOnline, c.KeepRead = !slices.Contains(values, "online"), !slices.Contains(values, "read")
			p.setCalm(c)
		}
	})
	p.premiumMediaToggle = toggle.NewToggle([]string{"media"}, nil, func(values []string) {
		if p.setPremiumMedia != nil {
			p.setPremiumMedia(len(values) == 1)
		}
	})
	p.ghostToggle = toggle.NewToggle([]string{"ghost"}, nil, func(values []string) {
		if p.setGhost != nil {
			g := p.ghost()
			g.On = len(values) == 1
			p.setGhost(g)
		}
	})
	p.ghostBoxes = checkbox.NewCheckboxes(ghostHidden, nil, func(values []string) {
		if p.setGhost != nil {
			p.setGhost(ghostWithHidden(p.ghost(), values))
		}
	})
	p.ghostInteract = checkbox.NewCheckboxes([]string{"interact"}, nil, func(values []string) {
		if p.setGhost != nil {
			g := p.ghost()
			g.ReadOnInteract = len(values) == 1
			p.setGhost(g)
		}
	})
	p.notifyView = newNotifySettings()
	p.filtersView = newFilterSettings()
	p.lookView = newLookSettings()
	p.fontsView = newFontSettings(invalidate)
	p.emojiView = newEmojiSettings(invalidate)
	p.chats = newChatsSettings(invalidate)
	p.chats.toast = &p.toast
	p.keepOpts = toggle.NewToggle([]string{"deleted", "edits"}, nil, func(values []string) {
		if p.setKeep != nil {
			p.setKeep(preferences.Keep{Deleted: slices.Contains(values, "deleted"), Edits: slices.Contains(values, "edits")})
		}
	})
	p.composer = radio.NewRadios([]preferences.ComposerStyle{preferences.ComposerClassic, preferences.ComposerFloating}, preferences.ComposerFloating, func(style preferences.ComposerStyle) {
		if p.setComposerStyle != nil {
			p.setComposerStyle(style)
		}
	})
	p.confirmBoxes = checkbox.NewCheckboxes([]string{"sticker", "gif"}, nil, func(values []string) {
		if p.setConfirmations != nil {
			p.setConfirmations(slices.Contains(values, "sticker"), slices.Contains(values, "gif"))
		}
	})
	p.inputBoxes = checkbox.NewCheckboxes([]string{"focus", "picker"}, nil, func(values []string) {
		if p.setInput != nil {
			p.setInput(slices.Contains(values, "focus"), slices.Contains(values, "picker"))
		}
	})
	p.blur = checkbox.NewCheckboxes([]string{"composer", "menus", "toasts"}, nil, func(values []string) {
		if p.setComposerBlur != nil {
			p.setComposerBlur(slices.Contains(values, "composer"))
		}
		if p.overlays != nil && p.setOverlays != nil {
			o := p.overlays()
			o.MenusBlur, o.ToastsBlur = slices.Contains(values, "menus"), slices.Contains(values, "toasts")
			p.setOverlays(o)
		}
	})
	var steps []int
	for v := 0; v <= preferences.TransparencyMax; v += 5 {
		steps = append(steps, v)
	}
	p.pauseInactiveCheckbox = checkbox.NewCheckboxes([]string{"pause"}, nil, func(values []string) {
		if p.setPauseInactive != nil {
			p.setPauseInactive(slices.Contains(values, "pause"))
		}
	})
	p.windowBlurCheckbox = checkbox.NewCheckboxes([]string{"window"}, nil, func(values []string) {
		if p.setWindowBlur != nil {
			p.setWindowBlur(slices.Contains(values, "window"))
		}
	})
	p.windowTransparencySlider = slider.StandardSlider(steps, 0, func(v int) {
		if p.setWindowTransparency != nil {
			p.setWindowTransparency(v)
		}
	})
	var shadowSteps []int
	for v := 0; v <= preferences.ShadowMax; v += shadowStep {
		shadowSteps = append(shadowSteps, v)
	}
	p.shadow = slider.StandardSlider(shadowSteps, preferences.ShadowDefault, func(v int) {
		if p.overlays != nil && p.setOverlays != nil {
			o := p.overlays()
			o.Shadow = v
			p.setOverlays(o)
		}
	})
	p.transparency = slider.StandardSlider(steps, 30, func(v int) {
		if p.overlays != nil && p.setOverlays != nil {
			o := p.overlays()
			o.Transparency = v
			p.setOverlays(o)
		}
	})
	if m != nil {
		p.about.animate = m.AnimationsEnabled
	}
	return p
}

// open shows the main settings page.
func (p *settingsPage) open() {
	p.section = settingsMain
	p.confirmingLogOut = false
	p.list.Position = layout.Position{}
	p.players.refresh()
	// The main page counts the devices.
	p.sessions.refresh()
}

func (p *settingsPage) Update(gtx layout.Context, mode themeMode, language string) {
	if l := localization.For(language); l != p.worded {
		p.worded = l
		p.animations.SetStrings(viewtexts.Motion(l))
		p.privacy.SetStrings(viewtexts.MiniApps(l))
	}
	for _, account := range p.shownAccounts {
		if row := p.accountRows[account.ID]; row != nil && row.click.Clicked(gtx) {
			p.accounts.Open(account.ID)
		}
	}
	if p.addAccount.click.Clicked(gtx) && p.accounts != nil {
		p.accounts.Add()
	}
	if p.uninstallItem.click.Clicked(gtx) && p.uninstall != nil {
		p.uninstall()
	}
	if p.logOut.click.Clicked(gtx) && !p.loggingOut {
		p.confirmingLogOut, p.revealLogOut = true, true
	}
	if p.cancelLogOut.Clicked(gtx) {
		p.confirmingLogOut = false
	}
	if p.confirmLogOut.Clicked(gtx) && p.confirmingLogOut && !p.loggingOut && p.accounts != nil {
		if id := p.accountID(); id != "" {
			p.loggingOut = true
			p.accounts.LogOut(id)
		}
	}
	for section, item := range p.items {
		if item.click.Clicked(gtx) {
			p.section = section
			p.list.Position = layout.Position{}
			if section == settingsIntegrations {
				p.refreshPrograms()
			}
			if section == settingsDevices {
				p.sessions.refresh()
			}
			if section == settingsAbout {
				p.about.open()
			}
			if section == settingsProxy && p.proxy != nil {
				p.proxy.Open()
			}
		}
	}
	if p.back.Clicked(gtx) {
		p.open()
	}
	if p.subscribe.Clicked(gtx) && p.premium != nil {
		if bot := p.premium.Premium().Bot; bot != "" {
			p.openPremiumBot(bot)
		}
	}
	// The sidebar can switch the theme too.
	p.theme.SetValue(mode)
	p.theme.Update(gtx)
	p.language.SetValue(language)
	p.language.Update(gtx)
	if p.composerStyle != nil {
		p.composer.SetValue(p.composerStyle())
		p.composer.Update(gtx)
	}
	if p.composerBlur != nil {
		// What blurs may have changed in another window since the last frame.
		var want []string
		if p.composerBlur() {
			want = append(want, "composer")
		}
		if p.overlays != nil {
			o := p.overlays()
			if o.MenusBlur {
				want = append(want, "menus")
			}
			if o.ToastsBlur {
				want = append(want, "toasts")
			}
			if p.transparency.GetValue() != o.Transparency {
				p.transparency.SetValue(o.Transparency)
			}
			if shadow := (o.Shadow + shadowStep/2) / shadowStep * shadowStep; p.shadow.GetValue() != shadow {
				p.shadow.SetValue(shadow)
			}
		}
		if got := p.blur.GetValues(); !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
			p.blur.SetValues(want)
		}
		if p.blurAvailable() {
			p.blur.Enable()
		} else {
			p.blur.Disable()
		}
		p.blur.Update(gtx)
	}
	if p.windowBlur != nil {
		var want []string
		if p.windowBlur() {
			want = []string{"window"}
		}
		if !slices.Equal(p.windowBlurCheckbox.GetValues(), want) {
			p.windowBlurCheckbox.SetValues(want)
		}
		if p.windowTransparencyAvailable != nil && !p.windowTransparencyAvailable() {
			p.windowBlurCheckbox.Disable()
		} else {
			p.windowBlurCheckbox.Enable()
		}
		p.windowBlurCheckbox.Update(gtx)
	}
	if p.windowTransparency != nil {
		// Imported preferences may be between the slider stops.
		v := (p.windowTransparency() + 2) / 5 * 5
		if p.windowTransparencySlider.GetValue() != v {
			p.windowTransparencySlider.SetValue(v)
		}
	}
	if p.confirmations != nil {
		sticker, gif := p.confirmations()
		var want []string
		if sticker {
			want = append(want, "sticker")
		}
		if gif {
			want = append(want, "gif")
		}
		if !slices.Equal(want, p.confirmBoxes.GetValues()) {
			p.confirmBoxes.SetValues(want)
		}
		p.confirmBoxes.Update(gtx)
	}
	if p.input != nil {
		// Another window may have changed the shared preference.
		focus, picker := p.input()
		var want []string
		if focus {
			want = append(want, "focus")
		}
		if picker {
			want = append(want, "picker")
		}
		if !slices.Equal(want, p.inputBoxes.GetValues()) {
			p.inputBoxes.SetValues(want)
		}
		p.inputBoxes.Update(gtx)
	}
	p.animations.Update(gtx)
	if p.pauseInactive != nil {
		// Another window may have changed the shared preference.
		var want []string
		if p.pauseInactive() {
			want = []string{"pause"}
		}
		if !slices.Equal(p.pauseInactiveCheckbox.GetValues(), want) {
			p.pauseInactiveCheckbox.SetValues(want)
		}
		p.pauseInactiveCheckbox.Update(gtx)
	}
	p.privacy.Update(gtx)
	if p.section == settingsIntegrations {
		p.players.Update(gtx)
		p.decoders.Update(gtx)
		p.browser.Update(gtx)
	}
	if p.section == settingsDevices && p.sessions != nil {
		p.sessions.Update(gtx)
	}
	if p.section == settingsPrivacy {
		p.security.UpdateSettings(gtx)
	}
	if p.section == settingsVisibility {
		p.filtersView.Update(gtx)
		if p.calm != nil {
			p.calmBoxes.Update(gtx)
		}
		if p.ghost != nil {
			p.ghostBoxes.Update(gtx)
			p.ghostInteract.Update(gtx)
		}
		if p.keepPeerBadges != nil {
			p.peerBadgeBoxes.Update(gtx)
		}
	}
	if p.shortcuts != nil {
		if p.section == settingsShortcuts {
			p.shortcuts.Update(gtx, localization.For(language))
		} else if p.shortcuts.busy || p.shortcuts.dialog.Shown() {
			p.shortcuts.Close()
			p.shortcuts.signature = ""
		}
	}
	if p.section == settingsMenuOrder && p.menus != nil {
		p.menus.Update(gtx, localization.For(language))
	} else if p.menus != nil {
		p.menus.CancelDrag()
	}
	if p.section == settingsAppearance {
		p.emojiView.Update(gtx)
		p.fontsView.Update(gtx)
	}
	if p.section == settingsChats {
		p.lookView.Update(gtx)
		p.chats.Update(gtx)
	}
	if p.section == settingsAbout {
		p.about.Update(gtx)
	}
	if p.section == settingsProxy && p.proxy != nil {
		p.proxy.Update(gtx)
	}
}

func (p *settingsPage) Layout(gtx layout.Context, mode themeMode, system appearance.Scheme, dark bool, l localization.Catalog) layout.Dimensions {
	p.layingOut = true
	sc := scheme(gtx)
	titles := settingsTitles(l)
	dims := p.layout(gtx, func(gtx layout.Context) layout.Dimensions {
		header := layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			title := func(gtx layout.Context) layout.Dimensions {
				return label(gtx, titles[p.section], token.TypestyleHeadlineSmall, sc.Surface.OnColor, 1)
			}
			if p.section == settingsMain {
				return title(gtx)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return p.back.LayoutIconOnly(gtx, l.T("settings.back"), iconBack)
				}),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(title),
			)
		})
		var content layout.Widget
		switch p.section {
		case settingsShortcuts:
			content = func(gtx layout.Context) layout.Dimensions {
				if p.shortcuts == nil {
					return layout.Dimensions{}
				}
				return p.shortcuts.Layout(gtx, p, l)
			}
		case settingsMenuOrder:
			content = func(gtx layout.Context) layout.Dimensions {
				if p.menus == nil {
					return layout.Dimensions{}
				}
				return p.menus.Layout(gtx, p, l)
			}
		case settingsAppearance:
			content = func(gtx layout.Context) layout.Dimensions {
				return p.layoutAppearance(gtx, mode, system, dark, l)
			}
		case settingsPrivacy:
			content = func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.setPrivate == nil {
							return layout.Dimensions{}
						}
						return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return p.cardHeight("Layout-1").Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.layoutVisualPrivacy(gtx, l) }, defaultCardPadding)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.cardHeight("Layout-2").Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.security.SettingsLayout(gtx, l) }, defaultCardPadding)
					}),
					vspace(12),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.cardHeight("Layout-3").Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.security.WindowLockLayout(gtx, l) }, defaultCardPadding)
					}),
					vspace(12),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.cardHeight("Layout-7").Card(gtx, p.privacy.Layout, defaultCardPadding)
					}),
				)
			}
		case settingsVisibility:
			content = func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.calm == nil {
							return layout.Dimensions{}
						}
						return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return p.cardHeight("visibility-calm").Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.layoutCalm(gtx, l) }, defaultCardPadding)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.hidePeerBadges == nil {
							return layout.Dimensions{}
						}
						return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return p.cardHeight("visibility-badges").Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.layoutPeerBadges(gtx, l) }, defaultCardPadding)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.ghost == nil {
							return layout.Dimensions{}
						}
						return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return p.cardHeight("Layout-4").Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.layoutGhost(gtx, l) }, defaultCardPadding)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.filtersView.filters == nil {
							return layout.Dimensions{}
						}
						return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return p.cardHeight("Layout-5").Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.filtersView.Layout(gtx, l) }, defaultCardPadding)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.keep == nil {
							return layout.Dimensions{}
						}
						return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return p.cardHeight("Layout-6").Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.layoutKeep(gtx, l) }, defaultCardPadding)
						})
					}),
				)
			}
		case settingsPower:
			content = func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.cardHeight("Layout-8").Card(gtx, p.animations.Layout, defaultCardPadding)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.pauseInactive == nil {
							return layout.Dimensions{}
						}
						return layout.Inset{Top: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return p.cardHeight("Layout-9").Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.layoutPauseInactive(gtx, l) }, defaultCardPadding)
						})
					}),
				)
			}
		case settingsPremium:
			content = func(gtx layout.Context) layout.Dimensions { return p.layoutPremium(gtx, l) }
		case settingsIntegrations:
			content = func(gtx layout.Context) layout.Dimensions { return p.layoutIntegrations(gtx, l) }
		case settingsDevices:
			content = func(gtx layout.Context) layout.Dimensions { return p.sessions.Layout(gtx, l) }
		case settingsChats:
			content = func(gtx layout.Context) layout.Dimensions { return p.layoutChats(gtx, l) }
		case settingsAbout:
			content = func(gtx layout.Context) layout.Dimensions { return p.about.Layout(gtx, l) }
		case settingsProxy:
			content = func(gtx layout.Context) layout.Dimensions {
				return p.cardHeight("layoutProxy").Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.proxy.Layout(gtx, l) }, defaultCardPadding)
			}
		case settingsNotify:
			// The accounts are as the main page last listed them.
			content = func(gtx layout.Context) layout.Dimensions { return p.notifyView.Layout(gtx, l, len(p.shownAccounts)) }
		default:
			content = func(gtx layout.Context) layout.Dimensions {
				return p.layoutMain(gtx, mode, dark, l)
			}
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			header,
			vspace(16),
			layout.Rigid(content),
		)
	})
	p.layingOut = false
	p.followLogOut(gtx)
	if p.section == settingsShortcuts && p.shortcuts != nil {
		p.shortcuts.Overlay(gtx, l)
	}
	return dims
}

func (p *settingsPage) isPrivate() bool {
	return p.private != nil && p.private()
}

// layoutVisualPrivacy draws the switch of visual privacy mode. The mode may
// have changed in another window since the last frame; the switch follows.
func (p *settingsPage) layoutVisualPrivacy(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	if on := p.isPrivate(); on != (len(p.visual.GetValues()) == 1) {
		if on {
			p.visual.SetValues([]string{"visual"})
		} else {
			p.visual.SetValues(nil)
		}
	}
	streamer := p.streamer != nil && appwindow.CaptureExclusionSupported
	if streamer && p.streamer() != (len(p.streamerMode.GetValues()) == 1) {
		if p.streamer() {
			p.streamerMode.SetValues([]string{"streamer"})
		} else {
			p.streamerMode.SetValues(nil)
		}
	}
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.visual.Layout(gtx, map[string]string{"visual": l.T("privacy.visual")})
		}),
		vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("privacy.visual_body"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
	}
	if streamer {
		children = append(children, vspace(8),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return p.streamerMode.Layout(gtx, map[string]string{"streamer": l.T("privacy.streamer")})
			}),
			vspace(4),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("privacy.streamer_body"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
			}),
		)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (p *settingsPage) layoutMain(gtx layout.Context, mode themeMode, dark bool, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	themes := themeLabels(l)
	titles := settingsTitles(l)
	themeText := themes[mode]
	if mode == themeAuto {
		themeText = l.T("settings.auto_light")
		if dark {
			themeText = l.T("settings.auto_dark")
		}
	}
	animationsText := l.T("settings.animations") + ": " + motionShort(l)[p.motion.Mode()]
	if !p.motion.AnimationsEnabled() {
		animationsText += ", " + l.T("settings.now_off")
	}
	securityText := l.T("security.off")
	if p.security != nil && p.security.manager != nil {
		state := p.security.manager.State()
		switch {
		case !state.Available:
			securityText = l.T("security.unavailable_short")
		case state.Enabled:
			securityText = l.T("security.on_short")
		}
	}
	subtitles := map[settingsSection]string{
		settingsAppearance:   l.T("settings.theme") + ": " + themeText,
		settingsPrivacy:      privacyText(securityText, p.isPrivate(), l),
		settingsPower:        animationsText,
		settingsPremium:      p.premiumText(l),
		settingsIntegrations: p.players.subtitle(l),
		settingsDevices:      l.T("settings.devices_hint"),
		settingsChats:        p.chats.subtitle(l),
		settingsNotify:       p.notifyView.subtitle(l),
		settingsMenuOrder:    l.T("menus.subtitle"),
		settingsShortcuts:    l.T("keys.subtitle"),
		settingsVisibility:   l.T("settings.visibility_hint"),
	}
	if p.proxy != nil {
		subtitles[settingsProxy] = p.proxy.Summary(l)
	}
	if n := p.sessions.count(); n > 0 {
		subtitles[settingsDevices] = l.Count("sessions.count", n, nil)
	}
	p.shownAccounts = nil
	if p.accounts != nil {
		p.shownAccounts = p.accounts.All()
	}
	currentID := p.accountID()
	if currentID == "" && len(p.shownAccounts) == 1 {
		currentID = p.shownAccounts[0].ID
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.cardHeight("layoutMain-1").Card(gtx, func(gtx layout.Context) layout.Dimensions {
				var children []layout.FlexChild
				if len(p.shownAccounts) == 0 {
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: 14, Right: 14, Top: 10, Bottom: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return label(gtx, l.T("settings.no_accounts"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
						})
					}))
				}
				for _, account := range p.shownAccounts {
					row := p.accountRows[account.ID]
					if row == nil {
						row = new(settingsItem)
						p.accountRows[account.ID] = row
					}
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						name := account.Name
						if name == "" {
							name = l.T("settings.telegram_account")
						}
						row.badge = nil
						if account.Premium || p.isLocalPremium() {
							row.badge = func(gtx layout.Context) layout.Dimensions {
								px := gtx.Dp(18)
								return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions {
									return p.layoutPremiumMark(gtx, 18)
								})
							}
						}
						return row.layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return p.layoutAccountAvatar(gtx, account, name)
						}, gtx.Dp(accountAvatarSize), name, accountSubtitle(account, currentID, p.isPrivate(), l))
					}))
				}
				if p.accounts != nil {
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						// The icon takes an avatar's place, so that the text lines up with the names.
						slot, iconPx := gtx.Dp(accountAvatarSize), gtx.Dp(24)
						return p.addAccount.layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return offset(gtx, image.Pt((slot-iconPx)/2, (slot-iconPx)/2), func(gtx layout.Context) layout.Dimensions {
								return exact(gtx, image.Pt(iconPx, iconPx), func(gtx layout.Context) layout.Dimensions {
									return iconAddAccount(gtx, sc.Primary.Color)
								})
							})
						}, slot, l.T("settings.add_account"), l.T("settings.add_account_hint"))
					}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
			}, 6)
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.cardHeight("layoutMain-2").Card(gtx, func(gtx layout.Context) layout.Dimensions {
				var rows []layout.FlexChild
				sections := []settingsSection{settingsAppearance}
				if p.menus != nil {
					sections = append(sections, settingsMenuOrder)
				}
				if p.shortcuts != nil {
					sections = append(sections, settingsShortcuts)
				}
				if p.chats.available() || p.composerStyle != nil || p.lookView.look != nil {
					sections = append(sections, settingsChats)
				}
				if p.hasVisibility() {
					sections = append(sections, settingsVisibility)
				}
				if p.notifyView.get != nil {
					sections = append(sections, settingsNotify)
				}
				sections = append(sections, settingsPrivacy, settingsPower)
				if p.proxy != nil {
					sections = append(sections, settingsProxy)
				}
				if p.sessions != nil {
					sections = append(sections, settingsDevices)
				}
				sections = append(sections, settingsIntegrations)
				if p.premium != nil {
					sections = append([]settingsSection{settingsPremium}, sections...)
				}
				for _, section := range sections {
					rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.items[section].Layout(gtx, settingsIcons[section], titles[section], subtitles[section])
					}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
			}, 6)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if p.accounts == nil || currentID == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.layoutLogOut(gtx, l)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			dims := layout.Inset{Top: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.cardHeight("about").Card(gtx, func(gtx layout.Context) layout.Dimensions {
					return p.items[settingsAbout].Layout(gtx, iconInfo, l.T("settings.about"), deviceinfo.Platform())
				}, 6)
			})
			p.belowLogOut = dims.Size.Y
			return dims
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if p.uninstall == nil {
				return layout.Dimensions{}
			}
			dims := layout.Inset{Top: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.cardHeight("uninstall").Card(gtx, func(gtx layout.Context) layout.Dimensions {
					return p.uninstallItem.Layout(gtx, iconUninstall, l.T("uninstall.settings"), l.T("uninstall.settings_hint"))
				}, 6)
			})
			p.belowLogOut += dims.Size.Y
			return dims
		}),
	)
}

// followLogOut scrolls the main page, while the confirmation of logging
// out opens, so that its end is in view; done once it has opened, the page
// is the user's to scroll again. Where the card ends is the page's length
// less what follows it and the page's inset under it.
func (p *settingsPage) followLogOut(gtx layout.Context) {
	if !p.revealLogOut {
		return
	}
	card := p.cardHeight("logout")
	if p.section != settingsMain || !p.confirmingLogOut {
		p.revealLogOut = false
		return
	}
	bottom := p.list.Position.Length - p.belowLogOut - gtx.Dp(24)
	// With the space before the next card under it, its corners in view.
	if over := bottom + gtx.Dp(12) - (p.list.Position.Offset + gtx.Constraints.Max.Y); over > 0 {
		p.list.Position.Offset += over
		gtx.Execute(op.InvalidateCmd{})
	}
	if card.initialized && card.value >= card.target {
		p.revealLogOut = false
	}
}

// layoutLogOut draws the row that leaves the window's account and, once it
// is clicked, what leaving does with two buttons to confirm or cancel.
func (p *settingsPage) layoutLogOut(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	if !p.confirmingLogOut {
		return p.cardHeight("logout").Card(gtx, func(gtx layout.Context) layout.Dimensions {
			return p.logOut.Layout(gtx, iconLogOut, l.T("account.logout"), l.T("account.logout_hint"))
		}, 6)
	}
	return p.cardHeight("logout").Card(gtx, func(gtx layout.Context) layout.Dimensions {
		if p.loggingOut {
			p.confirmLogOut.Disable()
		} else {
			p.confirmLogOut.Enable()
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("account.logout")+"?", token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
			}),
			vspace(8),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("account.logout_confirm"), token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
			}),
			vspace(8),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("account.logout_offline"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
			}),
			vspace(16),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Spacing: layout.SpaceStart, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.loggingOut {
							return layout.Dimensions{}
						}
						return p.cancelLogOut.Layout(gtx, l.T("account.cancel"))
					}),
					layout.Rigid(layout.Spacer{Width: 8}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						text := l.T("account.logout")
						if p.loggingOut {
							text = l.T("account.logging_out")
						}
						return p.confirmLogOut.Layout(gtx, text)
					}),
				)
			}),
		)
	}, defaultCardPadding)
}

func (p *settingsPage) layoutAppearance(gtx layout.Context, mode themeMode, system appearance.Scheme, dark bool, l localization.Catalog) layout.Dimensions {
	hint := l.T("settings.system_light")
	switch system {
	case appearance.Dark:
		hint = l.T("settings.system_dark")
	case appearance.Unknown:
		hint = l.T("settings.system_unknown")
	}
	cards := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return settingsChoiceCard(gtx, p.cardHeight("choice-theme"), l.T("settings.theme"), hint, func(gtx layout.Context) layout.Dimensions {
				return p.theme.Layout(gtx, radio.LeadingKind, themeLabels(l))
			})
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return settingsChoiceCard(gtx, p.cardHeight("choice-language"), l.T("settings.language"), "", func(gtx layout.Context) layout.Dimensions {
				return p.language.Layout(gtx, radio.LeadingKind, languageNames())
			})
		}),
	}
	if p.composerBlur != nil {
		cards = append(cards, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.layoutOverlays(gtx, l) }))
	}
	if p.overlays != nil {
		cards = append(cards, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.layoutShadow(gtx, l) }))
	}
	if p.emojiView.available() {
		cards = append(cards, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.cardHeight("layoutAppearance-1").Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.emojiView.Layout(gtx, l) }, defaultCardPadding)
		}))
	}
	if p.fontsView.files != nil {
		cards = append(cards, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.cardHeight("layoutAppearance-2").Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.fontsView.Layout(gtx, l) }, defaultCardPadding)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, cards...)
}

// layoutChats draws the section of the chats: their theme and wallpaper,
// the composer and how messages look.
func (p *settingsPage) layoutChats(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	var cards []layout.FlexChild
	if p.chats.available() {
		cards = append(cards, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.chats.Layout(gtx, l) }), vspace(12))
	}
	if p.composerStyle != nil {
		cards = append(cards, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return settingsChoiceCard(gtx, p.cardHeight("choice-composer"), l.T("settings.composer"), "", func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.composer.Layout(gtx, radio.LeadingKind, map[preferences.ComposerStyle]string{
							preferences.ComposerClassic:  l.T("settings.composer_classic"),
							preferences.ComposerFloating: l.T("settings.composer_floating"),
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.confirmations == nil {
							return layout.Dimensions{}
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: 8, Left: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return label(gtx, l.T("settings.confirm"), token.TypestyleTitleSmall, scheme(gtx).Primary.Color, 1)
								})
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return p.confirmBoxes.Layout(gtx, map[string]string{"sticker": l.T("settings.confirm_sticker"), "gif": l.T("settings.confirm_gif")})
							}),
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.input == nil {
							return layout.Dimensions{}
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: 8, Left: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return label(gtx, l.T("settings.input"), token.TypestyleTitleSmall, scheme(gtx).Primary.Color, 1)
								})
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return p.inputBoxes.Layout(gtx, map[string]string{"focus": l.T("settings.input_manual_focus"), "picker": l.T("settings.input_picker_click")})
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: 4, Right: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return label(gtx, l.T("settings.input_hint"), token.TypestyleBodyMedium, scheme(gtx).SurfaceVariant.OnColor, 0)
								})
							}),
						)
					}),
				)
			})
		}))
	}
	if p.lookView.look != nil {
		cards = append(cards, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.cardHeight("layoutChats-1").Card(gtx, func(gtx layout.Context) layout.Dimensions { return p.lookView.Layout(gtx, l) }, defaultCardPadding)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, cards...)
}

// layoutPauseInactive draws the card of the switch that pauses media
// animations while the window is not active.
func (p *settingsPage) layoutPauseInactive(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.pauseInactiveCheckbox.Layout(gtx, map[string]string{"pause": l.T("settings.pause_inactive")})
		}),
		vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("settings.pause_inactive_hint"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
	)
}

// blurAvailable reports whether the blur checkboxes have an effect: it costs
// power, so it is off while animations are.
func (p *settingsPage) blurAvailable() bool {
	return p.motion.AnimationsEnabled()
}

// layoutOverlays draws the card of the overlays: the transparency of them
// all, and which of them blur what is behind them.
func (p *settingsPage) layoutOverlays(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	transparency := 0
	if p.overlays != nil {
		transparency = p.overlays().Transparency
	}
	var hint string
	switch {
	case !p.motion.AnimationsEnabled():
		hint = l.T("settings.composer_blur_off")
	case p.composerStyle != nil && p.composerStyle() != preferences.ComposerFloating:
		hint = l.T("settings.overlays_classic")
	}
	return p.cardHeight("layoutOverlays-1").Card(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("settings.overlays"), token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
			}),
			vspace(8),
		}
		if p.overlays != nil {
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("settings.overlays_transparency")+": "+strconv.Itoa(transparency)+"%", token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
				}),
				layout.Rigid(p.transparency.Layout),
				vspace(4),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("settings.overlays_transparency_hint"), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
				}),
				vspace(8),
			)
		}
		if p.windowTransparency != nil {
			children = append(children, vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("settings.window_transparency")+": "+strconv.Itoa(p.windowTransparency())+"%", token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if p.windowTransparencyAvailable != nil && !p.windowTransparencyAvailable() {
						gtx = gtx.Disabled()
					}
					return p.windowTransparencySlider.Layout(gtx)
				}),
				vspace(4),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("settings.window_transparency_hint"), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
				}),
			)
		}
		children = append(children, vspace(8))
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.blur.Layout(gtx, map[string]string{
				"composer": l.T("settings.composer_blur"),
				"menus":    l.T("settings.menus_blur"),
				"toasts":   l.T("settings.toasts_blur"),
			})
		}))
		if hint != "" {
			children = append(children, vspace(4), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, hint, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
			}))
		}
		if p.windowBlur != nil {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return p.windowBlurCheckbox.Layout(gtx, map[string]string{"window": l.T("settings.window_blur")})
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}, defaultCardPadding)
}

// shadowStep is the difference between the stops of the shadow's slider, in
// percent.
const shadowStep = 25

// layoutShadow draws the card of the shadows under dialogs and menus: how
// dark they are.
func (p *settingsPage) layoutShadow(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	return p.cardHeight("layoutShadow-1").Card(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("settings.shadow"), token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
			}),
			vspace(8),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("settings.shadow_intensity")+": "+strconv.Itoa(p.overlays().Shadow)+"%", token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
			}),
			layout.Rigid(p.shadow.Layout),
			vspace(4),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("settings.shadow_hint"), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
			}),
		)
	}, defaultCardPadding)
}

// settingsChoiceCard draws a card with a title, the choices and, if set, a
// hint below them.
func settingsChoiceCard(gtx layout.Context, height *heightTransition, title, hint string, choices layout.Widget) layout.Dimensions {
	sc := scheme(gtx)
	return height.Card(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, title, token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
			}),
			vspace(8),
			layout.Rigid(choices),
		}
		if hint != "" {
			children = append(children, vspace(4), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, hint, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}, defaultCardPadding)
}

const (
	settingsItemHeight = unit.Dp(64)
	accountAvatarSize  = unit.Dp(40)
)

// privacyText is the subtitle of the privacy section.
func privacyText(security string, private bool, l localization.Catalog) string {
	if private {
		return security + " · " + l.T("privacy.visual_on")
	}
	return security
}

// accountSubtitle says who the account is and what a click on it does.
func accountSubtitle(account model.AccountInfo, currentID string, private bool, l localization.Catalog) string {
	// Visual privacy leaves the name only: the list is not the place to
	// uncover the phone number or the username one by one.
	who := account.Phone
	if account.Username != "" {
		who = "@" + account.Username
	}
	if private {
		who = ""
	}
	action := l.T("settings.open_new")
	if account.ID == currentID {
		action = l.T("settings.current")
	} else if account.Open {
		action = l.T("settings.show_open")
	}
	if who == "" {
		return action
	}
	return who + " · " + action
}

// layoutAccountAvatar draws the account's profile photo, or its initials
// until the photo is known.
func (p *settingsPage) layoutAccountAvatar(gtx layout.Context, account model.AccountInfo, name string) layout.Dimensions {
	if account.Avatar == nil || p.images == nil {
		return avatar(gtx, account.UserID, model.KindUser, name, accountAvatarSize)
	}
	size := image.Pt(gtx.Dp(accountAvatarSize), gtx.Dp(accountAvatarSize))
	defer avatarShape(gtx, size).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(size)
	widget.Image{Src: p.images.Op(account.Avatar), Fit: widget.Cover}.Layout(gtx)
	return layout.Dimensions{Size: size}
}

// settingsItem is a row of the main settings page that opens a section.
type settingsItem struct {
	surface
	// badge, if set, is drawn after the title, such as the Premium star.
	badge layout.Widget
}

func (it *settingsItem) Layout(gtx layout.Context, icon wdk.IconWidget, title, subtitle string) layout.Dimensions {
	sc := scheme(gtx)
	iconPx := gtx.Dp(24)
	return it.layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return exact(gtx, image.Pt(iconPx, iconPx), func(gtx layout.Context) layout.Dimensions {
			return icon(gtx, sc.Primary.Color)
		})
	}, iconPx, title, subtitle)
}

// layout draws the row with leading, a square of leadingPx, at its start.
func (it *settingsItem) layout(gtx layout.Context, leading layout.Widget, leadingPx int, title, subtitle string) layout.Dimensions {
	return it.layoutWith(gtx, leading, leadingPx, title, func(gtx layout.Context) layout.Dimensions {
		return label(gtx, subtitle, token.TypestyleBodySmall, scheme(gtx).SurfaceVariant.OnColor, 1)
	})
}

// layoutWith is layout with a subtitle of its own, as a richLabel, in the
// width it is given.
func (it *settingsItem) layoutWith(gtx layout.Context, leading layout.Widget, leadingPx int, title string, subtitle layout.Widget) layout.Dimensions {
	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(settingsItemHeight))
	style := surfaceStyle{radius: gtx.Dp(12), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor}
	return it.surface.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		pad := gtx.Dp(14)
		iconPx := gtx.Dp(24)
		offset(gtx, image.Pt(pad, (size.Y-leadingPx)/2), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(leadingPx, leadingPx))
			return leading(gtx)
		})
		offset(gtx, image.Pt(size.X-pad-iconPx, (size.Y-iconPx)/2), func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(iconPx, iconPx), func(gtx layout.Context) layout.Dimensions {
				return iconChevron(gtx, sc.SurfaceVariant.OnColor)
			})
		})
		textX := pad + leadingPx + gtx.Dp(16)
		textGtx := gtx
		textGtx.Constraints = layout.Constraints{Max: image.Pt(max(size.X-textX-2*pad-iconPx, 0), size.Y)}
		macro := op.Record(gtx.Ops)
		titleDims := withBadges(textGtx, nil, func(gtx layout.Context) layout.Dimensions {
			return label(gtx, title, token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
		}, it.badge)
		titleCall := macro.Stop()
		macro = op.Record(gtx.Ops)
		subDims := subtitle(textGtx)
		subCall := macro.Stop()
		top := (size.Y - titleDims.Size.Y - subDims.Size.Y) / 2
		offset(gtx, image.Pt(textX, top), func(gtx layout.Context) layout.Dimensions {
			titleCall.Add(gtx.Ops)
			return titleDims
		})
		offset(gtx, image.Pt(textX, top+titleDims.Size.Y), func(gtx layout.Context) layout.Dimensions {
			subCall.Add(gtx.Ops)
			return subDims
		})
		return layout.Dimensions{Size: size}
	})
}

// premiumText is the subtitle of the Premium section.
func (p *settingsPage) premiumText(l localization.Catalog) string {
	switch {
	case p.premium != nil && p.premium.Premium().Active:
		return l.T("premium.on")
	case p.isLocalPremium():
		return l.T("premium.on_local")
	}
	return l.T("premium.off")
}

// layoutPremiumMark draws the Premium star in a square of size, nothing
// where there is no window to take it from.
func (p *settingsPage) layoutPremiumMark(gtx layout.Context, size unit.Dp) layout.Dimensions {
	if p.premiumMark != nil {
		return p.premiumMark(gtx, size)
	}
	return layout.Dimensions{}
}

// isLocalPremium reports whether Local Premium is on.
func (p *settingsPage) isLocalPremium() bool {
	return p.localPremium != nil && p.localPremium()
}

// layoutPremium draws the Premium section: whether the account has it, how
// to get it, and the limits it raises as the server sets them.
func (p *settingsPage) layoutPremium(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	premium := p.premium.Premium()
	status := l.T("premium.inactive")
	if premium.Active {
		status = l.T("premium.active")
	}
	// Local Premium shows the star but changes nothing Telegram knows: an
	// account without Premium keeps its limits, and the offer to subscribe.
	local := p.isLocalPremium() && !premium.Active
	if local {
		status = l.T("premium.local_active")
	}
	if p.localPremiumToggle != nil {
		if want := p.isLocalPremium(); want != (len(p.localPremiumToggle.GetValues()) == 1) {
			if want {
				p.localPremiumToggle.SetValues([]string{"local"})
			} else {
				p.localPremiumToggle.SetValues(nil)
			}
		}
	}
	if p.premiumMediaToggle != nil && p.premiumMedia != nil {
		if want := p.premiumMedia(); want != (len(p.premiumMediaToggle.GetValues()) == 1) {
			if want {
				p.premiumMediaToggle.SetValues([]string{"media"})
			} else {
				p.premiumMediaToggle.SetValues(nil)
			}
		}
	}
	var offer []layout.FlexChild
	switch {
	case premium.Active:
	case premium.Purchasable && premium.Bot != "":
		offer = append(offer, vspace(16), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.subscribe.Layout(gtx, fmt.Sprintf(l.T("premium.subscribe"), premium.Bot))
		}))
	default:
		offer = append(offer, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("premium.blocked"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}))
	}
	head := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			title := l.T("premium.off")
			switch {
			case premium.Active:
				title = l.T("premium.on")
			case local:
				title = l.T("premium.on_local")
			}
			return withBadges(gtx, nil, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, title, token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
			}, func(gtx layout.Context) layout.Dimensions {
				px := gtx.Dp(20)
				return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions {
					return p.layoutPremiumMark(gtx, 20)
				})
			})
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, status, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
	}
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("premium.limits"), token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
		}),
		vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("premium.limits_note"), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
		}),
		vspace(8),
	}
	for _, limit := range premium.Limits {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// The value the account has now is the emphasized one.
			without, with := sc.Surface.OnColor, sc.SurfaceVariant.OnColor
			if premium.Active && !local {
				without, with = sc.SurfaceVariant.OnColor, sc.Primary.Color
			}
			return layout.Inset{Top: 6, Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return label(gtx, l.T("premium.limit."+limit.Key), token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, limitValue(limit.Key, limit.Default, l), token.TypestyleBodyLarge, without, 1)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, " · ", token.TypestyleBodyLarge, sc.SurfaceVariant.OnColor, 1)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, limitValue(limit.Key, limit.Premium, l), token.TypestyleLabelLargeEmphasized, with, 1)
					}),
				)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.cardHeight("layoutPremium-1").Card(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, append(head, offer...)...)
			}, defaultCardPadding)
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// A lever for those who have Premium, and only where it can act: the
			// address is the WebSocket's, and Telegram's own. Nothing is known of
			// what it changes.
			if !premium.Active || !p.proxy.usesTelegramWebSocket() || p.premiumMediaToggle == nil || p.setPremiumMedia == nil {
				return layout.Dimensions{}
			}
			return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.cardHeight("layoutPremium-4").Card(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return p.premiumMediaToggle.Layout(gtx, map[string]string{"media": l.T("premium.media")})
						}),
						vspace(4),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return label(gtx, l.T("premium.media_body"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
						}),
					)
				}, defaultCardPadding)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if p.localPremiumToggle == nil || p.setLocalPremium == nil {
				return layout.Dimensions{}
			}
			return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.cardHeight("layoutPremium-2").Card(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return p.localPremiumToggle.Layout(gtx, map[string]string{"local": l.T("premium.local")})
						}),
						vspace(4),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return label(gtx, l.T("premium.local_body"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
						}),
					)
				}, defaultCardPadding)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.cardHeight("layoutPremium-3").Card(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
			}, defaultCardPadding)
		}),
	)
}

// limitValue shows a limit's value: the upload limit in gigabytes as
// Telegram counts them, 1000 MiB each (4000 parts are "2 GB"), the others as
// they are.
func limitValue(key string, value int, l localization.Catalog) string {
	if key != "upload_max_fileparts" {
		return strconv.Itoa(value)
	}
	gb := float64(value) * model.UploadPartSize / (1 << 20) / 1000
	number := strings.Replace(strconv.FormatFloat(math.Round(gb*10)/10, 'f', -1, 64), ".", l.T("number.decimal"), 1)
	return fmt.Sprintf(l.T("premium.gb"), number)
}

func (p *settingsPage) cardHeight(key string) *heightTransition {
	if p.cardHeights == nil {
		p.cardHeights = make(map[string]*heightTransition)
	}
	if p.cardHeights[key] == nil {
		p.cardHeights[key] = new(heightTransition)
	}
	return p.cardHeights[key]
}
