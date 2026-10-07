// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"komarugram/internal/diagnostics"
	"log"
	"math"
	"slices"
	"sync/atomic"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/exp/powersave"
	"gio-mw/token"
	"gio-mw/widget/button"
	"gio-mw/widget/overlay"

	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"gio-mw/exp/appearance"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/emojipacks"
	"komarugram/internal/messenger/formula"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/messenger/security"
	"komarugram/internal/miniappprefs"
	"komarugram/pkg/miniapp"
	"komarugram/pkg/player"
)

// App is the messenger window content.
type App struct {
	images imageOps
	window *appwindow.Window
	store  model.Store

	windowEffectsSet, windowBlurWanted, windowTransparentWanted bool

	preferences *preferences.Store
	// ownUsers are the users of the accounts signed in here, which Local
	// Premium marks.
	ownUsers atomic.Pointer[map[int64]bool]
	// focused is set while the window has the focus.
	focused bool
	// shownChat and shownFocus tell other goroutines the open chat and the
	// focus (see Showing); openChat is a chat to open (see OpenChat).
	shownChat  atomic.Int64
	shownFocus atomic.Bool
	openChat   atomic.Int64
	// filter hides messages as the settings ask.
	filter     *messageFilter
	lightTheme *token.Theme
	darkTheme  *token.Theme
	// themeFonts is the version of the fonts the themes were made with.
	themeFonts uint64
	// emojiPacks keeps the emoji packs installed, beside the settings.
	emojiPacks *emojipacks.Store

	section section
	// beforeSearch is the section search was opened from; opening search
	// again goes back there.
	beforeSearch section
	selected     int64 // Open chat, 0 when none.
	// found is the open chat when a search found it outside the chat list.
	found model.Chat

	sidebar  *sidebar
	chats    *chatList
	splitter splitter

	// When the window is too small for the sidebar, its items move to a
	// drawer opened by the menu button, and the folders to a bar over the
	// chat list.
	compact   bool
	menu      *button.Button
	drawer    *drawer
	folderBar *folderBar
	overlay   *overlay.Overlay
	// listRequest is the chat list width the user asked for by dragging;
	// the shown width follows from it and the window size.
	listRequest unit.Dp
	profile     *profilePage
	avatars     *avatarImages
	info        *chatInfo
	themes      *chatThemeController
	history     *chatPage
	// comments is the page of the comments to a channel post, which thread
	// shows over its channel while it is set.
	comments *chatPage
	thread   *commentsView
	// drop is a drag of files over the window.
	drop fileDrop
	// mini runs the Mini Apps of bots; nil when the store cannot ask for
	// them.
	mini *webApps
	// forum is the list of topics that shows in place of the history of a
	// forum; a topic opens as thread, like comments.
	forum  *forumPage
	viewer *photoViewer
	// openWindow is the host of the windows the viewer and articles open
	// in; photoWindows and articleWindows, their lists, which close with
	// this window and when it locks.
	openWindow     func(appwindow.Spec)
	photoWindows   photoWindows
	articleWindows articleWindows
	settings       *settingsPage
	// sessionEnded asks what to do once Telegram ended the session.
	sessionEnded *sessionEndedDialog
	// connectionFailed offers to connect again once the connection stopped.
	connectionFailed *connectionFailedDialog
	// frozen tells that Telegram froze the account.
	frozen *frozenView

	// signIn is the sign-in window shown instead of everything else until
	// the account is signed in; nil when no sign-in is needed.
	signIn       *loginPage
	security     *securityView
	windowLocked *atomic.Bool
	visualLock   *visualLockView
	lastInput    time.Time
	unsubscribe  []func()
}

// Services are process-wide dependencies shared by every account window.
type Services struct {
	Preferences *preferences.Store
	MiniApps    *miniappprefs.Settings
	Accounts    model.Accounts
	AccountID   string
	// CurrentAccount, if set, replaces AccountID for a window whose account
	// is not known when it opens: a sign-in window.
	CurrentAccount func() string
	Security       *security.Manager
	WindowLocked   *atomic.Bool
	// OpenWindow opens another window in this process, such as a photo
	// viewer of its own. Without it, the viewer offers no such button.
	OpenWindow func(appwindow.Spec)
}

