// SPDX-License-Identifier: Unlicense OR MIT

//go:build windows

package notify

import (
	"encoding/xml"
	"fmt"
	"hash/fnv"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"komarugram/internal/appicon"
)

const toastAUMID = "KomaruGram"

var (
	combase                       = windows.NewLazySystemDLL("combase.dll")
	procRoInitialize              = combase.NewProc("RoInitialize")
	procRoUninitialize            = combase.NewProc("RoUninitialize")
	procRoGetActivationFactory    = combase.NewProc("RoGetActivationFactory")
	procRoActivateInstance        = combase.NewProc("RoActivateInstance")
	procWindowsCreateString       = combase.NewProc("WindowsCreateString")
	procWindowsDeleteString       = combase.NewProc("WindowsDeleteString")
	procRoInitializeCheckedOnce   sync.Once
	winrtToastAvailable           bool
)

const (
	roInitMultithreaded = 1
	sOK                 = 0
	sFalse              = 1
	rpcEChangedMode     = 0x80010106
)

var (
	iidIXmlDocumentIO              = windows.GUID{Data1: 0x6cd0e74e, Data2: 0xee65, Data3: 0x4489, Data4: [8]byte{0x9e, 0xbf, 0xca, 0x43, 0xe8, 0x7b, 0xa6, 0x37}}
	iidIToastNotificationFactory   = windows.GUID{Data1: 0x04124b20, Data2: 0x82c6, Data3: 0x4229, Data4: [8]byte{0xb1, 0x09, 0xfd, 0x9e, 0xd4, 0x66, 0x2b, 0x53}}
	iidIToastNotificationManager   = windows.GUID{Data1: 0x50ac103f, Data2: 0xd235, Data3: 0x4598, Data4: [8]byte{0xbb, 0xef, 0x98, 0xfe, 0x4d, 0x1a, 0x3a, 0xd4}}
	iidIToastNotification2         = windows.GUID{Data1: 0x9dfb9fd1, Data2: 0x143a, Data3: 0x490e, Data4: [8]byte{0x9c, 0xee, 0xb9, 0x34, 0xea, 0xfe, 0x7b, 0x9e}}
	iidITypedEventHandlerActivated = windows.GUID{Data1: 0xab54de2d, Data2: 0x97d9, Data3: 0x5528, Data4: [8]byte{0xb6, 0xad, 0x10, 0x5a, 0xfe, 0x15, 0x65, 0x30}}
	iidIUnknown                    = windows.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIAgileObject                = windows.GUID{Data1: 0x94ea2b94, Data2: 0xe9cc, Data3: 0x49e0, Data4: [8]byte{0xc0, 0xff, 0xee, 0x64, 0xca, 0x8f, 0x5b, 0x90}}
)

type comVtbl struct {
	queryInterface uintptr
	addRef         uintptr
	release        uintptr
	// Methods beyond IUnknown start at slot 3 (IInspectable has 3 more: slots 3..5).
	methods [16]uintptr
}

type comObject struct {
	vtbl *comVtbl
}

func (o *comObject) release() {
	if o != nil {
		syscall.SyscallN(o.vtbl.release, uintptr(unsafe.Pointer(o)))
	}
}

