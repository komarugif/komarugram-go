// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"log"
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
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/messenger/security"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
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
)

func settingsTitles(l localization.Catalog) map[settingsSection]string {
	return map[settingsSection]string{
		settingsMain: l.T("settings.title"), settingsAppearance: l.T("settings.appearance"),
		settingsPrivacy: l.T("settings.privacy"), settingsPower: l.T("settings.power"),
		settingsPremium: l.T("premium.title"), settingsIntegrations: l.T("settings.integrations"),
		settingsDevices: l.T("settings.devices"), settingsChats: l.T("settings.chats"),
		settingsNotify: l.T("settings.notify"),
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
	cardHeights map[string]*heightTransition
	scrollPage
	section settingsSection
	// layingOut stays set on panic, identifying the failed page to RecoverFrame.
	layingOut bool
	items     map[settingsSection]*settingsItem
	back      *button.Button

	motion      *motion.Settings
	miniapps    *miniappprefs.Settings
	theme       *radio.Radios[themeMode]
	language    *radio.Radios[string]
	animations  *motion.View
	privacy     *miniappprefs.View
	security    *securityView
	accounts    model.Accounts
	accountID   func() string
	accountRows map[string]*settingsItem
	addAccount  settingsItem
	// logOut asks to leave the window's account; confirmLogOut does it.
	logOut           settingsItem
	confirmingLogOut bool
	// uninstallItem opens the uninstaller through uninstall, which is nil
	// where the program does not install itself.
	uninstallItem settingsItem
	uninstall     func()
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
	// without them the section is hidden.
	ghost     func() preferences.Ghost
	setGhost  func(preferences.Ghost)
	ghostOpts *toggle.Toggle[string]
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
	// premium is the account's Premium; nil hides the section.
	premium model.PremiumSource
	// localPremium and setLocalPremium read and switch Local Premium; the
	// toggle shows it.
	localPremium       func() bool
	setLocalPremium    func(bool)
	localPremiumToggle *toggle.Toggle[string]
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
	animations := motion.NewView(m, motion.Russian)
	animations.TitleStyle = token.TypestyleTitleMedium
	privacy := miniappprefs.NewView(miniapps, miniappprefs.Russian)
	privacy.TitleStyle = token.TypestyleTitleMedium
	p := &settingsPage{
		items: map[settingsSection]*settingsItem{
			settingsAppearance:   new(settingsItem),
			settingsPrivacy:      new(settingsItem),
			settingsPower:        new(settingsItem),
			settingsPremium:      new(settingsItem),
			settingsIntegrations: new(settingsItem),
			settingsDevices:      new(settingsItem),
			settingsChats:        new(settingsItem),
			settingsNotify:       new(settingsItem),
		},
		back:          button.Text(),
		motion:        m,
		miniapps:      miniapps,
		theme:         radio.NewRadios([]themeMode{themeAuto, themeLight, themeDark}, mode, setMode),
		language:      radio.NewRadios([]string{"ru", "en"}, language, setLanguage),
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
	p.ghostOpts = toggle.NewToggle(ghostOptions, nil, func(values []string) {
		if p.setGhost != nil {
			p.setGhost(ghostFromOptions(values))
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
	p.transparency = slider.StandardSlider(steps, 30, func(v int) {
		if p.overlays != nil && p.setOverlays != nil {
			o := p.overlays()
			o.Transparency = v
			p.setOverlays(o)
		}
	})
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
	if language == "en" {
		p.animations.SetStrings(motion.English)
		p.privacy.SetStrings(miniappprefs.English)
	} else {
		p.animations.SetStrings(motion.Russian)
		p.privacy.SetStrings(miniappprefs.Russian)
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
		p.confirmingLogOut = true
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
		}
	}
	if p.back.Clicked(gtx) {
		p.open()
	}
	if p.subscribe.Clicked(gtx) && p.premium != nil {
		if bot := p.premium.Premium().Bot; bot != "" {
			go func() {
				if err := openBrowser("https://t.me/" + bot); err != nil {
					log.Printf("open @%s: %v", bot, err)
				}
			}()
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
	p.animations.Update(gtx)
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
		p.filtersView.Update(gtx)
	}
	if p.section == settingsAppearance {
		p.emojiView.Update(gtx)
		p.fontsView.Update(gtx)
	}
	if p.section == settingsChats {
		p.lookView.Update(gtx)
		p.chats.Update(gtx)
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
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.cardHeight("Layout-7").Card(gtx, p.privacy.Layout, defaultCardPadding)
					}),
				)
			}
		case settingsPower:
			content = func(gtx layout.Context) layout.Dimensions {
				return p.cardHeight("Layout-8").Card(gtx, p.animations.Layout, defaultCardPadding)
			}
		case settingsPremium:
			content = func(gtx layout.Context) layout.Dimensions { return p.layoutPremium(gtx, l) }
		case settingsIntegrations:
			content = func(gtx layout.Context) layout.Dimensions { return p.layoutIntegrations(gtx, l) }
		case settingsDevices:
			content = func(gtx layout.Context) layout.Dimensions { return p.sessions.Layout(gtx, l) }
		case settingsChats:
			content = func(gtx layout.Context) layout.Dimensions { return p.layoutChats(gtx, l) }
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
									return layoutPremiumStar(gtx, 18)
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
				if p.chats.available() || p.composerStyle != nil || p.lookView.look != nil {
					sections = append(sections, settingsChats)
				}
				if p.notifyView.get != nil {
					sections = append(sections, settingsNotify)
				}
				sections = append(sections, settingsPrivacy, settingsPower)
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
			if p.uninstall == nil {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.cardHeight("uninstall").Card(gtx, func(gtx layout.Context) layout.Dimensions {
					return p.uninstallItem.Layout(gtx, iconUninstall, l.T("uninstall.settings"), l.T("uninstall.settings_hint"))
				}, 6)
			})
		}),
	)
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
				return p.language.Layout(gtx, radio.LeadingKind, map[string]string{"ru": "Русский", "en": "English"})
			})
		}),
	}
	if p.composerBlur != nil {
		cards = append(cards, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.layoutOverlays(gtx, l) }))
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
		subDims := label(textGtx, subtitle, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
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
					return layoutPremiumStar(gtx, 20)
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
	number := strconv.FormatFloat(math.Round(gb*10)/10, 'f', -1, 64)
	if l.Language() == localization.Russian {
		number = strings.Replace(number, ".", ",", 1)
	}
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
