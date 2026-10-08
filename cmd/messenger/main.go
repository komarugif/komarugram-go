// SPDX-License-Identifier: Unlicense OR MIT

// Command messenger is the desktop messenger client. It signs in with a phone
// number and keeps the session for the next start; it can also run on a real
// account imported from a Telegram Desktop tdata archive, or on demo data.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"gioui.org/io/system"
	"gioui.org/unit"
	"github.com/gotd/td/tg"

	"komarugram/internal/alert"
	"komarugram/internal/appwindow"
	"komarugram/internal/crash"
	"komarugram/internal/diagnostics"
	"komarugram/internal/messenger/account"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/messenger/security"
	"komarugram/internal/messenger/tgstore"
	"komarugram/internal/messenger/ui"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/notify"
	"komarugram/internal/profilerui"
	"komarugram/internal/tray"
	"komarugram/pkg/miniapp"
	"komarugram/pkg/program"
	"komarugram/pkg/sandbox"
)

type pathsFlag []string

func (p *pathsFlag) String() string { return strings.Join(*p, ",") }
func (p *pathsFlag) Set(value string) error {
	*p = append(*p, value)
	return nil
}

func main() {
	demo := flag.Bool("demo", false, "run on demo data instead of signing in")
	demoChats := flag.Int("demo-chats", 0, "run on demo data with this many generated chats added")
	demoPanic := flag.Bool("demo-panic", false, "show the recovered-panic dialog once on demo data")
	demoNotify := flag.Duration("demo-notify", 0, "on demo data, receive a message this often, to try notifications")
	var tdataPaths pathsFlag
	flag.Var(&tdataPaths, "tdata", "import a Telegram Desktop tdata zip or every zip in a directory; may be repeated")
	check := flag.Bool("check", false, "import/load accounts without windows, print counts and exit")
	proxy := flag.String("proxy", os.Getenv("KOMARUGRAM_PROXY"), "connect through this MTProxy link, tg://proxy?server=…&port=…&secret=… (default: $KOMARUGRAM_PROXY)")
	profile := flag.Bool("profile", false, "open performance profiler in a separate window")
	profileDir := flag.String("profile-dir", "profiles", "directory for explicitly exported profiler artifacts")
	profileExport := flag.Duration("profile-export", 0, "automatically export JSON at this interval (e.g. 2s); enables collection without profiler UI")
	profileCapture := flag.String("profile-capture", "", "capture profiles without GUI: comma-separated cpu,heap,allocs,trace,goroutine")
	scrollLog := flag.String("scroll-log", "", "write every scroll event of the lists to this file, for measuring what the wheel and the touchpad send")
	notified := flag.String("notified", "", "open the chat of the notification with this tag in the running messenger; Haiku's notifications start it so when clicked")
	noIntegrations := flag.Bool("no-integrations", false, "do not look for FFmpeg, mpv, VLC or a browser on the system; only the paths set in the settings are used, as on a machine without them")
	flag.Parse()
	if *noIntegrations {
		program.SetSearching(false)
	}
	useWasmCache()
	if *scrollLog != "" {
		if err := logScroll(*scrollLog); err != nil {
			log.Printf("scroll log: %v", err)
		}
	}
	// fail ends the process telling why: in the log and, as a program
	// without a console shows no log, in a message box. -check is run from a
	// console, and has only the log.
	catalog := localization.For("")
	fail := func(v ...any) {
		text := fmt.Sprint(v...)
		log.Print(text)
		if !*check {
			alert.Error(catalog.T("app.title"), catalog.T("fatal.start")+"\n\n"+text)
		}
		os.Exit(1)
	}
	if *profileExport < 0 || *profileExport > 0 && *profileExport < 100*time.Millisecond {
		fail("-profile-export must be zero or at least 100ms")
	}
	captures := []string{}
	for _, kind := range strings.Split(*profileCapture, ",") {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			continue
		}
		switch kind {
		case "cpu", "heap", "allocs", "trace", "goroutine":
			captures = append(captures, kind)
		default:
			fail(fmt.Sprintf("unknown profile capture %q", kind))
		}
	}
	profiling := *profile || *profileExport > 0 || len(captures) > 0
	if profiling && *check {
		fail("-profile requires a window; cannot be combined with -check")
	}
	demoMode := *demo || *demoChats > 0 || *demoPanic || *demoNotify > 0
	if demoMode && (*check || len(tdataPaths) > 0) {
		fail("-demo runs without accounts; cannot be combined with -check or -tdata")
	}
	if profiling {
		r := diagnostics.Enable()
		if *profileExport > 0 {
			exporter, err := r.StartAutoExport(*profileDir, *profileExport)
			if err != nil {
				fail(err)
			}
			log.Printf("automatic profiler export: %s", exporter.Directory)
		}
		if len(captures) > 0 {
			go func() {
				for _, kind := range captures {
					path, err := r.Capture(*profileDir, kind)
					if err != nil {
						log.Printf("profile %s: %v", kind, err)
						return
					}
					log.Printf("profile %s saved: %s", kind, path)
				}
			}()
		}
	}

	if demoMode {
		reportLastCrash(catalog)
		runDemo(*demoChats, *profile, *profileDir, *demoPanic, *demoNotify)
		return
	}
	// A second start brings the running instance back from the tray.
	var windows atomic.Pointer[accountWindows]
	releaseInstance := func() {}
	if !*check {
		var err error
		notice := *notified
		if notice == "" {
			notice = launchNotice()
		}
		releaseInstance, err = claimInstance(notice, func(token string) {
			if w := windows.Load(); w != nil {
				w.ShowAll(token)
			}
		}, func(tag string) {
			if w := windows.Load(); w != nil {
				w.openNotice(tag)
			}
		})
		if errors.Is(err, errRunning) && len(tdataPaths) > 0 {
			fail("the messenger is already running; quit it from the tray to import tdata")
		}
		if errors.Is(err, errRunning) {
			log.Print(err)
			return
		}
		if err != nil {
			log.Printf("single instance: %v", err)
		}
	}
	// The settings come first: they tell the language of what follows.
	var sharedPreferences *preferences.Store
	if !*check {
		var err error
		if sharedPreferences, err = preferences.Open(); err != nil {
			fail("settings: ", err)
		}
		catalog = localization.For(sharedPreferences.Global().Language)
		reportLastCrash(catalog)
	}

	protection, err := security.Open()
	if err != nil {
		fail(err)
	}
	manager, err := newManager(*proxy, protection)
	if err != nil {
		fail(err)
	}
	defer manager.Close()
	var imports []*account.TData
	if len(tdataPaths) > 0 {
		paths, err := expandTDataPaths(tdataPaths)
		if err != nil {
			fail(err)
		}
		for _, path := range paths {
			archive, err := os.ReadFile(path)
			if err != nil {
				fail(err)
			}
			imported, err := account.ReadTData(archive)
			if err != nil {
				fail(filepath.Base(path), ": ", err)
			}
			imports = append(imports, imported)
		}
	}
	if *check {
		if err := checkAccounts(manager, imports); err != nil {
			fail(err)
		}
		return
	}
	sharedMiniApps := miniApps(sharedPreferences)
	process := newProcess(*profile, *profileDir, func() {
		if w := windows.Load(); w != nil {
			w.Quit()
		}
	})
	accounts := newAccountWindows(process, windowOptions(sharedPreferences), manager, protection, sharedPreferences, sharedMiniApps, imports)
	balloon := &trayBalloon{}
	notifier := notify.New(catalog.T("app.title"), balloon)
	accounts.notifier = notifier
	icon, err := tray.Start(tray.Options{
		ID:       "komarugram-go",
		Title:    catalog.T("app.title"),
		Activate: accounts.ShowAll,
		Notified: notifier.Clicked,
		Items: []tray.Item{
			{Label: catalog.T("tray.open"), Action: accounts.ShowAll},
			{Separator: true},
			{Label: catalog.T("tray.quit"), Action: func(string) { accounts.Quit() }},
		},
	})
	switch {
	case err == nil:
		accounts.tray = icon
	case !errors.Is(err, tray.ErrUnsupported):
		log.Print(err)
	}
	balloon.icon.Store(icon)
	flush := process.BeforeExit
	process.BeforeExit = func() {
		notifier.Close()
		if icon != nil {
			icon.Close()
		}
		releaseInstance()
		if flush != nil {
			flush()
		}
	}
	windows.Store(accounts)
	process.Open(accounts.startSpec())
	process.Main()
}

