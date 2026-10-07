// SPDX-License-Identifier: Unlicense OR MIT

// Package appwindow runs the window shared by the applications of this
// project: it sets up the Material theme and the animation switch for every
// frame, watches the system color scheme and handles the window keys,
// leaving the content to the caller.
package appwindow

import (
	"image"
	"image/color"
	"komarugram/internal/alert"
	"komarugram/internal/crash"
	"komarugram/internal/diagnostics"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"gio-mw/exp/appearance"
	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"komarugram/internal/appicon"
	"komarugram/internal/motion"
)

// Content is what a window shows. It may also implement RecoverFrame() bool
// to leave or rebuild a failed view after its Update or Layout panics.
// Recovery runs after the frame's operations have been discarded; returning
// true schedules an immediate frame. See recoverContent.
type Content interface {
	// Theme returns the Material theme of the frame. It may create the theme
	// on its first call, which needs a frame context.
	Theme(gtx layout.Context) *token.Theme
	Update(gtx layout.Context)
	Layout(gtx layout.Context)
}

type Options struct {
	Title         string
	ProfileName   string // Empty excludes this window from opt-in profiling.
	Width, Height unit.Dp
	Locale        system.Locale
	// QuitOnEscape closes the window when Escape is released.
	QuitOnEscape bool
	TopMost      bool
	// DemoPanic raises one recoverable panic before the first UI update.
	DemoPanic bool
	// panicDialog marks the standalone crash window so its own failures do
	// not recursively open more crash windows.
	panicDialog bool
	// Transparent asks for a window the desktop shows through where the
	// content does not paint, and BlurBehind for the compositor to blur what
	// shows. Where either is unsupported the window is opaque or unblurred;
	// Window.Translucency reports what was granted.
	Transparent, BlurBehind bool
}

// Spec describes one independent application window. Closed is called after
// its event loop has stopped and before the process considers the window gone.
type Spec struct {
	Options Options
	Build   func(w *Window) Content
	// Activated is called whenever this window gains keyboard focus.
	Activated func()
	Closed    func()
}

// Window is the running window as seen by its content.
type Window struct {
	*app.Window
	host *Host
	// Motion decides whether widgets animate; the window applies it to
	// every frame.
	Motion *motion.Settings
	// Appearance follows the system color scheme; the window redraws when
	// it changes.
	Appearance  *appearance.Monitor
	fullscreen  bool
	profileName string
	titleMu     sync.Mutex
	title       string
	// titleDirty is whether the system has yet to get title.
	titleDirty bool
	suspended  atomic.Bool
	// transparent and blurred are what the platform granted of
	// Options.Transparent and Options.BlurBehind.
	transparent, blurred    bool
	frameDark, frameDarkSet bool
	frameColor              color.NRGBA
	// blurAsked is whether blur was asked for last; frame is the window's
	// own frame, drawn where the blur leaves it without the system's.
	blurAsked bool
	frame     frame
	// view is the native window, once there is one; captureExcluded is
	// whether it is hidden from screen capture, once captureApplied.
	view                            uintptr
	captureApplied, captureExcluded bool
	// caption is the height of the window's own frame over the content,
	// in pixels, as the last frame drew it.
	caption int
}

// Translucency reports whether the window is transparent and whether the
// compositor blurs behind it. It is meant for the window's own goroutine.
func (w *Window) Translucency() (transparent, blurred bool) {
	return w.transparent, w.blurred
}

// CanBeTransparent reports whether the window can be made transparent: it is
// now, or the system makes it when asked (macOS, where it is asked only when
// the surfaces are translucent, see WantsTransparent).
func (w *Window) CanBeTransparent() bool {
	return runtime.GOOS == "darwin" || w.transparent
}

