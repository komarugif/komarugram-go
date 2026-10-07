// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package app

// The Haiku driver. Windows and input are the Be API's, in C++, in
// libgiohaiku (internal/haiku): Go's linker cannot take C++ objects into a
// Haiku program, so the library is built apart and loaded with dlopen.
// Each window's events wait in a queue in the library, read here by the
// window's goroutine. Gio draws with OSMesa into memory on that goroutine,
// and the window's own thread shows the finished frames (gl_haiku.go).

/*
#cgo CFLAGS: -Werror

#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include "internal/haiku/giohaiku.h"

static void *gh_lib;

#define GH_FUNCS(X) \
	X(int32_t, gh_abi, (void), ()) \
	X(int32_t, gh_init, (const char *a), (a)) \
	X(float, gh_ui_scale, (void), ()) \
	X(void *, gh_window_create, (int32_t a, int32_t b, const char *c, int32_t d), (a, b, c, d)) \
	X(void, gh_window_destroy, (void *a), (a)) \
	X(int32_t, gh_window_next_event, (void *a, gh_event *b, int64_t c), (a, b, c)) \
	X(void, gh_window_wake, (void *a), (a)) \
	X(void, gh_window_size, (void *a, int32_t *b, int32_t *c), (a, b, c)) \
	X(void, gh_window_set_title, (void *a, const char *b), (a, b)) \
	X(void, gh_window_set_size, (void *a, int32_t b, int32_t c), (a, b, c)) \
	X(void, gh_window_set_limits, (void *a, int32_t b, int32_t c, int32_t d, int32_t e), (a, b, c, d, e)) \
	X(void, gh_window_set_decorated, (void *a, int32_t b), (a, b)) \
	X(void, gh_window_set_mode, (void *a, int32_t b), (a, b)) \
	X(void, gh_window_center, (void *a), (a)) \
	X(void, gh_window_raise, (void *a), (a)) \
	X(void, gh_window_show, (void *a), (a)) \
	X(void, gh_window_set_cursor, (void *a, int32_t b), (a, b)) \
	X(int32_t, gh_gl_lock, (void *a, int32_t b, int32_t c), (a, b, c)) \
	X(void, gh_gl_unlock, (void *a), (a)) \
	X(void, gh_gl_swap, (void *a), (a)) \
	X(int32_t, gh_clipboard_write, (const char *a, const void *b, int32_t c), (a, b, c)) \
	X(void *, gh_clipboard_read, (const char *a, int32_t *b), (a, b)) \
	X(void, gh_free, (void *a), (a))

// The pointers to the library's functions, and a function of each name
// with a "p" in front that calls through its pointer, for cgo.
#define GH_PTR(ret, name, params, args) static ret (*name##_ptr) params;
GH_FUNCS(GH_PTR)
#define GH_CALL(ret, name, params, args) static ret p##name params { return name##_ptr args; }
GH_FUNCS(GH_CALL)

// gh_load opens the library at path and finds its functions. It returns
// NULL, or what went wrong.
static const char *gh_load(const char *path) {
	gh_lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (gh_lib == NULL)
		return dlerror();
#define GH_SYM(ret, name, params, args) \
	if ((name##_ptr = (ret (*) params)dlsym(gh_lib, #name)) == NULL) \
		return "missing " #name;
	GH_FUNCS(GH_SYM)
	return NULL;
}
*/
import "C"

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/io/transfer"
	"gioui.org/op"
	"gioui.org/unit"
)

// HaikuViewEvent is sent when a window gets or loses its BWindow.
type HaikuViewEvent struct {
	// Window is the BWindow*, nil when the window is gone.
	Window unsafe.Pointer
}

func (HaikuViewEvent) implementsViewEvent() {}
func (HaikuViewEvent) ImplementsEvent()     {}
func (h HaikuViewEvent) Valid() bool {
	return h.Window != nil
}