// reportLastCrash makes the runtime keep a report of what kills the process,
// and tells of the one the previous run left: that run could show nothing.
func reportLastCrash(catalog localization.Catalog) {
	previous, err := crash.CaptureFatal()
	if err != nil {
		log.Printf("crash reports: %v", err)
	}
	if previous == "" {
		return
	}
	log.Printf("the previous run crashed: %s", previous)
	go alert.Error(catalog.T("app.title"), catalog.T("fatal.crashed")+"\n\n"+previous)
}

// runDemo shows demo data with settings kept in memory only: the demo
// neither reads nor changes the user's settings, accounts or local-data
// protection.
func runDemo(chats int, profile bool, profileDir string, panicDemo bool, receive time.Duration) {
	prefs := preferences.Memory()
	process := newProcess(profile, profileDir, nil)
	opts := windowOptions(prefs)
	opts.ProfileName = "demo"
	opts.DemoPanic = panicDemo
	store := mockstore.New(time.Now(), chats)
	var window atomic.Pointer[appwindow.Window]
	var app atomic.Pointer[ui.App]
	catalog := localization.For(prefs.Global().Language)
	// Windows shows notifications as balloons of the tray's icon: with
	// -demo-notify the demo has one, so that they can be tried there too.
	var balloon notify.Balloon
	if receive > 0 {
		balloon = &trayBalloon{}
	}
	notifier := notify.New(catalog.T("app.title"), balloon)
	if b, ok := balloon.(*trayBalloon); ok {
		activate := func(token string) {
			if w := window.Load(); w != nil {
				w.Activate(token)
			}
		}
		icon, err := tray.Start(tray.Options{
			ID:       "komarugram-go-demo",
			Title:    catalog.T("app.title"),
			Activate: activate,
			Notified: notifier.Clicked,
			Items: []tray.Item{
				{Label: catalog.T("tray.open"), Action: activate},
				{Separator: true},
				{Label: catalog.T("tray.quit"), Action: func(string) {
					if w := window.Load(); w != nil {
						w.PerformLater(system.ActionClose)
					}
				}},
			},
		})
		switch {
		case err == nil:
			b.icon.Store(icon)
			flush := process.BeforeExit
			process.BeforeExit = func() {
				icon.Close()
				if flush != nil {
					flush()
				}
			}
		case !errors.Is(err, tray.ErrUnsupported):
			log.Print(err)
		}
	}
	store.SetNotices(func(n model.MessageNotice) {
		chat := n.Chat.ID
		showNotice(notifier, prefs.Global(), "demo", viewOf(app.Load(), false), n, func(token string) {
			if a, w := app.Load(), window.Load(); a != nil && w != nil {
				a.OpenChat(chat)
				w.Activate(token)
			}
		})
	})
	store.SetChanged(func() {
		if w := window.Load(); w != nil {
			w.Invalidate()
		}
	})
	if receive > 0 {
		go func() {
			for range time.Tick(receive) {
				store.Receive()
				if w := window.Load(); w != nil {
					w.Invalidate()
				}
			}
		}()
	}
	process.Open(appwindow.Spec{Options: opts, Build: func(w *appwindow.Window) appwindow.Content {
		content := ui.New(w, store, ui.Services{
			Preferences: prefs,
			MiniApps:    miniApps(prefs),
			OpenWindow:  process.Open,
			OfferEmoji:  true,
		})
		window.Store(w)
		app.Store(content)
		return content
	}})
	process.Main()
}