// SetTitle changes the window title, from any goroutine. Setting the title
// it has does nothing: callers may repeat it on every update, and on
// Windows each change reconfigures the whole window.
//
// The window's own goroutine gives the title to the system, at its next
// event: an Option waits for the main thread, which, while it hands an
// event to another window, serves only that window's calls. A setting
// changed in one window, which tells every window's title, locked both
// windows so (the zoom of the article window, on macOS).
func (w *Window) SetTitle(title string) {
	w.titleMu.Lock()
	same := w.title == title
	w.title = title
	if !same {
		w.titleDirty = true
	}
	w.titleMu.Unlock()
	if !same && w.Window != nil {
		// Not w.Invalidate, which a suspended window ignores: the title
		// waits for its next event, which a hidden window may only have
		// once it is shown.
		w.Window.Invalidate()
	}
}

// applyTitle gives the system the title SetTitle was given last, if it has
// not had it. It runs on the window's goroutine.
func (w *Window) applyTitle() {
	w.titleMu.Lock()
	dirty, title := w.titleDirty, w.title
	w.titleDirty = false
	w.titleMu.Unlock()
	if dirty {
		setOption(w, app.Title(title))
	}
}

// setOption is w.Option, for the tests to see what is asked of the window.
var setOption = func(w *Window, opts ...app.Option) { w.Option(opts...) }

// PerformLater performs actions on w without waiting for them, from any
// goroutine; one window raising or closing another does so. Perform waits
// for the main thread, which, while it hands an event to the window
// calling, serves only that window: on macOS the two would wait for each
// other, as SetTitle's comment tells. The actions are not queued for w's
// own goroutine as the title is, since a minimized window may have no
// event to take them with, and a raise must reach it.
func (w *Window) PerformLater(actions system.Action) {
	go perform(w, actions)
}

// perform is w.Perform, for the tests to hold it.
var perform = func(w *Window, actions system.Action) { w.Perform(actions) }

// SetFrameDark picks the dark or the light look of the system's window frame
// (macOS), which otherwise follows the system, not the theme of the program.
// Setting the look it has does nothing.
func (w *Window) SetFrameDark(dark bool) {
	w.titleMu.Lock()
	same := w.frameDarkSet && w.frameDark == dark
	w.frameDarkSet, w.frameDark = true, dark
	w.titleMu.Unlock()
	if !same && w.Window != nil && runtime.GOOS == "darwin" {
		w.Option(app.DarkFrame(dark))
	}
}

// SetFrameColor colors the system's window frame (macOS) as the content, so
// that the two match. Setting the color it has does nothing.
func (w *Window) SetFrameColor(c color.NRGBA) {
	w.titleMu.Lock()
	same := w.frameColor == c
	w.frameColor = c
	w.titleMu.Unlock()
	if !same && w.Window != nil && runtime.GOOS == "darwin" {
		w.Option(app.FrameColor(c))
	}
}

// ToggleFullscreen switches between fullscreen and windowed mode. F11 does
// the same.
func (w *Window) ToggleFullscreen() {
	if w.fullscreen {
		w.Option(app.Windowed.Option())
	} else {
		w.Option(app.Fullscreen.Option())
	}
}

// Main opens the window, creates its content with build and runs it until
// the window is closed, then exits the process. It must be called from the
// main goroutine, like app.Main.
func Main(opts Options, build func(w *Window) Content) {
	MainMany([]Spec{{Options: opts, Build: build}})
}

// MainMany opens all windows in one process, each with its own event loop.
// Closing one window leaves the others running; the process exits after the
// last one closes. It must be called from the main goroutine.
func MainMany(specs []Spec) {
	if len(specs) == 0 {
		return
	}
	host := new(Host)
	host.EnableCrashDialogs()
	for _, spec := range specs {
		host.Open(spec)
	}
	host.Main()
}

// Host owns a dynamic group of windows in one process. Open may be called
// while Main is running, which lets one account window reopen another.
// The process exits once every window has closed and every Hold has been
// released.
type Host struct {
	// BeforeExit flushes process-scoped diagnostics after all windows close.
	BeforeExit func()
	windows    map[*Window]struct{}
	// hidden are the windows minimized or not presented. When they are all
	// of them, release gives the memory they dropped back to the system.
	hidden  map[*Window]struct{}
	release *time.Timer
	// trims give the memory of closed windows back to the system.
	trims []*time.Timer
	// freed gives back the memory a view dropped; see ReleaseMemoryLater.
	freed *time.Timer
	mu    sync.Mutex
	// remaining counts the open windows and holds; held counts the holds.
	remaining, held int64
	started         bool
	failed          atomic.Bool
	crashOnce       sync.Once
	crashDialogs    map[string]bool
	ignoredPanics   map[string]bool
	// captureExcluded hides the windows from screen capture.
	captureExcluded atomic.Bool
}

