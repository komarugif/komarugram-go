// SPDX-License-Identifier: Unlicense OR MIT

//go:build ((linux && !android) || freebsd || openbsd) && !nox11

package app

/*
#cgo freebsd openbsd CFLAGS: -I/usr/X11R6/include -I/usr/local/include
#cgo freebsd openbsd LDFLAGS: -L/usr/X11R6/lib -L/usr/local/lib
#cgo freebsd openbsd LDFLAGS: -lX11 -lxkbcommon -lxkbcommon-x11 -lX11-xcb -lXcursor -lXfixes
#cgo linux pkg-config: x11 xkbcommon xkbcommon-x11 x11-xcb xcursor xfixes

#include <stdlib.h>
#include <locale.h>
#include <X11/Xlib.h>
#include <X11/Xatom.h>
#include <X11/Xutil.h>
#include <X11/Xresource.h>
#include <X11/XKBlib.h>
#include <X11/Xlib-xcb.h>
#include <X11/extensions/Xfixes.h>
#include <X11/Xcursor/Xcursor.h>
#include <xkbcommon/xkbcommon-x11.h>

*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/op"
	"gioui.org/unit"

	syscall "golang.org/x/sys/unix"

	"gioui.org/app/internal/xkb"
)

const (
	_NET_WM_STATE_REMOVE = 0
	_NET_WM_STATE_ADD    = 1
)

type x11Window struct {
	w            *callbacks
	x            *C.Display
	xkb          *xkb.Context
	xkbEventBase C.int
	xw           C.Window

	atoms struct {
		// "UTF8_STRING".
		utf8string C.Atom
		// "text/plain;charset=utf-8".
		plaintext C.Atom
		// "TARGETS"
		targets C.Atom
		// "image/png"
		imagePNG C.Atom
		// "text/html", the HTML beside the text.
		textHTML C.Atom
		// "CLIPBOARD".
		clipboard C.Atom
		// "PRIMARY".
		primary C.Atom
		// "CLIPBOARD_CONTENT", the clipboard destination property.
		clipboardContent C.Atom
		// "WM_DELETE_WINDOW"
		evDelWindow C.Atom
		// "ATOM"
		atom C.Atom
		// "GTK_TEXT_BUFFER_CONTENTS"
		gtk_text_buffer_contents C.Atom
		// "_NET_WM_NAME"
		wmName C.Atom
		// "_NET_WM_STATE"
		wmState C.Atom
		// "_NET_WM_STATE_FULLSCREEN"
		wmStateFullscreen C.Atom
		// "_NET_ACTIVE_WINDOW"
		wmActiveWindow C.Atom
		// _NET_WM_STATE_MAXIMIZED_HORZ
		wmStateMaximizedHorz C.Atom
		// _NET_WM_STATE_MAXIMIZED_VERT
		wmStateMaximizedVert C.Atom
		// "_NET_WM_ICON"
		wmIcon C.Atom
		// "CARDINAL"
		cardinal C.Atom
	}
	metric unit.Metric
	notify struct {
		read, write int
	}

	animating   bool
	restoreMode WindowMode

	pointerBtns pointer.Buttons

	clipboard struct {
		content []byte
		// mime is the type of content: "image/png", or text.
		mime string
		// html is the text's HTML, offered beside it, or nil.
		html []byte
	}
	cursor pointer.Cursor
	config Config

	// dnd is the drag of files over the window.
	dnd x11Drag
	// clipRead is a read of the clipboard (os_x11_clipboard.go).
	clipRead x11ClipRead

	// xi2 is set when the window takes its pointer events through
	// XInput 2, for smooth scrolling (os_x11_xi2.go).
	xi2 *x11XI2

	wakeups chan struct{}
	handler x11EventHandler
	buf     [100]byte
}

var (
	newX11EGLContext    func(w *x11Window) (context, error)
	newX11VulkanContext func(w *x11Window) (context, error)
)

// X11 and Vulkan doesn't work reliably on NVIDIA systems.
// See https://gioui.org/issue/347.
const vulkanBuggy = true

func (w *x11Window) NewContext() (context, error) {
	var firstErr error
	if f := newX11VulkanContext; f != nil && !vulkanBuggy {
		c, err := f(w)
		if err == nil {
			return c, nil
		}
		firstErr = err
	}
	if f := newX11EGLContext; f != nil {
		c, err := f(w)
		if err == nil {
			return c, nil
		}
		firstErr = err
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, errors.New("x11: no available GPU backends")
}

func (w *x11Window) SetAnimating(anim bool) {
	w.animating = anim
}

func (w *x11Window) WriteClipboard(mime string, s []byte) {
	w.clipboard.content = s
	w.clipboard.mime = mime
	w.clipboard.html = nil
	C.XSetSelectionOwner(w.x, w.atoms.clipboard, w.xw, C.CurrentTime)
	C.XSetSelectionOwner(w.x, w.atoms.primary, w.xw, C.CurrentTime)
}

func (w *x11Window) WriteClipboardHTML(text, html []byte) {
	w.clipboard.content = text
	w.clipboard.mime = "application/text"
	w.clipboard.html = html
	C.XSetSelectionOwner(w.x, w.atoms.clipboard, w.xw, C.CurrentTime)
	C.XSetSelectionOwner(w.x, w.atoms.primary, w.xw, C.CurrentTime)
}

func (w *x11Window) Configure(options []Option) {
	var shints C.XSizeHints
	prev := w.config
	cnf := w.config
	cnf.apply(w.metric, options)
	// Decorations are never disabled.
	cnf.Decorated = true
	if cnf.icon != nil {
		w.setIcon(cnf.icon)
	}

	switch cnf.Mode {
	case Fullscreen:
		switch prev.Mode {
		case Fullscreen:
		case Minimized:
			w.raise()
			fallthrough
		default:
			w.config.Mode = Fullscreen
			w.sendWMStateEvent(_NET_WM_STATE_ADD, w.atoms.wmStateFullscreen, 0)
		}
	case Minimized:
		switch prev.Mode {
		case Minimized, Fullscreen:
		default:
			w.config.Mode = Minimized
			screen := C.XDefaultScreen(w.x)
			C.XIconifyWindow(w.x, w.xw, screen)
		}
	case Maximized:
		switch prev.Mode {
		case Fullscreen:
		case Minimized:
			w.raise()
			fallthrough
		default:
			w.config.Mode = Maximized
			w.sendWMStateEvent(_NET_WM_STATE_ADD, w.atoms.wmStateMaximizedHorz, w.atoms.wmStateMaximizedVert)
			w.setTitle(prev, cnf)
		}
	case Windowed:
		switch prev.Mode {
		case Fullscreen:
			w.config.Mode = Windowed
			w.sendWMStateEvent(_NET_WM_STATE_REMOVE, w.atoms.wmStateFullscreen, 0)
			C.XResizeWindow(w.x, w.xw, C.uint(cnf.Size.X), C.uint(cnf.Size.Y))
		case Minimized:
			w.config.Mode = Windowed
			w.raise()
		case Maximized:
			w.config.Mode = Windowed
			w.sendWMStateEvent(_NET_WM_STATE_REMOVE, w.atoms.wmStateMaximizedHorz, w.atoms.wmStateMaximizedVert)
		}
		w.setTitle(prev, cnf)
		if prev.Size != cnf.Size {
			w.config.Size = cnf.Size
			C.XResizeWindow(w.x, w.xw, C.uint(cnf.Size.X), C.uint(cnf.Size.Y))
		}
		if prev.MinSize != cnf.MinSize {
			w.config.MinSize = cnf.MinSize
			shints.min_width = C.int(cnf.MinSize.X)
			shints.min_height = C.int(cnf.MinSize.Y)
			shints.flags = C.PMinSize
		}
		if prev.MaxSize != cnf.MaxSize {
			w.config.MaxSize = cnf.MaxSize
			shints.max_width = C.int(cnf.MaxSize.X)
			shints.max_height = C.int(cnf.MaxSize.Y)
			shints.flags = shints.flags | C.PMaxSize
		}
		if shints.flags != 0 {
			C.XSetWMNormalHints(w.x, w.xw, &shints)
		}
	}
	if cnf.Decorated != prev.Decorated {
		w.config.Decorated = cnf.Decorated
	}
	w.ProcessEvent(ConfigEvent{Config: w.config})
}

// setIcon sets _NET_WM_ICON: the width, the height and the pixels of each
// image in turn, non-premultiplied ARGB in the longs of a 32-bit property.
// Images that would make the request longer than the server takes are left
// out.
func (w *x11Window) setIcon(icon *windowIcon) {
	limit := int(C.XExtendedMaxRequestSize(w.x))
	if limit == 0 {
		limit = int(C.XMaxRequestSize(w.x))
	}
	// The request's own fields take 6 of its 4-byte units.
	limit -= 6
	var data []C.ulong
	for _, img := range icon.images {
		size, pixels := iconPixels(img, false)
		if size == 0 || len(data)+2+len(pixels) > limit {
			continue
		}
		data = append(data, C.ulong(size), C.ulong(size))
		for _, p := range pixels {
			data = append(data, C.ulong(p))
		}
	}
	if len(data) == 0 {
		return
	}
	C.XChangeProperty(w.x, w.xw, w.atoms.wmIcon, w.atoms.cardinal, 32, C.PropModeReplace,
		(*C.uchar)(unsafe.Pointer(&data[0])), C.int(len(data)))
}

func (w *x11Window) setTitle(prev, cnf Config) {
	if prev.Title != cnf.Title {
		title := cnf.Title
		ctitle := C.CString(title)
		defer C.free(unsafe.Pointer(ctitle))
		C.XStoreName(w.x, w.xw, ctitle)
		// set _NET_WM_NAME as well for UTF-8 support in window title.
		C.XSetTextProperty(w.x, w.xw,
			&C.XTextProperty{
				value:    (*C.uchar)(unsafe.Pointer(ctitle)),
				encoding: w.atoms.utf8string,
				format:   8,
				nitems:   C.ulong(len(title)),
			},
			w.atoms.wmName)
	}
}

func (w *x11Window) Perform(acts system.Action) {
	walkActions(acts, func(a system.Action) {
		switch a {
		case system.ActionCenter:
			w.center()
		case system.ActionRaise:
			w.raise()
		}
	})
	if acts&system.ActionClose != 0 {
		w.close()
	}
}

func (w *x11Window) center() {
	screen := C.XDefaultScreen(w.x)
	width := C.XDisplayWidth(w.x, screen)
	height := C.XDisplayHeight(w.x, screen)

	var attrs C.XWindowAttributes
	C.XGetWindowAttributes(w.x, w.xw, &attrs)
	width -= attrs.border_width
	height -= attrs.border_width

	sz := w.config.Size
	x := (int(width) - sz.X) / 2
	y := (int(height) - sz.Y) / 2

	C.XMoveResizeWindow(w.x, w.xw, C.int(x), C.int(y), C.uint(sz.X), C.uint(sz.Y))
}

func (w *x11Window) raise() {
	var xev C.XEvent
	ev := (*C.XClientMessageEvent)(unsafe.Pointer(&xev))
	*ev = C.XClientMessageEvent{
		_type:        C.ClientMessage,
		display:      w.x,
		window:       w.xw,
		message_type: w.atoms.wmActiveWindow,
		format:       32,
	}
	// Source indication 2, a pager: the user asked for the window, which
	// spares the request the focus stealing prevention window managers
	// apply to legacy (0) sources.
	*(*C.long)(unsafe.Pointer(&ev.data[0])) = 2
	C.XSendEvent(
		w.x,
		C.XDefaultRootWindow(w.x), // MUST be the root window
		C.False,
		C.SubstructureNotifyMask|C.SubstructureRedirectMask,
		&xev,
	)
	C.XMapRaised(w.display(), w.xw)
}

func (w *x11Window) SetCursor(cursor pointer.Cursor) {
	if cursor == pointer.CursorNone {
		w.cursor = cursor
		C.XFixesHideCursor(w.x, w.xw)
		return
	}

	xcursor := xCursor[cursor]
	cname := C.CString(xcursor)
	defer C.free(unsafe.Pointer(cname))
	c := C.XcursorLibraryLoadCursor(w.x, cname)
	if c == 0 {
		cursor = pointer.CursorDefault
	}
	w.cursor = cursor
	// If c if null (i.e. cursor was not found),
	// XDefineCursor will use the default cursor.
	C.XDefineCursor(w.x, w.xw, c)
}

func (w *x11Window) ShowTextInput(show bool) {}

func (w *x11Window) SetInputHint(_ key.InputHint) {}

func (w *x11Window) EditorStateChanged(old, new editorState) {}

// close the window.
func (w *x11Window) close() {
	var xev C.XEvent
	ev := (*C.XClientMessageEvent)(unsafe.Pointer(&xev))
	*ev = C.XClientMessageEvent{
		_type:        C.ClientMessage,
		display:      w.x,
		window:       w.xw,
		message_type: w.atom("WM_PROTOCOLS", true),
		format:       32,
	}
	arr := (*[5]C.long)(unsafe.Pointer(&ev.data))
	arr[0] = C.long(w.atoms.evDelWindow)
	arr[1] = C.CurrentTime
	C.XSendEvent(w.x, w.xw, C.False, C.NoEventMask, &xev)
}

// action is one of _NET_WM_STATE_REMOVE, _NET_WM_STATE_ADD.
func (w *x11Window) sendWMStateEvent(action C.long, atom1, atom2 C.ulong) {
	var xev C.XEvent
	ev := (*C.XClientMessageEvent)(unsafe.Pointer(&xev))
	*ev = C.XClientMessageEvent{
		_type:        C.ClientMessage,
		display:      w.x,
		window:       w.xw,
		message_type: w.atoms.wmState,
		format:       32,
	}
	data := (*[5]C.long)(unsafe.Pointer(&ev.data))
	data[0] = C.long(action)
	data[1] = C.long(atom1)
	data[2] = C.long(atom2)
	data[3] = 1 // application

	C.XSendEvent(
		w.x,
		C.XDefaultRootWindow(w.x), // MUST be the root window
		C.False,
		C.SubstructureNotifyMask|C.SubstructureRedirectMask,
		&xev,
	)
}

var x11OneByte = make([]byte, 1)

func (w *x11Window) ProcessEvent(e event.Event) {
	w.w.ProcessEvent(e)
}

func (w *x11Window) shutdown(err error) {
	w.ProcessEvent(X11ViewEvent{})
	w.ProcessEvent(DestroyEvent{Err: err})
	w.destroy()
}

func (w *x11Window) Event() event.Event {
	for {
		evt, ok := w.w.nextEvent()
		if !ok {
			w.dispatch()
			continue
		}
		return evt
	}
}

func (w *x11Window) Run(f func()) {
	f()
	// f runs on the caller's goroutine, and the requests it made would
	// wait in Xlib's buffer until the event loop woke for another reason:
	// a window closed by the application stayed open, and one raised from
	// a tray icon stayed minimized. Xlib is initialized for threads.
	if w.x != nil {
		C.XFlush(w.x)
	}
}

func (w *x11Window) Frame(frame *op.Ops) {
	w.w.ProcessFrame(frame, nil)
}

func (w *x11Window) Invalidate() {
	select {
	case w.wakeups <- struct{}{}:
	default:
	}
	if _, err := syscall.Write(w.notify.write, x11OneByte); err != nil && err != syscall.EAGAIN {
		panic(fmt.Errorf("failed to write to pipe: %v", err))
	}
}

func (w *x11Window) display() *C.Display {
	return w.x
}

func (w *x11Window) window() (C.Window, int, int) {
	return w.xw, w.config.Size.X, w.config.Size.Y
}

func (w *x11Window) dispatch() {
	if w.x == nil {
		// Only Invalidate can wake us up.
		<-w.wakeups
		w.w.Invalidate()
		return
	}

	select {
	case <-w.wakeups:
		w.w.Invalidate()
	default:
	}

	xfd := C.XConnectionNumber(w.x)

	// Poll for events and notifications.
	pollfds := []syscall.PollFd{
		{Fd: int32(xfd), Events: syscall.POLLIN | syscall.POLLERR},
		{Fd: int32(w.notify.read), Events: syscall.POLLIN | syscall.POLLERR},
	}
	xEvents := &pollfds[0].Revents
	// Plenty of room for a backlog of notifications.

	var syn, anim bool
	// Check for pending draw events before checking animation or blocking.
	// This fixes an issue on Xephyr where on startup XPending() > 0 but
	// poll will still block. This also prevents no-op calls to poll.
	syn = w.handler.handleEvents()
	if w.x == nil {
		// handleEvents received a close request and destroyed the window.
		return
	}
	if !syn {
		anim = w.animating && !w.config.Suspended && w.config.Mode != Minimized
		if !anim && !w.xi2Flinging() {
			// Clear poll events.
			*xEvents = 0
			// Wait for X event or gio notification, or for the time a
			// touchpad's scrolling is taken to have ended (os_x11_xi2.go).
			if _, err := syscall.Poll(pollfds, w.xi2FlingWait()); err != nil && err != syscall.EINTR {
				panic(fmt.Errorf("x11 loop: poll failed: %w", err))
			}
			switch {
			case *xEvents&syscall.POLLIN != 0:
				syn = w.handler.handleEvents()
				if w.x == nil {
					return
				}
			case *xEvents&(syscall.POLLERR|syscall.POLLHUP) != 0:
			}
		}
	}
	// The kinetic scrolling after a touchpad's moves on each frame.
	if w.xi2Fling(time.Now()) {
		anim = true
	}
	// Clear notifications.
	for {
		_, err := syscall.Read(w.notify.read, w.buf[:])
		if err == syscall.EAGAIN {
			break
		}
		if err != nil {
			panic(fmt.Errorf("x11 loop: read from notify pipe failed: %w", err))
		}
	}
	if (anim || syn) && !w.config.Suspended && w.config.Mode != Minimized && w.config.Size.X != 0 && w.config.Size.Y != 0 {
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

func (w *x11Window) destroy() {
	if w.notify.write != 0 {
		syscall.Close(w.notify.write)
		w.notify.write = 0
	}
	if w.notify.read != 0 {
		syscall.Close(w.notify.read)
		w.notify.read = 0
	}
	if w.xkb != nil {
		w.xkb.Destroy()
		w.xkb = nil
	}
	C.XDestroyWindow(w.x, w.xw)
	C.XCloseDisplay(w.x)
	w.x = nil
}

// atom is a wrapper around XInternAtom. Callers should cache the result
// in order to limit round-trips to the X server.
func (w *x11Window) atom(name string, onlyIfExists bool) C.Atom {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	flag := C.Bool(C.False)
	if onlyIfExists {
		flag = C.True
	}
	return C.XInternAtom(w.x, cname, flag)
}

// pointerButton handles a press or a release of a pointer button, from the
// core protocol or from XInput 2. The buttons of a wheel scroll.
func (w *x11Window) pointerButton(release bool, button uint, pos f32.Point, t C.Time) {
	ev := pointer.Event{
		Kind:      pointer.Press,
		Source:    pointer.Mouse,
		Position:  pos,
		Time:      time.Duration(t) * time.Millisecond,
		Modifiers: w.xkb.Modifiers(),
	}
	if release {
		ev.Kind = pointer.Release
	}
	var btn pointer.Buttons
	const scrollScale = 10
	switch button {
	case C.Button1:
		btn = pointer.ButtonPrimary
	case C.Button2:
		btn = pointer.ButtonTertiary
	case C.Button3:
		btn = pointer.ButtonSecondary
	case C.Button4:
		ev.Kind = pointer.Scroll
		// scroll up or left (if shift is pressed).
		if ev.Modifiers == key.ModShift {
			ev.Scroll.X = -scrollScale
		} else {
			ev.Scroll.Y = -scrollScale
		}
	case C.Button5:
		// scroll down or right (if shift is pressed).
		ev.Kind = pointer.Scroll
		if ev.Modifiers == key.ModShift {
			ev.Scroll.X = +scrollScale
		} else {
			ev.Scroll.Y = +scrollScale
		}
	case 6:
		// http://xahlee.info/linux/linux_x11_mouse_button_number.html
		// scroll left.
		ev.Kind = pointer.Scroll
		ev.Scroll.X = -scrollScale * 2
	case 7:
		// scroll right
		ev.Kind = pointer.Scroll
		ev.Scroll.X = +scrollScale * 2
	default:
		return
	}
	// The core protocol tells scrolling only by the buttons of the wheel; a
	// touchpad's driver presses them too, unless XInput 2 takes its
	// scrolling (os_x11_xi2.go).
	ev.Wheel = ev.Kind == pointer.Scroll
	if release {
		w.pointerBtns &^= btn
	} else {
		w.pointerBtns |= btn
	}
	ev.Buttons = w.pointerBtns
	w.ProcessEvent(ev)
}

// x11EventHandler wraps static variables for the main event loop.
// Its sole purpose is to prevent heap allocation and reduce clutter
// in x11window.loop.
type x11EventHandler struct {
	w    *x11Window
	text []byte
	xev  *C.XEvent
}

// handleEvents returns true if the window needs to be redrawn.
func (h *x11EventHandler) handleEvents() bool {
	w := h.w
	xev := h.xev
	redraw := false
	for C.XPending(w.x) != 0 {
		C.XNextEvent(w.x, xev)
		if C.XFilterEvent(xev, C.None) == C.True {
			continue
		}
		switch _type := (*C.XAnyEvent)(unsafe.Pointer(xev))._type; _type {
		case h.w.xkbEventBase:
			xkbEvent := (*C.XkbAnyEvent)(unsafe.Pointer(xev))
			switch xkbEvent.xkb_type {
			case C.XkbNewKeyboardNotify, C.XkbMapNotify:
				if err := h.w.updateXkbKeymap(); err != nil {
					panic(err)
				}
			case C.XkbStateNotify:
				state := (*C.XkbStateNotifyEvent)(unsafe.Pointer(xev))
				h.w.xkb.UpdateMask(uint32(state.base_mods), uint32(state.latched_mods), uint32(state.locked_mods),
					uint32(state.base_group), uint32(state.latched_group), uint32(state.locked_group))
			}
		case C.KeyPress, C.KeyRelease:
			ks := key.Press
			if _type == C.KeyRelease {
				ks = key.Release
			}
			kevt := (*C.XKeyPressedEvent)(unsafe.Pointer(xev))
			for _, e := range h.w.xkb.DispatchKey(uint32(kevt.keycode), ks) {
				if ee, ok := e.(key.EditEvent); ok {
					// There's no support for IME yet.
					w.w.EditorInsert(ee.Text)
				} else {
					w.ProcessEvent(e)
				}
			}
		case C.ButtonPress, C.ButtonRelease:
			bevt := (*C.XButtonEvent)(unsafe.Pointer(xev))
			w.pointerButton(bevt._type == C.ButtonRelease, uint(bevt.button), f32.Pt(float32(bevt.x), float32(bevt.y)), bevt.time)
		case C.GenericEvent:
			w.handleXI2(xev)
		case C.MotionNotify:
			mevt := (*C.XMotionEvent)(unsafe.Pointer(xev))
			w.ProcessEvent(pointer.Event{
				Kind:    pointer.Move,
				Source:  pointer.Mouse,
				Buttons: w.pointerBtns,
				Position: f32.Point{
					X: float32(mevt.x),
					Y: float32(mevt.y),
				},
				Time:      time.Duration(mevt.time) * time.Millisecond,
				Modifiers: w.xkb.Modifiers(),
			})
		case C.UnmapNotify:
			if (*C.XUnmapEvent)(unsafe.Pointer(xev)).window == w.xw {
				w.config.Suspended = true
				w.ProcessEvent(ConfigEvent{Config: w.config})
			}
		case C.MapNotify:
			if (*C.XMapEvent)(unsafe.Pointer(xev)).window == w.xw {
				w.config.Suspended = false
				if w.config.Mode == Minimized {
					w.config.Mode = w.restoreMode
				}
				w.ProcessEvent(ConfigEvent{Config: w.config})
				redraw = true
			}
		case C.Expose: // update
			// redraw only on the last expose event
			redraw = (*C.XExposeEvent)(unsafe.Pointer(xev)).count == 0
		case C.FocusIn:
			w.config.Focused = true
			w.ProcessEvent(ConfigEvent{Config: w.config})
		case C.FocusOut:
			w.config.Focused = false
			w.ProcessEvent(ConfigEvent{Config: w.config})
		case C.ConfigureNotify: // window configuration change
			cevt := (*C.XConfigureEvent)(unsafe.Pointer(xev))
			if sz := image.Pt(int(cevt.width), int(cevt.height)); sz != w.config.Size {
				w.config.Size = sz
				w.ProcessEvent(ConfigEvent{Config: w.config})
			}
			// redraw will be done by a later expose event
		case C.SelectionNotify:
			cevt := (*C.XSelectionEvent)(unsafe.Pointer(xev))
			if cevt.selection == w.dnd.atoms.selection {
				w.dropSelection(cevt)
				break
			}
			w.clipboardNotify(cevt)
		case C.PropertyNotify:
			w.clipboardProperty((*C.XPropertyEvent)(unsafe.Pointer(xev)))
		case C.SelectionRequest:
			cevt := (*C.XSelectionRequestEvent)(unsafe.Pointer(xev))
			if (cevt.selection != w.atoms.clipboard && cevt.selection != w.atoms.primary) || cevt.property == C.None {
				// Unsupported clipboard or obsolete requestor.
				break
			}
			notify := func() {
				var xev C.XEvent
				ev := (*C.XSelectionEvent)(unsafe.Pointer(&xev))
				*ev = C.XSelectionEvent{
					_type:     C.SelectionNotify,
					display:   cevt.display,
					requestor: cevt.requestor,
					selection: cevt.selection,
					target:    cevt.target,
					property:  cevt.property,
					time:      cevt.time,
				}
				C.XSendEvent(w.x, cevt.requestor, 0, 0, &xev)
			}
			switch cevt.target {
			case w.atoms.targets:
				// The requestor wants the supported clipboard
				// formats. First write the targets...
				formats := []C.long{
					C.long(w.atoms.targets),
					C.long(w.atoms.utf8string),
					C.long(w.atoms.plaintext),
					// GTK clients need this.
					C.long(w.atoms.gtk_text_buffer_contents),
				}
				if w.clipboard.mime == "image/png" {
					formats = []C.long{C.long(w.atoms.targets), C.long(w.atoms.imagePNG)}
				} else if w.clipboard.html != nil {
					formats = append(formats, C.long(w.atoms.textHTML))
				}
				C.XChangeProperty(w.x, cevt.requestor, cevt.property, w.atoms.atom,
					32 /* bitwidth of formats */, C.PropModeReplace,
					(*C.uchar)(unsafe.Pointer(unsafe.SliceData(formats))), C.int(len(formats)),
				)
				// ...then notify the requestor.
				notify()
			case w.atoms.imagePNG:
				if w.clipboard.mime != "image/png" {
					break
				}
				content := w.clipboard.content
				ptr := (*C.uchar)(unsafe.Pointer(unsafe.SliceData(content)))
				C.XChangeProperty(w.x, cevt.requestor, cevt.property, cevt.target,
					8 /* bitwidth */, C.PropModeReplace,
					ptr, C.int(len(content)),
				)
				notify()
			case w.atoms.textHTML:
				if w.clipboard.html == nil {
					break
				}
				content := w.clipboard.html
				ptr := (*C.uchar)(unsafe.Pointer(unsafe.SliceData(content)))
				C.XChangeProperty(w.x, cevt.requestor, cevt.property, cevt.target,
					8 /* bitwidth */, C.PropModeReplace,
					ptr, C.int(len(content)),
				)
				notify()
			case w.atoms.plaintext, w.atoms.utf8string, w.atoms.gtk_text_buffer_contents:
				if w.clipboard.mime == "image/png" {
					break
				}
				content := w.clipboard.content
				ptr := (*C.uchar)(unsafe.Pointer(unsafe.SliceData(content)))
				C.XChangeProperty(w.x, cevt.requestor, cevt.property, cevt.target,
					8 /* bitwidth */, C.PropModeReplace,
					ptr, C.int(len(content)),
				)
				notify()
			}
		case C.ClientMessage: // extensions
			cevt := (*C.XClientMessageEvent)(unsafe.Pointer(xev))
			if w.dropMessage(cevt) {
				break
			}
			switch *(*C.long)(unsafe.Pointer(&cevt.data)) {
			case C.long(w.atoms.evDelWindow):
				w.shutdown(nil)
				return false
			}
		}
	}
	return redraw
}