type haikuWindow struct {
	w   *callbacks
	win unsafe.Pointer

	config      Config
	metric      unit.Metric
	animating   bool
	pointerBtns pointer.Buttons
	// pointerPos is where the pointer last was: Haiku's wheel messages
	// carry no position, and Gio scrolls what is under the pointer.
	pointerPos f32.Point
	modifiers  key.Modifiers
	cursor     pointer.Cursor
	// closing is set by Perform(ActionClose), for the event loop to end
	// the window.
	closing bool
	ev      C.gh_event
}

var haikuLib struct {
	once sync.Once
	err  error
}

// loadHaiku opens libgiohaiku and starts the BApplication. The library is
// looked for in GIO_HAIKU_LIB, beside the program, in its lib directory,
// and where Haiku looks for libraries.
func loadHaiku() error {
	haikuLib.once.Do(func() {
		var tried []string
		var candidates []string
		if p := os.Getenv("GIO_HAIKU_LIB"); p != "" {
			candidates = append(candidates, p)
		}
		if exe, err := os.Executable(); err == nil {
			dir := filepath.Dir(exe)
			candidates = append(candidates, filepath.Join(dir, "libgiohaiku.so"), filepath.Join(dir, "lib", "libgiohaiku.so"))
		}
		candidates = append(candidates, "libgiohaiku.so")
		for _, p := range candidates {
			cp := C.CString(p)
			msg := C.gh_load(cp)
			C.free(unsafe.Pointer(cp))
			if msg == nil {
				haikuLib.err = nil
				break
			}
			tried = append(tried, C.GoString(msg))
			haikuLib.err = fmt.Errorf("haiku: cannot load libgiohaiku: %s", strings.Join(tried, "; "))
		}
		if haikuLib.err != nil {
			return
		}
		if abi := C.pgh_abi(); abi != C.GH_ABI {
			haikuLib.err = fmt.Errorf("haiku: libgiohaiku is of interface %d, not %d", abi, C.GH_ABI)
			return
		}
		sig := C.CString("application/x-vnd." + haikuSignature(ID))
		defer C.free(unsafe.Pointer(sig))
		if st := C.pgh_init(sig); st != 0 {
			haikuLib.err = fmt.Errorf("haiku: BApplication failed: %d", st)
		}
	})
	return haikuLib.err
}

// haikuSignature makes a MIME subtype of the program's ID.
func haikuSignature(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "gio-app"
	}
	return b.String()
}

func osMain() {
	select {}
}

func newWindow(window *callbacks, options []Option) {
	if err := newHaikuWindow(window, options); err != nil {
		window.ProcessEvent(DestroyEvent{Err: err})
	}
}

func newHaikuWindow(gioWin *callbacks, options []Option) error {
	if err := loadHaiku(); err != nil {
		return err
	}
	scale := float32(C.pgh_ui_scale())
	if scale <= 0 {
		scale = 1
	}
	w := &haikuWindow{
		w:      gioWin,
		metric: unit.Metric{PxPerDp: scale, PxPerSp: scale},
	}
	cnf := Config{Decorated: true}
	cnf.apply(w.metric, options)
	title := C.CString(cnf.Title)
	defer C.free(unsafe.Pointer(title))
	size := cnf.Size
	if size.X <= 0 || size.Y <= 0 {
		size = image.Pt(800, 600)
	}
	dec := C.int32_t(0)
	if cnf.Decorated {
		dec = 1
	}
	w.win = C.pgh_window_create(C.int32_t(size.X), C.int32_t(size.Y), title, dec)
	if w.win == nil {
		return errors.New("haiku: cannot create the window")
	}
	var gotW, gotH C.int32_t
	C.pgh_window_size(w.win, &gotW, &gotH)
	if gotW > 0 && gotH > 0 {
		size = image.Pt(int(gotW), int(gotH))
	}
	w.config = Config{Size: size, Title: cnf.Title, Decorated: cnf.Decorated, Mode: Windowed}
	w.Configure(options)
	C.pgh_window_show(w.win)
	w.w.SetDriver(w)
	w.ProcessEvent(HaikuViewEvent{Window: w.win})
	return nil
}

// haikuTrace prints the events the driver gives Gio, with
// GIO_HAIKU_TRACE=1, for finding what input goes wrong.
var haikuTrace = os.Getenv("GIO_HAIKU_TRACE") == "1"

