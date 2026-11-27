// SPDX-License-Identifier: Unlicense OR MIT

//go:build windows

package miniapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wv2Shim is injected before any page script when running a Telegram Mini App
// in Edge WebView2. It forwards TelegramWebviewProxy.postEvent to the host via
// window.chrome.webview.postMessage.
const wv2Shim = `
window.TelegramWebviewProxy = {
  postEvent: function (eventType, eventData) {
    window.chrome.webview.postMessage(JSON.stringify({eventType: eventType, eventData: eventData}));
  }
};
`

const (
	wmApp        = 0x8000
	wmDispatch   = wmApp + 1
	wmDestroy    = 0x0002
	wmSize       = 0x0005
	wmClose      = 0x0010
	wmNCDestroy  = 0x0082
	wsOverlapped = 0x00000000
	wsCaption    = 0x00C00000
	wsSysMenu    = 0x00080000
	wsThickFrame = 0x00040000
	wsMinBox     = 0x00020000
	wsMaxBox     = 0x00010000
	wsOverlappedWindow = wsOverlapped | wsCaption | wsSysMenu | wsThickFrame | wsMinBox | wsMaxBox
	swShow       = 5
	swpNoZOrder  = 0x0004
	swpNoActivate = 0x0010
	cwUseDefault = ^uintptr(0x7fffffff) // int32(-2147483648) sign-extended
	csHRedraw    = 0x0002
	csVRedraw    = 0x0001
	idcArrow     = 32512
	colorWindow  = 5
	coinitApartmentThreaded = 0x2
	dwmwaUseImmersiveDarkMode = 20
)

var (
	wv2User32   = windows.NewLazySystemDLL("user32.dll")
	wv2Kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	wv2Ole32    = windows.NewLazySystemDLL("ole32.dll")
	wv2Dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")

	procRegisterClassExW      = wv2User32.NewProc("RegisterClassExW")
	procCreateWindowExW       = wv2User32.NewProc("CreateWindowExW")
	procDefWindowProcW        = wv2User32.NewProc("DefWindowProcW")
	procDestroyWindow         = wv2User32.NewProc("DestroyWindow")
	procShowWindow            = wv2User32.NewProc("ShowWindow")
	procUpdateWindow          = wv2User32.NewProc("UpdateWindow")
	procGetMessageW           = wv2User32.NewProc("GetMessageW")
	procTranslateMessage      = wv2User32.NewProc("TranslateMessage")
	procDispatchMessageW      = wv2User32.NewProc("DispatchMessageW")
	procPostMessageW          = wv2User32.NewProc("PostMessageW")
	procPostQuitMessage       = wv2User32.NewProc("PostQuitMessage")
	procGetClientRect         = wv2User32.NewProc("GetClientRect")
	procGetWindowRect         = wv2User32.NewProc("GetWindowRect")
	procSetWindowPos          = wv2User32.NewProc("SetWindowPos")
	procSetWindowTextW        = wv2User32.NewProc("SetWindowTextW")
	procLoadCursorW           = wv2User32.NewProc("LoadCursorW")
	procGetDpiForWindow       = wv2User32.NewProc("GetDpiForWindow")
	procGetModuleHandleW      = wv2Kernel32.NewProc("GetModuleHandleW")
	procCoInitializeEx        = wv2Ole32.NewProc("CoInitializeEx")
	procCoUninitialize        = wv2Ole32.NewProc("CoUninitialize")
	procCoTaskMemFree         = wv2Ole32.NewProc("CoTaskMemFree")
	procDwmSetWindowAttribute = wv2Dwmapi.NewProc("DwmSetWindowAttribute")
)

type wv2Rect struct {
	Left, Top, Right, Bottom int32
}

type wv2Point struct {
	X, Y int32
}

type wv2Msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      wv2Point
}

type wndClassExW struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

// webView2DLLPath returns the EmbeddedBrowserWebView.dll path beside an
// msedgewebview2.exe binary for the current CPU architecture, or "" if absent.
func webView2DLLPath(exePath string) string {
	if exePath == "" {
		return ""
	}
	var arch string
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "386":
		arch = "x86"
	case "arm64":
		arch = "arm64"
	default:
		return ""
	}
	dll := filepath.Join(filepath.Dir(exePath), "EBWebView", arch, "EmbeddedBrowserWebView.dll")
	if st, err := os.Stat(dll); err == nil && !st.IsDir() {
		return dll
	}
	return ""
}

// COM vtables matching Microsoft's WebView2.h.

type iUnknownVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
}

