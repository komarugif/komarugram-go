// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package headless

import (
	"errors"
	"runtime"

	"gioui.org/gpu"
)

/*
#cgo CFLAGS: -Werror
#include <GL/osmesa.h>
#include <dlfcn.h>
#include <stdlib.h>

// Load dynamically, as the window driver does: Haiku's Go internal linker
// cannot link OSMesa's C++ dependencies directly.
typedef struct {
	void *lib;
	OSMesaContext ctx;
	GLboolean (*makeCurrent)(OSMesaContext, void *, GLenum, GLsizei, GLsizei);
	void (*destroy)(OSMesaContext);
	unsigned char pixel[4];
} gio_headless;

static void gio_headless_destroy(gio_headless *c) {
	if (c->ctx) c->destroy(c->ctx);
	if (c->lib) dlclose(c->lib);
	free(c);
}

static gio_headless *gio_headless_create(const char **error) {
	gio_headless *c = calloc(1, sizeof(*c));
	if (!c) {
		*error = "headless: cannot allocate OSMesa context";
		return NULL;
	}
	c->lib = dlopen("libOSMesa.so.8", RTLD_NOW | RTLD_LOCAL);
	if (!c->lib) {
		*error = "headless: cannot load libOSMesa.so.8";
		goto fail;
	}
	OSMesaContext (*create)(const int *, OSMesaContext) = dlsym(c->lib, "OSMesaCreateContextAttribs");
	c->makeCurrent = dlsym(c->lib, "OSMesaMakeCurrent");
	c->destroy = dlsym(c->lib, "OSMesaDestroyContext");
	if (!create || !c->makeCurrent || !c->destroy) {
		*error = "headless: missing OSMesa entry points";
		goto fail;
	}
	const int attribs[] = {
		OSMESA_FORMAT, OSMESA_RGBA,
		OSMESA_DEPTH_BITS, 0,
		OSMESA_STENCIL_BITS, 0,
		OSMESA_ACCUM_BITS, 0,
		OSMESA_PROFILE, OSMESA_CORE_PROFILE,
		OSMESA_CONTEXT_MAJOR_VERSION, 3,
		OSMESA_CONTEXT_MINOR_VERSION, 3,
		0,
	};
	c->ctx = create(attribs, NULL);
	if (!c->ctx) {
		*error = "headless: cannot create OSMesa OpenGL 3.3 context";
		goto fail;
	}
	return c;
fail:
	gio_headless_destroy(c);
	return NULL;
}

static int gio_headless_current(gio_headless *c) {
	// Gio renders into its own sRGB texture, not this default framebuffer.
	return c->makeCurrent(c->ctx, c->pixel, GL_UNSIGNED_BYTE, 1, 1);
}

static void gio_headless_clear(gio_headless *c) {
	c->makeCurrent(NULL, NULL, GL_UNSIGNED_BYTE, 0, 0);
}
*/
import "C"

type osmesaContext struct {
	c *C.gio_headless
}

func init() {
	newContextPrimary = func() (context, error) {
		var message *C.char
		c := C.gio_headless_create(&message)
		if c == nil {
			return nil, errors.New(C.GoString(message))
		}
		return &osmesaContext{c: c}, nil
	}
}

func (c *osmesaContext) API() gpu.API { return gpu.OpenGL{} }

func (c *osmesaContext) MakeCurrent() error {
	runtime.LockOSThread()
	if C.gio_headless_current(c.c) == 0 {
		runtime.UnlockOSThread()
		return errors.New("headless: OSMesaMakeCurrent failed")
	}
	return nil
}

func (c *osmesaContext) ReleaseCurrent() {
	C.gio_headless_clear(c.c)
	runtime.UnlockOSThread()
}

func (c *osmesaContext) Release() {
	if c.c != nil {
		C.gio_headless_destroy(c.c)
		c.c = nil
	}
}