func (w *haikuWindow) ProcessEvent(e event.Event) {
	if haikuTrace {
		switch e.(type) {
		case frameEvent:
		default:
			fmt.Fprintf(os.Stderr, "gio-haiku: %T %+v\n", e, e)
		}
	}
	w.w.ProcessEvent(e)
}

func (w *haikuWindow) Event() event.Event {
	for {
		evt, ok := w.w.nextEvent()
		if !ok {
			w.dispatch()
			continue
		}
		return evt
	}
}

// frameInterval paces the frames of an animation: nothing tells when the
// screen is redrawn.
const frameInterval = time.Second / 60

// dispatch waits for the window's events, hands them to Gio, and asks for
// a frame when one is due.
func (w *haikuWindow) dispatch() {
	if w.win == nil {
		// The window is gone; only Invalidate wakes us, and nothing more
		// comes.
		time.Sleep(time.Hour)
		return
	}
	anim := w.animating && !w.config.Suspended && w.config.Mode != Minimized
	timeout := C.int64_t(-1)
	if anim {
		timeout = C.int64_t(frameInterval / time.Microsecond)
	}
	var syn bool
	for C.pgh_window_next_event(w.win, &w.ev, timeout) != 0 {
		redraw, done := w.handle(&w.ev)
		if done {
			return
		}
		syn = syn || redraw
		// Take what else is waiting, without waiting more.
		timeout = 0
	}
	if w.closing {
		w.shutdown(nil)
		return
	}
	anim = w.animating && !w.config.Suspended && w.config.Mode != Minimized
	if (anim || syn) && w.config.Mode != Minimized && w.config.Size.X != 0 && w.config.Size.Y != 0 {
		w.ProcessEvent(frameEvent{
			FrameEvent: FrameEvent{
				Now:    time.Now(),
				Size:   w.config.Size,
				Metric: w.metric,
			},
			Sync: syn,
		})
	}
}

// handle hands one event of the library to Gio. It reports whether the
// window must be drawn again, and whether it is gone.
func (w *haikuWindow) handle(ev *C.gh_event) (redraw, done bool) {
	switch ev._type {
	case C.GH_EV_WAKE:
		w.w.Invalidate()
	case C.GH_EV_CLOSE:
		w.shutdown(nil)
		return false, true
	case C.GH_EV_RESIZE:
		size := image.Pt(int(ev.x), int(ev.y))
		if size != w.config.Size {
			w.config.Size = size
			w.ProcessEvent(ConfigEvent{Config: w.config})
		}
		redraw = true
	case C.GH_EV_REDRAW:
		redraw = true
	case C.GH_EV_FOCUS:
		w.config.Focused = ev.x != 0
		w.ProcessEvent(ConfigEvent{Config: w.config})
	case C.GH_EV_MINIMIZE:
		if ev.x != 0 {
			w.config.Mode = Minimized
		} else if w.config.Mode == Minimized {
			w.config.Mode = Windowed
		}
		w.ProcessEvent(ConfigEvent{Config: w.config})
		redraw = ev.x == 0
	case C.GH_EV_ZOOM:
		if ev.x != 0 {
			w.config.Mode = Maximized
		} else if w.config.Mode == Maximized {
			w.config.Mode = Windowed
		}
		w.ProcessEvent(ConfigEvent{Config: w.config})
	case C.GH_EV_MOUSE_DOWN, C.GH_EV_MOUSE_UP, C.GH_EV_MOUSE_MOVE:
		w.pointer(ev)
	case C.GH_EV_MOUSE_EXIT:
		w.ProcessEvent(pointer.Event{
			Kind:      pointer.Leave,
			Source:    pointer.Mouse,
			Time:      haikuTime(ev.when),
			Modifiers: w.modifiers,
		})
	case C.GH_EV_WHEEL:
		w.modifiers = haikuModifiers(uint32(ev.modifiers))
		// Haiku tells a wheel's notch as 1; it scrolls 100 pixels, as a
		// notch does on Wayland and Windows (scroll.NotchPixels in gio-mw).
		const notchPixels = 100
		w.ProcessEvent(pointer.Event{
			Kind:      pointer.Scroll,
			Source:    pointer.Mouse,
			Buttons:   w.pointerBtns,
			Position:  w.pointerPos,
			Scroll:    f32.Pt(float32(ev.fx)*notchPixels, float32(ev.fy)*notchPixels),
			Wheel:     true,
			Time:      haikuTime(ev.when),
			Modifiers: w.modifiers,
		})
	case C.GH_EV_KEY_DOWN, C.GH_EV_KEY_UP:
		w.key(ev)
	case C.GH_EV_MODIFIERS:
		w.modifiers = haikuModifiers(uint32(ev.modifiers))
	case C.GH_EV_INPUT_METHOD:
		if ev.x != 0 {
			if s := C.GoString(&ev.text[0]); s != "" {
				w.w.EditorInsert(s)
			}
		}
	}
	return redraw, false
}