func (o *comObject) queryInterface(iid *windows.GUID) (*comObject, error) {
	var out *comObject
	hr, _, _ := syscall.SyscallN(o.vtbl.queryInterface, uintptr(unsafe.Pointer(o)), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	if int32(hr) < 0 {
		return nil, fmt.Errorf("QueryInterface: 0x%08x", uint32(hr))
	}
	return out, nil
}

func newHString(s string) (uintptr, error) {
	u16, err := windows.UTF16FromString(s)
	if err != nil {
		return 0, err
	}
	var hs uintptr
	var ptr *uint16
	if len(u16) > 0 {
		ptr = &u16[0]
	}
	hr, _, _ := procWindowsCreateString.Call(uintptr(unsafe.Pointer(ptr)), uintptr(len(u16)-1), uintptr(unsafe.Pointer(&hs)))
	if int32(hr) < 0 {
		return 0, fmt.Errorf("WindowsCreateString: 0x%08x", uint32(hr))
	}
	return hs, nil
}

func deleteHString(hs uintptr) {
	if hs != 0 {
		procWindowsDeleteString.Call(hs)
	}
}

func checkWinRTToast() bool {
	procRoInitializeCheckedOnce.Do(func() {
		if err := combase.Load(); err != nil {
			return
		}
		if err := procRoGetActivationFactory.Find(); err != nil {
			return
		}
		winrtToastAvailable = true
	})
	return winrtToastAvailable
}

// toastActivatedHandler implements ITypedEventHandler<ToastNotification*, IInspectable*>.
type toastActivatedHandler struct {
	vtbl   *toastActivatedVtbl
	refs   atomic.Int32
	id     uint64
}

type toastActivatedVtbl struct {
	queryInterface uintptr
	addRef         uintptr
	release        uintptr
	invoke         uintptr
}

var (
	toastHandlerVtbl = &toastActivatedVtbl{
		queryInterface: syscall.NewCallback(toastHandlerQI),
		addRef:         syscall.NewCallback(toastHandlerAddRef),
		release:        syscall.NewCallback(toastHandlerRelease),
		invoke:         syscall.NewCallback(toastHandlerInvoke),
	}
	toastHandlersMu sync.Mutex
	toastHandlers   = map[uint64]*toastHandlerEntry{}
	toastHandlerSeq atomic.Uint64
)

type toastHandlerEntry struct {
	handler *toastActivatedHandler
	open    func(string)
}

func toastHandlerQI(self *toastActivatedHandler, iid *windows.GUID, out *uintptr) uintptr {
	if *iid == iidIUnknown || *iid == iidIAgileObject || *iid == iidITypedEventHandlerActivated {
		*out = uintptr(unsafe.Pointer(self))
		toastHandlerAddRef(self)
		return sOK
	}
	*out = 0
	return 0x80004002 // E_NOINTERFACE
}

func toastHandlerAddRef(self *toastActivatedHandler) uintptr {
	return uintptr(self.refs.Add(1))
}

func toastHandlerRelease(self *toastActivatedHandler) uintptr {
	n := self.refs.Add(-1)
	if n <= 0 {
		toastHandlersMu.Lock()
		delete(toastHandlers, self.id)
		toastHandlersMu.Unlock()
	}
	return uintptr(n)
}

func toastHandlerInvoke(self *toastActivatedHandler, sender, args uintptr) uintptr {
	toastHandlersMu.Lock()
	entry := toastHandlers[self.id]
	toastHandlersMu.Unlock()
	if entry != nil && entry.open != nil {
		go entry.open("")
	}
	return sOK
}

// winToast shows native Windows 10/11 WinRT Toast Notifications with
// appLogoOverride + hint-crop="circle", falling back to balloon on Windows 7.
type winToast struct {
	fallback *balloon
	app      string
	dir      string
	mu       sync.Mutex
	iconName string
	iconPath string
	avatarSeq uint64
	lastAvatar string
	closed   atomic.Bool
}

func newWinToast(app string, tray Balloon) Notifier {
	fb := newBalloon(tray)
	if !checkWinRTToast() {
		return fb
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	dir := filepath.Join(cacheDir, "komarugram-go", "toasts")
	_ = os.MkdirAll(dir, 0o755)
	w := &winToast{fallback: fb, app: app, dir: dir}
	w.ensureAUMID()
	return w
}

func (w *winToast) ensureAUMID() {
	iconName := appicon.Current()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.iconPath != "" && w.iconName == iconName {
		return
	}
	iconPath := filepath.Join(w.dir, "app-"+iconName+".png")
	if err := writePNG(iconPath, appicon.Image(128)); err != nil {
		return
	}
	w.iconName = iconName
	w.iconPath = iconPath
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+toastAUMID, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	display := w.app
	if display == "" {
		display = toastAUMID
	}
	_ = k.SetStringValue("DisplayName", display)
	_ = k.SetStringValue("IconUri", iconPath)
}

func (w *winToast) writeAvatar(im image.Image) (string, error) {
	w.mu.Lock()
	w.avatarSeq++
	seq := w.avatarSeq
	prev := w.lastAvatar
	path := filepath.Join(w.dir, fmt.Sprintf("avatar-%d.png", seq))
	w.lastAvatar = path
	w.mu.Unlock()
	if err := writePNG(path, im); err != nil {
		return "", err
	}
	if prev != "" && prev != path {
		_ = os.Remove(prev)
	}
	return path, nil
}

func writePNG(path string, im image.Image) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := png.Encode(f, im); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func (w *winToast) Show(n Notification) {
	if w.closed.Load() {
		return
	}
	w.ensureAUMID()
	avatarPath := ""
	if n.Icon != nil {
		if p, err := w.writeAvatar(n.Icon); err == nil {
			avatarPath = p
		}
	}
	if avatarPath == "" {
		w.mu.Lock()
		avatarPath = w.iconPath
		w.mu.Unlock()
	}
	if err := w.showWinRT(n, avatarPath); err != nil {
		log.Printf("notify: winrt toast failed (%v), falling back to balloon", err)
		w.fallback.Show(n)
	}
}

func (w *winToast) Clicked(token string) {
	w.fallback.Clicked(token)
}

func (w *winToast) Close() {
	w.closed.Store(true)
	w.fallback.Close()
}

func buildToastXML(title, body, imagePath string, sound bool) string {
	var b strings.Builder
	b.WriteString(`<toast><visual><binding template="ToastGeneric">`)
	if imagePath != "" {
		uri := "file:///" + strings.ReplaceAll(imagePath, `\`, `/`)
		b.WriteString(`<image placement="appLogoOverride" hint-crop="circle" src="`)
		_ = xml.EscapeText(&b, []byte(uri))
		b.WriteString(`"/>`)
	}
	b.WriteString(`<text>`)
	_ = xml.EscapeText(&b, []byte(title))
	b.WriteString(`</text>`)
	if body != "" {
		b.WriteString(`<text>`)
		_ = xml.EscapeText(&b, []byte(body))
		b.WriteString(`</text>`)
	}
	b.WriteString(`</binding></visual>`)
	if !sound {
		b.WriteString(`<audio silent="true"/>`)
	}
	b.WriteString(`</toast>`)
	return b.String()
}

func toastTag(tag string) string {
	if tag == "" {
		return ""
	}
	// Windows ToastNotification.Tag is limited to 16 characters.
	h := fnv.New64a()
	_, _ = h.Write([]byte(tag))
	return fmt.Sprintf("%016x", h.Sum64())
}

func (w *winToast) showWinRT(n Notification, imagePath string) error {
	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		hr, _, _ := procRoInitialize.Call(roInitMultithreaded)
		inited := uint32(hr) == sOK || uint32(hr) == sFalse
		if !inited && uint32(hr) != rpcEChangedMode {
			errCh <- fmt.Errorf("RoInitialize: 0x%08x", uint32(hr))
			return
		}
		if inited {
			defer procRoUninitialize.Call()
		}

		// 1. Activate XmlDocument and load toast XML.
		xmlClass, err := newHString("Windows.Data.Xml.Dom.XmlDocument")
		if err != nil {
			errCh <- err
			return
		}
		defer deleteHString(xmlClass)
		var xmlDoc *comObject
		hr, _, _ = procRoActivateInstance.Call(xmlClass, uintptr(unsafe.Pointer(&xmlDoc)))
		if int32(hr) < 0 {
			errCh <- fmt.Errorf("RoActivateInstance(XmlDocument): 0x%08x", uint32(hr))
			return
		}
		defer xmlDoc.release()

		xmlIO, err := xmlDoc.queryInterface(&iidIXmlDocumentIO)
		if err != nil {
			errCh <- err
			return
		}
		defer xmlIO.release()

		xmlStr, err := newHString(buildToastXML(n.Title, n.Body, imagePath, n.Sound))
		if err != nil {
			errCh <- err
			return
		}
		defer deleteHString(xmlStr)
		// IXmlDocumentIO::LoadXml is slot 6 (after IUnknown[0..2] + IInspectable[3..5]).
		hr, _, _ = syscall.SyscallN(xmlIO.vtbl.methods[3], uintptr(unsafe.Pointer(xmlIO)), xmlStr)
		if int32(hr) < 0 {
			errCh <- fmt.Errorf("LoadXml: 0x%08x", uint32(hr))
			return
		}

		// 2. Create ToastNotification from XmlDocument.
		toastClass, err := newHString("Windows.UI.Notifications.ToastNotification")
		if err != nil {
			errCh <- err
			return
		}
		defer deleteHString(toastClass)
		var toastFactory *comObject
		hr, _, _ = procRoGetActivationFactory.Call(toastClass, uintptr(unsafe.Pointer(&iidIToastNotificationFactory)), uintptr(unsafe.Pointer(&toastFactory)))
		if int32(hr) < 0 {
			errCh <- fmt.Errorf("RoGetActivationFactory(ToastNotification): 0x%08x", uint32(hr))
			return
		}
		defer toastFactory.release()

		var toast *comObject
		// IToastNotificationFactory::CreateToastNotification is slot 6 (methods[3]).
		hr, _, _ = syscall.SyscallN(toastFactory.vtbl.methods[3], uintptr(unsafe.Pointer(toastFactory)), uintptr(unsafe.Pointer(xmlDoc)), uintptr(unsafe.Pointer(&toast)))
		if int32(hr) < 0 {
			errCh <- fmt.Errorf("CreateToastNotification: 0x%08x", uint32(hr))
			return
		}
		defer toast.release()

		// Set Tag so newer messages in the same chat replace the previous toast.
		if tag := toastTag(n.Tag); tag != "" {
			if toast2, err := toast.queryInterface(&iidIToastNotification2); err == nil {
				if hs, err := newHString(tag); err == nil {
					// IToastNotification2::put_Tag is slot 6 (methods[3]).
					syscall.SyscallN(toast2.vtbl.methods[3], uintptr(unsafe.Pointer(toast2)), hs)
					deleteHString(hs)
				}
				toast2.release()
			}
		}

		// Attach click handler (IToastNotification::add_Activated is slot 11 -> methods[8],
		// after get_Content[6], put/get_ExpirationTime[7..8], add/remove_Dismissed[9..10]).
		if n.Open != nil {
			id := toastHandlerSeq.Add(1)
			handler := &toastActivatedHandler{vtbl: toastHandlerVtbl, id: id}
			handler.refs.Store(1)
			toastHandlersMu.Lock()
			if len(toastHandlers) > 64 {
				// Prevent unbounded growth if notifications are never clicked.
				for k := range toastHandlers {
					if k+32 < id {
						delete(toastHandlers, k)
					}
				}
			}
			toastHandlers[id] = &toastHandlerEntry{handler: handler, open: n.Open}
			toastHandlersMu.Unlock()
			var token int64
			syscall.SyscallN(toast.vtbl.methods[8], uintptr(unsafe.Pointer(toast)), uintptr(unsafe.Pointer(handler)), uintptr(unsafe.Pointer(&token)))
		}

		// 3. Get ToastNotifier for our AUMID and Show the toast.
		mgrClass, err := newHString("Windows.UI.Notifications.ToastNotificationManager")
		if err != nil {
			errCh <- err
			return
		}
		defer deleteHString(mgrClass)
		var mgr *comObject
		hr, _, _ = procRoGetActivationFactory.Call(mgrClass, uintptr(unsafe.Pointer(&iidIToastNotificationManager)), uintptr(unsafe.Pointer(&mgr)))
		if int32(hr) < 0 {
			errCh <- fmt.Errorf("RoGetActivationFactory(ToastNotificationManager): 0x%08x", uint32(hr))
			return
		}
		defer mgr.release()

		aumidHS, err := newHString(toastAUMID)
		if err != nil {
			errCh <- err
			return
		}
		defer deleteHString(aumidHS)

		var notifier *comObject
		// IToastNotificationManagerStatics::CreateToastNotifierWithId is slot 7 (methods[4]).
		hr, _, _ = syscall.SyscallN(mgr.vtbl.methods[4], uintptr(unsafe.Pointer(mgr)), aumidHS, uintptr(unsafe.Pointer(&notifier)))
		if int32(hr) < 0 {
			errCh <- fmt.Errorf("CreateToastNotifierWithId: 0x%08x", uint32(hr))
			return
		}
		defer notifier.release()

		// IToastNotifier::Show is slot 6 (methods[3]).
		hr, _, _ = syscall.SyscallN(notifier.vtbl.methods[3], uintptr(unsafe.Pointer(notifier)), uintptr(unsafe.Pointer(toast)))
		if int32(hr) < 0 {
			errCh <- fmt.Errorf("Show: 0x%08x", uint32(hr))
			return
		}
		errCh <- nil
	}()
	return <-errCh
}