var x11Threads sync.Once

func init() {
	x11Driver = newX11Window
}

func newX11Window(gioWin *callbacks, options []Option) error {
	var err error

	pipe := make([]int, 2)
	if err := syscall.Pipe2(pipe, syscall.O_NONBLOCK|syscall.O_CLOEXEC); err != nil {
		return fmt.Errorf("NewX11Window: failed to create pipe: %w", err)
	}

	x11Threads.Do(func() {
		if C.XInitThreads() == 0 {
			err = errors.New("x11: threads init failed")
		}
		C.XrmInitialize()
	})
	if err != nil {
		return err
	}
	dpy := C.XOpenDisplay(nil)
	if dpy == nil {
		return errors.New("x11: cannot connect to the X server")
	}
	var major, minor C.int = C.XkbMajorVersion, C.XkbMinorVersion
	var xkbEventBase C.int
	if C.XkbQueryExtension(dpy, nil, &xkbEventBase, nil, &major, &minor) != C.True {
		C.XCloseDisplay(dpy)
		return errors.New("x11: XkbQueryExtension failed")
	}
	const bits = C.uint(C.XkbNewKeyboardNotifyMask | C.XkbMapNotifyMask | C.XkbStateNotifyMask)
	if C.XkbSelectEvents(dpy, C.XkbUseCoreKbd, bits, bits) != C.True {
		C.XCloseDisplay(dpy)
		return errors.New("x11: XkbSelectEvents failed")
	}
	xkb, err := xkb.New()
	if err != nil {
		C.XCloseDisplay(dpy)
		return fmt.Errorf("x11: %v", err)
	}

	ppsp := x11DetectUIScale(dpy)
	cfg := unit.Metric{PxPerDp: ppsp, PxPerSp: ppsp}
	// Only use cnf for getting the window size.
	var cnf Config
	cnf.apply(cfg, options)

	swa := C.XSetWindowAttributes{
		event_mask: C.ExposureMask | C.FocusChangeMask | // update
			C.KeyPressMask | C.KeyReleaseMask | // keyboard
			C.ButtonPressMask | C.ButtonReleaseMask | // mouse clicks
			C.PointerMotionMask | // mouse movement
			C.StructureNotifyMask | // resize
			C.PropertyChangeMask, // clipboard content in parts
		background_pixmap: C.None,
		override_redirect: C.False,
	}
	win := C.XCreateWindow(dpy, C.XDefaultRootWindow(dpy),
		0, 0, C.uint(cnf.Size.X), C.uint(cnf.Size.Y),
		0, C.CopyFromParent, C.InputOutput, nil,
		C.CWEventMask|C.CWBackPixmap|C.CWOverrideRedirect, &swa)

	w := &x11Window{
		w: gioWin, x: dpy, xw: win,
		metric:       cfg,
		xkb:          xkb,
		xkbEventBase: xkbEventBase,
		wakeups:      make(chan struct{}, 1),
		config:       Config{Size: cnf.Size},
	}

	var hints C.XWMHints
	hints.input = C.True
	hints.flags = C.InputHint
	C.XSetWMHints(dpy, win, &hints)

	name := C.CString(ID)
	defer C.free(unsafe.Pointer(name))
	wmhints := C.XClassHint{name, name}
	C.XSetClassHint(dpy, win, &wmhints)

	w.atoms.utf8string = w.atom("UTF8_STRING", false)
	w.atoms.plaintext = w.atom("text/plain;charset=utf-8", false)
	w.atoms.gtk_text_buffer_contents = w.atom("GTK_TEXT_BUFFER_CONTENTS", false)
	w.atoms.evDelWindow = w.atom("WM_DELETE_WINDOW", false)
	w.atoms.clipboard = w.atom("CLIPBOARD", false)
	w.atoms.primary = w.atom("PRIMARY", false)
	w.atoms.clipboardContent = w.atom("CLIPBOARD_CONTENT", false)
	w.atoms.atom = w.atom("ATOM", false)
	w.atoms.targets = w.atom("TARGETS", false)
	w.atoms.imagePNG = w.atom("image/png", false)
	w.atoms.textHTML = w.atom("text/html", false)
	w.atoms.wmName = w.atom("_NET_WM_NAME", false)
	w.atoms.wmState = w.atom("_NET_WM_STATE", false)
	w.atoms.wmStateFullscreen = w.atom("_NET_WM_STATE_FULLSCREEN", false)
	w.atoms.wmActiveWindow = w.atom("_NET_ACTIVE_WINDOW", false)
	w.atoms.wmStateMaximizedHorz = w.atom("_NET_WM_STATE_MAXIMIZED_HORZ", false)
	w.atoms.wmStateMaximizedVert = w.atom("_NET_WM_STATE_MAXIMIZED_VERT", false)
	w.atoms.wmIcon = w.atom("_NET_WM_ICON", false)
	w.atoms.cardinal = w.atom("CARDINAL", false)
	w.setupDrop()
	w.setupXI2()

	// extensions
	C.XSetWMProtocols(dpy, win, &w.atoms.evDelWindow, 1)

	w.handler = x11EventHandler{w: w, xev: new(C.XEvent), text: make([]byte, 4)}
	w.notify.read = pipe[0]
	w.notify.write = pipe[1]

	if err := w.updateXkbKeymap(); err != nil {
		w.destroy()
		return err
	}

	// make the window visible on the screen
	C.XMapWindow(dpy, win)
	w.Configure(options)
	// Configure runs on the caller's goroutine as soon as SetDriver
	// publishes the window (see Run), so the window is set up completely
	// first: a title set early went out with atom 0, and Xlib's default
	// error handler ended the process with BadAtom in X_ChangeProperty;
	// and the initial options above overwrote the title set meanwhile.
	w.w.SetDriver(w)
	w.ProcessEvent(X11ViewEvent{Display: unsafe.Pointer(dpy), Window: uintptr(win)})
	return nil
}

