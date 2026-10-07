// SPDX-License-Identifier: Unlicense OR MIT

// The C interface of libgiohaiku, the part of Gio's Haiku driver written
// against the Be API in C++ (giohaiku.cpp). Gio loads it with dlopen
// (app/os_haiku.go), so nothing in it is needed to build a program.
//
// Every window runs in its own BWindow thread; what happens to it is kept
// in a queue that gh_window_next_event reads. The GL context of a window
// is an OSMesa one, made current on the caller's thread by gh_gl_lock; the
// GL functions are libOSMesa's. gh_gl_swap hands the frame to the window.

#ifndef GIOHAIKU_H
#define GIOHAIKU_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// GH_ABI changes whenever this interface does; gh_abi returns it, and Gio
// refuses a library of another version.
#define GH_ABI 2

enum {
	GH_EV_NONE = 0,
	GH_EV_WAKE,         // gh_window_wake was called
	GH_EV_CLOSE,        // the user asked the window to close
	GH_EV_RESIZE,       // x, y: the new size of the content, in pixels
	GH_EV_REDRAW,       // the content must be drawn again
	GH_EV_FOCUS,        // x: 1 when the window became active, 0 when not
	GH_EV_MINIMIZE,     // x: 1 when minimized, 0 when restored
	GH_EV_ZOOM,         // x: 1 when zoomed (maximized), 0 when not
	GH_EV_MOUSE_DOWN,   // fx, fy, buttons, modifiers, clicks
	GH_EV_MOUSE_UP,     // fx, fy, buttons (still pressed), modifiers
	GH_EV_MOUSE_MOVE,   // fx, fy, buttons, modifiers
	GH_EV_MOUSE_EXIT,   // the pointer left the content
	GH_EV_WHEEL,        // fx, fy: the wheel's delta, in lines
	GH_EV_KEY_DOWN,     // key, raw_char, modifiers, text (UTF-8), x: 1 on repeat
	GH_EV_KEY_UP,       // key, raw_char, modifiers, text
	GH_EV_MODIFIERS,    // modifiers changed
	GH_EV_INPUT_METHOD, // text: the composed string; x: 1 when confirmed
};

typedef struct {
	int32_t type;
	int32_t x, y;
	float fx, fy;
	uint32_t buttons;
	uint32_t modifiers;
	int32_t key;
	int32_t raw_char;
	int32_t clicks;
	int64_t when; // system_time(), in microseconds
	char text[64];
} gh_event;

enum {
	GH_CURSOR_DEFAULT = 0,
	GH_CURSOR_NONE,
	GH_CURSOR_TEXT,
	GH_CURSOR_VERTICAL_TEXT,
	GH_CURSOR_POINTER,
	GH_CURSOR_CROSSHAIR,
	GH_CURSOR_ALL_SCROLL,
	GH_CURSOR_COL_RESIZE,
	GH_CURSOR_ROW_RESIZE,
	GH_CURSOR_GRAB,
	GH_CURSOR_GRABBING,
	GH_CURSOR_NOT_ALLOWED,
	GH_CURSOR_WAIT,
	GH_CURSOR_PROGRESS,
	GH_CURSOR_NW_RESIZE,
	GH_CURSOR_NE_RESIZE,
	GH_CURSOR_SW_RESIZE,
	GH_CURSOR_SE_RESIZE,
	GH_CURSOR_NS_RESIZE,
	GH_CURSOR_EW_RESIZE,
	GH_CURSOR_W_RESIZE,
	GH_CURSOR_E_RESIZE,
	GH_CURSOR_N_RESIZE,
	GH_CURSOR_S_RESIZE,
	GH_CURSOR_NESW_RESIZE,
	GH_CURSOR_NWSE_RESIZE,
};

enum {
	GH_MODE_WINDOWED = 0,
	GH_MODE_MINIMIZED,
	GH_MODE_MAXIMIZED,
	GH_MODE_FULLSCREEN,
};

int32_t gh_abi(void);

// gh_init starts the BApplication, with the MIME signature given, in a
// thread of its own. It may be called again; only the first call counts.
// It returns 0, or a negative status_t.
int32_t gh_init(const char *signature);

// gh_ui_scale is the user's scale for the interface: the size of the
// plain font over 12.
float gh_ui_scale(void);

void *gh_window_create(int32_t width, int32_t height, const char *title, int32_t decorated);
// gh_window_destroy closes the window and frees it; the handle is invalid after.
void gh_window_destroy(void *w);

// gh_window_next_event waits up to timeout microseconds (negative: forever)
// for an event, and returns 1 with *ev set, or 0.
int32_t gh_window_next_event(void *w, gh_event *ev, int64_t timeout);
void gh_window_wake(void *w);

void gh_window_size(void *w, int32_t *width, int32_t *height);
void gh_window_set_title(void *w, const char *title);
void gh_window_set_size(void *w, int32_t width, int32_t height);
void gh_window_set_limits(void *w, int32_t minw, int32_t minh, int32_t maxw, int32_t maxh);
void gh_window_set_decorated(void *w, int32_t decorated);
void gh_window_set_mode(void *w, int32_t mode);
void gh_window_center(void *w);
void gh_window_raise(void *w);
void gh_window_show(void *w);
void gh_window_set_cursor(void *w, int32_t cursor);

int32_t gh_gl_lock(void *w, int32_t width, int32_t height);
void gh_gl_unlock(void *w);
void gh_gl_swap(void *w);

// gh_clipboard_write replaces the clipboard with data of the MIME type mime.
int32_t gh_clipboard_write(const char *mime, const void *data, int32_t len);
// gh_clipboard_read returns the clipboard's data of the MIME type mime, in
// memory gh_free frees, or NULL.
void *gh_clipboard_read(const char *mime, int32_t *len);
void gh_free(void *p);

#ifdef __cplusplus
}
#endif

#endif
