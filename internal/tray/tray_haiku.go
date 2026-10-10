// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package tray

// The icon is an item of the Deskbar: libtrayhaiku (haiku/), an add-on the
// Deskbar loads into its own process. The client loads it too, to add and
// remove the item, and makes the port the item writes clicks and choices
// to and asks its menu on (haiku/trayhaiku.h). Go's linker cannot take C++
// objects into a Haiku program, so the library is built apart.

/*
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <OS.h>
#include "haiku/trayhaiku.h"

static void *th_lib;

#define TH_FUNCS(X) \
	X(int32_t, th_abi, (void), ()) \
	X(int32_t, th_add, (const char *a), (a)) \
	X(void, th_remove, (void), ()) \
	X(int32_t, th_shown, (void), ())

#define TH_PTR(ret, name, params, args) static ret (*name##_ptr) params;
TH_FUNCS(TH_PTR)
#define TH_CALL(ret, name, params, args) static ret p##name params { return name##_ptr args; }
TH_FUNCS(TH_CALL)

static const char *th_load(const char *path) {
	th_lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (th_lib == NULL)
		return dlerror();
#define TH_SYM(ret, name, params, args) \
	if ((name##_ptr = (ret (*) params)dlsym(th_lib, #name)) == NULL) \
		return "missing " #name;
	TH_FUNCS(TH_SYM)
	return NULL;
}
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"
)

// Tray is the client's item in the Deskbar.
type Tray struct {
	opts Options
	port C.port_id
	done chan struct{}
	once sync.Once
}

// Start makes the port and adds the item. Options.ID is not used: the item
// and the port have the names of trayhaiku.h, the client's.
func Start(opts Options) (*Tray, error) {
	path, err := load()
	if err != nil {
		return nil, err
	}
	name := C.CString(C.TH_PORT)
	defer C.free(unsafe.Pointer(name))
	port := C.create_port(16, name)
	if port < 0 {
		return nil, fmt.Errorf("tray: create_port: %d", port)
	}
	t := &Tray{opts: opts, port: port, done: make(chan struct{})}
	go t.serve()
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	if st := C.pth_add(cpath); st != 0 {
		t.Close()
		return nil, fmt.Errorf("tray: the Deskbar did not take the item: %d", st)
	}
	return t, nil
}

// load opens libtrayhaiku from $KOMARUGRAM_TRAY_LIB, from beside the
// program or from lib beside it, and returns its path, which the Deskbar
// loads it from.
func load() (string, error) {
	var candidates, tried []string
	if p := os.Getenv("KOMARUGRAM_TRAY_LIB"); p != "" {
		candidates = append(candidates, p)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "libtrayhaiku.so"), filepath.Join(dir, "lib", "libtrayhaiku.so"))
	}
	for _, p := range candidates {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		cp := C.CString(abs)
		msg := C.th_load(cp)
		C.free(unsafe.Pointer(cp))
		if msg != nil {
			tried = append(tried, C.GoString(msg))
			continue
		}
		if abi := C.pth_abi(); abi != C.TH_ABI {
			return "", fmt.Errorf("tray: libtrayhaiku is of interface %d, not %d", abi, C.TH_ABI)
		}
		return abs, nil
	}
	return "", fmt.Errorf("tray: cannot load libtrayhaiku: %s", strings.Join(tried, "; "))
}

// serve answers the item until the port is deleted.
func (t *Tray) serve() {
	defer close(t.done)
	buf := make([]byte, 64)
	for {
		size := C.port_buffer_size(t.port)
		if size < 0 {
			return
		}
		if int(size) > len(buf) {
			buf = make([]byte, size)
		}
		var code C.int32
		n := C.read_port(t.port, &code, unsafe.Pointer(&buf[0]), C.size_t(len(buf)))
		if n < 0 {
			return
		}
		data := buf[:n]
		switch code {
		case C.TH_ACTIVATE:
			if t.opts.Activate != nil {
				go t.opts.Activate("")
			}
		case C.TH_MENU:
			if len(data) == 4 {
				t.answer(C.port_id(int32(binary.LittleEndian.Uint32(data))))
			}
		case C.TH_ITEM:
			if len(data) == 4 {
				i := int(int32(binary.LittleEndian.Uint32(data)))
				if i >= 0 && i < len(t.opts.Items) && t.opts.Items[i].Action != nil {
					go t.opts.Items[i].Action("")
				}
			}
		}
	}
}

// answer writes the tooltip and the menu's labels to the item's port.
func (t *Tray) answer(reply C.port_id) {
	var b []byte
	b = append(append(b, t.opts.Title...), 0)
	for _, it := range t.opts.Items {
		if !it.Separator {
			b = append(b, it.Label...)
		}
		b = append(b, 0)
	}
	// The item waits a second for it; it does not wait more.
	C.write_port_etc(reply, C.TH_MENU, unsafe.Pointer(&b[0]), C.size_t(len(b)), C.B_RELATIVE_TIMEOUT, 1000000)
}

// Available reports whether the Deskbar shows the item, so that closing
// the last window may leave the client running in it. A Deskbar that
// restarted lost it: it is added again.
func (t *Tray) Available() bool {
	if C.pth_shown() != 0 {
		return true
	}
	path, err := load()
	if err != nil {
		return false
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	return C.pth_add(cpath) == 0 && C.pth_shown() != 0
}

// Close removes the item and the port.
func (t *Tray) Close() {
	t.once.Do(func() {
		C.pth_remove()
		C.delete_port(t.port)
		<-t.done
	})
}

func (*Tray) Notify(string, string, bool) error { return ErrUnsupported }
