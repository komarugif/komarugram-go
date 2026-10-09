// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"bytes"
	"errors"
	"fmt"
	"gioui.org/io/transfer"
	syscall "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"image"
	"io"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"gioui.org/app/internal/windows"
	"gioui.org/op"
	"gioui.org/unit"
	gowindows "golang.org/x/sys/windows"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
)

type Win32ViewEvent struct {
	HWND uintptr
}

type window struct {
	hwnd syscall.Handle
	hdc  syscall.Handle
	w    *callbacks
	// placed is set once Configure has sized and shown the window.
	placed bool

	// cursorIn tracks whether the cursor was inside the window according
	// to the most recent WM_SETCURSOR.
	cursorIn bool
	cursor   syscall.Handle

	animating bool

	borderSize image.Point
	config     Config
	// frameDims stores the last seen window frame width and height.
	frameDims image.Point
	loop      *eventLoop

	// blurWanted and transparentWanted are the BlurBehind and Transparent
	// options; config.BlurBehind and config.Transparent are what was
	// granted.
	blurWanted, transparentWanted bool
	// accent is set once the window has an accent policy, which is then to
	// be taken off it.
	accent bool
	// unseen are the borders the system's frame had around what was seen of
	// the window when it last had that frame; zero before.
	unseen windows.Rect
	// drop takes the files dragged over the window; nil if it cannot.
	drop *dropTarget
	// legacyMouse is set where the system has no pointer input, before
	// Windows 8: the mouse comes as WM_MOUSEMOVE, WM_LBUTTONDOWN and the
	// like, and mouseBtns are the buttons held.
	legacyMouse bool
	mouseBtns   pointer.Buttons
	// highSurrogate is the first half of a character beyond the Basic
	// Multilingual Plane, an emoji say, which comes as two WM_CHAR.
	highSurrogate uint16
}

const _WM_WAKEUP = windows.WM_USER + iota

const copyDataURLType = 0xffffff00

type gpuAPI struct {
	priority    int
	initializer func(w *window) (context, error)
}

// drivers is the list of potential Context implementations.
var drivers []gpuAPI

// winMap maps win32 HWNDs to *windows.
var winMap sync.Map

// iconID is the ID of the icon in the resource file.
const iconID = 1

var resources struct {
	once sync.Once
	// handle is the module handle from GetModuleHandle.
	handle syscall.Handle
	// class is the Gio window class from RegisterClassEx.
	class uint16
	// cursor is the arrow cursor resource.
	cursor syscall.Handle
}

func osMain() {
	processURLEvent(startupURI())
	select {}
}

func newWindow(win *callbacks, options []Option) {
	done := make(chan struct{})
	go func() {
		// GetMessage and PeekMessage can filter on a window HWND, but
		// then thread-specific messages such as WM_QUIT are ignored.
		// Instead lock the thread so window messages arrive through
		// unfiltered GetMessage calls.
		runtime.LockOSThread()

		w := &window{
			w: win,
		}
		w.loop = newEventLoop(w.w, w.wakeup)
		w.w.SetDriver(w)
		err := w.init()
		done <- struct{}{}
		if err != nil {
			w.ProcessEvent(DestroyEvent{Err: err})
			return
		}
		winMap.Store(w.hwnd, w)
		defer winMap.Delete(w.hwnd)
		w.Configure(options)
		w.ProcessEvent(Win32ViewEvent{HWND: uintptr(w.hwnd)})
		windows.SetForegroundWindow(w.hwnd)
		windows.SetFocus(w.hwnd)
		w.registerDropTarget()
		// Since the window class for the cursor is null,
		// set it here to show the cursor.
		w.SetCursor(pointer.CursorDefault)
		w.runLoop()
	}()
	<-done
}

// initResources initializes the resources global.
func initResources() error {
	windows.SetProcessDPIAware()
	hInst, err := windows.GetModuleHandle()
	if err != nil {
		return err
	}
	resources.handle = hInst
	c, err := windows.LoadCursor(windows.IDC_ARROW)
	if err != nil {
		return err
	}
	resources.cursor = c
	// Prefer an icon supplied at IDI_APPLICATION, which is where a
	// resource author puts an icon meant for the window and title bar.
	// Fall back to the first icon group for resources built without
	// one, which is the previous behavior. A binary with no icon
	// resources at all keeps an icon-less window class, as before.
	var icon syscall.Handle
	for _, id := range []uint32{windows.IDI_APPLICATION, iconID} {
		h, err := windows.LoadImage(hInst, id, windows.IMAGE_ICON, 0, 0, windows.LR_DEFAULTSIZE|windows.LR_SHARED)
		if err == nil {
			icon = h
			break
		}
	}

	appid, err := syscall.UTF16PtrFromString(ID)
	if err != nil {
		return err
	}

	wcls := windows.WndClassEx{
		CbSize:        uint32(unsafe.Sizeof(windows.WndClassEx{})),
		Style:         windows.CS_HREDRAW | windows.CS_VREDRAW | windows.CS_OWNDC,
		LpfnWndProc:   syscall.NewCallback(windowProc),
		HInstance:     hInst,
		HIcon:         icon,
		LpszClassName: appid,
	}
	cls, err := windows.RegisterClassEx(&wcls)
	if err != nil {
		return err
	}
	resources.class = cls
	return nil
}

const dwExStyle = windows.WS_EX_APPWINDOW | windows.WS_EX_WINDOWEDGE

func (w *window) init() error {
	var resErr error
	resources.once.Do(func() {
		resErr = initResources()
	})
	if resErr != nil {
		return resErr
	}
	const dwStyle = windows.WS_OVERLAPPEDWINDOW

	hwnd, err := windows.CreateWindowEx(
		dwExStyle,
		resources.class,
		"",
		dwStyle|windows.WS_CLIPSIBLINGS|windows.WS_CLIPCHILDREN,
		windows.CW_USEDEFAULT, windows.CW_USEDEFAULT,
		windows.CW_USEDEFAULT, windows.CW_USEDEFAULT,
		0,
		0,
		resources.handle,
		0)
	if err != nil {
		return err
	}
	if err := windows.RegisterTouchWindow(hwnd, 0); err != nil {
		return err
	}
	if err := windows.EnableMouseInPointer(1); err == windows.ErrNoPointerInput {
		w.legacyMouse = true
	} else if err != nil {
		return err
	}
	w.hdc, err = windows.GetDC(hwnd)
	if err != nil {
		windows.DestroyWindow(hwnd)
		return err
	}
	w.hwnd = hwnd
	return nil
}

// update handles changes done by the user, and updates the configuration.
// It reads the window style and size/position and updates w.config.
// If anything has changed it emits a ConfigEvent to notify the application.
func (w *window) update() {
	p := windows.GetWindowPlacement(w.hwnd)
	if !p.IsMinimized() {
		r := windows.GetWindowRect(w.hwnd)
		cr := windows.GetClientRect(w.hwnd)
		w.config.Size = image.Point{
			X: int(cr.Right - cr.Left),
			Y: int(cr.Bottom - cr.Top),
		}
		w.frameDims = image.Point{
			X: int(r.Right - r.Left),
			Y: int(r.Bottom - r.Top),
		}.Sub(w.config.Size)
	}

	w.borderSize = image.Pt(
		windows.GetSystemMetrics(windows.SM_CXSIZEFRAME),
		windows.GetSystemMetrics(windows.SM_CYSIZEFRAME),
	)
	style := windows.GetWindowLong(w.hwnd, windows.GWL_STYLE)
	switch {
	case p.IsMinimized():
		w.config.Mode = Minimized
	case p.IsMaximized() && style&windows.WS_OVERLAPPEDWINDOW != 0:
		w.config.Mode = Maximized
	case p.IsMaximized():
		w.config.Mode = Fullscreen
	default:
		w.config.Mode = Windowed
	}
	w.ProcessEvent(ConfigEvent{Config: w.config})
	w.draw(true)
}

func windowProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	win, exists := winMap.Load(hwnd)
	if !exists {
		return windows.DefWindowProc(hwnd, msg, wParam, lParam)
	}

	w := win.(*window)

	switch msg {
	case windows.WM_UNICHAR:
		if wParam == windows.UNICODE_NOCHAR {
			// Tell the system that we accept WM_UNICHAR messages.
			return windows.TRUE
		}
		fallthrough
	case windows.WM_CHAR:
		if text := w.char(uint16(wParam)); text != "" {
			w.w.EditorInsert(text)
		}
		// The message is processed.
		return windows.TRUE
	case windows.WM_DWMCOMPOSITIONCHANGED:
		// The Basic and Classic themes of Windows 7 compose nothing: the
		// window cannot be seen through under them.
		if t, b := w.effects(w.config.Decorated); t != w.config.Transparent || b != w.config.BlurBehind {
			w.config.Transparent, w.config.BlurBehind = t, b
			w.applyEffects()
			windows.SetWindowPos(w.hwnd, 0, 0, 0, 0, 0, windows.SWP_NOMOVE|windows.SWP_NOSIZE|windows.SWP_NOZORDER|windows.SWP_FRAMECHANGED)
			w.update()
		}
	case windows.WM_DPICHANGED:
		// Let Windows know we're prepared for runtime DPI changes.
		return windows.TRUE
	case windows.WM_ERASEBKGND:
		// Avoid flickering between GPU content and background color.
		return windows.TRUE
	case windows.WM_KEYDOWN, windows.WM_KEYUP, windows.WM_SYSKEYDOWN, windows.WM_SYSKEYUP:
		if n, ok := convertKeyCode(wParam); ok {
			e := key.Event{
				Name:      n,
				Modifiers: getModifiers(),
				State:     key.Press,
			}
			if msg == windows.WM_KEYUP || msg == windows.WM_SYSKEYUP {
				e.State = key.Release
			}

			w.ProcessEvent(e)

			if (wParam == windows.VK_F10) && (msg == windows.WM_SYSKEYDOWN || msg == windows.WM_SYSKEYUP) {
				// Reserve F10 for ourselves, and don't let it open the system menu. Other Windows programs
				// such as cmd.exe and graphical debuggers also reserve F10.
				return 0
			}
		}
	case windows.WM_POINTERDOWN, windows.WM_POINTERUP, windows.WM_POINTERUPDATE, windows.WM_POINTERCAPTURECHANGED:
		pid := getPointerIDwParam(wParam)
		pi, err := windows.GetPointerInfo(uint32(pid))
		if err != nil {
			panic(err)
		}
		switch msg {
		case windows.WM_POINTERDOWN:
			windows.SetCapture(w.hwnd)
		case windows.WM_POINTERUP:
			windows.ReleaseCapture()
		}

		kind := pointer.Move
		switch pi.ButtonChangeType {
		case windows.POINTER_CHANGE_FIRSTBUTTON_DOWN, windows.POINTER_CHANGE_SECONDBUTTON_DOWN, windows.POINTER_CHANGE_THIRDBUTTON_DOWN, windows.POINTER_CHANGE_FOURTHBUTTON_DOWN, windows.POINTER_CHANGE_FIFTHBUTTON_DOWN:
			kind = pointer.Press
		case windows.POINTER_CHANGE_FIRSTBUTTON_UP, windows.POINTER_CHANGE_SECONDBUTTON_UP, windows.POINTER_CHANGE_THIRDBUTTON_UP, windows.POINTER_CHANGE_FOURTHBUTTON_UP, windows.POINTER_CHANGE_FIFTHBUTTON_UP:
			kind = pointer.Release
		}

		if (pi.PointerFlags&windows.POINTER_FLAG_CANCELED != 0) || (msg == windows.WM_POINTERCAPTURECHANGED) {
			kind = pointer.Cancel
		}

		w.pointerUpdate(pi, pid, kind, lParam)
	case windows.WM_LBUTTONDOWN, windows.WM_LBUTTONUP, windows.WM_RBUTTONDOWN, windows.WM_RBUTTONUP,
		windows.WM_MBUTTONDOWN, windows.WM_MBUTTONUP, windows.WM_XBUTTONDOWN, windows.WM_XBUTTONUP:
		if !w.legacyMouse {
			break
		}
		btn, press := mouseButton(msg, wParam)
		w.mouseButton(btn, press, lParam)
		if msg == windows.WM_XBUTTONDOWN || msg == windows.WM_XBUTTONUP {
			return 1
		}
		return 0
	case windows.WM_MOUSEMOVE:
		if !w.legacyMouse {
			break
		}
		x, y := coordsFromlParam(lParam)
		w.ProcessEvent(pointer.Event{
			Kind:      pointer.Move,
			Source:    pointer.Mouse,
			Position:  f32.Pt(float32(x), float32(y)),
			Buttons:   w.mouseBtns,
			Time:      windows.GetMessageTime(),
			Modifiers: getModifiers(),
		})
		return 0
	case windows.WM_MOUSEWHEEL, windows.WM_MOUSEHWHEEL:
		if !w.legacyMouse {
			break
		}
		w.scrollEvent(wParam, lParam, msg == windows.WM_MOUSEHWHEEL, getModifiers(), w.mouseBtns)
		return 0
	case windows.WM_CAPTURECHANGED:
		// Another window took the mouse while buttons were held.
		if w.legacyMouse && w.mouseBtns != 0 {
			w.mouseBtns = 0
			w.ProcessEvent(pointer.Event{Kind: pointer.Cancel, Source: pointer.Mouse})
		}
	case windows.WM_CANCELMODE:
		w.ProcessEvent(pointer.Event{
			Kind: pointer.Cancel,
		})
	case windows.WM_SETFOCUS:
		w.config.Focused = true
		w.ProcessEvent(ConfigEvent{Config: w.config})
	case windows.WM_KILLFOCUS:
		w.config.Focused = false
		w.ProcessEvent(ConfigEvent{Config: w.config})
	case windows.WM_NCHITTEST:
		if w.config.Decorated {
			// Let the system handle it.
			break
		}
		x, y := coordsFromlParam(lParam)
		np := windows.Point{X: int32(x), Y: int32(y)}
		windows.ScreenToClient(w.hwnd, &np)
		return w.hitTest(int(np.X), int(np.Y))
	case windows.WM_POINTERWHEEL, windows.WM_POINTERHWHEEL:
		pi, err := windows.GetPointerInfo(uint32(getPointerIDwParam(wParam)))
		if err != nil {
			panic(err)
		}
		w.scrollEvent(wParam, lParam, msg == windows.WM_POINTERHWHEEL, getModifiers(), getPointerButtons(pi))
	case windows.WM_DESTROY:
		w.revokeDropTarget()
		w.ProcessEvent(Win32ViewEvent{})
		w.ProcessEvent(DestroyEvent{})
		w.w = nil
		if w.hdc != 0 {
			windows.ReleaseDC(w.hdc)
			w.hdc = 0
		}
		// The system destroys the HWND for us.
		w.hwnd = 0
		windows.PostQuitMessage(0)
		return 0
	case windows.WM_NCCALCSIZE:
		if w.config.Decorated {
			// Let Windows handle decorations.
			break
		}
		// No client areas; we draw decorations ourselves.
		if wParam != 1 {
			return 0
		}
		// lParam contains an NCCALCSIZE_PARAMS for us to adjust.
		place := windows.GetWindowPlacement(w.hwnd)
		// A fullscreen window is maximized too, and is all of its screen,
		// not of the work area.
		fullscreen := windows.GetWindowLong(w.hwnd, windows.GWL_STYLE)&windows.WS_OVERLAPPEDWINDOW == 0
		if !place.IsMaximized() || fullscreen {
			if w.acrylic() {
				// With a client area that is all of the window, acrylic is
				// drawn over the content instead of behind it.
				szp := (*windows.NCCalcSizeParams)(unsafe.Pointer(lParam))
				szp.Rgrc[0].Bottom--
			}
			return 0
		}
		// Adjust window position to avoid the extra padding in maximized
		// state. See https://devblogs.microsoft.com/oldnewthing/20150304-00/?p=44543.
		// Note that trying to do the adjustment in WM_GETMINMAXINFO is ignored by Windows.
		szp := (*windows.NCCalcSizeParams)(unsafe.Pointer(lParam))
		mi := windows.GetMonitorInfo(w.hwnd)
		szp.Rgrc[0] = mi.WorkArea
		return 0
	case windows.WM_PAINT:
		w.draw(true)
	case windows.WM_STYLECHANGED:
		w.update()
	case windows.WM_WINDOWPOSCHANGED:
		w.update()
		return 0
	case windows.WM_GETMINMAXINFO:
		mm := (*windows.MinMaxInfo)(unsafe.Pointer(lParam))

		var frameDims image.Point
		if w.config.Decorated {
			frameDims = w.frameDims
		}
		if p := w.config.MinSize; p.X > 0 || p.Y > 0 {
			p = p.Add(frameDims)
			mm.PtMinTrackSize = windows.Point{
				X: int32(p.X),
				Y: int32(p.Y),
			}
		}
		if p := w.config.MaxSize; p.X > 0 || p.Y > 0 {
			p = p.Add(frameDims)
			mm.PtMaxTrackSize = windows.Point{
				X: int32(p.X),
				Y: int32(p.Y),
			}
		}
		return 0
	case windows.WM_SETCURSOR:
		w.cursorIn = (lParam & 0xffff) == windows.HTCLIENT
		if w.cursorIn {
			windows.SetCursor(w.cursor)
			return windows.TRUE
		}
	case _WM_WAKEUP:
		w.loop.Wakeup()
		w.loop.FlushEvents()
	case windows.WM_IME_STARTCOMPOSITION:
		imc := windows.ImmGetContext(w.hwnd)
		if imc == 0 {
			return windows.TRUE
		}
		defer windows.ImmReleaseContext(w.hwnd, imc)
		w.updateIMEWindows(imc)
		return windows.TRUE
	case windows.WM_IME_COMPOSITION:
		imc := windows.ImmGetContext(w.hwnd)
		if imc == 0 {
			return windows.TRUE
		}
		defer windows.ImmReleaseContext(w.hwnd, imc)
		defer w.updateIMEWindows(imc)
		state := w.w.EditorState()
		if lParam&windows.GCS_RESULTSTR != 0 {
			// RESULTSTR is committed text. Keep it separate from COMPSTR so a
			// preedit update never looks like a commit.
			rng := imeRange(state)
			result := windows.ImmGetCompositionString(imc, windows.GCS_RESULTSTR)
			start := rng.Start
			w.w.EditorReplace(rng, result)
			end := start + utf8.RuneCountInString(result)
			w.w.SetComposingRegion(key.Range{Start: -1, End: -1})
			w.w.SetEditorSelection(key.Range{Start: end, End: end})
			if lParam&windows.GCS_COMPSTR == 0 {
				return windows.TRUE
			}
			state = w.w.EditorState()
		}
		if lParam&windows.GCS_COMPSTR != 0 {
			// COMPSTR is still preedit text, so keep the composing range alive.
			rng := imeRange(state)
			replacement := windows.ImmGetCompositionString(imc, windows.GCS_COMPSTR)
			end := rng.Start + utf8.RuneCountInString(replacement)
			w.w.EditorReplace(rng, replacement)
			state = w.w.EditorState()
			comp := key.Range{
				Start: rng.Start,
				End:   end,
			}
			if lParam&windows.GCS_DELTASTART != 0 {
				start := windows.ImmGetCompositionValue(imc, windows.GCS_DELTASTART)
				comp.Start = state.RunesIndex(state.UTF16Index(comp.Start) + start)
			}
			w.w.SetComposingRegion(comp)
			pos := end
			if lParam&windows.GCS_CURSORPOS != 0 {
				rel := windows.ImmGetCompositionValue(imc, windows.GCS_CURSORPOS)
				pos = state.RunesIndex(state.UTF16Index(rng.Start) + rel)
			}
			w.w.SetEditorSelection(key.Range{Start: pos, End: pos})
			return windows.TRUE
		}
		if lParam&(windows.GCS_DELTASTART|windows.GCS_CURSORPOS) == 0 || state.compose.Start == -1 {
			return windows.TRUE
		}
		// Some composition messages only move the IME cursor or clause start.
		rng := normRange(state.compose)
		comp := rng
		if lParam&windows.GCS_DELTASTART != 0 {
			start := windows.ImmGetCompositionValue(imc, windows.GCS_DELTASTART)
			comp.Start = state.RunesIndex(state.UTF16Index(comp.Start) + start)
			w.w.SetComposingRegion(comp)
		}
		if lParam&windows.GCS_CURSORPOS != 0 {
			rel := windows.ImmGetCompositionValue(imc, windows.GCS_CURSORPOS)
			pos := state.RunesIndex(state.UTF16Index(rng.Start) + rel)
			w.w.SetEditorSelection(key.Range{Start: pos, End: pos})
		}
		return windows.TRUE
	case windows.WM_IME_ENDCOMPOSITION:
		w.w.SetComposingRegion(key.Range{Start: -1, End: -1})
		return windows.TRUE
	case windows.WM_COPYDATA:
		data := (*windows.CopyDataStruct)(unsafe.Pointer(lParam))
		switch data.DwData {
		case copyDataURLType:
			if schemesURI == "" {
				return windows.TRUE
			}

			uri := syscall.UTF16PtrToString((*uint16)(unsafe.Pointer(data.LpData)))
			if processURLEvent(uri) {
				w.Perform(system.ActionRaise)
			}
			return windows.TRUE
		}
	}

	return windows.DefWindowProc(hwnd, msg, wParam, lParam)
}