// Haiku's mouse buttons.
const (
	haikuPrimaryButton    = 1
	haikuSecondaryButton  = 2
	haikuTertiaryButton   = 4
	haikuQuaternaryButton = 8
	haikuQuinaryButton    = 16
)

func haikuButtons(b uint32) pointer.Buttons {
	var btns pointer.Buttons
	if b&haikuPrimaryButton != 0 {
		btns |= pointer.ButtonPrimary
	}
	if b&haikuSecondaryButton != 0 {
		btns |= pointer.ButtonSecondary
	}
	if b&haikuTertiaryButton != 0 {
		btns |= pointer.ButtonTertiary
	}
	if b&haikuQuaternaryButton != 0 {
		btns |= pointer.ButtonQuaternary
	}
	if b&haikuQuinaryButton != 0 {
		btns |= pointer.ButtonQuinary
	}
	return btns
}

func (w *haikuWindow) pointer(ev *C.gh_event) {
	btns := haikuButtons(uint32(ev.buttons))
	w.modifiers = haikuModifiers(uint32(ev.modifiers))
	e := pointer.Event{
		Source:    pointer.Mouse,
		Position:  f32.Pt(float32(ev.fx), float32(ev.fy)),
		Time:      haikuTime(ev.when),
		Modifiers: w.modifiers,
	}
	switch ev._type {
	case C.GH_EV_MOUSE_DOWN:
		e.Kind = pointer.Press
		// A press of a second button while one is held comes as a move
		// on some systems; here every press is one.
		e.Buttons = btns | w.pointerBtns
	case C.GH_EV_MOUSE_UP:
		e.Kind = pointer.Release
		e.Buttons = btns
	default:
		e.Kind = pointer.Move
		e.Buttons = btns
		if btns != w.pointerBtns {
			// A button changed without its own event; tell it as Gio
			// expects.
			pressed := btns &^ w.pointerBtns
			released := w.pointerBtns &^ btns
			if pressed != 0 {
				e.Kind = pointer.Press
			} else if released != 0 {
				e.Kind = pointer.Release
			}
		}
	}
	w.pointerBtns = e.Buttons
	w.pointerPos = e.Position
	w.ProcessEvent(e)
}

func haikuTime(when C.int64_t) time.Duration {
	return time.Duration(when) * time.Microsecond
}

// Haiku's modifier keys (InterfaceDefs.h).
const (
	haikuShiftKey   = 0x01
	haikuCommandKey = 0x02
	haikuControlKey = 0x04
	haikuOptionKey  = 0x40
)

// haikuModifiers maps Haiku's modifiers to Gio's. Haiku's shortcut key is
// Command, Alt on most keyboards: it is Gio's Ctrl, as Qt's Haiku port has
// it, and Control is too, for those used to it elsewhere. Option, the
// Windows key, is Alt.
func haikuModifiers(m uint32) key.Modifiers {
	var mods key.Modifiers
	if m&haikuShiftKey != 0 {
		mods |= key.ModShift
	}
	if m&(haikuCommandKey|haikuControlKey) != 0 {
		mods |= key.ModCtrl
	}
	if m&haikuOptionKey != 0 {
		mods |= key.ModAlt
	}
	return mods
}