// New creates the messenger UI.
func New(w *appwindow.Window, store model.Store, services Services) *App {
	if services.Preferences == nil {
		services.Preferences = preferences.Memory()
	}
	if services.MiniApps == nil {
		services.MiniApps = miniappprefs.New(services.Preferences.Global().MiniAppStorage)
	}
	a := &App{
		window:       w,
		store:        store,
		preferences:  services.Preferences,
		sidebar:      newSidebar(),
		chats:        newChatList(),
		menu:         button.Text(),
		drawer:       newDrawer(),
		folderBar:    newFolderBar(),
		overlay:      &overlay.Overlay{},
		listRequest:  listDefaultWidth,
		windowLocked: services.WindowLocked,
		visualLock:   newVisualLockView(services.Security, w.Invalidate),
	}
	editor, _ := store.(model.ProfileEditor)
	a.profile = newProfilePage(editor)
	a.profile.premium, _ = store.(model.PremiumSource)
	a.profile.badges = a.badges
	a.chats.badges = a.badges
	a.chats.menu.store, _ = store.(model.ChatListActions)
	a.chats.menu.premium, _ = store.(model.PremiumSource)
	a.chats.menu.invalidate = w.Invalidate
	if searcher, ok := store.(model.Searcher); ok {
		a.chats.panel = newSearchPanel(searcher)
	}
	a.drawer.badges = a.badges
	global := services.Preferences.Global()
	w.Motion.SetMode(global.MotionMode)
	w.Motion.SetLowBattery(global.LowBattery)
	w.Motion.SetPreferenceChanged(func(mode powersave.Mode, lowBattery int) {
		if err := services.Preferences.SetMotion(mode, lowBattery); err != nil {
			log.Printf("save settings: %v", err)
		}
	})
	a.unsubscribe = append(a.unsubscribe, services.Preferences.Subscribe(func() {
		global := services.Preferences.Global()
		w.Motion.SetMode(global.MotionMode)
		w.Motion.SetLowBattery(global.LowBattery)
		services.MiniApps.SetStorage(global.MiniAppStorage)
		miniapp.SetBrowser(global.BrowserPath)
		applyFonts(global.Fonts, a.emojiPacks)
		if a.history != nil {
			a.history.audio.setSpeed(global.VoiceSpeed)
			a.history.audio.setVolume(float64(global.AudioVolume)/100, false)
		}
		title := localization.For(global.Language).T("app.title")
		if services.WindowLocked == nil || !services.WindowLocked.Load() {
			if name := store.Me().Name(); name != "" {
				title += " — " + name
			}
		}
		w.SetTitle(title)
		w.Invalidate()
	}))
	if services.Accounts != nil {
		a.refreshOwnUsers(services.Accounts)
		a.unsubscribe = append(a.unsubscribe, services.Accounts.Subscribe(func() {
			a.refreshOwnUsers(services.Accounts)
			w.Invalidate()
		}))
	}
	currentAccount := services.CurrentAccount
	if currentAccount == nil {
		id := services.AccountID
		currentAccount = func() string { return id }
	}
	a.initMiniApps(store, services.MiniApps.Storage, currentAccount)
	var leave func()
	if services.Accounts != nil {
		leave = func() {
			if id := currentAccount(); id != "" {
				services.Accounts.LogOut(id)
			}
		}
	}
	a.sessionEnded = newSessionEndedDialog(leave)
	a.connectionFailed = newConnectionFailedDialog()
	a.frozen = newFrozenView(store)
	a.profile.frozen = a.frozen
	a.profile.openAvatar = func(chat int64) {
		if a.viewer != nil {
			a.viewer.OpenProfile(chat)
		}
	}
	a.settings = newSettingsPage(w.Motion, services.MiniApps, services.Security, w.Invalidate, services.Accounts, currentAccount, themeMode(global.Theme), global.Language, a.setThemeMode, a.setLanguage)
	a.settings.security.SetPreferences(services.Preferences)
	a.settings.images = &a.images
	a.settings.private = a.private
	a.settings.premium, _ = store.(model.PremiumSource)
	a.settings.setPrivate = func(on bool) {
		if err := services.Preferences.SetVisualPrivacy(on); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.filtersView.filters = func() preferences.Filters { return a.preferences.Global().Filters }
	a.settings.filtersView.setFilters = func(f preferences.Filters) {
		if err := services.Preferences.SetFilters(f); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.lookView.look = func() preferences.Look { return a.preferences.Global().Look }
	a.settings.lookView.setLook = func(l preferences.Look) {
		if err := services.Preferences.SetLook(l); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.fontsView.files = func() preferences.Fonts { return a.preferences.Global().Fonts }
	a.settings.fontsView.setFiles = func(f preferences.Fonts) {
		if err := services.Preferences.SetFonts(f); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.emojiPacks = emojipacks.Open(emojiPackDir(services.Preferences))
	a.settings.emojiView.files = a.settings.fontsView.files
	a.settings.emojiView.setFiles = a.settings.fontsView.setFiles
	a.settings.emojiView.store = a.emojiPacks
	// The packs whose files are in Telegram's cloud are downloaded through
	// the window's account, which only reads the channel they are in. The
	// demo's store has no way there, and its settings do not offer them.
	a.settings.emojiView.source = emojipacks.SourceFromEnv()
	if files, ok := store.(interface {
		ChannelFile(ctx context.Context, username string, post int, progress func(done, total int64)) ([]byte, error)
	}); ok {
		a.settings.emojiView.source = emojipacks.WithTelegram(a.settings.emojiView.source, files.ChannelFile)
	}
	a.settings.emojiView.applied = func() {
		applyFonts(a.preferences.Global().Fonts, a.emojiPacks)
		w.Invalidate()
	}
	a.settings.keep = func() preferences.Keep { return a.preferences.Global().Keep }
	a.settings.setKeep = func(k preferences.Keep) {
		if err := services.Preferences.SetKeep(k); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.notifyView.get = func() preferences.Notify { return a.preferences.Global().Notify }
	a.settings.notifyView.set = func(n preferences.Notify) {
		if err := services.Preferences.SetNotify(n); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.ghost = func() preferences.Ghost { return a.preferences.Global().Ghost }
	a.settings.setGhost = func(g preferences.Ghost) {
		if err := services.Preferences.SetGhost(g); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.streamer = func() bool { return a.preferences.Global().StreamerMode }
	a.settings.setStreamer = func(on bool) {
		if err := services.Preferences.SetStreamerMode(on); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.localPremium = func() bool { return a.preferences.Global().LocalPremium }
	a.settings.setLocalPremium = func(on bool) {
		if err := services.Preferences.SetLocalPremium(on); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.confirmations = func() (bool, bool) {
		g := a.preferences.Global()
		return g.ConfirmSticker, g.ConfirmGIF
	}
	a.settings.setConfirmations = func(sticker, gif bool) {
		if err := services.Preferences.SetConfirmations(sticker, gif); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.composerStyle = a.composerStyle
	a.settings.setComposerStyle = func(style preferences.ComposerStyle) {
		if err := services.Preferences.SetComposer(style); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.composerBlur = func() bool { return a.preferences.Global().ComposerBlur }
	a.settings.setComposerBlur = func(on bool) {
		if err := services.Preferences.SetComposerBlur(on); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.windowBlur = func() bool { return a.preferences.Global().WindowBlur }
	a.settings.setWindowBlur = func(on bool) {
		if err := services.Preferences.SetWindowBlur(on); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.windowTransparency = func() int { return a.preferences.Global().WindowTransparency }
	a.settings.setWindowTransparency = func(value int) {
		if err := services.Preferences.SetWindowTransparency(value); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.windowTransparencyAvailable = func() bool {
		return w.CanBeTransparent()
	}
	a.settings.overlays = func() preferences.Overlays { return a.preferences.Global().Overlays }
	a.settings.setOverlays = func(o preferences.Overlays) {
		if err := services.Preferences.SetOverlays(o); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.chats.overlays = a.overlayPrefs
	a.settings.decoders.stickers.chosen = func() string { return a.preferences.Global().StickerPlayer }
	a.settings.decoders.stickers.choose = func(value string) {
		if err := services.Preferences.SetStickerPlayer(value); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.decoders.audio.chosen = func() string { return a.preferences.Global().AudioPlayer }
	a.settings.decoders.audio.choose = func(value string) {
		if err := services.Preferences.SetAudioPlayer(value); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.decoders.animations.chosen = func() string { return a.preferences.Global().AnimationPlayer }
	a.settings.decoders.animations.choose = func(value string) {
		if err := services.Preferences.SetAnimationPlayer(value); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.decoders.program.custom = func() string { return a.preferences.Global().FFmpegPath }
	a.settings.decoders.program.save = func(path string) {
		if err := services.Preferences.SetFFmpegPath(path); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	a.settings.players.chosen = func() player.Kind { return a.preferences.Global().Player }
	a.settings.players.paths = func() map[player.Kind]string { return a.preferences.Global().PlayerPaths() }
	for kind, setting := range a.settings.players.programs {
		setting.save = func(path string) {
			if err := services.Preferences.SetPlayerPath(kind, path); err != nil {
				log.Printf("save settings: %v", err)
			}
			a.settings.players.refresh()
		}
	}
	a.settings.browser.custom = func() string { return a.preferences.Global().BrowserPath }
	a.settings.browser.save = func(path string) {
		if err := services.Preferences.SetBrowserPath(path); err != nil {
			log.Printf("save settings: %v", err)
		}
		// Videos play in this browser when there is no player: the choice
		// of the player is looked at again with it.
		miniapp.SetBrowser(path)
		a.settings.players.refresh()
	}
	miniapp.SetBrowser(global.BrowserPath)
	applyFonts(global.Fonts, a.emojiPacks)
	a.settings.players.choose = func(kind player.Kind) {
		if err := services.Preferences.SetPlayer(kind); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	if source, ok := store.(model.SessionsSource); ok {
		a.settings.sessions = newSessionsView(source, w.Invalidate)
		a.settings.sessions.toast = &a.settings.toast
		a.settings.sessions.private = a.private
	}
	a.security = a.settings.security
	if services.Security != nil {
		a.unsubscribe = append(a.unsubscribe, services.Security.Subscribe(w.Invalidate))
	}
	if source, ok := store.(model.ConversationStore); ok {
		a.themes = newChatThemeController(source, &a.images, w.Invalidate)
		a.themes.look = a.chatMode
		a.themes.loadWallpaper = services.Preferences.LoadWallpaper
		chats := a.settings.chats
		chats.chats = func() preferences.ChatLook { return a.preferences.Global().Chats }
		chats.setChats = services.Preferences.SetChats
		chats.dark = a.dark
		chats.setDark = func(dark bool) {
			if dark {
				a.setThemeMode(themeDark)
			} else {
				a.setThemeMode(themeLight)
			}
		}
		chats.store = services.Preferences
		chats.wallpapers, _ = store.(model.WallpaperSource)
		chats.media = source
		chats.images = &a.images
		chats.thumbs = newWallpaperThumbs(source, w.Invalidate)
		a.info = newChatInfo(source, &a.images, w.Invalidate)
		a.info.themes = a.themes
		a.info.drawAvatar = a.layoutAvatar
		a.avatars = newAvatarImages(source, w.Invalidate)
		a.chats.avatar = a.layoutAvatar
		a.viewer = newPhotoViewer(source, &a.images, w.Invalidate)
		a.history = a.newChatPage(source, store, w)
		a.viewer.play = func(gtx layout.Context, m model.Message, l localization.Catalog) {
			a.history.play(gtx, m, a.viewer.reportPlay, l)
		}
		if _, ok := store.(model.CommentsStore); ok {
			a.history.openComments = a.openComments
			a.comments = a.newChatPage(source, store, w)
			a.comments.thread = true
		}
		if _, ok := store.(model.ForumSource); ok {
			a.forum = newForumPage()
			a.forum.open = a.openTopic
			a.forum.openAt = a.openTopicAt
			a.forum.emoji = a.layoutCustomEmoji
			if a.comments == nil {
				a.comments = a.newChatPage(source, store, w)
				a.comments.thread = true
			}
		}
		// One player for the window: what plays goes on in another chat,
		// and in the comments.
		a.history.audio.setSpeed(global.VoiceSpeed)
		a.history.audio.saveSpeed = func(speed float64) {
			if err := services.Preferences.SetVoiceSpeed(speed); err != nil {
				log.Printf("save settings: %v", err)
			}
		}
		a.history.audio.setVolume(float64(global.AudioVolume)/100, false)
		a.history.audio.saveVolume = func(volume float64) {
			if err := services.Preferences.SetAudioVolume(int(math.Round(volume * 100))); err != nil {
				log.Printf("save settings: %v", err)
			}
		}
		if a.comments != nil {
			a.comments.audio = a.history.audio
		}
		a.info.renderer.openPhoto = func(m model.Message) { a.viewer.Open(m.Key.ChatID, m, a.info.messages) }
		a.info.openAvatar = func(chat int64) { a.viewer.OpenProfile(chat) }
		if services.OpenWindow != nil {
			a.openWindow = services.OpenWindow
			a.viewer.popout = a.openPhotoWindow
			a.history.openArticle = a.openArticleWindow
			a.history.openSourceWindow = a.openSourceWindow
			if a.comments != nil {
				a.comments.openArticle = a.openArticleWindow
				a.comments.openSourceWindow = a.openSourceWindow
			}
		}
	}
	return a
}

// newChatPage is a page of a chat's history, set up as every one in this
// window is.
func (a *App) newChatPage(source model.ConversationStore, store model.Store, w *appwindow.Window) *chatPage {
	p := newChatPage(source, w.Invalidate)
	p.membershipNotice = a.chats.toast.Show
	p.frozen = a.frozen
	p.chats = store.Chats
	p.images = &a.images
	p.classic = func() bool { return a.composerStyle() == preferences.ComposerClassic }
	p.blur = func() bool { return a.preferences.Global().ComposerBlur && w.Motion.AnimationsEnabled() }
	p.overlays = a.overlayPrefs
	p.player = a.settings.players.chosen
	p.setPlayer = a.settings.players.choose
	p.playerPaths = a.settings.players.paths
	p.audioExternal = func() bool { return a.preferences.Global().AudioPlayer == "external" }
	p.appearance = a.themes
	p.avatar = a.layoutAvatar
	p.openChat = func(chat model.Chat, post model.MessageID) {
		a.open(chatPick{ID: chat.ID, Chat: &chat, Message: post})
		a.window.Invalidate()
	}
	p.openAudio = func(m model.Message) {
		a.thread = nil
		a.open(chatPick{ID: m.Key.ChatID, Message: m.Key.MessageID})
		a.window.Invalidate()
	}
	p.openPhoto = func(m model.Message) { a.viewer.Open(p.chat, m, p.photos()) }
	p.openAlone = func(m model.Message) { a.viewer.OpenAlone(p.chat, m) }
	p.releaseMemory, p.keepMemory = w.ReleaseMemoryLater, w.KeepMemory
	formula.SetRelease(w.ReleaseMemoryLater)
	p.openWebApp = a.launchWebApp
	if p.composer != nil {
		p.composer.confirmations = func() (bool, bool) {
			g := a.preferences.Global()
			return g.ConfirmSticker, g.ConfirmGIF
		}
		p.composer.ffmpeg = func() string { return a.preferences.Global().FFmpegPath }
	}
	p.addFilter = func(pattern preferences.FilterPattern) {
		f := a.preferences.Global().Filters
		f.Patterns = append(slices.Clone(f.Patterns), pattern)
		f.Enabled = true
		if err := a.preferences.SetFilters(f); err != nil {
			log.Printf("save settings: %v", err)
		}
	}
	return p
}

// messageFilter is the filter the settings ask for, compiled again only
// when they change.
func (a *App) messageFilter() *messageFilter {
	f := a.preferences.Global().Filters
	if a.filter == nil || !a.filter.same(f) {
		a.filter = compileFilter(f)
	}
	return a.filter
}

// Close releases process-wide subscriptions when this window closes.
func (a *App) Close() {
	a.closeMiniApps()
	a.photoWindows.closeAll()
	a.articleWindows.closeAll()
	if a.avatars != nil {
		a.avatars.media.Close()
	}
	if a.info != nil {
		a.info.Destroy()
	}
	if a.themes != nil {
		a.themes.Close()
	}
	if a.settings != nil && a.settings.chats.thumbs != nil {
		a.settings.chats.thumbs.Close()
	}
	if a.viewer != nil {
		a.viewer.Destroy()
	}
	if a.history != nil {
		a.history.Close()
	}
	if a.comments != nil {
		a.comments.Close()
	}
	for _, unsubscribe := range a.unsubscribe {
		unsubscribe()
	}
}

// RequireLogin makes the window show the sign-in of l until it is done, and
// the messenger after that.
func (a *App) RequireLogin(l *login.Login) {
	a.signIn = newLoginPage(l, a.security)
}

// signingIn reports whether the sign-in window is what to show.
func (a *App) signingIn() bool {
	if a.signIn != nil && a.signIn.done() {
		a.signIn = nil
	}
	return a.signIn != nil
}

func (a *App) setThemeMode(mode themeMode) {
	if err := a.preferences.SetTheme(preferences.Theme(mode)); err != nil {
		log.Printf("save settings: %v", err)
	}
}

func (a *App) setLanguage(language string) {
	if err := a.preferences.SetLanguage(language); err != nil {
		log.Printf("save settings: %v", err)
	}
}

func (a *App) catalog() localization.Catalog {
	language := a.preferences.Global().Language
	catalog := localization.For(language)
	if source, ok := a.store.(interface {
		LanguagePack(string) map[string]string
	}); ok {
		catalog = catalog.WithTelegram(source.LanguagePack(language))
	}
	return catalog
}

// Locale lets appwindow expose the selected language to Gio's text shaper and
// accessibility semantics on the very next frame.
func (a *App) Locale() system.Locale {
	return system.Locale{Language: string(a.catalog().Language()), Direction: system.LTR}
}

// private reports whether visual privacy mode is on.
func (a *App) private() bool { return a.preferences.Global().VisualPrivacy }

func (a *App) composerStyle() preferences.ComposerStyle { return a.preferences.Global().Composer }

func (a *App) themeMode() themeMode { return themeMode(a.preferences.Global().Theme) }

// dark reports whether the dark theme is in use: picked by the user, or
// followed from the system in auto mode.
func (a *App) dark() bool {
	switch a.themeMode() {
	case themeDark:
		return true
	case themeLight:
		return false
	}
	return a.window.Appearance.Scheme() == appearance.Dark
}

// Theme implements appwindow.Content.
func (a *App) Theme(gtx layout.Context) *token.Theme {
	// Themes keep the fonts they were made with.
	if v := defaults.FontsVersion(); v != a.themeFonts {
		a.themeFonts, a.darkTheme, a.lightTheme = v, nil, nil
	}
	theme := a.lightTheme
	if a.dark() {
		if a.darkTheme == nil {
			a.darkTheme = defaults.NewTheme(gtx, schemes.SchemeBaselineDark())
		}
		theme = a.darkTheme
	} else {
		if a.lightTheme == nil {
			a.lightTheme = defaults.NewTheme(gtx, schemes.SchemeBaselineLight())
		}
		theme = a.lightTheme
	}
	a.window.SetFrameDark(a.dark())
	a.window.SetFrameColor(theme.Scheme.Surface.Color.AsNRGBA())
	return theme
}

// Update implements appwindow.Content.
func (a *App) Update(gtx layout.Context) {
	a.updateWindowEffects()
	if a.windowLocked != nil && a.windowLocked.Load() &&
		(a.security == nil || a.security.manager == nil || !a.security.manager.Enabled()) {
		a.windowLocked.Store(false)
		a.lastInput = gtx.Now
		a.restoreAccountTitle()
	}
	if a.security != nil && a.security.manager != nil {
		state := a.security.manager.State()
		if state.Enabled && !state.Unlocked {
			a.security.UpdateUnlock(gtx)
			if !a.security.manager.State().Unlocked {
				return
			}
		}
	}
	if a.updateVisualLock(gtx) {
		return
	}
	if a.signingIn() {
		a.signIn.Update(gtx)
		return
	}
	a.sessionEnded.Update(gtx, a.store)
	a.connectionFailed.Update(gtx, a.store)
	a.frozen.Update(gtx)
	if id := a.openChat.Swap(0); id != 0 {
		a.open(chatPick{ID: id})
	}
	a.shownChat.Store(a.selected)
	folders := a.store.Folders()
	a.overlay.Update(gtx)
	if a.compact {
		if a.menu.Clicked(gtx) {
			a.drawer.open(a.overlay, a.layoutDrawer)
		}
		if sec, ok := a.folderBar.Update(gtx, folders); ok {
			a.openSection(gtx, sec)
		}
	} else {
		a.drawer.close()
		ev := a.sidebar.Update(gtx, folders)
		if ev.toggleTheme {
			a.toggleTheme()
		}
		if ev.section != nil {
			a.openSection(gtx, *ev.section)
		}
	}
	// The profile can arrive after the Saved Messages button was pressed.
	if a.section.kind == sectionSaved && a.selected == 0 {
		a.selected = a.savedChat()
	}
	if a.section.showsChats() {
		if pick, ok := a.chats.Update(gtx, a.section, a.catalog()); ok {
			a.open(pick)
		}
	}
	if a.section.kind == sectionProfile {
		a.profile.Update(gtx, a.store.Me(), a.window.Invalidate)
	}
	if a.section.kind == sectionSettings {
		global := a.preferences.Global()
		a.settings.Update(gtx, themeMode(global.Theme), global.Language)
	}
}

// chatMode is the look the settings give every chat in the theme of dark.
func (a *App) chatMode(dark bool) preferences.ChatMode {
	c := a.preferences.Global().Chats
	if dark {
		return c.Night
	}
	return c.Day
}

// toggleTheme switches to the other theme for good.
func (a *App) toggleTheme() {
	if a.dark() {
		a.setThemeMode(themeLight)
	} else {
		a.setThemeMode(themeDark)
	}
}

// layoutDrawer is the content of the drawer overlay item.
func (a *App) layoutDrawer(gtx layout.Context) layout.Dimensions {
	entries := drawerEntries(a.store.Folders(), a.store.Chats(), a.dark(), a.catalog())
	if key, ok := a.drawer.Update(gtx, entries); ok {
		if key.theme {
			a.toggleTheme()
		} else {
			a.openSection(gtx, key.section)
		}
		a.drawer.close()
	}
	return a.drawer.Layout(gtx, entries, a.section, a.store.Me(), a.layoutAvatar, a.private())
}

func (a *App) openSection(gtx layout.Context, sec section) {
	if sec.kind == sectionSearch {
		if a.section.kind == sectionSearch {
			a.closeSearch()
			return
		}
		a.beforeSearch = a.section
	}
	a.section = sec
	switch sec.kind {
	case sectionSaved:
		a.selected = a.savedChat()
	case sectionSearch:
		a.chats.search.Focus(gtx)
	case sectionSettings:
		a.settings.open()
	case sectionProfile:
		a.profile.open()
	}
}

// closeSearch leaves search for the section it was opened from, as it was
// left: the open chat and the settings page stay as they are.
func (a *App) closeSearch() {
	a.chats.search.ClearText()
	a.section = a.beforeSearch
}

// savedChat returns the account's own chat even before it has a dialog row.
func (a *App) savedChat() int64 {
	for _, c := range a.store.Chats() {
		if c.Kind == model.KindSaved {
			return c.ID
		}
	}
	return a.store.Me().ID
}

func (a *App) selectedChat() (model.Chat, bool) {
	for _, c := range a.store.Chats() {
		if c.ID == a.selected {
			return c, true
		}
	}
	if me := a.store.Me(); me.ID != 0 && a.selected == me.ID {
		return model.Chat{ID: me.ID, Kind: model.KindSaved, Title: a.catalog().T("nav.saved")}, true
	}
	if a.selected != 0 && a.found.ID == a.selected {
		return a.found, true
	}
	return model.Chat{}, false
}

// open opens the chat picked in the chat list, at the message picked when a
// search found one.
func (a *App) open(pick chatPick) {
	if !a.section.showsChats() {
		a.section = section{kind: sectionAll}
	}
	a.selected = pick.ID
	if pick.Chat != nil {
		a.found = *pick.Chat
	}
	if a.section.kind == sectionSaved && pick.ID != a.savedChat() {
		a.section = section{kind: sectionAll}
	}
	if pick.Message != 0 && a.history != nil {
		if r, ok := a.store.(model.MessageRevealer); ok {
			r.Reveal(pick.ID, pick.Message)
			a.history.forget()
		}
	}
}

// Layout implements appwindow.Content.
func (a *App) Layout(gtx layout.Context) {
	transparent, _ := a.window.Translucency()
	a.layoutWindow(gtx, transparent)
}

// layoutWindow paints the main window using the transparency granted by the backend.
func (a *App) layoutWindow(gtx layout.Context, transparent bool) {
	withWindowSurfaceOpacity(gtx, a.preferences.Global().WindowTransparency, transparent)
	withLook(gtx, a.preferences.Global().Look)
	if trace := diagnostics.From(gtx.Values); trace != nil && trace.Recorder.Due(trace.Window, "cache-gauges", time.Second) {
		defer func() {
			r := trace.Recorder
			r.Gauge(trace.Window, "image-textures", len(a.images.entries), 0)
			if a.history != nil {
				count, bytes := a.history.media.Stats()
				r.Gauge(trace.Window, "media.frames", count, bytes)
				r.Gauge(trace.Window, "history.row-state", len(a.history.rows), 0)
			}
			if a.avatars != nil {
				count, bytes := a.avatars.media.Stats()
				r.Gauge(trace.Window, "avatar.frames", count, bytes)
			}
			if a.viewer != nil {
				count, bytes := a.viewer.full.Stats()
				r.Gauge(trace.Window, "viewer.frames", count, bytes)
				count, bytes = a.viewer.thumbs.Stats()
				r.Gauge(trace.Window, "viewer.thumbnails", count, bytes)
			}
		}()
	}
	decoderPrefs := a.preferences.Global()
	configureMedia := func(m *chatmedia.Manager) {
		m.ConfigureDecoders(decoderPrefs.StickerPlayer == "wasm", decoderPrefs.AnimationPlayer == "wasm", decoderPrefs.FFmpegPath)
	}
	if a.info != nil {
		configureMedia(a.info.renderer.media)
	}
	a.images.BeginFrame()
	defer a.images.EndFrame()
	if a.avatars != nil {
		configureMedia(a.avatars.media)
		a.avatars.media.BeginFrame()
		defer a.avatars.media.EndFrame()
	}
	if a.history != nil {
		configureMedia(a.history.media)
		a.history.media.BeginFrame()
		defer a.history.media.EndFrame()
	}
	if a.comments != nil {
		configureMedia(a.comments.media)
		a.comments.media.BeginFrame()
		defer a.comments.media.EndFrame()
	}
	// The comments belong to the channel they were opened over.
	if a.thread != nil && a.thread.from != a.selected {
		a.closeComments()
	}

	// Each main surface paints its own background; an opaque root would
	// hide the desktop behind every translucent surface.
	opaqueBackground := func() { fillRect(gtx, scheme(gtx).Background.Color, gtx.Constraints.Max) }
	if !transparent || a.preferences.Global().WindowTransparency == 0 {
		opaqueBackground()
	}
	if a.security != nil && a.security.manager != nil {
		state := a.security.manager.State()
		if state.Enabled && !state.Unlocked {
			opaqueBackground()
			a.security.UnlockLayout(gtx, a.catalog())
			return
		}
	}
	if a.windowLocked != nil && a.windowLocked.Load() {
		opaqueBackground()
		a.visualLock.Layout(gtx, a.catalog())
		return
	}
	if a.signingIn() {
		opaqueBackground()
		a.signIn.Layout(gtx, a.catalog(), a.private())
		return
	}
	a.tellGhost()
	if a.thread != nil {
		a.updateMiniApps(a.comments, a.catalog())
	} else {
		a.updateMiniApps(a.history, a.catalog())
	}
	a.window.SetCaptureExcluded(a.preferences.Global().StreamerMode)
	// Leaving can remove the selected dialog before its history is laid out.
	// Drain the completed operation even when the right pane is now empty.
	a.history.updateMembership(a.selected, a.catalog())
	a.history.filter = a.messageFilter()
	if a.comments != nil {
		a.comments.filter = a.history.filter
	}
	if a.viewer != nil && (a.history.headAvatar.Clicked(gtx) || a.forum != nil && a.forum.avatar.Clicked(gtx)) {
		if c, ok := a.selectedChat(); ok {
			a.viewer.OpenProfile(c.ID)
		}
	}
	if a.info != nil && (a.history.header.Clicked(gtx) || a.history.takeInfoAsked() || a.forum != nil && a.forum.header.Clicked(gtx)) {
		if c, ok := a.selectedChat(); ok {
			a.info.Open(c)
			if a.history.themeShown {
				a.info.OpenTheme()
			}
		}
		a.history.themeShown = false
	}
	overlayGtx := gtx
	if a.info != nil && a.info.visible {
		gtx = gtx.Disabled()
	}
	size := gtx.Constraints.Max
	folders := a.store.Folders()
	chats := a.store.Chats()
	sc := scheme(gtx)

	sidebarPx := gtx.Dp(sidebarWidth)
	column := func(x, y, width, height int, w layout.Widget) {
		cgtx := gtx
		cgtx.Constraints = layout.Exact(image.Pt(max(width, 0), max(height, 0)))
		offset(cgtx, image.Pt(x, y), w)
	}
	toDp := func(px int) unit.Dp { return unit.Dp(float32(px) / gtx.Metric.PxPerDp) }

	// The sidebar needs room for itself, the list and the page across,
	// and for all its buttons down.
	a.compact = toDp(size.X) < sidebarWidth+listMinWidth+pageMinWidth ||
		toDp(size.Y) < sidebarNeededHeight(len(folders))
	x := 0
	if !a.compact {
		column(0, 0, sidebarPx, size.Y, func(gtx layout.Context) layout.Dimensions {
			return a.sidebar.Layout(gtx, a.section, folders, chats, a.dark(), a.catalog())
		})
		x = sidebarPx
	}

	if a.section.showsChats() {
		available := toDp(size.X - x)
		width, narrow := listWidth(a.listRequest, available)
		listPx := gtx.Dp(width)
		if edge, ok := a.splitter.Update(gtx, x+listPx); ok {
			a.listRequest = toDp(edge - x)
			width, narrow = listWidth(a.listRequest, available)
			listPx = gtx.Dp(width)
		}
		top := 0
		if a.compact {
			top = gtx.Dp(compactBarHeight)
			column(x, 0, listPx, top, func(gtx layout.Context) layout.Dimensions {
				return a.layoutCompactBar(gtx, folders, chats, narrow)
			})
		}
		if a.frozen.Frozen() && !narrow {
			bar := gtx.Dp(56)
			column(x, top, listPx, bar, func(gtx layout.Context) layout.Dimensions {
				return a.frozen.layoutBar(gtx, a.catalog())
			})
			top += bar
		}
		column(x, top, listPx, size.Y-top, func(gtx layout.Context) layout.Dimensions {
			return a.chats.Layout(gtx, a.section, folders, chats, a.selected, narrow, a.catalog())
		})
		x += listPx
		column(x, 0, gtx.Dp(1), size.Y, func(gtx layout.Context) layout.Dimensions {
			fillRect(gtx, sc.OutlineVariant, gtx.Constraints.Max)
			return layout.Dimensions{Size: gtx.Constraints.Max}
		})
		x += gtx.Dp(1)
	}

	pageTop := 0
	if a.compact && !a.section.showsChats() {
		// Without the chat list, the menu button gets a bar over the page.
		pageTop = gtx.Dp(compactBarHeight)
		column(x, 0, size.X-x, pageTop, func(gtx layout.Context) layout.Dimensions {
			fillWindowSurface(gtx, sc.SurfaceContainerLow, gtx.Constraints.Max)
			a.layoutMenuButton(gtx)
			return layout.Dimensions{Size: gtx.Constraints.Max}
		})
	}
	// The page is where files dragged over the window are dropped, when it
	// is a chat that takes them.
	a.drop.page, a.drop.area, a.drop.metric = nil, image.Rect(x, pageTop, size.X, size.Y), gtx.Metric
	column(x, pageTop, size.X-x, size.Y-pageTop, func(gtx layout.Context) layout.Dimensions {
		l := a.catalog()
		c, chat := a.selectedChat()
		// A chat's page and its comments draw the bar of what plays
		// themselves; the others get it here.
		switch {
		case a.section.showsChats() && a.thread != nil:
			if a.comments.takesFiles() {
				a.drop.page = a.comments
			}
			return a.layoutComments(gtx, l)
		case a.section.showsChats() && chat && !(c.Forum && a.forum != nil):
			if a.history.takesFiles() {
				a.drop.page = a.history
			}
			return layoutChatPage(gtx, c, l, a.layoutAvatar, a.badges, func(gtx layout.Context) layout.Dimensions {
				if a.history != nil {
					return a.history.Layout(gtx, c, l, a.window.Motion.AnimationsEnabled())
				}
				return layoutEmptyPage(gtx, l)
			}, a.history)
		}
		return a.withAudioBar(gtx, l, func(gtx layout.Context) layout.Dimensions {
			switch {
			case a.section.kind == sectionProfile:
				return a.profile.Layout(gtx, a.store.Me(), l, a.layoutAvatar, a.private(), a.window.Motion.AnimationsEnabled())
			case a.section.kind == sectionSettings:
				return a.settings.Layout(gtx, a.themeMode(), a.window.Appearance.Scheme(), a.dark(), l)
			case chat:
				return a.layoutForum(gtx, c, l)
			}
			return layoutEmptyPage(gtx, l)
		})
	})

	// The splitter goes last so that it takes the pointer over the columns.
	if a.section.showsChats() {
		a.splitter.Layout(gtx, x, size.Y)
	}
	a.drop.layout(gtx, a.catalog())
	a.overlay.Layout(gtx)
	if a.info != nil {
		a.info.Layout(overlayGtx, a.catalog(), a.window.Motion.AnimationsEnabled())
	}
	if a.viewer != nil {
		a.viewer.Layout(overlayGtx, a.catalog(), a.window.Motion.AnimationsEnabled())
	}
	if a.chats.panel != nil {
		a.chats.panel.recent.layoutConfirm(overlayGtx, a.catalog())
	}
	a.settings.sessions.layoutDialog(overlayGtx, a.catalog())
	a.settings.chats.layoutDialog(overlayGtx, a.catalog())
	a.frozen.Layout(overlayGtx, a.catalog())
	a.sessionEnded.Layout(overlayGtx, a.catalog())
	a.connectionFailed.Layout(overlayGtx, a.catalog())
}

// withAudioBar draws the bar of what plays over a page that is not a chat's,
// which has its own, and the page under it.
func (a *App) withAudioBar(gtx layout.Context, l localization.Catalog, page layout.Widget) layout.Dimensions {
	h := 0
	if a.history != nil {
		h = a.history.audioBarSize(gtx)
	}
	if h == 0 {
		return page(gtx)
	}
	size := gtx.Constraints.Max
	body := gtx
	body.Constraints = layout.Exact(image.Pt(size.X, max(0, size.Y-h)))
	offset(body, image.Pt(0, h), page)
	a.history.layoutAudioBar(gtx, l, true)
	return layout.Dimensions{Size: size}
}

const compactBarHeight = unit.Dp(52)

// layoutMenuButton draws the menu button of a compact bar centered over the
// column of avatars, so that it stays in place when the chat list collapses,
// and returns where its right edge is.
func (a *App) layoutMenuButton(gtx layout.Context) int {
	size := gtx.Constraints.Max
	btnGtx := gtx
	btnGtx.Constraints = layout.Constraints{Max: size}
	macro := op.Record(gtx.Ops)
	dims := a.menu.LayoutIconOnly(btnGtx, a.catalog().T("nav.menu"), iconMenu)
	call := macro.Stop()
	center := gtx.Dp(chatAvatarInset) + gtx.Dp(chatAvatarSize)/2
	origin := image.Pt(center-dims.Size.X/2, (size.Y-dims.Size.Y)/2)
	offset(gtx, origin, func(gtx layout.Context) layout.Dimensions {
		call.Add(gtx.Ops)
		return dims
	})
	return origin.X + dims.Size.X
}

// layoutCompactBar draws the bar over the chat list when the sidebar is
// hidden: the menu button and, if there is room, the folders.
func (a *App) layoutCompactBar(gtx layout.Context, folders []model.Folder, chats []model.Chat, narrow bool) layout.Dimensions {
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	fillWindowSurface(gtx, sc.Surface.Color, size)
	menuWidth := a.layoutMenuButton(gtx) + gtx.Dp(4)
	if narrow {
		return layout.Dimensions{Size: size}
	}
	barGtx := gtx
	barGtx.Constraints = layout.Exact(image.Pt(max(size.X-menuWidth, 0), size.Y))
	offset(barGtx, image.Pt(menuWidth, 0), func(gtx layout.Context) layout.Dimensions {
		return a.folderBar.Layout(gtx, a.section, folders, chats, a.catalog())
	})
	return layout.Dimensions{Size: size}
}

// SetSuspended is called on the UI goroutine. A hidden window stops its
// decoder workers and drops every decoded picture, so that the process can
// give the memory back while it keeps receiving updates; the pictures on
// screen are decoded again from the media cache when the window is shown.
func (a *App) SetSuspended(hidden bool) {
	if !hidden {
		return
	}
	// X11 and Wayland can report a minimized window as suspended while its
	// mode still says Windowed. Lock on either hidden-window signal.
	a.SetMinimized(true)
	if a.history != nil {
		a.history.save(true)
		a.history.media.Release()
	}
	if a.comments != nil {
		a.comments.media.Release()
	}
	if a.viewer != nil {
		a.viewer.Release()
	}
	if a.info != nil {
		a.info.Release()
	}
	if a.themes != nil {
		a.themes.Release()
	}
	if a.settings != nil && a.settings.chats.thumbs != nil {
		a.settings.chats.thumbs.Release()
	}
	if a.avatars != nil {
		a.avatars.media.Release()
	}
	a.images.Release()
}

// SetFocused is called on the UI goroutine when the window gains or loses
// the focus: the account shows online while it has it, if Ghost allows.
// OpenChat opens chat in the window, as a click in the chat list would; it
// may be called from any goroutine.
func (a *App) OpenChat(chat int64) {
	a.openChat.Store(chat)
	a.window.Invalidate()
}

// Showing is the chat open in the window and whether the window has the
// focus; it may be called from any goroutine.
func (a *App) Showing() (chat int64, focused bool) {
	return a.shownChat.Load(), a.shownFocus.Load()
}

func (a *App) SetFocused(focused bool) {
	a.focused = focused
	a.shownFocus.Store(focused)
	a.tellGhost()
}

// tellGhost gives the store what Ghost allows, and whether the window is
// active.
func (a *App) tellGhost() {
	g, ok := a.store.(model.GhostStore)
	if !ok {
		return
	}
	p := a.preferences.Global().Ghost
	g.SetGhost(model.Ghost{SendRead: p.SendRead, SendOnline: p.SendOnline, SendTyping: p.SendTyping, ReadOnInteract: p.ReadOnInteract})
	g.SetOnline(a.focused)
	if k, ok := a.store.(model.KeepStore); ok {
		keep := a.preferences.Global().Keep
		k.SetKeep(model.Keep{Deleted: keep.Deleted, Edits: keep.Edits})
	}
}

// SetMinimized is called for an explicit minimize or a suspended window.
func (a *App) SetMinimized(minimized bool) {
	if minimized && a.windowLocked != nil && a.security != nil &&
		a.security.manager != nil && a.security.manager.State().Unlocked &&
		a.security.manager.Enabled() && !a.signingIn() &&
		a.preferences.Global().LockOnMinimize {
		a.windowLocked.Store(true)
		a.window.SetTitle(a.catalog().T("app.title"))
		a.photoWindows.closeAll()
		a.articleWindows.closeAll()
	}
}

// updateVisualLock returns true only while the lock screen still owns this
// frame. After a successful password check, normal Update must run before
// normal Layout, including Overlay.Update.
func (a *App) updateVisualLock(gtx layout.Context) bool {
	if !a.checkWindowLock(gtx) {
		return false
	}
	a.window.SetTitle(a.catalog().T("app.title"))
	a.visualLock.Update(gtx, a.windowLocked)
	if a.windowLocked.Load() {
		return true
	}
	a.lastInput = gtx.Now
	a.restoreAccountTitle()
	return false
}

// checkWindowLock uses input timestamps from Gio's event loop. It only
// changes what this window draws; the account store and network keep running.
func (a *App) checkWindowLock(gtx layout.Context) bool {
	if a.windowLocked == nil || a.security == nil || a.security.manager == nil ||
		!a.security.manager.State().Unlocked || !a.security.manager.Enabled() || a.signingIn() {
		return false
	}
	if a.windowLocked.Load() {
		return true
	}
	if at := a.window.LastInput(); at.After(a.lastInput) {
		a.lastInput = at
	}
	if a.lastInput.IsZero() {
		a.lastInput = gtx.Now
	}
	minutes := a.preferences.Global().AutoLockMinutes
	if minutes > 0 {
		deadline := a.lastInput.Add(time.Duration(minutes) * time.Minute)
		if !gtx.Now.Before(deadline) {
			a.windowLocked.Store(true)
			a.window.SetTitle(a.catalog().T("app.title"))
			a.photoWindows.closeAll()
			a.articleWindows.closeAll()
			return true
		}
		gtx.Execute(op.InvalidateCmd{At: deadline})
	}
	return false
}

func (a *App) restoreAccountTitle() {
	title := a.catalog().T("app.title")
	if name := a.store.Me().Name(); name != "" {
		title += " — " + name
	}
	a.window.SetTitle(title)
}

// overlayPrefs is how the overlays are drawn now: the menus and toasts blur
// if the preferences say so and animations are on, and blurring overlays let
// as much show through as the preferences give.
func (a *App) overlayPrefs() overlayPrefs {
	o := a.preferences.Global().Overlays
	animations := a.window.Motion.AnimationsEnabled()
	return overlayPrefs{menus: o.MenusBlur && animations, toasts: o.ToastsBlur && animations, opacity: o.Opacity()}
}