func getModifiers() key.Modifiers {
	var kmods key.Modifiers
	if windows.GetKeyState(windows.VK_LWIN)&0x1000 != 0 || windows.GetKeyState(windows.VK_RWIN)&0x1000 != 0 {
		kmods |= key.ModSuper
	}
	if windows.GetKeyState(windows.VK_MENU)&0x1000 != 0 {
		kmods |= key.ModAlt
	}
	if windows.GetKeyState(windows.VK_CONTROL)&0x1000 != 0 {
		kmods |= key.ModCtrl
	}
	if windows.GetKeyState(windows.VK_SHIFT)&0x1000 != 0 {
		kmods |= key.ModShift
	}
	return kmods
}

// updateIMEWindows keeps the Windows IME popup near the text being edited.
func (w *window) updateIMEWindows(imc syscall.Handle) {
	sel := w.w.EditorState().Selection
	top := sel.Transform.Transform(sel.Caret.Pos.Add(f32.Pt(0, -sel.Caret.Ascent)))
	base := sel.Transform.Transform(sel.Caret.Pos)
	bottom := sel.Transform.Transform(sel.Caret.Pos.Add(f32.Pt(0, sel.Caret.Descent)))

	itop := image.Pt(int(top.X+.5), int(top.Y+.5))
	ibase := image.Pt(int(base.X+.5), int(base.Y+.5))
	ibottom := image.Pt(int(bottom.X+.5), int(bottom.Y+.5))
	if ibottom.Y <= itop.Y {
		ibottom.Y = itop.Y + 1
	}
	exclude := windows.Rect{
		Left:   int32(ibase.X),
		Top:    int32(itop.Y),
		Right:  int32(ibase.X + 1),
		Bottom: int32(ibottom.Y),
	}
	x, y := ibottom.X, ibottom.Y
	if !sel.CompositionBounds.Empty() {
		exclude = transformRect(sel.Transform, sel.CompositionBounds)
		x = int(exclude.Left)
		y = int(exclude.Bottom)
	}
	windows.ImmSetCompositionWindow(imc, x, y)
	windows.ImmSetCandidateWindow(imc, x, y, exclude)
}