// detectUIScale reports the system UI scale, or 1.0 if it fails.
func x11DetectUIScale(dpy *C.Display) float32 {
	// default fixed DPI value used in most desktop UI toolkits
	const defaultDesktopDPI = 96
	var scale float32 = 1.0

	// Get actual DPI from X resource Xft.dpi (set by GTK and Qt).
	// This value is entirely based on user preferences and conflates both
	// screen (UI) scaling and font scale.
	rms := C.XResourceManagerString(dpy)
	if rms != nil {
		db := C.XrmGetStringDatabase(rms)
		if db != nil {
			var (
				t *C.char
				v C.XrmValue
			)
			if C.XrmGetResource(db, (*C.char)(unsafe.Pointer(unsafe.StringData("Xft.dpi\x00"))),
				(*C.char)(unsafe.Pointer(unsafe.StringData("Xft.Dpi\x00"))), &t, &v) != C.False {
				if t != nil && C.GoString(t) == "String" {
					f, err := strconv.ParseFloat(C.GoString(v.addr), 32)
					if err == nil {
						scale = float32(f) / defaultDesktopDPI
					}
				}
			}
			C.XrmDestroyDatabase(db)
		}
	}

	return scale
}

func (w *x11Window) updateXkbKeymap() error {
	w.xkb.DestroyKeymapState()
	ctx := (*C.struct_xkb_context)(unsafe.Pointer(w.xkb.Ctx))
	xcb := C.XGetXCBConnection(w.x)
	if xcb == nil {
		return errors.New("x11: XGetXCBConnection failed")
	}
	xkbDevID := C.xkb_x11_get_core_keyboard_device_id(xcb)
	if xkbDevID == -1 {
		return errors.New("x11: xkb_x11_get_core_keyboard_device_id failed")
	}
	keymap := C.xkb_x11_keymap_new_from_device(ctx, xcb, xkbDevID, C.XKB_KEYMAP_COMPILE_NO_FLAGS)
	if keymap == nil {
		return errors.New("x11: xkb_x11_keymap_new_from_device failed")
	}
	state := C.xkb_x11_state_new_from_device(keymap, xcb, xkbDevID)
	if state == nil {
		C.xkb_keymap_unref(keymap)
		return errors.New("x11: xkb_x11_keymap_new_from_device failed")
	}
	w.xkb.SetKeymap(unsafe.Pointer(keymap), unsafe.Pointer(state))
	return nil
}