// miniApps returns the Mini App settings kept in prefs.
func miniApps(prefs *preferences.Store) *miniappprefs.Settings {
	settings := miniappprefs.New(prefs.Global().MiniAppStorage)
	settings.SetPreferenceChanged(func(storage miniapp.Storage) {
		if err := prefs.SetMiniAppStorage(storage); err != nil {
			log.Printf("save settings: %v", err)
		}
	})
	return settings
}

func windowOptions(prefs *preferences.Store) appwindow.Options {
	global := prefs.Global()
	catalog := localization.For(global.Language)
	return appwindow.Options{
		Transparent: appwindow.WantsTransparent(global.WindowTransparency > 0),
		BlurBehind:  global.WindowBlur && global.WindowTransparency > 0,
		Title:       catalog.T("app.title"),
		Width:       unit.Dp(1200),
		Height:      unit.Dp(760),
		Locale:      system.Locale{Language: string(catalog.Language()), Direction: system.LTR},
	}
}

// newProcess returns the window host, which with diagnostics enabled quits on
// a signal, so that profiles are flushed, and opens the profiler window if
// asked. quit ends the process, nil for closing every window.
func newProcess(profile bool, profileDir string, quit func()) *appwindow.Host {
	process := new(appwindow.Host)
	process.EnableCrashDialogs()
	if quit == nil {
		quit = process.CloseAll
	}
	if r := diagnostics.Current(); r != nil {
		process.BeforeExit = r.Close
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		go func() { <-signals; quit() }()
		if profile {
			process.Open(profilerui.Spec(r, profileDir))
		}
	}
	return process
}