type iCoreWebView2EnvironmentVtbl struct {
	iUnknownVtbl
	CreateCoreWebView2Controller     uintptr
	CreateWebResourceResponse        uintptr
	GetBrowserVersionString          uintptr
	AddNewBrowserVersionAvailable    uintptr
	RemoveNewBrowserVersionAvailable uintptr
}

type iCoreWebView2Environment struct {
	vtbl *iCoreWebView2EnvironmentVtbl
}

type iCoreWebView2ControllerVtbl struct {
	iUnknownVtbl
	GetIsVisible                      uintptr
	PutIsVisible                      uintptr
	GetBounds                         uintptr
	PutBounds                         uintptr
	GetZoomFactor                     uintptr
	PutZoomFactor                     uintptr
	AddZoomFactorChanged              uintptr
	RemoveZoomFactorChanged           uintptr
	SetBoundsAndZoomFactor            uintptr
	MoveFocus                         uintptr
	AddMoveFocusRequested             uintptr
	RemoveMoveFocusRequested          uintptr
	AddGotFocus                       uintptr
	RemoveGotFocus                    uintptr
	AddLostFocus                      uintptr
	RemoveLostFocus                   uintptr
	AddAcceleratorKeyPressed          uintptr
	RemoveAcceleratorKeyPressed       uintptr
	GetParentWindow                   uintptr
	PutParentWindow                   uintptr
	NotifyParentWindowPositionChanged uintptr
	Close                             uintptr
	GetCoreWebView2                   uintptr
}

type iCoreWebView2Controller struct {
	vtbl *iCoreWebView2ControllerVtbl
}

type iCoreWebView2Vtbl struct {
	iUnknownVtbl
	GetSettings                            uintptr
	GetSource                              uintptr
	Navigate                               uintptr
	NavigateToString                       uintptr
	AddNavigationStarting                  uintptr
	RemoveNavigationStarting               uintptr
	AddContentLoading                      uintptr
	RemoveContentLoading                   uintptr
	AddSourceChanged                       uintptr
	RemoveSourceChanged                    uintptr
	AddHistoryChanged                      uintptr
	RemoveHistoryChanged                   uintptr
	AddNavigationCompleted                 uintptr
	RemoveNavigationCompleted              uintptr
	AddFrameNavigationStarting             uintptr
	RemoveFrameNavigationStarting          uintptr
	AddFrameNavigationCompleted            uintptr
	RemoveFrameNavigationCompleted         uintptr
	AddScriptDialogOpening                 uintptr
	RemoveScriptDialogOpening              uintptr
	AddPermissionRequested                 uintptr
	RemovePermissionRequested              uintptr
	AddProcessFailed                       uintptr
	RemoveProcessFailed                    uintptr
	AddScriptToExecuteOnDocumentCreated    uintptr
	RemoveScriptToExecuteOnDocumentCreated uintptr
	ExecuteScript                          uintptr
	CapturePreview                         uintptr
	Reload                                 uintptr
	PostWebMessageAsJSON                   uintptr
	PostWebMessageAsString                 uintptr
	AddWebMessageReceived                  uintptr
	RemoveWebMessageReceived               uintptr
	CallDevToolsProtocolMethod             uintptr
	GetBrowserProcessID                    uintptr
	GetCanGoBack                           uintptr
	GetCanGoForward                        uintptr
	GoBack                                 uintptr
	GoForward                              uintptr
	GetDevToolsProtocolEventReceiver       uintptr
	Stop                                   uintptr
	AddNewWindowRequested                  uintptr
	RemoveNewWindowRequested               uintptr
	AddDocumentTitleChanged                uintptr
	RemoveDocumentTitleChanged             uintptr
	GetDocumentTitle                       uintptr
	AddHostObjectToScript                  uintptr
	RemoveHostObjectFromScript             uintptr
	OpenDevToolsWindow                     uintptr
	AddContainsFullScreenElementChanged    uintptr
	RemoveContainsFullScreenElementChanged uintptr
	GetContainsFullScreenElement           uintptr
	AddWebResourceRequested                uintptr
	RemoveWebResourceRequested             uintptr
	AddWebResourceRequestedFilter          uintptr
	RemoveWebResourceRequestedFilter       uintptr
	AddWindowCloseRequested                uintptr
	RemoveWindowCloseRequested             uintptr
}

type iCoreWebView2 struct {
	vtbl *iCoreWebView2Vtbl
}

type iCoreWebView2WebMessageReceivedEventArgsVtbl struct {
	iUnknownVtbl
	GetSource                uintptr
	GetWebMessageAsJSON      uintptr
	TryGetWebMessageAsString uintptr
}

type iCoreWebView2WebMessageReceivedEventArgs struct {
	vtbl *iCoreWebView2WebMessageReceivedEventArgsVtbl
}

