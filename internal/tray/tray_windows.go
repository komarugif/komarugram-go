// SPDX-License-Identifier: Unlicense OR MIT

package tray

import (
	"errors"
	"fmt"
	"image"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"komarugram/internal/appicon"
	"komarugram/pkg/resample"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW       = user32.NewProc("RegisterClassExW")
	procCreateWindowExW        = user32.NewProc("CreateWindowExW")
	procDefWindowProcW         = user32.NewProc("DefWindowProcW")
	procDestroyWindow          = user32.NewProc("DestroyWindow")
	procGetMessageW            = user32.NewProc("GetMessageW")
	procTranslateMessage       = user32.NewProc("TranslateMessage")
	procDispatchMessageW       = user32.NewProc("DispatchMessageW")
	procPostMessageW           = user32.NewProc("PostMessageW")
	procPostQuitMessage        = user32.NewProc("PostQuitMessage")
	procRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	procAppendMenuW            = user32.NewProc("AppendMenuW")
	procTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	procDestroyMenu            = user32.NewProc("DestroyMenu")
	procSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	procGetCursorPos           = user32.NewProc("GetCursorPos")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procCreateIconIndirect     = user32.NewProc("CreateIconIndirect")
	procDestroyIcon            = user32.NewProc("DestroyIcon")
	procCreateDIBSection       = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap           = gdi32.NewProc("CreateBitmap")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procShellNotifyIconW       = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW       = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmNull          = 0x0000
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmApp           = 0x8000
	trayMessage     = wmApp + 1
	nimAdd          = 0
	nimModify       = 1
	nimDelete       = 2
	nifInfo         = 0x10
	niifUser        = 0x4
	niifNoSound     = 0x10
	niifLargeIcon   = 0x20
	niifQuietTime   = 0x80
	ninBalloonClick = 0x405
	nifMessage      = 0x1
	nifIcon         = 0x2
	nifTip          = 0x4
	mfString        = 0x0
	mfSeparator     = 0x800
	tpmRightButton  = 0x2
	tpmNoNotify     = 0x80
	tpmReturnCmd    = 0x100
	smCxSmIcon      = 49
	smCySmIcon      = 50
	errClassExists  = 1410
	dibRGBColors    = 0
	biRGB           = 0
	trayIconID      = 1
	closeTimeout    = 2 * time.Second
	trayWindowClass = "komarugramGoTrayWindow"
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

// notifyIconData is NOTIFYICONDATAW.
type notifyIconData struct {
	Size            uint32
	Wnd             windows.HWND
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            windows.Handle
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUIDItem        windows.GUID
	BalloonIcon     windows.Handle
}

type msg struct {
	Wnd     windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type point struct{ X, Y int32 }

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type iconInfo struct {
	Icon     int32
	XHotspot uint32
	YHotspot uint32
	Mask     windows.Handle
	Color    windows.Handle
}

// Tray is the application's icon in the notification area. Its window, and
// so its messages, belong to a thread of its own.
type Tray struct {
	opts           Options
	wnd            windows.HWND
	menu           windows.Handle
	icon           windows.Handle
	balloonMu      sync.Mutex
	balloonIcon    windows.Handle
	lastBalloon    windows.Handle
	taskbarCreated uint32
	added          atomic.Bool
	done           chan struct{}
}

// Start adds the icon. If the taskbar is not running yet, the icon appears
// when it starts.
func Start(opts Options) (*Tray, error) {
	t := &Tray{opts: opts, done: make(chan struct{})}
	ready := make(chan error, 1)
	go t.loop(ready)
	if err := <-ready; err != nil {
		return nil, fmt.Errorf("tray: %w", err)
	}
	return t, nil
}

// Available reports whether the icon is shown.
func (t *Tray) Available() bool { return t.added.Load() }

// Close removes the icon. The notification area would otherwise keep it until
// the pointer passes over it.
func (t *Tray) Close() {
	procPostMessageW.Call(uintptr(t.wnd), wmClose, 0, 0)
	select {
	case <-t.done:
	case <-time.After(closeTimeout):
	}
}

func (t *Tray) loop(ready chan<- error) {
	runtime.LockOSThread()
	defer close(t.done)
	if err := t.create(); err != nil {
		ready <- err
		return
	}
	t.add()
	ready <- nil
	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		// 0 is WM_QUIT and -1 an error.
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	procDestroyMenu.Call(uintptr(t.menu))
	procDestroyIcon.Call(uintptr(t.icon))
	t.balloonMu.Lock()
	if t.balloonIcon != 0 {
		procDestroyIcon.Call(uintptr(t.balloonIcon))
		t.balloonIcon = 0
	}
	if t.lastBalloon != 0 {
		procDestroyIcon.Call(uintptr(t.lastBalloon))
		t.lastBalloon = 0
	}
	t.balloonMu.Unlock()
}

// create makes the hidden window that receives the icon's messages. It is a
// top-level window, not a message-only one, since only those are told when
// the taskbar restarts.
func (t *Tray) create() error {
	instance, _, _ := procGetModuleHandleW.Call(0)
	class, err := windows.UTF16PtrFromString(trayWindowClass)
	if err != nil {
		return err
	}
	wc := wndClassEx{
		WndProc:   windows.NewCallback(t.wndProc),
		Instance:  windows.Handle(instance),
		ClassName: class,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 && !errors.Is(err, windows.Errno(errClassExists)) {
		return fmt.Errorf("register window class: %w", err)
	}
	title, err := windows.UTF16PtrFromString(t.opts.Title)
	if err != nil {
		return err
	}
	wnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if wnd == 0 {
		return fmt.Errorf("create window: %w", err)
	}
	t.wnd = windows.HWND(wnd)
	created, err := windows.UTF16PtrFromString("TaskbarCreated")
	if err != nil {
		return err
	}
	r, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(created)))
	t.taskbarCreated = uint32(r)
	if t.menu, err = t.createMenu(); err != nil {
		return err
	}
	w, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	h, _, _ := procGetSystemMetrics.Call(smCySmIcon)
	size := max(int(w), int(h), 16)
	if t.icon, err = createIcon(size); err != nil {
		return err
	}
	if t.balloonIcon, err = createHICON(appicon.Image(128)); err != nil {
		return err
	}
	return nil
}

func (t *Tray) createMenu() (windows.Handle, error) {
	m, _, err := procCreatePopupMenu.Call()
	if m == 0 {
		return 0, fmt.Errorf("create menu: %w", err)
	}
	for i, it := range t.opts.Items {
		if it.Separator {
			procAppendMenuW.Call(m, mfSeparator, 0, 0)
			continue
		}
		label, err := windows.UTF16PtrFromString(it.Label)
		if err != nil {
			return 0, err
		}
		procAppendMenuW.Call(m, mfString, uintptr(i+1), uintptr(unsafe.Pointer(label)))
	}
	return windows.Handle(m), nil
}

// createIcon makes the monochrome tray icon of size pixels square.
func createIcon(size int) (windows.Handle, error) {
	return createHICON(appicon.TrayImage(size))
}

// createHICON converts a square NRGBA image into a Win32 HICON with alpha.
func createHICON(im *image.NRGBA) (windows.Handle, error) {
	size := im.Bounds().Dx()
	header := bitmapInfoHeader{
		Width:    int32(size),
		Height:   -int32(size), // top-down rows
		Planes:   1,
		BitCount: 32,
	}
	header.Size = uint32(unsafe.Sizeof(header))
	// BITMAPINFO is the header and, for 32-bit pixels, an unused palette entry.
	info := struct {
		Header bitmapInfoHeader
		Colors [1]uint32
	}{Header: header}
	var bits unsafe.Pointer
	color, _, err := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&info)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 {
		return 0, fmt.Errorf("create icon bitmap: %w", err)
	}
	defer procDeleteObject.Call(color)
	pixels := unsafe.Slice((*byte)(bits), size*size*4)
	for i := 0; i < len(im.Pix); i += 4 {
		// BGRA, not premultiplied.
		pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = im.Pix[i+2], im.Pix[i+1], im.Pix[i], im.Pix[i+3]
	}
	// The mask is required but unused where the color bitmap has alpha.
	mask, _, err := procCreateBitmap.Call(uintptr(size), uintptr(size), 1, 1, 0)
	if mask == 0 {
		return 0, fmt.Errorf("create icon mask: %w", err)
	}
	defer procDeleteObject.Call(mask)
	ii := iconInfo{Icon: 1, Mask: windows.Handle(mask), Color: windows.Handle(color)}
	icon, _, err := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	if icon == 0 {
		return 0, fmt.Errorf("create icon: %w", err)
	}
	return windows.Handle(icon), nil
}