// haikuKeys names the keys Gio has names for, by Haiku's key codes, which
// do not change with the keyboard layout.
var haikuKeys = map[int32]key.Name{
	0x01: key.NameEscape,
	0x02: key.NameF1, 0x03: key.NameF2, 0x04: key.NameF3, 0x05: key.NameF4,
	0x06: key.NameF5, 0x07: key.NameF6, 0x08: key.NameF7, 0x09: key.NameF8,
	0x0a: key.NameF9, 0x0b: key.NameF10, 0x0c: key.NameF11, 0x0d: key.NameF12,
	0x1e: key.NameDeleteBackward,
	0x20: key.NameHome, 0x21: key.NamePageUp,
	0x26: key.NameTab,
	0x34: key.NameDeleteForward, 0x35: key.NameEnd, 0x36: key.NamePageDown,
	0x47: key.NameReturn,
	0x57: key.NameUpArrow,
	0x5b: key.NameEnter,
	0x5e: key.NameSpace,
	0x61: key.NameLeftArrow, 0x62: key.NameDownArrow, 0x63: key.NameRightArrow,
	0x4b: key.NameShift, 0x56: key.NameShift,
	0x5c: key.NameCtrl, 0x60: key.NameCtrl,
	0x5d: key.NameCtrl, 0x5f: key.NameCtrl, // Command: Alt on most keyboards
	0x66: key.NameAlt, 0x67: key.NameAlt, // Option: the Windows keys
}

// haikuLatinKeys is the US layout's letter or digit of each key, the name
// of a key in a shortcut whatever the layout: Ctrl+C is Ctrl+C with a
// Russian layout as well.
var haikuLatinKeys = map[int32]key.Name{
	0x12: "1", 0x13: "2", 0x14: "3", 0x15: "4", 0x16: "5",
	0x17: "6", 0x18: "7", 0x19: "8", 0x1a: "9", 0x1b: "0",
	0x27: "Q", 0x28: "W", 0x29: "E", 0x2a: "R", 0x2b: "T",
	0x2c: "Y", 0x2d: "U", 0x2e: "I", 0x2f: "O", 0x30: "P",
	0x3c: "A", 0x3d: "S", 0x3e: "D", 0x3f: "F", 0x40: "G",
	0x41: "H", 0x42: "J", 0x43: "K", 0x44: "L",
	0x4c: "Z", 0x4d: "X", 0x4e: "C", 0x4f: "V", 0x50: "B",
	0x51: "N", 0x52: "M",
}

func (w *haikuWindow) key(ev *C.gh_event) {
	w.modifiers = haikuModifiers(uint32(ev.modifiers))
	text := C.GoString(&ev.text[0])
	code := int32(ev.key)
	name, named := haikuKeys[code]
	shortcut := w.modifiers&(key.ModCtrl|key.ModAlt) != 0
	if !named {
		if latin, ok := haikuLatinKeys[code]; ok && shortcut {
			name, named = latin, true
		} else if r, _ := utf8.DecodeRuneInString(text); r != utf8.RuneError && unicode.IsPrint(r) {
			name, named = key.Name(strings.ToUpper(string(r))), true
		} else if ok {
			name, named = latin, true
		}
	}
	state := key.Press
	if ev._type == C.GH_EV_KEY_UP {
		state = key.Release
	}
	if named {
		w.ProcessEvent(key.Event{Name: name, Modifiers: w.modifiers, State: state})
	}
	// Text comes with the presses of keys that are not shortcuts.
	if state == key.Press && w.modifiers&key.ModCtrl == 0 && text != "" {
		if r, _ := utf8.DecodeRuneInString(text); unicode.IsPrint(r) {
			w.w.EditorInsert(text)
		}
	}
}

// The GL context's calls, for gl_haiku.go.
func haikuGLLock(win unsafe.Pointer, size image.Point) error {
	if st := C.pgh_gl_lock(win, C.int32_t(size.X), C.int32_t(size.Y)); st != 0 {
		return fmt.Errorf("haiku: OSMesa context failed: %d", st)
	}
	return nil
}
func haikuGLUnlock(win unsafe.Pointer) { C.pgh_gl_unlock(win) }
func haikuGLSwap(win unsafe.Pointer)   { C.pgh_gl_swap(win) }