type comHandlerVtbl struct {
	iUnknownVtbl
	Invoke uintptr
}

type wv2EnvHandler struct {
	vtbl *comHandlerVtbl
	p    *wv2Proto
}

type wv2CtrlHandler struct {
	vtbl *comHandlerVtbl
	p    *wv2Proto
}

type wv2MsgHandler struct {
	vtbl *comHandlerVtbl
	p    *wv2Proto
}

type wv2TitleHandler struct {
	vtbl *comHandlerVtbl
	p    *wv2Proto
}

type wv2CloseHandler struct {
	vtbl *comHandlerVtbl
	p    *wv2Proto
}

var (
	wv2ClassOnce   sync.Once
	wv2ClassErr    error
	wv2ClassName   = "KomaruGramWebView2"
	wv2Windows     sync.Map // hwnd (uintptr) -> *wv2Proto
	wv2ProfilesMu  sync.Mutex
	wv2Profiles    = map[string]bool{}

	wv2EnvHandlerVtbl = comHandlerVtbl{
		iUnknownVtbl: iUnknownVtbl{
			QueryInterface: syscall.NewCallback(wv2EnvQI),
			AddRef:         syscall.NewCallback(wv2RefStub),
			Release:        syscall.NewCallback(wv2RefStub),
		},
		Invoke: syscall.NewCallback(wv2EnvInvoke),
	}
	wv2CtrlHandlerVtbl = comHandlerVtbl{
		iUnknownVtbl: iUnknownVtbl{
			QueryInterface: syscall.NewCallback(wv2CtrlQI),
			AddRef:         syscall.NewCallback(wv2RefStub),
			Release:        syscall.NewCallback(wv2RefStub),
		},
		Invoke: syscall.NewCallback(wv2CtrlInvoke),
	}
	wv2MsgHandlerVtbl = comHandlerVtbl{
		iUnknownVtbl: iUnknownVtbl{
			QueryInterface: syscall.NewCallback(wv2MsgQI),
			AddRef:         syscall.NewCallback(wv2RefStub),
			Release:        syscall.NewCallback(wv2RefStub),
		},
		Invoke: syscall.NewCallback(wv2MsgInvoke),
	}
	wv2TitleHandlerVtbl = comHandlerVtbl{
		iUnknownVtbl: iUnknownVtbl{
			QueryInterface: syscall.NewCallback(wv2TitleQI),
			AddRef:         syscall.NewCallback(wv2RefStub),
			Release:        syscall.NewCallback(wv2RefStub),
		},
		Invoke: syscall.NewCallback(wv2TitleInvoke),
	}
	wv2CloseHandlerVtbl = comHandlerVtbl{
		iUnknownVtbl: iUnknownVtbl{
			QueryInterface: syscall.NewCallback(wv2CloseQI),
			AddRef:         syscall.NewCallback(wv2RefStub),
			Release:        syscall.NewCallback(wv2RefStub),
		},
		Invoke: syscall.NewCallback(wv2CloseInvoke),
	}
)

func wv2RefStub(_ uintptr) uintptr { return 1 }

func wv2EnvQI(this *wv2EnvHandler, _ uintptr, ppv *uintptr) uintptr {
	if ppv != nil {
		*ppv = uintptr(unsafe.Pointer(this))
	}
	return 0
}

func wv2CtrlQI(this *wv2CtrlHandler, _ uintptr, ppv *uintptr) uintptr {
	if ppv != nil {
		*ppv = uintptr(unsafe.Pointer(this))
	}
	return 0
}

func wv2MsgQI(this *wv2MsgHandler, _ uintptr, ppv *uintptr) uintptr {
	if ppv != nil {
		*ppv = uintptr(unsafe.Pointer(this))
	}
	return 0
}

func wv2TitleQI(this *wv2TitleHandler, _ uintptr, ppv *uintptr) uintptr {
	if ppv != nil {
		*ppv = uintptr(unsafe.Pointer(this))
	}
	return 0
}

func wv2CloseQI(this *wv2CloseHandler, _ uintptr, ppv *uintptr) uintptr {
	if ppv != nil {
		*ppv = uintptr(unsafe.Pointer(this))
	}
	return 0
}

func wv2EnvInvoke(this *wv2EnvHandler, hr uintptr, env *iCoreWebView2Environment) uintptr {
	this.p.onEnvironmentCreated(hr, env)
	return 0
}

func wv2CtrlInvoke(this *wv2CtrlHandler, hr uintptr, ctrl *iCoreWebView2Controller) uintptr {
	this.p.onControllerCreated(hr, ctrl)
	return 0
}

func wv2MsgInvoke(this *wv2MsgHandler, _ *iCoreWebView2, args *iCoreWebView2WebMessageReceivedEventArgs) uintptr {
	this.p.onWebMessage(args)
	return 0
}