// transformRect maps a local rectangle to window coordinates. Transform all
// corners because an affine transform may flip or rotate the rectangle.
func transformRect(t f32.Affine2D, r image.Rectangle) windows.Rect {
	p0 := t.Transform(f32.Pt(float32(r.Min.X), float32(r.Min.Y)))
	p1 := t.Transform(f32.Pt(float32(r.Max.X), float32(r.Min.Y)))
	p2 := t.Transform(f32.Pt(float32(r.Max.X), float32(r.Max.Y)))
	p3 := t.Transform(f32.Pt(float32(r.Min.X), float32(r.Max.Y)))

	minX := min(min(p0.X, p1.X), min(p2.X, p3.X))
	minY := min(min(p0.Y, p1.Y), min(p2.Y, p3.Y))
	maxX := max(max(p0.X, p1.X), max(p2.X, p3.X))
	maxY := max(max(p0.Y, p1.Y), max(p2.Y, p3.Y))
	left := int32(math.Floor(float64(minX)))
	top := int32(math.Floor(float64(minY)))
	right := int32(math.Ceil(float64(maxX)))
	bottom := int32(math.Ceil(float64(maxY)))
	if right <= left {
		right = left + 1
	}
	if bottom <= top {
		bottom = top + 1
	}
	return windows.Rect{
		Left:   left,
		Top:    top,
		Right:  right,
		Bottom: bottom,
	}
}

// hitTest returns the non-client area hit by the point, needed to
// process WM_NCHITTEST.
func (w *window) hitTest(x, y int) uintptr {
	if w.config.Mode == Windowed {
		// Check for resize handle before system actions; otherwise it can be impossible to
		// resize a custom-decorations window when the system move area is flush with the
		// edge of the window.
		top := y <= w.borderSize.Y
		bottom := y >= w.config.Size.Y-w.borderSize.Y
		left := x <= w.borderSize.X
		right := x >= w.config.Size.X-w.borderSize.X
		switch {
		case top && left:
			return windows.HTTOPLEFT
		case top && right:
			return windows.HTTOPRIGHT
		case bottom && left:
			return windows.HTBOTTOMLEFT
		case bottom && right:
			return windows.HTBOTTOMRIGHT
		case top:
			return windows.HTTOP
		case bottom:
			return windows.HTBOTTOM
		case left:
			return windows.HTLEFT
		case right:
			return windows.HTRIGHT
		}
	}
	p := f32.Pt(float32(x), float32(y))
	if a, ok := w.w.ActionAt(p); ok && a == system.ActionMove {
		return windows.HTCAPTION
	}
	return windows.HTCLIENT
}

func (w *window) pointerUpdate(pi windows.PointerInfo, pid pointer.ID, kind pointer.Kind, lParam uintptr) {
	if !w.config.Focused {
		windows.SetFocus(w.hwnd)
	}

	src := pointer.Touch
	if pi.PointerType == windows.PT_MOUSE {
		src = pointer.Mouse
	}

	x, y := coordsFromlParam(lParam)
	np := windows.Point{X: int32(x), Y: int32(y)}
	windows.ScreenToClient(w.hwnd, &np)
	p := f32.Point{X: float32(np.X), Y: float32(np.Y)}
	w.ProcessEvent(pointer.Event{
		Kind:      kind,
		Source:    src,
		Position:  p,
		PointerID: pid,
		Buttons:   getPointerButtons(pi),
		Time:      windows.GetMessageTime(),
		Modifiers: getModifiers(),
	})
}

func coordsFromlParam(lParam uintptr) (int, int) {
	x := int(int16(lParam & 0xffff))
	y := int(int16((lParam >> 16) & 0xffff))
	return x, y
}

// char returns the text of a WM_CHAR: nothing for what does not print, and
// for the first half of a surrogate pair, kept until the second comes.
func (w *window) char(c uint16) string {
	high := w.highSurrogate
	w.highSurrogate = 0
	switch {
	case utf16.IsSurrogate(rune(c)) && c < 0xdc00:
		w.highSurrogate = c
		return ""
	case utf16.IsSurrogate(rune(c)):
		r := utf16.DecodeRune(rune(high), rune(c))
		if r == unicode.ReplacementChar || !unicode.IsPrint(r) {
			return ""
		}
		return string(r)
	}
	if r := rune(c); unicode.IsPrint(r) {
		return string(r)
	}
	return ""
}

// mouseButton returns the button of a button message of the mouse, and
// whether it went down.
func mouseButton(msg uint32, wParam uintptr) (pointer.Buttons, bool) {
	switch msg {
	case windows.WM_LBUTTONDOWN:
		return pointer.ButtonPrimary, true
	case windows.WM_LBUTTONUP:
		return pointer.ButtonPrimary, false
	case windows.WM_RBUTTONDOWN:
		return pointer.ButtonSecondary, true
	case windows.WM_RBUTTONUP:
		return pointer.ButtonSecondary, false
	case windows.WM_MBUTTONDOWN:
		return pointer.ButtonTertiary, true
	case windows.WM_MBUTTONUP:
		return pointer.ButtonTertiary, false
	}
	// WM_XBUTTONDOWN and WM_XBUTTONUP: GET_XBUTTON_WPARAM says which.
	btn := pointer.ButtonQuaternary
	if (wParam>>16)&0xffff == windows.XBUTTON2 {
		btn = pointer.ButtonQuinary
	}
	return btn, msg == windows.WM_XBUTTONDOWN
}

// mouseButton handles a button of the mouse where there is no pointer
// input: the window holds the mouse while any button is down, as with
// pointer input.
func (w *window) mouseButton(btn pointer.Buttons, press bool, lParam uintptr) {
	if !w.config.Focused {
		windows.SetFocus(w.hwnd)
	}
	kind := pointer.Release
	if press {
		kind = pointer.Press
		if w.mouseBtns == 0 {
			windows.SetCapture(w.hwnd)
		}
		w.mouseBtns |= btn
	} else {
		w.mouseBtns &^= btn
		if w.mouseBtns == 0 {
			windows.ReleaseCapture()
		}
	}
	// Unlike the pointer's, these coordinates are the client area's.
	x, y := coordsFromlParam(lParam)
	w.ProcessEvent(pointer.Event{
		Kind:      kind,
		Source:    pointer.Mouse,
		Position:  f32.Pt(float32(x), float32(y)),
		Buttons:   w.mouseBtns,
		Time:      windows.GetMessageTime(),
		Modifiers: getModifiers(),
	})
}

