// SPDX-License-Identifier: Unlicense OR MIT

package wallpaper

import (
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	_ "golang.org/x/image/bmp"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"gioui.org/io/event"

	"komarugram/internal/crash"
)

// The wallpaper is one file for all monitors, which SystemParametersInfo
// names, placed on each as the registry tells; Windows 8 and later can keep
// a picture per monitor (IDesktopWallpaper), which is not read: the
// monitors then all get the one SystemParametersInfo names. A change of
// either is a change of the key the placement is in.

var (
	user32                = windows.NewLazySystemDLL("user32.dll")
	systemParametersInfoW = user32.NewProc("SystemParametersInfoW")
	clientToScreen        = user32.NewProc("ClientToScreen")
	enumDisplayMonitors   = user32.NewProc("EnumDisplayMonitors")
	getSystemMetrics      = user32.NewProc("GetSystemMetrics")
)

const (
	spiGetDeskWallpaper = 0x0073
	// The virtual screen, all monitors together, and their number.
	smXVirtualScreen  = 76
	smCMonitors       = 80
	desktopKey        = `Control Panel\Desktop`
	colorsKey         = `Control Panel\Colors`
	settleDelay       = 300 * time.Millisecond
	retryDelay        = time.Second
	retries           = 3
	regNotifyLastSet  = 0x00000004
	waitObject0       = 0
	infiniteTimeout   = 0xffffffff
	wallpaperPathSize = 1024
)

// Watcher follows a window over the desktop.
type Watcher struct {
	hwnd    uintptr
	changed func()
	desktop atomic.Pointer[Desktop]
	// stop ends the watcher, and kick has it look at the desktop again.
	stop, kick windows.Handle
	// metrics are the monitors as Origin saw them last.
	metrics atomic.Value
	// signature is what the desktop was made of, and failures how many
	// times in a row its file could not be read.
	signature string
	failures  int
	closed    sync.Once
}

// Supported reports whether a window of the view can be followed.
func Supported(v View) bool { return v.Window != 0 }

// Watch follows the window of the view, and calls changed, from another
// goroutine, when the desktop behind it is another. It returns nil where
// the window cannot be followed.
func Watch(v View, changed func()) *Watcher {
	if !Supported(v) {
		return nil
	}
	stop, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return nil
	}
	kick, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		windows.CloseHandle(stop)
		return nil
	}
	w := &Watcher{hwnd: v.Window, changed: changed, stop: stop, kick: kick}
	w.metrics.Store(screenMetrics())
	go w.run()
	return w
}

// Ready reports whether the desktop can be drawn, now or once it is read.
func (w *Watcher) Ready() bool { return w != nil }

// Desktop is the desktop as it was read last, nil before that.
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
	// Monitors plugged, unplugged or resized: nothing else tells of them.
	if m := screenMetrics(); w.metrics.Swap(m) != m {
		windows.SetEvent(w.kick)
	}
	var p struct{ X, Y int32 }
	if ok, _, _ := clientToScreen.Call(w.hwnd, uintptr(unsafe.Pointer(&p))); ok == 0 {
		return image.Point{}, false
	}
	return image.Pt(int(p.X), int(p.Y)), true
}

// Wants reports whether an event of the window is the watcher's: none is
// here, where the system is asked.
func Wants(event.Event) bool { return false }

// Event takes an event Wants reported.
func (w *Watcher) Event(event.Event) {}

// Close stops following the window.
func (w *Watcher) Close() {
	if w == nil {
		return
	}
	w.closed.Do(func() { windows.SetEvent(w.stop) })
}

func (w *Watcher) run() {
	defer crash.Recover("wallpaper", func(*crash.Panic) {})
	// The system ends a registry watch with the thread that asked for it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer windows.CloseHandle(w.stop)
	defer windows.CloseHandle(w.kick)
	changed, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return
	}
	defer windows.CloseHandle(changed)
	key, err := registry.OpenKey(registry.CURRENT_USER, desktopKey, registry.NOTIFY)
	watching := err == nil
	if watching {
		defer key.Close()
	}
	handles := []windows.Handle{w.stop, w.kick, changed}
	for {
		if watching {
			// Asked before the desktop is read, not to miss a change between.
			if err := windows.RegNotifyChangeKeyValue(windows.Handle(key), false, regNotifyLastSet, changed, true); err != nil {
				watching = false
			}
		}
		w.load()
		event, err := windows.WaitForMultipleObjects(handles, false, infiniteTimeout)
		if err != nil || event == waitObject0 {
			return
		}
		if event == waitObject0+2 {
			// The picture's file is written about when the key is.
			time.Sleep(settleDelay)
		}
	}
}