func wv2TitleInvoke(this *wv2TitleHandler, sender *iCoreWebView2, _ uintptr) uintptr {
	this.p.onTitleChanged(sender)
	return 0
}

func wv2CloseInvoke(this *wv2CloseHandler, _ *iCoreWebView2, _ uintptr) uintptr {
	if this.p.hwnd != 0 {
		_, _, _ = procPostMessageW.Call(this.p.hwnd, wmClose, 0, 0)
	}
	return 0
}

func registerWebView2Class() error {
	wv2ClassOnce.Do(func() {
		inst, _, _ := procGetModuleHandleW.Call(0)
		cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
		className, err := windows.UTF16PtrFromString(wv2ClassName)
		if err != nil {
			wv2ClassErr = err
			return
		}
		wc := wndClassExW{
			Size:       uint32(unsafe.Sizeof(wndClassExW{})),
			Style:      csHRedraw | csVRedraw,
			WndProc:    syscall.NewCallback(wv2WndProc),
			Instance:   inst,
			Cursor:     cursor,
			Background: colorWindow + 1,
			ClassName:  className,
		}
		atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		if atom == 0 {
			wv2ClassErr = fmt.Errorf("register WebView2 window class: %w", callErr)
		}
	})
	return wv2ClassErr
}

func wv2WndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmDispatch:
		if v, ok := wv2Windows.Load(hwnd); ok {
			v.(*wv2Proto).drain()
		}
		return 0
	case wmSize:
		if v, ok := wv2Windows.Load(hwnd); ok {
			v.(*wv2Proto).resize()
		}
		return 0
	case wmClose:
		_, _, _ = procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		if v, ok := wv2Windows.Load(hwnd); ok {
			v.(*wv2Proto).cleanupCOM()
		}
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	case wmNCDestroy:
		wv2Windows.Delete(hwnd)
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return ret
}

// wv2Proto drives an in-process Microsoft Edge WebView2 window on Windows.
type wv2Proto struct {
	bridge     *Bridge
	url        string
	telegram   bool
	profileKey string

	hwnd       uintptr
	browserPID uint32
	env        *iCoreWebView2Environment
	ctrl   *iCoreWebView2Controller
	view   *iCoreWebView2
	ready  chan error
	readyOnce sync.Once

	// Keep COM handlers pinned on the struct for the window's lifetime.
	envHandler   wv2EnvHandler
	ctrlHandler  wv2CtrlHandler
	msgHandler   wv2MsgHandler
	titleHandler wv2TitleHandler
	closeHandler wv2CloseHandler

	queueMu sync.Mutex
	queue   []func()
	closed  bool

	evalSeq atomic.Int64
	evalMu  sync.Mutex
	evals   map[int64]chan string
}

func launchWebView2(ctx context.Context, chosen browser, url, dir string, ephemeral bool, page Page, telegram bool) (*Bridge, error) {
	dllPath := webView2DLLPath(chosen.ref)
	if dllPath == "" {
		return nil, fmt.Errorf("Edge WebView2 runtime DLL not found beside %s", chosen.ref)
	}
	if err := registerWebView2Class(); err != nil {
		return nil, err
	}
	var profKey string
	if !ephemeral {
		profKey = strings.ToLower(filepath.Clean(dir))
		wv2ProfilesMu.Lock()
		if wv2Profiles[profKey] {
			wv2ProfilesMu.Unlock()
			return nil, fmt.Errorf("this Mini App is already open in another window")
		}
		wv2Profiles[profKey] = true
		wv2ProfilesMu.Unlock()
	}
	if page.Width <= 0 {
		page.Width = 420
	}
	if page.Height <= 0 {
		page.Height = 720
	}

	_, cancel := context.WithCancel(ctx)
	bridge := &Bridge{
		profile:   dir,
		ephemeral: ephemeral,
		exited:    make(chan struct{}),
		cancel:    cancel,
	}
	p := &wv2Proto{
		bridge:     bridge,
		url:        url,
		telegram:   telegram,
		profileKey: profKey,
		ready:      make(chan error, 1),
		evals:      map[int64]chan string{},
	}
	p.envHandler = wv2EnvHandler{vtbl: &wv2EnvHandlerVtbl, p: p}
	p.ctrlHandler = wv2CtrlHandler{vtbl: &wv2CtrlHandlerVtbl, p: p}
	p.msgHandler = wv2MsgHandler{vtbl: &wv2MsgHandlerVtbl, p: p}
	p.titleHandler = wv2TitleHandler{vtbl: &wv2TitleHandlerVtbl, p: p}
	p.closeHandler = wv2CloseHandler{vtbl: &wv2CloseHandlerVtbl, p: p}
	bridge.proto = p

	go p.runThread(dllPath, dir, page)

	select {
	case err := <-p.ready:
		if err != nil {
			bridge.Close()
			return nil, err
		}
		return bridge, nil
	case <-ctx.Done():
		bridge.Close()
		return nil, ctx.Err()
	}
}