func (w *window) scrollEvent(wParam, lParam uintptr, horizontal bool, kmods key.Modifiers, btns pointer.Buttons) {
	x, y := coordsFromlParam(lParam)
	// The WM_MOUSEWHEEL coordinates are in screen coordinates, in contrast
	// to other mouse events.
	np := windows.Point{X: int32(x), Y: int32(y)}
	windows.ScreenToClient(w.hwnd, &np)
	p := f32.Point{X: float32(np.X), Y: float32(np.Y)}
	dist := float32(int16(wParam >> 16))
	var sp f32.Point
	if horizontal {
		sp.X = dist
	} else {
		// support horizontal scroll (shift + mousewheel)
		if kmods == key.ModShift {
			sp.X = -dist
		} else {
			sp.Y = -dist
		}
	}
	w.ProcessEvent(pointer.Event{
		Kind:      pointer.Scroll,
		Source:    pointer.Mouse,
		Position:  p,
		Buttons:   btns,
		Scroll:    sp,
		Wheel:     isWheelDelta(dist),
		Modifiers: kmods,
		Time:      windows.GetMessageTime(),
	})
}

// Adapted from https://blogs.msdn.microsoft.com/oldnewthing/20060126-00/?p=32513/
func (w *window) runLoop() {
	msg := new(windows.Msg)
loop:
	for {
		anim := w.animating
		p := windows.GetWindowPlacement(w.hwnd)
		if anim && !p.IsMinimized() && !windows.PeekMessage(msg, 0, 0, 0, windows.PM_NOREMOVE) {
			w.draw(false)
			continue
		}
		switch ret := windows.GetMessage(msg, 0, 0, 0); ret {
		case -1:
			panic(errors.New("GetMessage failed"))
		case 0:
			// WM_QUIT received.
			break loop
		}
		windows.TranslateMessage(msg)
		windows.DispatchMessage(msg)
	}
}

func (w *window) EditorStateChanged(old, new editorState) {
	imc := windows.ImmGetContext(w.hwnd)
	if imc == 0 {
		return
	}
	defer windows.ImmReleaseContext(w.hwnd, imc)
	if old.Selection.Caret != new.Selection.Caret ||
		old.Selection.Transform != new.Selection.Transform ||
		old.Selection.CompositionBounds != new.Selection.CompositionBounds {
		w.updateIMEWindows(imc)
	}
	if shouldCancelComposition(old, new) {
		windows.ImmNotifyIME(imc, windows.NI_COMPOSITIONSTR, windows.CPS_CANCEL, 0)
	}
}

func (w *window) SetAnimating(anim bool) {
	w.animating = anim
}

func (w *window) ProcessEvent(e event.Event) {
	w.w.ProcessEvent(e)
	w.loop.FlushEvents()
}

func (w *window) Event() event.Event {
	return w.loop.Event()
}

func (w *window) Invalidate() {
	w.loop.Invalidate()
}

func (w *window) Run(f func()) {
	w.loop.Run(f)
}

func (w *window) Frame(frame *op.Ops) {
	w.loop.Frame(frame)
}

func (w *window) wakeup() {
	if err := windows.PostMessage(w.hwnd, _WM_WAKEUP, 0, 0); err != nil {
		panic(err)
	}
}

func (w *window) draw(sync bool) {
	if w.config.Size.X == 0 || w.config.Size.Y == 0 {
		return
	}
	dpi := windows.GetWindowDPI(w.hwnd)
	cfg := configForDPI(dpi)
	w.ProcessEvent(frameEvent{
		FrameEvent: FrameEvent{
			Now:    time.Now(),
			Size:   w.config.Size,
			Metric: cfg,
		},
		Sync: sync,
	})
}

func (w *window) NewContext() (context, error) {
	sort.Slice(drivers, func(i, j int) bool {
		return drivers[i].priority < drivers[j].priority
	})
	var errs []string
	for _, b := range drivers {
		ctx, err := b.initializer(w)
		if err == nil {
			return ctx, nil
		}
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("NewContext: failed to create a GPU device, tried: %s", strings.Join(errs, ", "))
	}
	return nil, errors.New("NewContext: no available GPU drivers")
}

func (w *window) ReadClipboard(types []string) {
	typ, content := "", []byte(nil)
	if windows.OpenClipboard(w.hwnd) == nil {
		typ, content = clipboardContent(types)
		windows.CloseClipboard()
	}
	w.ProcessEvent(transfer.DataEvent{
		Type: typ,
		Open: func() io.ReadCloser {
			return io.NopCloser(bytes.NewReader(content))
		},
	})
}

func (w *window) Configure(options []Option) {
	dpi := windows.GetSystemDPI()
	metric := configForDPI(dpi)
	prev := w.config
	cnf := w.config
	cnf.BlurBehind, cnf.Transparent = w.blurWanted, w.transparentWanted
	cnf.apply(metric, options)
	w.blurWanted, w.transparentWanted = cnf.BlurBehind, cnf.Transparent
	cnf.Transparent, cnf.BlurBehind = w.effects(cnf.Decorated)
	w.config.Title = cnf.Title
	w.config.Decorated = cnf.Decorated
	w.config.MinSize = cnf.MinSize
	w.config.MaxSize = cnf.MaxSize
	windows.SetWindowText(w.hwnd, cnf.Title)
	w.config.Transparent, w.config.BlurBehind = cnf.Transparent, cnf.BlurBehind
	effects := w.config.Transparent != prev.Transparent || w.config.BlurBehind != prev.BlurBehind
	if w.placed && cnf.Mode == prev.Mode && cnf.Size == prev.Size && cnf.Decorated == prev.Decorated &&
		cnf.TopMost == prev.TopMost && cnf.MinSize == prev.MinSize && cnf.MaxSize == prev.MaxSize {
		// Nothing but the title or the effects changed. Placing the window
		// again would size it from config.Size, which update may not have
		// set yet.
		if effects {
			w.applyEffects()
			// The client area follows the blur: see WM_NCCALCSIZE.
			windows.SetWindowPos(w.hwnd, 0, 0, 0, 0, 0, windows.SWP_NOMOVE|windows.SWP_NOSIZE|windows.SWP_NOZORDER|windows.SWP_FRAMECHANGED)
			w.update()
		}
		return
	}
	w.placed = true
	defer func() {
		if effects || cnf.Decorated != prev.Decorated {
			w.applyEffects()
			w.update()
		}
	}()

	style := windows.GetWindowLong(w.hwnd, windows.GWL_STYLE)
	var showMode int32
	var x, y, width, height int32
	swpStyle := uintptr(windows.SWP_FRAMECHANGED)
	if cnf.TopMost == w.config.TopMost {
		// Don't change the z-order if TopMost didn't change.
		swpStyle |= windows.SWP_NOZORDER
	}
	hwndAfter := windows.HWND_NOTOPMOST
	if cnf.TopMost {
		hwndAfter = windows.HWND_TOPMOST
	}
	w.config.TopMost = cnf.TopMost
	winStyle := uintptr(windows.WS_OVERLAPPEDWINDOW)
	style &^= winStyle
	switch cnf.Mode {
	case Minimized:
		style |= winStyle
		swpStyle |= windows.SWP_NOMOVE | windows.SWP_NOSIZE
		showMode = windows.SW_SHOWMINIMIZED

	case Maximized:
		style |= winStyle
		swpStyle |= windows.SWP_NOMOVE | windows.SWP_NOSIZE
		showMode = windows.SW_SHOWMAXIMIZED

	case Windowed:
		style |= winStyle
		showMode = windows.SW_SHOWNORMAL
		// Get target for client area size.
		width = int32(cnf.Size.X)
		height = int32(cnf.Size.Y)
		// Get the current window size and position.
		wr := windows.GetWindowRect(w.hwnd)
		x = wr.Left
		y = wr.Top
		if cnf.Decorated {
			// Compute client size and position. Note that the client size is
			// equal to the window size when we are in control of decorations.
			r := windows.Rect{
				Right:  width,
				Bottom: height,
			}
			windows.AdjustWindowRectEx(&r, uint32(style), 0, dwExStyle)
			width = r.Right - r.Left
			height = r.Bottom - r.Top
		} else {
			if w.acrylic() {
				// The client area is a pixel shorter than the window: see
				// WM_NCCALCSIZE.
				height++
			}
			if prev.Decorated && w.shown() {
				seen := windows.DwmVisibleRect(w.hwnd)
				w.unseen = windows.Rect{Left: seen.Left - wr.Left, Top: seen.Top - wr.Top, Right: wr.Right - seen.Right, Bottom: wr.Bottom - seen.Bottom}
			}
			if !cnf.Transparent {
				// Enable drop shadows when we draw decorations. The frame
				// is opaque, and is left out of a transparent window.
				windows.DwmExtendFrameIntoClientArea(w.hwnd, windows.Margins{-1, -1, -1, -1})
			}
		}
		if w.shown() && cnf.Decorated != prev.Decorated && cnf.Size == prev.Size && prev.Mode == Windowed {
			// Only the frame changes: what is seen of the window stays where
			// it is, and the content takes what the frame leaves of it.
			unseen := w.unseen
			if cnf.Decorated && unseen == (windows.Rect{}) {
				unseen = unseenBorders(uint32(style))
			}
			r := reframed(windows.DwmVisibleRect(w.hwnd), cnf.Decorated, unseen)
			x, y, width, height = r.Left, r.Top, r.Right-r.Left, r.Bottom-r.Top
		}

	case Fullscreen:
		swpStyle |= windows.SWP_NOMOVE | windows.SWP_NOSIZE
		showMode = windows.SW_SHOWMAXIMIZED
	}

	// Disable window resizing if MinSize and MaxSize are equal.
	if cnf.MaxSize != (image.Point{}) && cnf.MinSize == cnf.MaxSize {
		style &^= windows.WS_MAXIMIZEBOX
		style &^= windows.WS_THICKFRAME
	}

	// Note: these invocation all trigger the windows callback method which may process a pending system.ActionCenter
	// action, so SetWindowPos should come first so as to not "overwrite" system.ActionCenter.
	windows.SetWindowPos(w.hwnd, hwndAfter, x, y, width, height, swpStyle)
	windows.SetWindowLong(w.hwnd, windows.GWL_STYLE, style)
	windows.ShowWindow(w.hwnd, showMode)
}