// load reads the desktop, if it is not what was read last.
func (w *Watcher) load() {
	path, fit, span, back := source()
	monitors := monitors()
	signature := fmt.Sprint(path, fit, span, back, monitors)
	if info, err := os.Stat(path); err == nil {
		signature += fmt.Sprint(info.Size(), info.ModTime().UnixNano())
	}
	if signature == w.signature {
		return
	}
	var picture image.Image
	if path != "" {
		var err error
		if picture, err = decode(path); err != nil {
			// Being written, perhaps: the color is drawn meanwhile.
			if w.failures++; w.failures <= retries {
				kick := w.kick
				time.AfterFunc(retryDelay, func() { windows.SetEvent(kick) })
				signature = ""
			}
		} else {
			w.failures = 0
		}
	}
	w.signature = signature
	d := new(Desktop)
	if span && len(monitors) > 0 {
		all := monitors[0]
		for _, m := range monitors[1:] {
			all = all.Union(m)
		}
		d.Screens = []Screen{{Bounds: all, Picture: Render(picture, Placement{Fit: Fill}, back, all.Size())}}
	} else {
		// Monitors of one size show one picture.
		pictures := map[image.Point]*image.RGBA{}
		for _, m := range monitors {
			p := pictures[m.Size()]
			if p == nil {
				p = Render(picture, Placement{Fit: fit}, back, m.Size())
				pictures[m.Size()] = p
			}
			d.Screens = append(d.Screens, Screen{Bounds: m, Picture: p})
		}
	}
	w.desktop.Store(d)
	if picture != nil {
		// The picture as it was decoded is tens of megabytes.
		picture = nil
		debug.FreeOSMemory()
	}
	if w.changed != nil {
		w.changed()
	}
}

func decode(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	im, _, err := image.Decode(f)
	return im, err
}

// source is the wallpaper's file, empty when the desktop is of a color, how
// it is placed, whether over all monitors as one, and the color around it.
func source() (path string, fit Fit, span bool, back color.NRGBA) {
	var buf [wallpaperPathSize]uint16
	if ok, _, _ := systemParametersInfoW.Call(spiGetDeskWallpaper, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), 0); ok != 0 {
		path = windows.UTF16ToString(buf[:])
	}
	style, tile := "10", ""
	if key, err := registry.OpenKey(registry.CURRENT_USER, desktopKey, registry.QUERY_VALUE); err == nil {
		if v, _, err := key.GetStringValue("WallpaperStyle"); err == nil {
			style = strings.TrimSpace(v)
		}
		tile, _, _ = key.GetStringValue("TileWallpaper")
		key.Close()
	}
	switch style {
	case "0":
		fit = Center
		if strings.TrimSpace(tile) == "1" {
			fit = Tile
		}
	case "2":
		fit = Stretch
	case "6":
		fit = Contain
	case "22":
		fit, span = Fill, true
	default:
		fit = Fill
	}
	if key, err := registry.OpenKey(registry.CURRENT_USER, colorsKey, registry.QUERY_VALUE); err == nil {
		if v, _, err := key.GetStringValue("Background"); err == nil {
			if parts := strings.Fields(v); len(parts) == 3 {
				r, _ := strconv.Atoi(parts[0])
				g, _ := strconv.Atoi(parts[1])
				b, _ := strconv.Atoi(parts[2])
				back = color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 0xff}
			}
		}
		key.Close()
	}
	return path, fit, span, back
}

// screenMetrics are the virtual screen and the number of monitors.
func screenMetrics() [5]int32 {
	var m [5]int32
	for i := range m {
		v, _, _ := getSystemMetrics.Call(uintptr(smXVirtualScreen + i))
		m[i] = int32(v)
	}
	return m
}

var (
	monitorsMu    sync.Mutex
	monitorsFound []image.Rectangle
	// A callback is never freed, so there is one for all calls.
	monitorCallback = sync.OnceValue(func() uintptr {
		return windows.NewCallback(func(monitor, dc uintptr, r *windows.Rect, data uintptr) uintptr {
			monitorsFound = append(monitorsFound, image.Rect(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom)))
			return 1
		})
	})
)

// monitors are the monitors' places on the desktop.
func monitors() []image.Rectangle {
	monitorsMu.Lock()
	defer monitorsMu.Unlock()
	monitorsFound = nil
	enumDisplayMonitors.Call(0, 0, monitorCallback(), 0)
	return monitorsFound
}