func (p *wv2Proto) signalReady(err error) {
	p.readyOnce.Do(func() {
		p.ready <- err
	})
}

func (p *wv2Proto) runThread(dllPath, dir string, page Page) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer func() {
		if p.profileKey != "" {
			wv2ProfilesMu.Lock()
			delete(wv2Profiles, p.profileKey)
			wv2ProfilesMu.Unlock()
		}
		close(p.bridge.exited)
	}()
	defer p.failPendingEvals()

	_, _, _ = procCoInitializeEx.Call(0, coinitApartmentThreaded)
	defer procCoUninitialize.Call()

	inst, _, _ := procGetModuleHandleW.Call(0)
	className, _ := windows.UTF16PtrFromString(wv2ClassName)
	title, _ := windows.UTF16PtrFromString("KomaruGram")

	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow,
		cwUseDefault, cwUseDefault,
		uintptr(page.Width), uintptr(page.Height),
		0, 0, inst, 0,
	)
	if hwnd == 0 {
		p.signalReady(fmt.Errorf("create WebView2 window: %w", callErr))
		return
	}
	p.hwnd = hwnd
	wv2Windows.Store(hwnd, p)

	// Match dark title bar on Windows 10/11.
	dark := int32(1)
	_, _, _ = procDwmSetWindowAttribute.Call(
		hwnd,
		dwmwaUseImmersiveDarkMode,
		uintptr(unsafe.Pointer(&dark)),
		unsafe.Sizeof(dark),
	)

	// Scale initial window size for window DPI.
	if dpi := p.dpi(); dpi > 96 {
		w := int32(page.Width * dpi / 96)
		h := int32(page.Height * dpi / 96)
		_, _, _ = procSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(w), uintptr(h), swpNoZOrder|0x0002 /*SWP_NOMOVE*/)
	}

	_, _, _ = procShowWindow.Call(hwnd, swShow)
	_, _, _ = procUpdateWindow.Call(hwnd)

	dll := windows.NewLazyDLL(dllPath)
	createEnv := dll.NewProc("CreateWebViewEnvironmentWithOptionsInternal")
	if err := createEnv.Find(); err != nil {
		_, _, _ = procDestroyWindow.Call(hwnd)
		p.signalReady(fmt.Errorf("load Edge WebView2 runtime: %w", err))
		return
	}

	userDataDir, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		_, _, _ = procDestroyWindow.Call(hwnd)
		p.signalReady(err)
		return
	}

	hr, _, _ := createEnv.Call(
		1, // checkOverride / evergreen
		0, // runtimeType: 0 = Evergreen WebView2 Runtime
		uintptr(unsafe.Pointer(userDataDir)),
		0, // environmentOptions
		uintptr(unsafe.Pointer(&p.envHandler)),
	)
	if int32(hr) < 0 {
		_, _, _ = procDestroyWindow.Call(hwnd)
		p.signalReady(fmt.Errorf("CreateWebViewEnvironmentWithOptionsInternal failed: 0x%08x", uint32(hr)))
		return
	}

	var m wv2Msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	// Wait briefly for the WebView2 browser process to flush storage and release
	// profile file locks before signalling exit.
	if p.browserPID != 0 {
		if h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, p.browserPID); err == nil {
			_, _ = windows.WaitForSingleObject(h, 5000)
			_ = windows.CloseHandle(h)
		}
	}
	p.signalReady(fmt.Errorf("WebView2 window closed before initialization"))
}

func (p *wv2Proto) onEnvironmentCreated(hr uintptr, env *iCoreWebView2Environment) {
	if int32(hr) < 0 || env == nil {
		p.signalReady(fmt.Errorf("create WebView2 environment failed: 0x%08x", uint32(hr)))
		_, _, _ = procPostMessageW.Call(p.hwnd, wmClose, 0, 0)
		return
	}
	p.env = env
	_, _, _ = syscall.SyscallN(env.vtbl.AddRef, uintptr(unsafe.Pointer(env)))

	r, _, _ := syscall.SyscallN(
		env.vtbl.CreateCoreWebView2Controller,
		uintptr(unsafe.Pointer(env)),
		p.hwnd,
		uintptr(unsafe.Pointer(&p.ctrlHandler)),
	)
	if int32(r) < 0 {
		p.signalReady(fmt.Errorf("CreateCoreWebView2Controller failed: 0x%08x", uint32(r)))
		_, _, _ = procPostMessageW.Call(p.hwnd, wmClose, 0, 0)
	}
}