// Hold keeps the process running without a window, for work that goes on in
// the background, until release is called.
func (h *Host) Hold() (release func()) {
	h.mu.Lock()
	h.remaining++
	h.held++
	h.scheduleReleaseLocked()
	h.mu.Unlock()
	return sync.OnceFunc(func() {
		h.mu.Lock()
		h.held--
		h.scheduleReleaseLocked()
		h.mu.Unlock()
		h.done()
	})
}

// done ends a window or a hold, and the process with the last of them.
func (h *Host) done() {
	h.mu.Lock()
	h.remaining--
	last := h.started && h.remaining == 0
	h.mu.Unlock()
	if !last {
		return
	}
	if h.BeforeExit != nil {
		h.BeforeExit()
	}
	if h.failed.Load() {
		os.Exit(1)
	}
	os.Exit(0)
}

// ActivateAll raises every window with the XDG activation token, which may
// be empty; see [app.Window.Activate].
func (h *Host) ActivateAll(token string) {
	h.mu.Lock()
	windows := make([]*Window, 0, len(h.windows))
	for w := range h.windows {
		windows = append(windows, w)
	}
	h.mu.Unlock()
	for _, w := range windows {
		w.Activate(token)
	}
}

// releaseDelay lets the workers of hidden windows stop, and a window that
// is shown again at once skip the collection.
const releaseDelay = 5 * time.Second

// setHidden records whether w is hidden.
func (h *Host) setHidden(w *Window, hidden bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if hidden {
		if h.hidden == nil {
			h.hidden = map[*Window]struct{}{}
		}
		h.hidden[w] = struct{}{}
	} else {
		delete(h.hidden, w)
	}
	h.scheduleReleaseLocked()
}

