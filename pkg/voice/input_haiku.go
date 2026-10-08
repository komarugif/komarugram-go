// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package voice

// ffmpeg for Haiku reads no microphone: its only input device is lavfi.
// The Media Kit records it, in libvoicehaiku (haiku/), which this file
// opens with dlopen; Go's linker cannot take C++ objects into a Haiku
// program, so the library is built apart. ffmpeg still encodes the Opus.

/*
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include "haiku/voicehaiku.h"

static void *vh_lib;

#define VH_FUNCS(X) \
	X(int32_t, vh_abi, (void), ()) \
	X(void *, vh_open, (int32_t *a), (a)) \
	X(int32_t, vh_read, (void *a, float *b, int32_t c, int64_t d, int32_t *e), (a, b, c, d, e)) \
	X(void, vh_close, (void *a), (a))

#define VH_PTR(ret, name, params, args) static ret (*name##_ptr) params;
VH_FUNCS(VH_PTR)
#define VH_CALL(ret, name, params, args) static ret p##name params { return name##_ptr args; }
VH_FUNCS(VH_CALL)

static const char *vh_load(const char *path) {
	if (vh_lib != NULL)
		return NULL;
	vh_lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (vh_lib == NULL)
		return dlerror();
#define VH_SYM(ret, name, params, args) \
	if ((name##_ptr = (ret (*) params)dlsym(vh_lib, #name)) == NULL) { \
		vh_lib = NULL; \
		return "missing " #name; \
	}
	VH_FUNCS(VH_SYM)
	return NULL;
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"
)

func init() { native = recordMediaKit }

func inputs(context.Context, string) ([][]string, error) { return nil, nil }

var (
	loadOnce sync.Once
	loadErr  error
)

// load opens libvoicehaiku from $KOMARUGRAM_VOICE_LIB, from beside the
// program or from lib beside it.
func load() error {
	loadOnce.Do(func() {
		var candidates, tried []string
		if p := os.Getenv("KOMARUGRAM_VOICE_LIB"); p != "" {
			candidates = append(candidates, p)
		}
		if exe, err := os.Executable(); err == nil {
			dir := filepath.Dir(exe)
			candidates = append(candidates, filepath.Join(dir, "libvoicehaiku.so"), filepath.Join(dir, "lib", "libvoicehaiku.so"))
		}
		for _, p := range candidates {
			cp := C.CString(p)
			msg := C.vh_load(cp)
			C.free(unsafe.Pointer(cp))
			if msg != nil {
				tried = append(tried, C.GoString(msg))
				continue
			}
			if abi := C.pvh_abi(); abi != C.VH_ABI {
				loadErr = fmt.Errorf("libvoicehaiku is of interface %d, not %d", abi, C.VH_ABI)
			}
			return
		}
		loadErr = fmt.Errorf("cannot load libvoicehaiku: %s", strings.Join(tried, "; "))
	})
	return loadErr
}

// recordMediaKit records the system's audio input until r stops.
func recordMediaKit(r *Recorder) (bool, error) {
	if err := load(); err != nil {
		return false, err
	}
	var status C.int32_t
	h := C.pvh_open(&status)
	if h == nil {
		return false, fmt.Errorf("the Media Kit gives no audio input: status %#x", uint32(status))
	}
	defer C.pvh_close(h)
	buf := make([]float32, 4800)
	var resample resampler
	got := false
	for !r.isStopped() && r.ctx.Err() == nil {
		var rate C.int32_t
		n := C.pvh_read(h, (*C.float)(&buf[0]), C.int32_t(len(buf)), 100000, &rate)
		if n < 0 {
			return got, fmt.Errorf("recording: status %#x", uint32(n))
		}
		if n == 0 {
			continue
		}
		samples := resample.to16(buf[:n], int(rate))
		got = got || len(samples) > 0
		r.add(samples)
	}
	if !got {
		return false, errors.New("the audio input gave no sound")
	}
	return true, nil
}