func (p *wv2Proto) onControllerCreated(hr uintptr, ctrl *iCoreWebView2Controller) {
	if int32(hr) < 0 || ctrl == nil {
		p.signalReady(fmt.Errorf("create WebView2 controller failed: 0x%08x", uint32(hr)))
		_, _, _ = procPostMessageW.Call(p.hwnd, wmClose, 0, 0)
		return
	}
	p.ctrl = ctrl
	_, _, _ = syscall.SyscallN(ctrl.vtbl.AddRef, uintptr(unsafe.Pointer(ctrl)))

	var view *iCoreWebView2
	r, _, _ := syscall.SyscallN(
		ctrl.vtbl.GetCoreWebView2,
		uintptr(unsafe.Pointer(ctrl)),
		uintptr(unsafe.Pointer(&view)),
	)
	if int32(r) < 0 || view == nil {
		p.signalReady(fmt.Errorf("GetCoreWebView2 failed: 0x%08x", uint32(r)))
		_, _, _ = procPostMessageW.Call(p.hwnd, wmClose, 0, 0)
		return
	}
	p.view = view

	var pid uint32
	if r, _, _ := syscall.SyscallN(
		view.vtbl.GetBrowserProcessID,
		uintptr(unsafe.Pointer(view)),
		uintptr(unsafe.Pointer(&pid)),
	); int32(r) >= 0 {
		p.browserPID = pid
	}

	p.resize()
	_, _, _ = syscall.SyscallN(ctrl.vtbl.PutIsVisible, uintptr(unsafe.Pointer(ctrl)), 1)

	var token int64
	_, _, _ = syscall.SyscallN(
		view.vtbl.AddWebMessageReceived,
		uintptr(unsafe.Pointer(view)),
		uintptr(unsafe.Pointer(&p.msgHandler)),
		uintptr(unsafe.Pointer(&token)),
	)
	_, _, _ = syscall.SyscallN(
		view.vtbl.AddDocumentTitleChanged,
		uintptr(unsafe.Pointer(view)),
		uintptr(unsafe.Pointer(&p.titleHandler)),
		uintptr(unsafe.Pointer(&token)),
	)
	_, _, _ = syscall.SyscallN(
		view.vtbl.AddWindowCloseRequested,
		uintptr(unsafe.Pointer(view)),
		uintptr(unsafe.Pointer(&p.closeHandler)),
		uintptr(unsafe.Pointer(&token)),
	)

	if p.telegram {
		shimUTF16, _ := windows.UTF16PtrFromString(wv2Shim)
		_, _, _ = syscall.SyscallN(
			view.vtbl.AddScriptToExecuteOnDocumentCreated,
			uintptr(unsafe.Pointer(view)),
			uintptr(unsafe.Pointer(shimUTF16)),
			0,
		)
	}

	urlUTF16, err := windows.UTF16PtrFromString(p.url)
	if err != nil {
		p.signalReady(err)
		_, _, _ = procPostMessageW.Call(p.hwnd, wmClose, 0, 0)
		return
	}
	r, _, _ = syscall.SyscallN(
		view.vtbl.Navigate,
		uintptr(unsafe.Pointer(view)),
		uintptr(unsafe.Pointer(urlUTF16)),
	)
	if int32(r) < 0 {
		p.signalReady(fmt.Errorf("WebView2 Navigate failed: 0x%08x", uint32(r)))
		_, _, _ = procPostMessageW.Call(p.hwnd, wmClose, 0, 0)
		return
	}

	p.signalReady(nil)
}

func (p *wv2Proto) onWebMessage(args *iCoreWebView2WebMessageReceivedEventArgs) {
	if args == nil {
		return
	}
	var ptr *uint16
	r, _, _ := syscall.SyscallN(
		args.vtbl.TryGetWebMessageAsString,
		uintptr(unsafe.Pointer(args)),
		uintptr(unsafe.Pointer(&ptr)),
	)
	if int32(r) < 0 || ptr == nil {
		return
	}
	msg := windows.UTF16PtrToString(ptr)
	_, _, _ = procCoTaskMemFree.Call(uintptr(unsafe.Pointer(ptr)))

	if strings.HasPrefix(msg, `{"__komaru_eval":`) {
		var reply struct {
			ID int64  `json:"__komaru_eval"`
			V  string `json:"v"`
		}
		if json.Unmarshal([]byte(msg), &reply) == nil && reply.ID > 0 {
			p.evalMu.Lock()
			ch := p.evals[reply.ID]
			delete(p.evals, reply.ID)
			p.evalMu.Unlock()
			if ch != nil {
				ch <- reply.V
			}
			return
		}
	}
	p.bridge.message(msg)
}

