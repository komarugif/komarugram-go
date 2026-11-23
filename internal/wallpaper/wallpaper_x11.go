// SPDX-License-Identifier: Unlicense OR MIT

//go:build ((linux && !android) || freebsd || openbsd) && !nox11 && cgo

package wallpaper

/*
#cgo freebsd openbsd CFLAGS: -I/usr/X11R6/include -I/usr/local/include
#cgo freebsd openbsd LDFLAGS: -L/usr/X11R6/lib -L/usr/local/lib -lX11
#cgo linux pkg-config: x11

#include <stdlib.h>
#include <X11/Xlib.h>
#include <X11/Xatom.h>
#include <X11/Xutil.h>

// The displays of the watchers. An error on one of them is a pixmap or a
// window gone between two requests, and is ignored: Xlib's own handler
// ends the process.
#define KG_DISPLAYS 16
static Display *kg_displays[KG_DISPLAYS];
static XErrorHandler kg_previous;

static int kg_on_error(Display *d, XErrorEvent *e) {
	for (int i = 0; i < KG_DISPLAYS; i++) {
		if (kg_displays[i] == d) {
			return 0;
		}
	}
	return kg_previous ? kg_previous(d, e) : 0;
}

static void kg_install(void) {
	kg_previous = XSetErrorHandler(kg_on_error);
}

static void kg_add(Display *d) {
	for (int i = 0; i < KG_DISPLAYS; i++) {
		if (!kg_displays[i]) {
			kg_displays[i] = d;
			return;
		}
	}
}

static void kg_remove(Display *d) {
	for (int i = 0; i < KG_DISPLAYS; i++) {
		if (kg_displays[i] == d) {
			kg_displays[i] = NULL;
		}
	}
}

static Window kg_root(Display *d) {
	return DefaultRootWindow(d);
}

// kg_root_pixmap is the pixmap a property of the root window names, None
// when it names none.
static Pixmap kg_root_pixmap(Display *d, Window root, Atom property) {
	Atom type = None;
	int format = 0;
	unsigned long count = 0, left = 0;
	unsigned char *data = NULL;
	Pixmap pixmap = None;
	if (XGetWindowProperty(d, root, property, 0, 1, False, XA_PIXMAP, &type, &format, &count, &left, &data) != Success) {
		return None;
	}
	if (data) {
		if (type == XA_PIXMAP && format == 32 && count == 1) {
			pixmap = *(Pixmap *)data;
		}
		XFree(data);
	}
	return pixmap;
}

// kg_grab reads all of a pixmap, NULL when it is gone.
static XImage *kg_grab(Display *d, Pixmap pixmap, int *width, int *height) {
	Window root;
	int x, y;
	unsigned int w, h, border, depth;
	if (!XGetGeometry(d, pixmap, &root, &x, &y, &w, &h, &border, &depth) || !w || !h) {
		return NULL;
	}
	*width = (int)w;
	*height = (int)h;
	return XGetImage(d, pixmap, 0, 0, w, h, AllPlanes, ZPixmap);
}

static void kg_destroy(XImage *image) {
	XDestroyImage(image);
}

static int kg_shift(unsigned long mask) {
	int shift = 0;
	while (mask && !(mask & 1)) {
		mask >>= 1;
		shift++;
	}
	return shift;
}

// kg_shrink averages every f×f pixels of an image into one of out, which
// is w×h of RGBA. The image of a pixmap has no masks of its own: they are
// the default visual's.
static void kg_shrink(Display *d, XImage *image, int f, unsigned char *out, int w, int h) {
	Visual *visual = DefaultVisual(d, DefaultScreen(d));
	unsigned long masks[3] = {visual->red_mask, visual->green_mask, visual->blue_mask};
	int shifts[3];
	unsigned long tops[3];
	for (int c = 0; c < 3; c++) {
		shifts[c] = kg_shift(masks[c]);
		tops[c] = masks[c] >> shifts[c];
		if (!tops[c]) {
			tops[c] = 1;
		}
	}
	for (int y = 0; y < h; y++) {
		for (int x = 0; x < w; x++) {
			unsigned long sums[3] = {0, 0, 0}, count = 0;
			for (int j = 0; j < f && y * f + j < image->height; j++) {
				for (int i = 0; i < f && x * f + i < image->width; i++) {
					unsigned long pixel = XGetPixel(image, x * f + i, y * f + j);
					for (int c = 0; c < 3; c++) {
						sums[c] += (pixel & masks[c]) >> shifts[c];
					}
					count++;
				}
			}
			if (!count) {
				count = 1;
			}
			for (int c = 0; c < 3; c++) {
				out[c] = (unsigned char)(sums[c] * 255 / (count * tops[c]));
			}
			out[3] = 255;
			out += 4;
		}
	}
}

// kg_origin is where a window's top left corner is on the root window.
static int kg_origin(Display *d, Window window, Window root, int *x, int *y) {
	Window child;
	return XTranslateCoordinates(d, window, root, 0, 0, x, y, &child);
}

static int kg_type(XEvent *e) {
	return e->type;
}

static Atom kg_property(XEvent *e) {
	return e->xproperty.atom;
}

static Window kg_window(XEvent *e) {
	return e->xany.window;
}
*/
import "C"

import (
	"image"
	"sync"
	"sync/atomic"
	"unsafe"

	"gioui.org/io/event"

	"komarugram/internal/crash"
)

// The wallpaper is the pixmap the root window's _XROOTPMAP_ID names, which
// the program that sets the wallpaper keeps there for programs that draw
// it behind themselves (xfdesktop, feh, nitrogen, hsetroot do; a desktop
// that draws its wallpaper in a window of its own, as Plasma, does not, and
// then there is nothing to draw). It is the screen as a whole, every
// monitor's wallpaper where it is shown. A new wallpaper changes the
// property.