// scheduleReleaseLocked starts the release when every window is hidden and
// cancels it otherwise. h.mu must be held.
func (h *Host) scheduleReleaseLocked() {
	if h.release != nil {
		h.release.Stop()
		h.release = nil
	}
	// Without windows, the process only waits in the background.
	if len(h.hidden) < len(h.windows) || len(h.windows) == 0 && h.held == 0 {
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(releaseDelay, func() {
		h.mu.Lock()
		current := h.release == timer
		h.release = nil
		h.mu.Unlock()
		if current {
			releaseMemory()
		}
	})
	h.release = timer
}

// ReleaseMemoryLater gives the system back, releaseDelay after the last
// call, memory that a view dropped in bulk, such as decoded sticker loops:
// the Go runtime returns it only gradually. A call in the meantime puts the
// release off, so that a view opened and closed again costs one collection.
func (w *Window) ReleaseMemoryLater() {
	if w.host != nil {
		w.host.releaseMemoryLater()
	}
}

// KeepMemory cancels the release ReleaseMemoryLater put off: the view that
// dropped the memory is shown again, and takes it back.
func (w *Window) KeepMemory() {
	if w.host == nil {
		return
	}
	h := w.host
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.freed != nil {
		h.freed.Stop()
		h.freed = nil
	}
}

func (h *Host) releaseMemoryLater() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.freed != nil {
		h.freed.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(releaseDelay, func() {
		h.mu.Lock()
		current := h.freed == timer
		if current {
			h.freed = nil
		}
		h.mu.Unlock()
		if current {
			releaseMemory()
		}
	})
	h.freed = timer
}

// closedReleaseDelays are when memory is released after a window closes:
// its UI's garbage in the Go heap, and what the GPU driver freed in the C
// heap, which glibc never returned. Without them every closed window left
// about 35 MB behind with Mesa. The second pass is for drivers that free
// later.
var closedReleaseDelays = []time.Duration{2 * time.Second, 10 * time.Second}

// releaseLaterLocked releases memory after the last window closed. h.mu must
// be held.
func (h *Host) releaseLaterLocked() {
	for _, t := range h.trims {
		t.Stop()
	}
	h.trims = h.trims[:0]
	for _, d := range closedReleaseDelays {
		h.trims = append(h.trims, time.AfterFunc(d, releaseMemory))
	}
}

// releaseMemory collects garbage and returns the freed pages to the system,
// which the Go runtime otherwise does only gradually and the C heap not at all.
func releaseMemory() {
	start := time.Now()
	debug.FreeOSMemory()
	trimCHeap()
	if r := diagnostics.Current(); r != nil {
		r.Event("process", "memory.released", "", time.Since(start), 0)
	}
}

// Open starts a window asynchronously.
func (h *Host) Open(spec Spec) {
	h.mu.Lock()
	h.remaining++
	w := &Window{Window: new(app.Window), host: h, title: spec.Options.Title}
	w.blurAsked = spec.Options.Transparent && spec.Options.BlurBehind
	if h.windows == nil {
		h.windows = map[*Window]struct{}{}
	}
	h.windows[w] = struct{}{}
	h.mu.Unlock()
	go func() {
		opts := spec.Options
		w.Option(app.Title(opts.Title), app.Size(opts.Width, opts.Height))
		w.Option(windowIcon())
		w.Option(effectOptions(opts.Transparent, opts.BlurBehind)...)
		if opts.TopMost {
			w.Option(app.TopMost(true))
		}
		err := runSafely(w, opts, spec.Build, spec.Activated)
		if err != nil {
			log.Println(err)
			h.failed.Store(true)
		}
		if spec.Closed != nil {
			spec.Closed()
		}
		// A panic has its own dialog. What else ends a window is the system
		// refusing it, a GPU context mostly, and another window would fail
		// the same way: the system's message box tells it, before the
		// process ends with its last window.
		if _, panicked := err.(*crash.Panic); err != nil && !panicked {
			alert.Error("Ошибка приложения", "Не удалось показать окно «"+opts.Title+"»:\n\n"+err.Error())
		}
		h.mu.Lock()
		h.releaseLaterLocked()
		delete(h.windows, w)
		delete(h.hidden, w)
		// The windows left may all be hidden.
		h.scheduleReleaseLocked()
		h.mu.Unlock()
		h.done()
	}()
}

func runSafely(w *Window, opts Options, build func(*Window) Content, activated func()) (err error) {
	where := "window " + opts.Title
	if opts.panicDialog {
		where = panicDialogWhere
	}
	defer crash.Recover(where, func(p *crash.Panic) { err = p })
	return run(w, opts, build, activated)
}

// Main enters Gio's process event loop. At least one window must be opened
// first, and Main must be called from the main goroutine.
func (h *Host) Main() {
	h.EnableCrashDialogs()
	h.mu.Lock()
	if h.remaining == 0 {
		h.mu.Unlock()
		return
	}
	h.started = true
	h.mu.Unlock()
	app.Main()
}

// EnableCrashDialogs subscribes before the first frame, including when Open
// starts a window goroutine before Main enters the process event loop.
func (h *Host) EnableCrashDialogs() {
	h.crashOnce.Do(func() { crash.Subscribe(h.showPanic) })
}

func run(w *Window, opts Options, build func(w *Window) Content, activated func()) error {
	w.profileName = opts.ProfileName
	w.Motion = motion.New(w.Invalidate)
	defer w.Motion.Close()
	w.Appearance = appearance.Start(func(appearance.Scheme) { w.Invalidate() })
	defer w.Appearance.Close()
	content := build(w)
	if closer, ok := content.(interface{ Close() }); ok {
		defer closer.Close()
	}

	var ops op.Ops
	var fallback panicScreen
	demoPanicked := false
	focused := false
	for {
		ev := w.Event()
		w.applyTitle()
		if handle, ok := viewHandle(ev); ok {
			w.view, w.captureApplied = handle, false
		}
		switch e := ev.(type) {
		case app.DestroyEvent:
			return e.Err
		case app.ConfigEvent:
			w.fullscreen = e.Config.Mode == app.Fullscreen
			w.transparent, w.blurred = e.Config.Transparent, e.Config.BlurBehind
			w.frame.configure(e.Config)
			if ownsFrame() && w.blurAsked && !e.Config.Decorated && !e.Config.BlurBehind {
				// No blur was granted: the system's frame is the better one.
				w.blurAsked = false
				w.Option(app.Decorated(true))
			}
			hidden := e.Config.Suspended || e.Config.Mode == app.Minimized
			if observer, ok := content.(interface{ SetMinimized(bool) }); ok {
				observer.SetMinimized(hidden)
			}
			if w.suspended.Swap(hidden) != hidden {
				if observer, ok := content.(interface{ SetSuspended(bool) }); ok {
					observer.SetSuspended(hidden)
				}
				if hidden {
					// Reset keeps the buffers of the last frame; a hidden
					// window gives them back.
					ops = op.Ops{}
				}
				w.host.setHidden(w, hidden)
				if r := diagnostics.Current(); r != nil && opts.ProfileName != "" {
					name := "window.resumed"
					if hidden {
						name = "window.suspended"
					}
					r.Event(opts.ProfileName, name, "", 0, 0)
				}
				if !hidden {
					w.Invalidate()
				}
			}
			if e.Config.Focused && !focused && activated != nil {
				activated()
			}
			if observer, ok := content.(interface{ SetFocused(bool) }); ok && e.Config.Focused != focused {
				observer.SetFocused(e.Config.Focused)
			}
			focused = e.Config.Focused
		case app.DropEvent:
			// Files dragged from other programs, where the content is.
			if target, ok := content.(interface{ Drop(app.DropEvent) }); ok {
				e.Position.Y -= float32(w.caption)
				target.Drop(e)
				w.Invalidate()
			}
		case app.FrameEvent:
			if w.suspended.Load() {
				// Drain an already queued frame without running the UI or requesting more.
				ops.Reset()
				e.Frame(&ops)
				if r := diagnostics.Current(); r != nil && opts.ProfileName != "" {
					r.Count(opts.ProfileName, "window.frame-skipped")
				}
				continue
			}
			var trace *diagnostics.Trace
			var frame diagnostics.Frame
			r := diagnostics.Current()
			if r != nil && opts.ProfileName != "" {
				frame.At = time.Now()
				frame.Window = opts.ProfileName
			}
			w.applyCapture()
			gtx := app.NewContext(&ops, e)
			gtx.Values = make(map[string]any)
			if !frame.At.IsZero() {
				trace = diagnostics.NewTrace(r, opts.ProfileName, gtx.Values)
				frame.Width, frame.Height = gtx.Constraints.Max.X, gtx.Constraints.Max.Y
				frame.PxPerDp, frame.PxPerSp = gtx.Metric.PxPerDp, gtx.Metric.PxPerSp
			}
			gtx.Locale = opts.Locale
			if provider, ok := content.(interface{ Locale() system.Locale }); ok {
				gtx.Locale = provider.Locale()
			}
			wdk.InitMaterialThemeInContext(gtx, content.Theme(gtx))
			wdk.SetAnimationsEnabled(gtx, w.Motion.AnimationsEnabled())

			area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
			event.Op(gtx.Ops, w.Window)
			if quit := w.handleKeys(gtx, opts); quit {
				return nil
			}
			// The window's own frame takes the top of it, and the content
			// the rest.
			w.titleMu.Lock()
			title := w.title
			w.titleMu.Unlock()
			if actions := w.frame.layout(gtx, title, content); actions != 0 {
				w.Perform(actions)
			}
			caption, window := w.frame.height(gtx), gtx.Constraints.Max
			w.caption = caption
			gtx.Constraints.Max.Y = max(gtx.Constraints.Max.Y-caption, 0)
			gtx.Constraints.Min.Y = min(gtx.Constraints.Min.Y, gtx.Constraints.Max.Y)
			below := op.Offset(image.Pt(0, caption)).Push(gtx.Ops)
			var phase time.Time
			if trace != nil {
				phase = time.Now()
				frame.Setup = phase.Sub(frame.At)
			}
			where := "window " + opts.Title
			if opts.panicDialog {
				where = panicDialogWhere
			}
			if p := crash.Guard(where, func() {
				if opts.DemoPanic && !demoPanicked {
					demoPanicked = true
					panic("демонстрационная паника (-demo-panic)")
				}
				content.Update(gtx)
				if trace != nil {
					frame.Update = time.Since(phase)
					phase = time.Now()
				}
				content.Layout(gtx)
			}); p != nil {
				// A panic leaves clip/transform stacks unbalanced and the frame
				// half recorded, so it is dropped as a whole.
				ops.Reset()
				gtx = app.NewContext(&ops, e)
				gtx.Values = make(map[string]any)
				wdk.InitMaterialThemeInContext(gtx, content.Theme(gtx))
				if actions := w.frame.layout(gtx, title, nil); actions != 0 {
					w.Perform(actions)
				}
				gtx.Constraints.Max.Y = max(gtx.Constraints.Max.Y-caption, 0)
				recovered := recoverContent(content)
				below := op.Offset(image.Pt(0, caption)).Push(gtx.Ops)
				fallback.layout(gtx, p)
				below.Pop()
				w.frame.layoutBorder(gtx, window)
				if recovered {
					gtx.Execute(op.InvalidateCmd{})
				}
				e.Frame(gtx.Ops)
				continue
			}
			if trace != nil {
				frame.Layout = time.Since(phase)
				phase = time.Now()
			}
			below.Pop()
			w.frame.layoutBorder(gtx, window)
			area.Pop()
			e.Frame(gtx.Ops)
			if trace != nil {
				frame.Submit = time.Since(phase)
				frame.Total = time.Since(frame.At)
				trace.Finish(frame)
			}
		}
	}
}

// handleKeys processes the window keys and reports whether to quit. It
// reads them before the content, and a key it reads the content never
// sees: Escape is the window's only when it closes the window, and the
// content's menus and dialogs take it otherwise.
func (w *Window) handleKeys(gtx layout.Context, opts Options) bool {
	filters := []event.Filter{key.Filter{Name: key.NameF11}}
	if opts.QuitOnEscape {
		filters = append(filters, key.Filter{Name: key.NameEscape})
	}
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			return false
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Release {
			continue
		}
		switch e.Name {
		case key.NameEscape:
			if opts.QuitOnEscape {
				return true
			}
		case key.NameF11:
			w.ToggleFullscreen()
		}
	}
}