func (p *wv2Proto) onTitleChanged(sender *iCoreWebView2) {
	if sender == nil || p.hwnd == 0 {
		return
	}
	var ptr *uint16
	r, _, _ := syscall.SyscallN(
		sender.vtbl.GetDocumentTitle,
		uintptr(unsafe.Pointer(sender)),
		uintptr(unsafe.Pointer(&ptr)),
	)
	if int32(r) < 0 || ptr == nil {
		return
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(ptr)))
	if title := strings.TrimSpace(windows.UTF16PtrToString(ptr)); title != "" {
		_, _, _ = procSetWindowTextW.Call(p.hwnd, uintptr(unsafe.Pointer(ptr)))
	}
}

func (p *wv2Proto) resize() {
	if p.ctrl == nil || p.hwnd == 0 {
		return
	}
	var rc wv2Rect
	_, _, _ = procGetClientRect.Call(p.hwnd, uintptr(unsafe.Pointer(&rc)))
	p.putBounds(rc)
}

func (p *wv2Proto) putBounds(rc wv2Rect) {
	if p.ctrl == nil {
		return
	}
	switch runtime.GOARCH {
	case "386":
		_, _, _ = syscall.SyscallN(
			p.ctrl.vtbl.PutBounds,
			uintptr(unsafe.Pointer(p.ctrl)),
			uintptr(rc.Left), uintptr(rc.Top), uintptr(rc.Right), uintptr(rc.Bottom),
		)
	case "arm64":
		lo := uintptr(uint32(rc.Left)) | (uintptr(uint32(rc.Top)) << 32)
		hi := uintptr(uint32(rc.Right)) | (uintptr(uint32(rc.Bottom)) << 32)
		_, _, _ = syscall.SyscallN(
			p.ctrl.vtbl.PutBounds,
			uintptr(unsafe.Pointer(p.ctrl)),
			lo, hi,
		)
	default: // amd64: 16-byte struct passed by pointer
		_, _, _ = syscall.SyscallN(
			p.ctrl.vtbl.PutBounds,
			uintptr(unsafe.Pointer(p.ctrl)),
			uintptr(unsafe.Pointer(&rc)),
		)
	}
}

func (p *wv2Proto) cleanupCOM() {
	p.queueMu.Lock()
	p.closed = true
	p.queue = nil
	p.queueMu.Unlock()

	if p.view != nil {
		_, _, _ = syscall.SyscallN(p.view.vtbl.Release, uintptr(unsafe.Pointer(p.view)))
		p.view = nil
	}
	if p.ctrl != nil {
		_, _, _ = syscall.SyscallN(p.ctrl.vtbl.Close, uintptr(unsafe.Pointer(p.ctrl)))
		_, _, _ = syscall.SyscallN(p.ctrl.vtbl.Release, uintptr(unsafe.Pointer(p.ctrl)))
		p.ctrl = nil
	}
	if p.env != nil {
		_, _, _ = syscall.SyscallN(p.env.vtbl.Release, uintptr(unsafe.Pointer(p.env)))
		p.env = nil
	}
}

func (p *wv2Proto) failPendingEvals() {
	p.evalMu.Lock()
	for id, ch := range p.evals {
		close(ch)
		delete(p.evals, id)
	}
	p.evalMu.Unlock()
}

func (p *wv2Proto) post(fn func()) bool {
	p.queueMu.Lock()
	if p.closed || p.hwnd == 0 {
		p.queueMu.Unlock()
		return false
	}
	p.queue = append(p.queue, fn)
	hwnd := p.hwnd
	p.queueMu.Unlock()
	_, _, _ = procPostMessageW.Call(hwnd, wmDispatch, 0, 0)
	return true
}

func (p *wv2Proto) drain() {
	for {
		p.queueMu.Lock()
		if len(p.queue) == 0 {
			p.queueMu.Unlock()
			return
		}
		fn := p.queue[0]
		p.queue = p.queue[1:]
		p.queueMu.Unlock()
		fn()
	}
}

func (p *wv2Proto) dpi() int {
	if p.hwnd == 0 || procGetDpiForWindow.Find() != nil {
		return 96
	}
	d, _, _ := procGetDpiForWindow.Call(p.hwnd)
	if d < 96 {
		return 96
	}
	return int(d)
}