func expandTDataPaths(values []string) ([]string, error) {
	var paths []string
	for _, value := range values {
		info, err := os.Stat(value)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			paths = append(paths, value)
			continue
		}
		entries, err := os.ReadDir(value)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".zip") {
				paths = append(paths, filepath.Join(value, entry.Name()))
			}
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, errors.New("no tdata zip archives found")
	}
	return paths, nil
}

// checkAccounts adds the imports, loads every account without a window and
// prints what it loaded.
func checkAccounts(manager *account.Manager, imports []*account.TData) error {
	if err := manager.Load(); errors.Is(err, security.ErrLocked) {
		return errors.New("-check: local data is protected; unlock it in the window, -check cannot ask for the password")
	} else if err != nil {
		return err
	}
	for _, t := range imports {
		if _, err := manager.ImportTData(context.Background(), t); err != nil {
			return err
		}
	}
	accounts := manager.Accounts()
	if len(accounts) == 0 {
		return errors.New("-check needs an imported or saved account")
	}
	fmt.Printf("accounts: %d\n", len(accounts))
	for i, a := range accounts {
		store := tgstore.New(nil)
		if err := manager.Run(context.Background(), a, func(ctx context.Context, api *tg.Client) error {
			return store.Load(ctx, api)
		}); err != nil {
			return fmt.Errorf("account %d: %w", i+1, err)
		}
		fmt.Printf("account %d: ", i+1)
		printSummary(store)
	}
	return nil
}

// newManager returns an account manager that connects through the MTProxy
// in proxy, or directly if it is empty.
func newManager(proxy string, protection *security.Manager) (*account.Manager, error) {
	manager, err := account.NewManager(account.TDesktop(), protection)
	if err != nil {
		return nil, err
	}
	if err := manager.SetProxy(proxy); err != nil {
		return nil, err
	}
	return manager, nil
}

// printSummary tells what was loaded without printing anything personal.
func printSummary(store *tgstore.Store) {
	me := store.Me()
	kinds := map[model.ChatKind]int{}
	unread, pinned, muted := 0, 0, 0
	chats := store.Chats()
	for _, c := range chats {
		kinds[c.Kind]++
		if c.Unread > 0 {
			unread++
		}
		if c.Pinned {
			pinned++
		}
		if c.Muted {
			muted++
		}
	}
	fmt.Printf("profile: name %v, username %v, bio %v\n", me.FirstName != "", me.Username != "", me.Bio != "")
	fmt.Printf("chats: %d (users %d, saved %d, bots %d, groups %d, channels %d), pinned %d, unread %d, muted %d\n",
		len(chats), kinds[model.KindUser], kinds[model.KindSaved], kinds[model.KindBot],
		kinds[model.KindGroup], kinds[model.KindChannel], pinned, unread, muted)
	for i, f := range store.Folders() {
		n := 0
		for _, c := range chats {
			if f.Contains(c) {
				n++
			}
		}
		fmt.Printf("folder %d: %d chats\n", i+1, n)
	}
}

// trayBalloon shows notifications by the tray icon once there is one.
type trayBalloon struct{ icon atomic.Pointer[tray.Tray] }

func (b *trayBalloon) Notify(title, text string, sound bool) error {
	if icon := b.icon.Load(); icon != nil {
		return icon.Notify(title, text, sound)
	}
	return tray.ErrUnsupported
}

// useWasmCache keeps the decoders' compiled code on disk between starts:
// in the cache directory, signed with a key kept in the configuration
// directory, apart from it. Without it they still share code in memory.
func useWasmCache() {
	cache, err := os.UserCacheDir()
	if err != nil {
		log.Printf("wasm cache: %v", err)
		return
	}
	config, err := os.UserConfigDir()
	if err != nil {
		log.Printf("wasm cache: %v", err)
		return
	}
	c, err := sandbox.NewDiskCache(filepath.Join(cache, "komarugram-go", "wasm-cache"), filepath.Join(config, "komarugram-go", "wasm-cache.key"))
	if err != nil {
		log.Printf("wasm cache: %v; compiled code is kept in memory only", err)
		return
	}
	sandbox.SetCache(c)
}