func (t *Tray) notifyData() notifyIconData {
	nid := notifyIconData{
		Wnd:             t.wnd,
		ID:              trayIconID,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: trayMessage,
		Icon:            t.icon,
	}
	nid.Size = uint32(unsafe.Sizeof(nid))
	tip, _ := windows.UTF16FromString(t.opts.Title)
	copy(nid.Tip[:len(nid.Tip)-1], tip)
	return nid
}

// circularNotificationIcon crops src to a centered circle of size x size pixels
// so peer avatars look crisp in Windows toast notifications.
func circularNotificationIcon(src image.Image, size int) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return appicon.Image(size)
	}
	// Scale so the shorter side fills size, then center-crop to square.
	scaleW, scaleH := size, size
	if w > h {
		scaleW = max(size*w/h, size)
	} else if h > w {
		scaleH = max(size*h/w, size)
	}
	scaled := resample.Resize(src, scaleW, scaleH)
	ox := (scaleW - size) / 2
	oy := (scaleH - size) / 2
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	cx := float64(size) / 2
	r := cx - 0.5
	for y := 0; y < size; y++ {
		dy := float64(y) + 0.5 - cx
		for x := 0; x < size; x++ {
			dx := float64(x) + 0.5 - cx
			d := math.Hypot(dx, dy)
			if d > r+0.5 {
				continue
			}
			cov := 1.0
			if d > r-0.5 {
				cov = r + 0.5 - d
			}
			p := scaled.RGBAAt(ox+x, oy+y)
			if p.A == 0 {
				continue
			}
			// Convert premultiplied RGBA to straight NRGBA and apply circle AA.
			a := uint8(math.Round(float64(p.A) * cov))
			if a == 0 {
				continue
			}
			out.Pix[(y*size+x)*4+0] = uint8(min(int(p.R)*255/int(p.A), 255))
			out.Pix[(y*size+x)*4+1] = uint8(min(int(p.G)*255/int(p.A), 255))
			out.Pix[(y*size+x)*4+2] = uint8(min(int(p.B)*255/int(p.A), 255))
			out.Pix[(y*size+x)*4+3] = a
		}
	}
	return out
}