// shown reports whether the window was placed and is on the screen.
func (w *window) shown() bool {
	return w.placed && windows.IsWindowVisible(w.hwnd)
}

// unseenBorders are the borders that the system's frame of a window of the
// style has around what is seen of it: the frame without its line of a pixel,
// and nothing above the caption. It is for a window that never had that
// frame to be measured.
func unseenBorders(style uint32) windows.Rect {
	var r windows.Rect
	windows.AdjustWindowRectEx(&r, style, 0, dwExStyle)
	return windows.Rect{Left: max(-r.Left-1, 0), Right: max(r.Right-1, 0), Bottom: max(r.Bottom-1, 0)}
}

// reframed returns the rectangle of a window whose frame changes, for what
// is seen of it to stay where it is: seen is that, and unseen are the
// borders the system's frame has around it. A window without that frame is
// all seen.
func reframed(seen windows.Rect, decorated bool, unseen windows.Rect) windows.Rect {
	if !decorated {
		return seen
	}
	return windows.Rect{Left: seen.Left - unseen.Left, Top: seen.Top - unseen.Top, Right: seen.Right + unseen.Right, Bottom: seen.Bottom + unseen.Bottom}
}

// acrylicTint is the tint of the blur behind a window: next to none, for the
// content to tint it with what it paints. Acrylic is not drawn with a tint
// that is wholly transparent.
const acrylicTint = 0x01000000

// effects returns what is granted of the transparency and the blur wanted
// for a window with the system's frame or without: nothing where the desktop
// is not composed. Acrylic is drawn behind the content of a window without
// the system's frame, and around the one with it; Aero's glass, on Windows
// 7, behind the content of either.
func (w *window) effects(decorated bool) (transparent, blur bool) {
	transparent = w.transparentWanted && windows.Composition()
	blur = transparent && w.blurWanted && windows.TransparencyEffects() && (!decorated || windows.GlassBlur())
	return transparent, blur
}

// acrylic reports whether the blur behind the window is acrylic.
func (w *window) acrylic() bool { return w.config.BlurBehind && !windows.GlassBlur() }

// applyEffects makes the system show what is behind the window where its
// content is not opaque, as config.Transparent and config.BlurBehind say:
// blurred, with acrylic or Aero's glass, or as it is. config.BlurBehind is
// cleared when the system has no acrylic.
func (w *window) applyEffects() {
	if w.acrylic() {
		if err := windows.SetWindowAccent(w.hwnd, windows.AccentAcrylic, acrylicTint); err != nil {
			w.config.BlurBehind = false
		} else {
			w.accent = true
		}
	}
	if !w.acrylic() && w.accent {
		windows.SetWindowAccent(w.hwnd, windows.AccentDisabled, 0)
		w.accent = false
	}
	// Acrylic takes the alpha of the content itself; the glass is drawn
	// where DwmEnableBlurBehindWindow lets the desktop through.
	windows.DwmBlurBehind(w.hwnd, w.config.Transparent && !w.acrylic(), w.config.BlurBehind && !w.acrylic())
	if w.config.Transparent || w.config.Decorated {
		windows.DwmExtendFrameIntoClientArea(w.hwnd, windows.Margins{})
	}
}

func (w *window) WriteClipboard(mime string, s []byte) {
	if mime == "image/png" {
		w.writeClipboardImage(s)
		return
	}
	w.writeClipboard(string(s), nil)
}

func (w *window) WriteClipboardHTML(text, html []byte) {
	w.writeClipboard(string(text), html)
}

// putClipboard puts data on the open clipboard in format.
func putClipboard(format uint32, data []byte) error {
	mem, err := windows.GlobalAlloc(len(data))
	if err != nil {
		return err
	}
	ptr, err := windows.GlobalLock(mem)
	if err != nil {
		windows.GlobalFree(mem)
		return err
	}
	copy(unsafe.Slice((*byte)(ptr), len(data)), data)
	windows.GlobalUnlock(mem)
	if err := windows.SetClipboardData(format, mem); err != nil {
		windows.GlobalFree(mem)
		return err
	}
	return nil
}

// writeClipboardImage puts a PNG on the clipboard as itself, in the
// registered "PNG" format that browsers and editors read with its alpha,
// and as a device-independent bitmap for the others.
func (w *window) writeClipboardImage(png []byte) error {
	if err := windows.OpenClipboard(w.hwnd); err != nil {
		return err
	}
	defer windows.CloseClipboard()
	if err := windows.EmptyClipboard(); err != nil {
		return err
	}
	put := putClipboard
	if format, err := windows.RegisterClipboardFormat("PNG"); err == nil {
		if err := put(format, png); err != nil {
			return err
		}
	}
	dib, err := pngToDIB(png)
	if err != nil {
		return err
	}
	return put(windows.CF_DIB, dib)
}