func (w *haikuWindow) Invalidate() {
	if w.win != nil {
		C.pgh_window_wake(w.win)
	}
}

func (w *haikuWindow) SetAnimating(anim bool) {
	w.animating = anim
}

func (w *haikuWindow) ShowTextInput(show bool)                 {}
func (w *haikuWindow) SetInputHint(_ key.InputHint)            {}
func (w *haikuWindow) EditorStateChanged(old, new editorState) {}

func (w *haikuWindow) NewContext() (context, error) {
	return newHaikuGLContext(w)
}

func (w *haikuWindow) ReadClipboard(types []string) {
	for _, typ := range types {
		mime := typ
		if typ == "application/text" || typ == "text/plain" {
			mime = "text/plain"
		}
		cmime := C.CString(mime)
		var n C.int32_t
		p := C.pgh_clipboard_read(cmime, &n)
		C.free(unsafe.Pointer(cmime))
		if p == nil {
			continue
		}
		data := C.GoBytes(p, C.int(n))
		C.pgh_free(p)
		w.ProcessEvent(transfer.DataEvent{
			Type: typ,
			Open: func() io.ReadCloser { return io.NopCloser(bytes.NewReader(data)) },
		})
		return
	}
	w.ProcessEvent(transfer.DataEvent{
		Open: func() io.ReadCloser { return io.NopCloser(bytes.NewReader(nil)) },
	})
}

func (w *haikuWindow) WriteClipboard(mime string, s []byte) {
	if mime == "application/text" {
		mime = "text/plain"
	}
	cmime := C.CString(mime)
	defer C.free(unsafe.Pointer(cmime))
	var p unsafe.Pointer
	if len(s) > 0 {
		p = C.CBytes(s)
		defer C.free(p)
	}
	C.pgh_clipboard_write(cmime, p, C.int32_t(len(s)))
}

func (w *haikuWindow) WriteClipboardHTML(text, html []byte) {
	// Haiku's clipboard holds one message with any number of types; the
	// text is what other programs read.
	w.WriteClipboard("text/plain", text)
}

func (w *haikuWindow) Configure(options []Option) {
	prev := w.config
	cnf := w.config
	cnf.apply(w.metric, options)
	if cnf.Title != prev.Title {
		t := C.CString(cnf.Title)
		C.pgh_window_set_title(w.win, t)
		C.free(unsafe.Pointer(t))
		w.config.Title = cnf.Title
	}
	if cnf.Decorated != prev.Decorated {
		d := C.int32_t(0)
		if cnf.Decorated {
			d = 1
		}
		C.pgh_window_set_decorated(w.win, d)
		w.config.Decorated = cnf.Decorated
	}
	if cnf.MinSize != prev.MinSize || cnf.MaxSize != prev.MaxSize {
		C.pgh_window_set_limits(w.win, C.int32_t(cnf.MinSize.X), C.int32_t(cnf.MinSize.Y), C.int32_t(cnf.MaxSize.X), C.int32_t(cnf.MaxSize.Y))
		w.config.MinSize, w.config.MaxSize = cnf.MinSize, cnf.MaxSize
	}
	if cnf.Mode != prev.Mode {
		var mode C.int32_t
		switch cnf.Mode {
		case Minimized:
			mode = C.GH_MODE_MINIMIZED
		case Maximized:
			mode = C.GH_MODE_MAXIMIZED
		case Fullscreen:
			mode = C.GH_MODE_FULLSCREEN
		default:
			mode = C.GH_MODE_WINDOWED
		}
		C.pgh_window_set_mode(w.win, mode)
		w.config.Mode = cnf.Mode
	} else if cnf.Mode == Windowed && cnf.Size != prev.Size && cnf.Size.X > 0 && cnf.Size.Y > 0 {
		C.pgh_window_set_size(w.win, C.int32_t(cnf.Size.X), C.int32_t(cnf.Size.Y))
		// The window keeps within the screen; its size comes back as a
		// resize event.
	}
	w.ProcessEvent(ConfigEvent{Config: w.config})
}