func (p *wv2Proto) eval(ctx context.Context, expression string, value bool) (string, error) {
	if !value {
		done := make(chan error, 1)
		if !p.post(func() {
			if p.view == nil {
				done <- fmt.Errorf("WebView2 is closed")
				return
			}
			js, err := windows.UTF16PtrFromString(expression)
			if err != nil {
				done <- err
				return
			}
			r, _, _ := syscall.SyscallN(
				p.view.vtbl.ExecuteScript,
				uintptr(unsafe.Pointer(p.view)),
				uintptr(unsafe.Pointer(js)),
				0,
			)
			if int32(r) < 0 {
				done <- fmt.Errorf("ExecuteScript failed: 0x%08x", uint32(r))
				return
			}
			done <- nil
		}) {
			return "", fmt.Errorf("WebView2 is closed")
		}
		select {
		case err := <-done:
			return "", err
		case <-ctx.Done():
			return "", ctx.Err()
		case <-p.bridge.exited:
			return "", fmt.Errorf("WebView2 is closed")
		}
	}

	id := p.evalSeq.Add(1)
	ch := make(chan string, 1)
	p.evalMu.Lock()
	p.evals[id] = ch
	p.evalMu.Unlock()

	wrapped := "(async function(){try{var __r=await (" + expression +
		");window.chrome.webview.postMessage(JSON.stringify({__komaru_eval:" +
		strconv.FormatInt(id, 10) +
		",v:typeof __r==='string'?__r:''}));}catch(e){window.chrome.webview.postMessage(JSON.stringify({__komaru_eval:" +
		strconv.FormatInt(id, 10) + ",v:''}));}})()"

	if !p.post(func() {
		if p.view == nil {
			return
		}
		js, err := windows.UTF16PtrFromString(wrapped)
		if err != nil {
			return
		}
		_, _, _ = syscall.SyscallN(
			p.view.vtbl.ExecuteScript,
			uintptr(unsafe.Pointer(p.view)),
			uintptr(unsafe.Pointer(js)),
			0,
		)
	}) {
		p.evalMu.Lock()
		delete(p.evals, id)
		p.evalMu.Unlock()
		return "", fmt.Errorf("WebView2 is closed")
	}

	select {
	case val, ok := <-ch:
		if !ok {
			return "", fmt.Errorf("WebView2 is closed")
		}
		return val, nil
	case <-ctx.Done():
		p.evalMu.Lock()
		delete(p.evals, id)
		p.evalMu.Unlock()
		return "", ctx.Err()
	case <-p.bridge.exited:
		return "", fmt.Errorf("WebView2 is closed")
	}
}

func (p *wv2Proto) windowSize(ctx context.Context) (width, height int, err error) {
	type res struct {
		w, h int
		err  error
	}
	done := make(chan res, 1)
	if !p.post(func() {
		if p.hwnd == 0 {
			done <- res{err: fmt.Errorf("WebView2 is closed")}
			return
		}
		var rc wv2Rect
		_, _, _ = procGetWindowRect.Call(p.hwnd, uintptr(unsafe.Pointer(&rc)))
		dpi := p.dpi()
		done <- res{
			w: int(rc.Right-rc.Left) * 96 / dpi,
			h: int(rc.Bottom-rc.Top) * 96 / dpi,
		}
	}) {
		return 0, 0, fmt.Errorf("WebView2 is closed")
	}
	select {
	case r := <-done:
		return r.w, r.h, r.err
	case <-ctx.Done():
		return 0, 0, ctx.Err()
	case <-p.bridge.exited:
		return 0, 0, fmt.Errorf("WebView2 is closed")
	}
}

func (p *wv2Proto) setWindowBounds(ctx context.Context, left, top, width, height int) error {
	done := make(chan error, 1)
	if !p.post(func() {
		if p.hwnd == 0 {
			done <- fmt.Errorf("WebView2 is closed")
			return
		}
		dpi := p.dpi()
		l := int32(left * dpi / 96)
		t := int32(top * dpi / 96)
		w := int32(width * dpi / 96)
		h := int32(height * dpi / 96)
		_, _, _ = procSetWindowPos.Call(
			p.hwnd, 0,
			uintptr(l), uintptr(t), uintptr(w), uintptr(h),
			swpNoZOrder|swpNoActivate,
		)
		done <- nil
	}) {
		return fmt.Errorf("WebView2 is closed")
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-p.bridge.exited:
		return fmt.Errorf("WebView2 is closed")
	}
}

func (p *wv2Proto) shutdown(ctx context.Context) error {
	if p.hwnd != 0 {
		_, _, _ = procPostMessageW.Call(p.hwnd, wmClose, 0, 0)
	}
	return nil
}

func (p *wv2Proto) close() {
	if p.hwnd != 0 {
		_, _, _ = procPostMessageW.Call(p.hwnd, wmClose, 0, 0)
	}
}