// writeClipboard puts text on the clipboard, and html beside it in the
// registered "HTML Format" when it is not nil.
func (w *window) writeClipboard(s string, html []byte) error {
	if err := windows.OpenClipboard(w.hwnd); err != nil {
		return err
	}
	defer windows.CloseClipboard()
	if err := windows.EmptyClipboard(); err != nil {
		return err
	}
	u16, err := gowindows.UTF16FromString(s)
	if err != nil {
		return err
	}
	n := len(u16) * int(unsafe.Sizeof(u16[0]))
	mem, err := windows.GlobalAlloc(n)
	if err != nil {
		return err
	}
	ptr, err := windows.GlobalLock(mem)
	if err != nil {
		windows.GlobalFree(mem)
		return err
	}
	u16v := unsafe.Slice((*uint16)(ptr), len(u16))
	copy(u16v, u16)
	windows.GlobalUnlock(mem)
	if err := windows.SetClipboardData(windows.CF_UNICODETEXT, mem); err != nil {
		windows.GlobalFree(mem)
		return err
	}
	if html != nil {
		if format, err := windows.RegisterClipboardFormat("HTML Format"); err == nil {
			return putClipboard(format, cfHTML(html))
		}
	}
	return nil
}

func (w *window) SetCursor(cursor pointer.Cursor) {
	c, err := loadCursor(cursor)
	if err != nil {
		c = resources.cursor
	}
	w.cursor = c
	if w.cursorIn {
		windows.SetCursor(w.cursor)
	}
}

// windowsCursor contains mapping from pointer.Cursor to an IDC.
var windowsCursor = [...]uint16{
	pointer.CursorDefault:                  windows.IDC_ARROW,
	pointer.CursorNone:                     0,
	pointer.CursorText:                     windows.IDC_IBEAM,
	pointer.CursorVerticalText:             windows.IDC_IBEAM,
	pointer.CursorPointer:                  windows.IDC_HAND,
	pointer.CursorCrosshair:                windows.IDC_CROSS,
	pointer.CursorAllScroll:                windows.IDC_SIZEALL,
	pointer.CursorColResize:                windows.IDC_SIZEWE,
	pointer.CursorRowResize:                windows.IDC_SIZENS,
	pointer.CursorGrab:                     windows.IDC_SIZEALL,
	pointer.CursorGrabbing:                 windows.IDC_SIZEALL,
	pointer.CursorNotAllowed:               windows.IDC_NO,
	pointer.CursorWait:                     windows.IDC_WAIT,
	pointer.CursorProgress:                 windows.IDC_APPSTARTING,
	pointer.CursorNorthWestResize:          windows.IDC_SIZENWSE,
	pointer.CursorNorthEastResize:          windows.IDC_SIZENESW,
	pointer.CursorSouthWestResize:          windows.IDC_SIZENESW,
	pointer.CursorSouthEastResize:          windows.IDC_SIZENWSE,
	pointer.CursorNorthSouthResize:         windows.IDC_SIZENS,
	pointer.CursorEastWestResize:           windows.IDC_SIZEWE,
	pointer.CursorWestResize:               windows.IDC_SIZEWE,
	pointer.CursorEastResize:               windows.IDC_SIZEWE,
	pointer.CursorNorthResize:              windows.IDC_SIZENS,
	pointer.CursorSouthResize:              windows.IDC_SIZENS,
	pointer.CursorNorthEastSouthWestResize: windows.IDC_SIZENESW,
	pointer.CursorNorthWestSouthEastResize: windows.IDC_SIZENWSE,
}

func loadCursor(cursor pointer.Cursor) (syscall.Handle, error) {
	switch cursor {
	case pointer.CursorDefault:
		return resources.cursor, nil
	case pointer.CursorNone:
		return 0, nil
	default:
		return windows.LoadCursor(windowsCursor[cursor])
	}
}

func (w *window) ShowTextInput(show bool) {}

func (w *window) SetInputHint(_ key.InputHint) {}

func (w *window) HDC() syscall.Handle {
	return w.hdc
}

func (w *window) HWND() (syscall.Handle, int, int) {
	return w.hwnd, w.config.Size.X, w.config.Size.Y
}

func (w *window) Perform(acts system.Action) {
	walkActions(acts, func(a system.Action) {
		switch a {
		case system.ActionCenter:
			if w.config.Mode != Windowed {
				break
			}
			r := windows.GetWindowRect(w.hwnd)
			dx := r.Right - r.Left
			dy := r.Bottom - r.Top
			// Center in the usable area of the current monitor.
			area := windows.GetMonitorInfo(w.hwnd).WorkArea
			x := (area.Right + area.Left - dx) / 2
			y := (area.Bottom + area.Top - dy) / 2
			windows.SetWindowPos(w.hwnd, 0, x, y, dx, dy, windows.SWP_NOZORDER|windows.SWP_FRAMECHANGED)
		case system.ActionRaise:
			// A minimized window has to be restored before it can be
			// brought to the front; SetForegroundWindow on its own
			// leaves it minimized, so raising an iconified window did
			// nothing at all.
			if windows.IsIconic(w.hwnd) {
				windows.ShowWindow(w.hwnd, windows.SW_RESTORE)
			}
			windows.SetForegroundWindow(w.hwnd)
			windows.SetWindowPos(w.hwnd, windows.HWND_TOP, 0, 0, 0, 0,
				windows.SWP_NOMOVE|windows.SWP_NOSIZE|windows.SWP_SHOWWINDOW)
		case system.ActionClose:
			windows.PostMessage(w.hwnd, windows.WM_CLOSE, 0, 0)
		}
	})
}

func convertKeyCode(code uintptr) (key.Name, bool) {
	if '0' <= code && code <= '9' || 'A' <= code && code <= 'Z' {
		return key.Name(rune(code)), true
	}
	var r key.Name

	switch code {
	case windows.VK_ESCAPE:
		r = key.NameEscape
	case windows.VK_LEFT:
		r = key.NameLeftArrow
	case windows.VK_RIGHT:
		r = key.NameRightArrow
	case windows.VK_RETURN:
		r = key.NameReturn
	case windows.VK_UP:
		r = key.NameUpArrow
	case windows.VK_DOWN:
		r = key.NameDownArrow
	case windows.VK_HOME:
		r = key.NameHome
	case windows.VK_END:
		r = key.NameEnd
	case windows.VK_BACK:
		r = key.NameDeleteBackward
	case windows.VK_DELETE:
		r = key.NameDeleteForward
	case windows.VK_PRIOR:
		r = key.NamePageUp
	case windows.VK_NEXT:
		r = key.NamePageDown
	case windows.VK_F1:
		r = key.NameF1
	case windows.VK_F2:
		r = key.NameF2
	case windows.VK_F3:
		r = key.NameF3
	case windows.VK_F4:
		r = key.NameF4
	case windows.VK_F5:
		r = key.NameF5
	case windows.VK_F6:
		r = key.NameF6
	case windows.VK_F7:
		r = key.NameF7
	case windows.VK_F8:
		r = key.NameF8
	case windows.VK_F9:
		r = key.NameF9
	case windows.VK_F10:
		r = key.NameF10
	case windows.VK_F11:
		r = key.NameF11
	case windows.VK_F12:
		r = key.NameF12
	case windows.VK_TAB:
		r = key.NameTab
	case windows.VK_SPACE:
		r = key.NameSpace
	case windows.VK_OEM_1:
		r = ";"
	case windows.VK_OEM_PLUS:
		r = "+"
	case windows.VK_OEM_COMMA:
		r = ","
	case windows.VK_OEM_MINUS:
		r = "-"
	case windows.VK_OEM_PERIOD:
		r = "."
	case windows.VK_OEM_2:
		r = "/"
	case windows.VK_OEM_3:
		r = "`"
	case windows.VK_OEM_4:
		r = "["
	case windows.VK_OEM_5, windows.VK_OEM_102:
		r = "\\"
	case windows.VK_OEM_6:
		r = "]"
	case windows.VK_OEM_7:
		r = "'"
	case windows.VK_CONTROL:
		r = key.NameCtrl
	case windows.VK_SHIFT:
		r = key.NameShift
	case windows.VK_MENU:
		r = key.NameAlt
	case windows.VK_LWIN, windows.VK_RWIN:
		r = key.NameSuper
	default:
		return "", false
	}
	return r, true
}