func (w *haikuWindow) SetCursor(cursor pointer.Cursor) {
	w.cursor = cursor
	if w.win != nil {
		C.pgh_window_set_cursor(w.win, C.int32_t(haikuCursor(cursor)))
	}
}

func haikuCursor(c pointer.Cursor) int {
	switch c {
	case pointer.CursorNone:
		return C.GH_CURSOR_NONE
	case pointer.CursorText:
		return C.GH_CURSOR_TEXT
	case pointer.CursorVerticalText:
		return C.GH_CURSOR_VERTICAL_TEXT
	case pointer.CursorPointer:
		return C.GH_CURSOR_POINTER
	case pointer.CursorCrosshair:
		return C.GH_CURSOR_CROSSHAIR
	case pointer.CursorAllScroll:
		return C.GH_CURSOR_ALL_SCROLL
	case pointer.CursorColResize:
		return C.GH_CURSOR_COL_RESIZE
	case pointer.CursorRowResize:
		return C.GH_CURSOR_ROW_RESIZE
	case pointer.CursorGrab:
		return C.GH_CURSOR_GRAB
	case pointer.CursorGrabbing:
		return C.GH_CURSOR_GRABBING
	case pointer.CursorNotAllowed:
		return C.GH_CURSOR_NOT_ALLOWED
	case pointer.CursorWait:
		return C.GH_CURSOR_WAIT
	case pointer.CursorProgress:
		return C.GH_CURSOR_PROGRESS
	case pointer.CursorNorthWestResize:
		return C.GH_CURSOR_NW_RESIZE
	case pointer.CursorNorthEastResize:
		return C.GH_CURSOR_NE_RESIZE
	case pointer.CursorSouthWestResize:
		return C.GH_CURSOR_SW_RESIZE
	case pointer.CursorSouthEastResize:
		return C.GH_CURSOR_SE_RESIZE
	case pointer.CursorNorthSouthResize:
		return C.GH_CURSOR_NS_RESIZE
	case pointer.CursorEastWestResize:
		return C.GH_CURSOR_EW_RESIZE
	case pointer.CursorWestResize:
		return C.GH_CURSOR_W_RESIZE
	case pointer.CursorEastResize:
		return C.GH_CURSOR_E_RESIZE
	case pointer.CursorNorthResize:
		return C.GH_CURSOR_N_RESIZE
	case pointer.CursorSouthResize:
		return C.GH_CURSOR_S_RESIZE
	case pointer.CursorNorthEastSouthWestResize:
		return C.GH_CURSOR_NESW_RESIZE
	case pointer.CursorNorthWestSouthEastResize:
		return C.GH_CURSOR_NWSE_RESIZE
	}
	return C.GH_CURSOR_DEFAULT
}

func (w *haikuWindow) Perform(acts system.Action) {
	walkActions(acts, func(a system.Action) {
		switch a {
		case system.ActionCenter:
			C.pgh_window_center(w.win)
		case system.ActionRaise:
			C.pgh_window_raise(w.win)
		}
	})
	if acts&system.ActionClose != 0 {
		w.closing = true
		C.pgh_window_wake(w.win)
	}
}

func (w *haikuWindow) Run(f func()) {
	f()
}

func (w *haikuWindow) Frame(frame *op.Ops) {
	if !haikuTrace {
		w.w.ProcessFrame(frame, nil)
		return
	}
	start := time.Now()
	w.w.ProcessFrame(frame, nil)
	if d := time.Since(start); d > 30*time.Millisecond {
		fmt.Fprintf(os.Stderr, "gio-haiku: frame %v, size %v\n", d, w.config.Size)
	}
}

func (w *haikuWindow) shutdown(err error) {
	w.ProcessEvent(HaikuViewEvent{})
	w.ProcessEvent(DestroyEvent{Err: err})
	if w.win != nil {
		C.pgh_window_destroy(w.win)
		w.win = nil
	}
}