// Notify shows a notification by the icon: a balloon, which Windows 10 and
// later show as a toast. A new one takes the place of the last.
func (t *Tray) Notify(title, text string, sound bool, icon image.Image) error {
	if !t.added.Load() {
		return ErrUnsupported
	}
	nid := t.notifyData()
	nid.Flags |= nifInfo
	nid.InfoFlags = niifUser | niifLargeIcon | niifQuietTime
	if !sound {
		nid.InfoFlags |= niifNoSound
	}
	var custom windows.Handle
	if icon != nil {
		if h, err := createHICON(circularNotificationIcon(icon, 128)); err == nil {
			custom = h
		}
	}
	t.balloonMu.Lock()
	old := t.lastBalloon
	if custom != 0 {
		t.lastBalloon = custom
		nid.BalloonIcon = custom
	} else {
		t.lastBalloon = 0
		nid.BalloonIcon = t.balloonIcon
	}
	t.balloonMu.Unlock()
	t16, _ := windows.UTF16FromString(title)
	copy(nid.InfoTitle[:len(nid.InfoTitle)-1], t16)
	b16, _ := windows.UTF16FromString(text)
	copy(nid.Info[:len(nid.Info)-1], b16)
	r, _, callErr := procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
	if old != 0 {
		procDestroyIcon.Call(uintptr(old))
	}
	if r == 0 {
		return fmt.Errorf("tray: notify: %w", callErr)
	}
	return nil
}

func (t *Tray) add() {
	nid := t.notifyData()
	r, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	t.added.Store(r != 0)
}

func (t *Tray) wndProc(wnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch {
	case message == trayMessage:
		// Without NOTIFYICON_VERSION_4, lParam is the mouse message.
		switch uint32(lParam) & 0xffff {
		case wmLButtonUp:
			if t.opts.Activate != nil {
				go t.opts.Activate("")
			}
		case wmRButtonUp:
			t.showMenu()
		case ninBalloonClick:
			if t.opts.Notified != nil {
				go t.opts.Notified("")
			}
		}
		return 0
	case message == t.taskbarCreated && message != 0:
		// Explorer restarted and forgot every icon.
		t.add()
		return 0
	case message == wmClose:
		nid := t.notifyData()
		procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
		t.added.Store(false)
		procDestroyWindow.Call(wnd)
		return 0
	case message == wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(wnd, uintptr(message), wParam, lParam)
	return r
}

func (t *Tray) showMenu() {
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// Without the foreground, the menu does not close when clicked away.
	procSetForegroundWindow.Call(uintptr(t.wnd))
	cmd, _, _ := procTrackPopupMenu.Call(uintptr(t.menu), tpmReturnCmd|tpmNoNotify|tpmRightButton,
		uintptr(pt.X), uintptr(pt.Y), 0, uintptr(t.wnd), 0)
	procPostMessageW.Call(uintptr(t.wnd), wmNull, 0, 0)
	if cmd == 0 || int(cmd) > len(t.opts.Items) {
		return
	}
	if it := t.opts.Items[cmd-1]; it.Action != nil {
		go it.Action("")
	}
}