// rootPixmaps are the properties that name the pixmap.
var rootPixmaps = []string{"_XROOTPMAP_ID", "ESETROOT_PMAP_ID"}

var (
	displaysMu sync.Mutex
	handler    sync.Once
)

// Watcher follows a window over the desktop, on a connection of its own to
// the X server, which only its goroutine uses.
type Watcher struct {
	window  C.Window
	changed func()
	desktop atomic.Pointer[Desktop]
	closed  atomic.Bool
	// looked is closed once the desktop was looked at first.
	looked chan struct{}

	mu     sync.Mutex
	origin image.Point
	placed bool
}

// Supported reports whether a window of the view can be followed.
func Supported(v View) bool { return v.Window != 0 }

// Watch follows the window of the view, and calls changed, from another
// goroutine, when the desktop behind it is another, or the window is
// elsewhere on it. It returns nil where the window cannot be followed. The
// watcher lives as long as the window: Close only silences it.
func Watch(v View, changed func()) *Watcher {
	if !Supported(v) {
		return nil
	}
	w := &Watcher{window: C.Window(v.Window), changed: changed, looked: make(chan struct{})}
	go w.run()
	<-w.looked
	return w
}

// Ready reports whether the desktop can be drawn: it has a wallpaper kept
// where programs can read it.
func (w *Watcher) Ready() bool { return w.Desktop() != nil }

// Desktop is the desktop as it was read last, nil when it has no wallpaper
// to read.
func (w *Watcher) Desktop() *Desktop {
	if w == nil {
		return nil
	}
	return w.desktop.Load()
}

// Origin is where the content of the window is on the desktop.
func (w *Watcher) Origin() (image.Point, bool) {
	if w == nil {
		return image.Point{}, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.origin, w.placed
}

// Wants reports whether an event of the window is the watcher's: none is
// here, where the X server is asked.
func Wants(event.Event) bool { return false }

// Event takes an event Wants reported.
func (w *Watcher) Event(event.Event) {}

// Close stops telling of the desktop.
func (w *Watcher) Close() {
	if w != nil {
		w.closed.Store(true)
	}
}

func (w *Watcher) tell() {
	if w.changed != nil && !w.closed.Load() {
		w.changed()
	}
}

func (w *Watcher) run() {
	looked := sync.OnceFunc(func() { close(w.looked) })
	defer looked()
	defer crash.Recover("wallpaper", func(*crash.Panic) {})
	d := C.XOpenDisplay(nil)
	if d == nil {
		return
	}
	displaysMu.Lock()
	handler.Do(func() { C.kg_install() })
	C.kg_add(d)
	displaysMu.Unlock()
	defer func() {
		C.XCloseDisplay(d)
		displaysMu.Lock()
		C.kg_remove(d)
		displaysMu.Unlock()
	}()
	root := C.kg_root(d)
	atoms := make([]C.Atom, len(rootPixmaps))
	for i, name := range rootPixmaps {
		cs := C.CString(name)
		atoms[i] = C.XInternAtom(d, cs, C.False)
		C.free(unsafe.Pointer(cs))
	}
	C.XSelectInput(d, root, C.PropertyChangeMask)
	C.XSelectInput(d, w.window, C.StructureNotifyMask)
	w.grab(d, root, atoms)
	w.place(d, root)
	looked()
	for {
		var ev C.XEvent
		C.XNextEvent(d, &ev)
		switch C.kg_type(&ev) {
		case C.PropertyNotify:
			for _, a := range atoms {
				if C.kg_property(&ev) == a {
					w.grab(d, root, atoms)
					w.tell()
					break
				}
			}
		case C.ConfigureNotify, C.ReparentNotify, C.MapNotify:
			// A ConfigureNotify has the place in the window's parent, which
			// is the window manager's frame: the server is asked instead.
			if w.place(d, root) {
				w.tell()
			}
		case C.DestroyNotify:
			if C.kg_window(&ev) == w.window {
				return
			}
		}
	}
}

// place asks where the window is, and reports whether it has moved.
func (w *Watcher) place(d *C.Display, root C.Window) bool {
	var x, y C.int
	if C.kg_origin(d, w.window, root, &x, &y) == 0 {
		return false
	}
	at := image.Pt(int(x), int(y))
	w.mu.Lock()
	defer w.mu.Unlock()
	moved := !w.placed || w.origin != at
	w.origin, w.placed = at, true
	return moved
}

// grab reads the wallpaper.
func (w *Watcher) grab(d *C.Display, root C.Window, atoms []C.Atom) {
	var pixmap C.Pixmap
	for _, a := range atoms {
		if pixmap = C.kg_root_pixmap(d, root, a); pixmap != 0 {
			break
		}
	}
	if pixmap == 0 {
		w.desktop.Store(nil)
		return
	}
	var width, height C.int
	im := C.kg_grab(d, pixmap, &width, &height)
	if im == nil {
		w.desktop.Store(nil)
		return
	}
	defer C.kg_destroy(im)
	size := image.Pt(int(width), int(height))
	f := shrink(size)
	picture := image.NewRGBA(image.Rect(0, 0, max(1, size.X/f), max(1, size.Y/f)))
	b := picture.Bounds()
	C.kg_shrink(d, im, C.int(f), (*C.uchar)(unsafe.Pointer(&picture.Pix[0])), C.int(b.Dx()), C.int(b.Dy()))
	Blur(picture, blurRadius(size, f))
	w.desktop.Store(&Desktop{Screens: []Screen{{Bounds: image.Rectangle{Max: size}, Picture: picture}}})
}