func configForDPI(dpi int) unit.Metric {
	const inchPrDp = 1.0 / 96.0
	ppdp := float32(dpi) * inchPrDp
	return unit.Metric{
		PxPerDp: ppdp,
		PxPerSp: ppdp,
	}
}

func (Win32ViewEvent) implementsViewEvent() {}
func (Win32ViewEvent) ImplementsEvent()     {}
func (w Win32ViewEvent) Valid() bool {
	return w != (Win32ViewEvent{})
}

// LOWORD (minwindef.h)
func loWord(val uint32) uint16 {
	return uint16(val & 0xFFFF)
}

// GET_POINTERID_WPARAM (winuser.h)
func getPointerIDwParam(wParam uintptr) pointer.ID {
	return pointer.ID(loWord(uint32(wParam)))
}

func getPointerButtons(pi windows.PointerInfo) pointer.Buttons {
	var btns pointer.Buttons

	if pi.PointerFlags&windows.POINTER_FLAG_FIRSTBUTTON != 0 {
		btns |= pointer.ButtonPrimary
	} else {
		btns &^= pointer.ButtonPrimary
	}
	if pi.PointerFlags&windows.POINTER_FLAG_SECONDBUTTON != 0 {
		btns |= pointer.ButtonSecondary
	} else {
		btns &^= pointer.ButtonSecondary
	}
	if pi.PointerFlags&windows.POINTER_FLAG_THIRDBUTTON != 0 {
		btns |= pointer.ButtonTertiary
	} else {
		btns &^= pointer.ButtonTertiary
	}
	if pi.PointerFlags&windows.POINTER_FLAG_FOURTHBUTTON != 0 {
		btns |= pointer.ButtonQuaternary
	} else {
		btns &^= pointer.ButtonQuaternary
	}
	if pi.PointerFlags&windows.POINTER_FLAG_FIFTHBUTTON != 0 {
		btns |= pointer.ButtonQuinary
	} else {
		btns &^= pointer.ButtonQuinary
	}

	return btns
}

// schemesURI is a list of schemes, comma separated, that must be
// defined using -X compiler ldflag, that used in gogio.
var schemesURI string

func init() {
	if schemesURI == "" {
		return
	}

	currentSchemes := strings.Split(schemesURI, ",")
	oldSchemes := registeredSchemes(ID)

	for _, s := range currentSchemes {
		for i, o := range oldSchemes {
			if s == o {
				oldSchemes = append(oldSchemes[:i], oldSchemes[i+1:]...)
				break
			}
		}
	}

	if len(oldSchemes) > 0 {
		go unregisterSchemes(ID, oldSchemes)
	}

	if len(currentSchemes) == 0 {
		return
	}

	// On Windows, launching the app using a URI will start a new instance of the app,
	// a new window. That behavior, by default, doesn't align with iOS/Android/macOS, where
	// the deeplink sends the event to the running app (if any). We are emulating it.
	if hwnd, _ := windows.FindWindow(ID); hwnd != 0 {
		if u := startupURI(); u != "" {
			broadcastURI(hwnd, u)
		}
		os.Exit(0)
		return
	}

	go registerSchemes(ID, currentSchemes)
}

func startupURI() string {
	if len(os.Args) == 3 && os.Args[1] == "-gio_launch_url" {
		return os.Args[2]
	}
	return ""
}

func processURLEvent(rawurl string) bool {
	if rawurl == "" {
		return false
	}

	evt, err := newURLEvent(rawurl)
	if err != nil {
		return false
	}

	for _, scheme := range strings.Split(schemesURI, ",") {
		if strings.EqualFold(scheme, evt.URL.Scheme) {
			processGlobalEvent(evt)
			return true
		}
	}

	return false
}

func broadcastURI(hwnd syscall.Handle, uri string) {
	data, err := syscall.UTF16FromString(uri)
	if err != nil {
		return // Only happens if uri contains NULL character.
	}

	pinner := new(runtime.Pinner)
	defer pinner.Unpin()
	pinner.Pin(unsafe.Pointer(unsafe.SliceData(data)))

	msg := &windows.CopyDataStruct{
		DwData: copyDataURLType,
		CbData: uint32(len(data) * int(unsafe.Sizeof(data[0]))),
		LpData: uintptr(unsafe.Pointer(unsafe.SliceData(data))),
	}
	pinner.Pin(unsafe.Pointer(msg))

	// SendMessage blocks until the message is processed.
	windows.SendMessage(hwnd, windows.WM_COPYDATA, 0, uintptr(unsafe.Pointer(msg)))
}

func registeredSchemes(appid string) []string {
	meta, err := registry.OpenKey(registry.CURRENT_USER, `Software\\`+appid, registry.ALL_ACCESS)
	if err != nil {
		return nil
	}
	defer meta.Close()

	schemes, _, _ := meta.GetStringsValue("URISchemes")
	return schemes
}

func registerSchemes(appid string, schemes []string) error {
	reg := func(scheme string) error {
		key, existent, err := registry.CreateKey(registry.CURRENT_USER, `Software\\Classes\\`+scheme, registry.ALL_ACCESS)
		if err != nil {
			return err
		}
		defer key.Close()

		if existent {
			// Check if the existent key belongs to the current application
			id, _, err := key.GetStringValue("appid")
			if err != nil || id != appid {
				return fmt.Errorf("scheme %s already registered by another application", scheme)
			}
		}

		path, err := os.Executable()
		if err != nil {
			return err
		}

		if err = key.SetStringValue("", "URL:"+scheme+" Protocol"); err != nil {
			return err
		}
		if err = key.SetStringValue("URL Protocol", ""); err != nil {
			return err
		}
		if err = key.SetStringValue("appid", appid); err != nil {
			return err
		}

		icon, _, err := registry.CreateKey(key, `DefaultIcon`, registry.ALL_ACCESS)
		if err != nil {
			return err
		}
		defer icon.Close()

		if err = icon.SetStringValue("", `"`+path+`",1`); err != nil {
			return err
		}

		cmd, _, err := registry.CreateKey(key, `shell\\open\\command`, registry.ALL_ACCESS)
		if err != nil {
			return err
		}
		defer cmd.Close()

		if err = cmd.SetStringValue("", `"`+path+`" -gio_launch_url "%1"`); err != nil {
			return err
		}

		return nil
	}

	for _, scheme := range schemes {
		if scheme == "" {
			continue // just in case
		}
		if err := reg(scheme); err != nil {
			return err
		}
	}

	meta, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\\`+appid, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer meta.Close()

	if err = meta.SetStringsValue("URISchemes", schemes); err != nil {
		return err
	}

	return nil
}

func unregisterSchemes(appid string, schemes []string) {
	classes, err := registry.OpenKey(registry.CURRENT_USER, `Software\\Classes`, registry.ALL_ACCESS)
	if err != nil {
		return
	}
	defer classes.Close()

	for _, scheme := range schemes {
		if scheme == "" {
			continue // just in case
		}

		key, err := registry.OpenKey(classes, scheme, registry.ALL_ACCESS)
		if err != nil {
			continue
		}

		id, _, err := key.GetStringValue("appid")
		if err == nil && id != appid {
			continue
		}

		for _, k := range []string{`DefaultIcon`, `shell\\open\\command`, `shell\\open`, `shell`} {
			registry.DeleteKey(key, k)
		}

		if err := key.Close(); err != nil {
			continue
		}

		registry.DeleteKey(classes, scheme)
	}
}