// Invalidate counts external redraw requests only while diagnostics is enabled.
func (w *Window) Invalidate() {
	if w.suspended.Load() {
		if r := diagnostics.Current(); r != nil && w.profileName != "" {
			r.Count(w.profileName, "window.invalidate-suppressed")
		}
		return
	}
	if r := diagnostics.Current(); r != nil && w.profileName != "" {
		r.Count(w.profileName, "window.invalidate")
	}
	// A Window made without its app.Window, as in tests, has nothing to
	// redraw; work finishing in the background may still ask.
	if w.Window == nil {
		return
	}
	w.Window.Invalidate()
}

// CloseAll requests normal teardown so account workers and profile exports flush.
func (h *Host) CloseAll() {
	h.mu.Lock()
	windows := make([]*Window, 0, len(h.windows))
	for w := range h.windows {
		windows = append(windows, w)
	}
	h.mu.Unlock()
	for _, w := range windows {
		w.Perform(system.ActionClose)
	}
}

// windowIcon is the application's icon for a window, at the sizes desktops
// show it. Gio does not keep the images.
func windowIcon() app.Option {
	images := appicon.Images(appicon.WindowSizes...)
	icons := make([]image.Image, len(images))
	for i, im := range images {
		icons[i] = im
	}
	return app.Icon(icons...)
}
