// SPDX-License-Identifier: Unlicense OR MIT

// libgiohaiku: windows, input and OpenGL for Gio on the Be API. The C
// interface is in giohaiku.h; build.go builds the library.

#include <AppFileInfo.h>
#include <Application.h>
#include <Clipboard.h>
#include <Deskbar.h>
#include <File.h>
#include <Cursor.h>
#include <Font.h>
#include <Bitmap.h>
#include <GL/gl.h>
#include <GL/osmesa.h>
#include <Input.h>
#include <Locker.h>
#include <Message.h>
#include <Path.h>
#include <Entry.h>
#include <OS.h>
#include <Screen.h>
#include <View.h>
#include <image.h>
#include <Window.h>

#include <deque>
#include <string>
#include <fcntl.h>
#include <stdio.h>
#include <unistd.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

#include "giohaiku.h"

namespace {

// Queue is a window's events, read by Gio's goroutine of the window.
class Queue {
public:
	Queue() : fSem(create_sem(0, "gio events")) {}
	~Queue() { delete_sem(fSem); }

	void Push(const gh_event &ev) {
		fLock.Lock();
		// A move or a resize replaces one not read yet: a slow reader gets
		// the last state, not a backlog.
		if ((ev.type == GH_EV_MOUSE_MOVE || ev.type == GH_EV_RESIZE || ev.type == GH_EV_WAKE || ev.type == GH_EV_REDRAW)
			&& !fEvents.empty() && fEvents.back().type == ev.type) {
			fEvents.back() = ev;
			fLock.Unlock();
			return;
		}
		fEvents.push_back(ev);
		fLock.Unlock();
		release_sem_etc(fSem, 1, B_DO_NOT_RESCHEDULE);
	}

	bool Pop(gh_event *ev, bigtime_t timeout) {
		for (;;) {
			fLock.Lock();
			if (!fEvents.empty()) {
				*ev = fEvents.front();
				fEvents.pop_front();
				fLock.Unlock();
				// The semaphore may count events merged away; it is only a
				// wakeup, so the extra counts end in an empty check.
				return true;
			}
			fLock.Unlock();
			status_t err;
			if (timeout < 0)
				err = acquire_sem(fSem);
			else
				err = acquire_sem_etc(fSem, 1, B_RELATIVE_TIMEOUT, timeout);
			if (err == B_TIMED_OUT || err == B_WOULD_BLOCK)
				return false;
			if (err == B_INTERRUPTED)
				continue;
			if (err != B_OK)
				return false;
			// Drain the counts left by merged events.
			int32 count = 0;
			if (get_sem_count(fSem, &count) == B_OK && count > 0)
				acquire_sem_etc(fSem, count, B_RELATIVE_TIMEOUT, 0);
		}
	}

private:
	BLocker fLock;
	std::deque<gh_event> fEvents;
	sem_id fSem;
};

gh_event NewEvent(int32 type) {
	gh_event ev;
	memset(&ev, 0, sizeof(ev));
	ev.type = type;
	ev.when = system_time();
	return ev;
}

class GioWindow;

// ShownFrame is the last frame drawn by Gio, which the window's thread shows.
// Gio's thread fills it in gh_gl_swap.
struct ShownFrame {
	BLocker lock;
	BBitmap *bitmap = NULL;
	// pending is set while a kShowFrame message waits for the window: one
	// is enough, as it shows the last frame. A message for every frame, as
	// it was, piled up in the window's queue when frames came faster than
	// the window drew them, and input waited behind them for seconds.
	int32 pending = 0;
};

// The message that tells a window its frame is ready.
const uint32 kShowFrame = 'gfrm';

// GioView is the content of a window: where the frames are shown, and where
// input arrives. Gio draws with OSMesa in its own thread (gh_gl_*); the
// window's thread only copies finished frames to the screen, so it is never
// held up by drawing and input keeps coming.
class GioView : public BView {
public:
	GioView(BRect frame, Queue *queue, ShownFrame *shown)
		: BView(frame, "gio", B_FOLLOW_ALL_SIDES, B_WILL_DRAW | B_FRAME_EVENTS | B_NAVIGABLE),
		  fQueue(queue), fFrame(shown), fButtons(0), fCursor(GH_CURSOR_DEFAULT) {
		SetViewColor(B_TRANSPARENT_COLOR);
	}

	void AttachedToWindow() override {
		BView::AttachedToWindow();
		MakeFocus(true);
	}

	void Draw(BRect updateRect) override {
		bool shown = ShowFrame();
		if (!shown)
			fQueue->Push(NewEvent(GH_EV_REDRAW));
	}

	// ShowFrame draws the last frame; it reports whether there was one of
	// the view's size.
	bool ShowFrame() {
		BRect b = Bounds();
		bool fits = false;
		fFrame->lock.Lock();
		if (fFrame->bitmap != NULL) {
			DrawBitmap(fFrame->bitmap, B_ORIGIN);
			BRect fb = fFrame->bitmap->Bounds();
			fits = fb.Width() >= b.Width() && fb.Height() >= b.Height();
		}
		fFrame->lock.Unlock();
		return fits;
	}

	void FrameResized(float width, float height) override {
		BView::FrameResized(width, height);
		gh_event ev = NewEvent(GH_EV_RESIZE);
		ev.x = (int32)width + 1;
		ev.y = (int32)height + 1;
		fQueue->Push(ev);
	}

	void MouseDown(BPoint where) override {
		// A window that takes the first click (B_WILL_ACCEPT_FIRST_CLICK)
		// is not activated by the app_server on a click: it is the
		// window's to do, or it stays behind and without the keyboard.
		if (!Window()->IsActive())
			Window()->Activate();
		MakeFocus(true);
		SetMouseEventMask(B_POINTER_EVENTS, B_NO_POINTER_HISTORY);
		gh_event ev = PointerEvent(GH_EV_MOUSE_DOWN, where);
		int32 clicks = 1;
		if (BMessage *msg = Window()->CurrentMessage())
			msg->FindInt32("clicks", &clicks);
		ev.clicks = clicks;
		fQueue->Push(ev);
	}

	void MouseUp(BPoint where) override {
		fQueue->Push(PointerEvent(GH_EV_MOUSE_UP, where));
	}

	void MouseMoved(BPoint where, uint32 transit, const BMessage *drag) override {
		if (drag != NULL && drag->HasRef("refs")) {
			DragFiles(where, transit, drag);
			return;
		}
		if (transit == B_EXITED_VIEW && fButtons == 0) {
			fQueue->Push(NewEvent(GH_EV_MOUSE_EXIT));
			return;
		}
		if (transit == B_ENTERED_VIEW)
			ApplyCursor();
		fQueue->Push(PointerEvent(GH_EV_MOUSE_MOVE, where));
	}

	void KeyDown(const char *bytes, int32 numBytes) override {
		KeyEvent(GH_EV_KEY_DOWN, bytes, numBytes);
	}

	void KeyUp(const char *bytes, int32 numBytes) override {
		KeyEvent(GH_EV_KEY_UP, bytes, numBytes);
	}

	void MessageReceived(BMessage *msg) override {
		if (msg->WasDropped() && msg->HasRef("refs")) {
			// Files let go over the view, as Tracker drops them.
			KeepPaths(msg);
			fDragging = false;
			gh_event ev = NewEvent(GH_EV_DROP);
			ev.x = GH_DROP_DROP;
			BPoint where = ConvertFromScreen(msg->DropPoint());
			ev.fx = where.x;
			ev.fy = where.y;
			fQueue->Push(ev);
			return;
		}
		switch (msg->what) {
		case B_MOUSE_WHEEL_CHANGED: {
			gh_event ev = NewEvent(GH_EV_WHEEL);
			msg->FindFloat("be:wheel_delta_x", &ev.fx);
			msg->FindFloat("be:wheel_delta_y", &ev.fy);
			ev.modifiers = modifiers();
			fQueue->Push(ev);
			return;
		}
		case B_MODIFIERS_CHANGED: {
			gh_event ev = NewEvent(GH_EV_MODIFIERS);
			int32 mods = 0;
			msg->FindInt32("modifiers", &mods);
			ev.modifiers = (uint32)mods;
			fQueue->Push(ev);
			return;
		}
		case B_INPUT_METHOD_EVENT: {
			int32 opcode = 0;
			msg->FindInt32("be:opcode", &opcode);
			if (opcode == B_INPUT_METHOD_CHANGED) {
				const char *s = NULL;
				bool confirmed = false;
				msg->FindString("be:string", &s);
				msg->FindBool("be:confirmed", &confirmed);
				gh_event ev = NewEvent(GH_EV_INPUT_METHOD);
				if (s != NULL)
					strlcpy(ev.text, s, sizeof(ev.text));
				ev.x = confirmed ? 1 : 0;
				fQueue->Push(ev);
			}
			return;
		}
		}
		BView::MessageReceived(msg);
	}

	// DropPaths copies the files of the last drag, each ended by a NUL.
	void *DropPaths(int32_t *len) {
		fPathsLock.Lock();
		void *out = NULL;
		*len = 0;
		if (!fPaths.empty() && (out = malloc(fPaths.size())) != NULL) {
			memcpy(out, fPaths.data(), fPaths.size());
			*len = (int32_t)fPaths.size();
		}
		fPathsLock.Unlock();
		return out;
	}

	void SetCursorShape(int32 cursor) {
		fCursor = cursor;
		ApplyCursor();
	}

private:
	gh_event PointerEvent(int32 type, BPoint where) {
		gh_event ev = NewEvent(type);
		ev.fx = where.x;
		ev.fy = where.y;
		int32 buttons = 0;
		int32 mods = (int32)modifiers();
		if (BMessage *msg = Window()->CurrentMessage()) {
			msg->FindInt32("buttons", &buttons);
			msg->FindInt32("modifiers", &mods);
			int64 when;
			if (msg->FindInt64("when", &when) == B_OK)
				ev.when = when;
		}
		fButtons = (uint32)buttons;
		ev.buttons = fButtons;
		ev.modifiers = (uint32)mods;
		return ev;
	}

	void KeyEvent(int32 type, const char *bytes, int32 numBytes) {
		gh_event ev = NewEvent(type);
		BMessage *msg = Window()->CurrentMessage();
		if (msg != NULL) {
			int32 v = 0;
			if (msg->FindInt32("key", &v) == B_OK)
				ev.key = v;
			if (msg->FindInt32("raw_char", &v) == B_OK)
				ev.raw_char = v;
			if (msg->FindInt32("modifiers", &v) == B_OK)
				ev.modifiers = (uint32)v;
			if (msg->FindInt32("be:key_repeat", &v) == B_OK && v > 0)
				ev.x = 1;
			int64 when;
			if (msg->FindInt64("when", &when) == B_OK)
				ev.when = when;
		}
		if (numBytes > (int32)sizeof(ev.text) - 1)
			numBytes = sizeof(ev.text) - 1;
		memcpy(ev.text, bytes, numBytes);
		ev.text[numBytes] = 0;
		fQueue->Push(ev);
	}

	// DragFiles tells of files dragged over the view, instead of the
	// pointer's moves. A drag that starts over the view comes in with
	// B_INSIDE_VIEW, not B_ENTERED_VIEW.
	void DragFiles(BPoint where, uint32 transit, const BMessage *drag) {
		int32 stage = GH_DROP_MOVE;
		if (transit == B_EXITED_VIEW || transit == B_OUTSIDE_VIEW) {
			if (!fDragging)
				return;
			fDragging = false;
			stage = GH_DROP_LEAVE;
		} else if (!fDragging) {
			KeepPaths(drag);
			fDragging = true;
			stage = GH_DROP_ENTER;
		}
		gh_event ev = NewEvent(GH_EV_DROP);
		ev.x = stage;
		ev.fx = where.x;
		ev.fy = where.y;
		fQueue->Push(ev);
	}

	// KeepPaths keeps the paths of the files of msg for DropPaths.
	void KeepPaths(const BMessage *msg) {
		std::string paths;
		entry_ref ref;
		for (int32 i = 0; msg->FindRef("refs", i, &ref) == B_OK; i++) {
			BPath path(&ref);
			if (path.InitCheck() != B_OK)
				continue;
			paths.append(path.Path());
			paths.push_back('\0');
		}
		fPathsLock.Lock();
		fPaths = paths;
		fPathsLock.Unlock();
	}

	void ApplyCursor() {
		BCursorID id;
		switch (fCursor) {
		case GH_CURSOR_NONE: id = B_CURSOR_ID_NO_CURSOR; break;
		case GH_CURSOR_TEXT: id = B_CURSOR_ID_I_BEAM; break;
		case GH_CURSOR_VERTICAL_TEXT: id = B_CURSOR_ID_I_BEAM_HORIZONTAL; break;
		case GH_CURSOR_POINTER: id = B_CURSOR_ID_FOLLOW_LINK; break;
		case GH_CURSOR_CROSSHAIR: id = B_CURSOR_ID_CROSS_HAIR; break;
		case GH_CURSOR_ALL_SCROLL: id = B_CURSOR_ID_MOVE; break;
		case GH_CURSOR_COL_RESIZE: id = B_CURSOR_ID_RESIZE_EAST_WEST; break;
		case GH_CURSOR_ROW_RESIZE: id = B_CURSOR_ID_RESIZE_NORTH_SOUTH; break;
		case GH_CURSOR_GRAB: id = B_CURSOR_ID_GRAB; break;
		case GH_CURSOR_GRABBING: id = B_CURSOR_ID_GRABBING; break;
		case GH_CURSOR_NOT_ALLOWED: id = B_CURSOR_ID_NOT_ALLOWED; break;
		case GH_CURSOR_WAIT: id = B_CURSOR_ID_PROGRESS; break;
		case GH_CURSOR_PROGRESS: id = B_CURSOR_ID_PROGRESS; break;
		case GH_CURSOR_NW_RESIZE: id = B_CURSOR_ID_RESIZE_NORTH_WEST; break;
		case GH_CURSOR_NE_RESIZE: id = B_CURSOR_ID_RESIZE_NORTH_EAST; break;
		case GH_CURSOR_SW_RESIZE: id = B_CURSOR_ID_RESIZE_SOUTH_WEST; break;
		case GH_CURSOR_SE_RESIZE: id = B_CURSOR_ID_RESIZE_SOUTH_EAST; break;
		case GH_CURSOR_NS_RESIZE: id = B_CURSOR_ID_RESIZE_NORTH_SOUTH; break;
		case GH_CURSOR_EW_RESIZE: id = B_CURSOR_ID_RESIZE_EAST_WEST; break;
		case GH_CURSOR_W_RESIZE: id = B_CURSOR_ID_RESIZE_WEST; break;
		case GH_CURSOR_E_RESIZE: id = B_CURSOR_ID_RESIZE_EAST; break;
		case GH_CURSOR_N_RESIZE: id = B_CURSOR_ID_RESIZE_NORTH; break;
		case GH_CURSOR_S_RESIZE: id = B_CURSOR_ID_RESIZE_SOUTH; break;
		case GH_CURSOR_NESW_RESIZE: id = B_CURSOR_ID_RESIZE_NORTH_EAST_SOUTH_WEST; break;
		case GH_CURSOR_NWSE_RESIZE: id = B_CURSOR_ID_RESIZE_NORTH_WEST_SOUTH_EAST; break;
		default: id = B_CURSOR_ID_SYSTEM_DEFAULT; break;
		}
		BCursor cursor(id);
		SetViewCursor(&cursor);
	}

	Queue *fQueue;
	ShownFrame *fFrame;
	uint32 fButtons;
	int32 fCursor;
	// fDragging is set while files are dragged over the view; fPaths are
	// theirs, under fPathsLock, as Gio's thread reads them.
	bool fDragging = false;
	BLocker fPathsLock;
	std::string fPaths;
};

class GioWindow : public BWindow {
public:
	GioWindow(BRect frame, const char *title, bool decorated)
		: BWindow(frame, title, decorated ? B_TITLED_WINDOW_LOOK : B_NO_BORDER_WINDOW_LOOK, B_NORMAL_WINDOW_FEEL,
			// The first click on an inactive window reaches Gio too, as on
			// other systems; without the flag it only activated the window.
			B_ASYNCHRONOUS_CONTROLS | B_WILL_ACCEPT_FIRST_CLICK),
		  fZoomed(false) {
		// Gio takes these keys itself: copy, paste and the rest are its
		// editors' shortcuts, and closing is the program's to decide.
		RemoveShortcut('W', B_COMMAND_KEY);
		RemoveShortcut('X', B_COMMAND_KEY);
		RemoveShortcut('C', B_COMMAND_KEY);
		RemoveShortcut('V', B_COMMAND_KEY);
		RemoveShortcut('A', B_COMMAND_KEY);
		fView = new GioView(Bounds(), &fQueue, &fFrame);
		AddChild(fView);
	}

	~GioWindow() {
		if (fContext != NULL)
			OSMesaDestroyContext(fContext);
		free(fBuffer);
		delete fFrame.bitmap;
	}

	void MessageReceived(BMessage *msg) override {
		if (msg->what == kShowFrame) {
			atomic_set(&fFrame.pending, 0);
			fView->ShowFrame();
			return;
		}
		BWindow::MessageReceived(msg);
	}

	bool QuitRequested() override {
		if (atomic_get(&fClosing) != 0)
			return true;
		fQueue.Push(NewEvent(GH_EV_CLOSE));
		return false;
	}

	void WindowActivated(bool active) override {
		BWindow::WindowActivated(active);
		gh_event ev = NewEvent(GH_EV_FOCUS);
		ev.x = active ? 1 : 0;
		fQueue.Push(ev);
	}

	void Minimize(bool minimize) override {
		BWindow::Minimize(minimize);
		gh_event ev = NewEvent(GH_EV_MINIMIZE);
		ev.x = minimize ? 1 : 0;
		fQueue.Push(ev);
	}

	void Zoom(BPoint origin, float width, float height) override {
		if (fZoomed) {
			MoveTo(fRestore.LeftTop());
			ResizeTo(fRestore.Width(), fRestore.Height());
			fZoomed = false;
		} else {
			fRestore = Frame();
			BWindow::Zoom(origin, width, height);
			fZoomed = true;
		}
		gh_event ev = NewEvent(GH_EV_ZOOM);
		ev.x = fZoomed ? 1 : 0;
		fQueue.Push(ev);
	}

	Queue fQueue;
	ShownFrame fFrame;
	GioView *fView;
	// The OSMesa context Gio draws with, and the buffer it draws into.
	OSMesaContext fContext = NULL;
	void *fBuffer = NULL;
	int32 fWidth = 0, fHeight = 0;
	// fClosing is set by gh_window_destroy, from another thread.
	int32 fClosing = 0;
	// fInjector is the input of GIO_HAIKU_INPUT, if any (see Injector).
	struct Injector *fInjector = NULL;
	bool fZoomed;
	BRect fRestore;
	bool fFullscreen = false;
	BRect fWindowed;
};

// The arguments of launches by message (GioApp::ArgvReceived).
BLocker gLaunchArgsLock;
std::string gLaunchArgs;

class GioApp : public BApplication {
public:
	GioApp(const char *signature, sem_id ready)
		: BApplication(signature), fReady(ready) {}

	void ReadyToRun() override { release_sem(fReady); }

	// ArgvReceived keeps the arguments of a launch by message, which come
	// before ReadyToRun, for gh_launch_args.
	void ArgvReceived(int32 argc, char **argv) override {
		std::string args;
		for (int32 i = 1; i < argc; i++) {
			args.append(argv[i]);
			args.push_back('\0');
		}
		gLaunchArgsLock.Lock();
		gLaunchArgs.append(args);
		gLaunchArgsLock.Unlock();
	}

	// Command+Q and the Deskbar's Quit ask every window, as the user
	// closing it would.
	bool QuitRequested() override {
		for (int32 i = 0; BWindow *w = WindowAt(i); i++) {
			if (GioWindow *gw = dynamic_cast<GioWindow *>(w))
				gw->fQueue.Push(NewEvent(GH_EV_CLOSE));
		}
		return false;
	}

private:
	sem_id fReady;
};

struct AppStart {
	const char *signature;
	sem_id ready;
};

status_t gInitStatus = B_NO_INIT;
pthread_once_t gInitOnce = PTHREAD_ONCE_INIT;
const char *gSignature;

int32 RunApp(void *data) {
	AppStart *start = (AppStart *)data;
	GioApp *app = new GioApp(start->signature, start->ready);
	app->Run();
	return 0;
}

void InitOnce() {
	sem_id ready = create_sem(0, "gio app ready");
	AppStart *start = new AppStart{gSignature, ready};
	thread_id t = spawn_thread(RunApp, "gio app", B_NORMAL_PRIORITY, start);
	if (t < 0) {
		gInitStatus = t;
		return;
	}
	resume_thread(t);
	gInitStatus = acquire_sem(ready);
	delete_sem(ready);
}

GioWindow *Win(void *w) { return (GioWindow *)w; }

// Usable is the part of the window's screen a window may take: the screen
// without the Deskbar when it lies across the top or the bottom.
BRect Usable(BWindow *win) {
	BRect screen = BScreen(win).Frame();
	BDeskbar deskbar;
	BRect bar = deskbar.Frame();
	switch (deskbar.Location()) {
	case B_DESKBAR_TOP:
		screen.top = bar.bottom + 1;
		break;
	case B_DESKBAR_BOTTOM:
		screen.bottom = bar.top - 1;
		break;
	default:
		// At a side, the Deskbar takes a corner only; windows go over it.
		break;
	}
	return screen;
}

// Fit sizes the window's content to width and height, kept so the window
// with its frame fits the usable screen, and centers it there. It reports
// whether the size was cut.
bool Fit(BWindow *win, int32 width, int32 height, bool center) {
	BRect usable = Usable(win);
	BRect frame = win->Frame();
	BRect deco = win->DecoratorFrame();
	float left = frame.left - deco.left, top = frame.top - deco.top;
	float right = deco.right - frame.right, bottom = deco.bottom - frame.bottom;
	int32 maxW = (int32)(usable.Width() + 1 - left - right);
	int32 maxH = (int32)(usable.Height() + 1 - top - bottom);
	bool cut = false;
	if (width > maxW) {
		width = maxW;
		cut = true;
	}
	if (height > maxH) {
		height = maxH;
		cut = true;
	}
	win->ResizeTo(width - 1, height - 1);
	if (center) {
		float x = usable.left + left + (usable.Width() + 1 - left - right - width) / 2;
		float y = usable.top + top + (usable.Height() + 1 - top - bottom - height) / 2;
		win->MoveTo((int32)x, (int32)y);
	}
	return cut;
}

// Locked runs f with the window locked, if it still is.
template <typename F> void Locked(void *w, F f) {
	GioWindow *win = Win(w);
	if (win->LockLooper()) {
		f(win);
		win->UnlockLooper();
	}
}


// Input from a file, for testing without a screen to click on: with
// GIO_HAIKU_INPUT naming a file, a thread of each window looks for it ten
// times a second, runs its commands, one a line, and deletes it. A command
// posts the window the messages the app_server would, through BWindow's own
// handling. Coordinates are the content's, in pixels. (A FIFO was tried
// first; opening one on BFS failed and ended the thread.)
//
//	move X Y | down X Y [BUTTONS] | up X Y | click X Y | wheel DX DY
//	key TEXT  (UTF-8 typed as is; "\b" backspace, "\n" return)
//	raw CODE MODIFIERS [TEXT]  (one key by its code, with Haiku's modifiers:
//	                            "raw 0x4f 0x2 v" is Command+V)
struct Injector {
	GioWindow *win;
	char path[B_PATH_NAME_LENGTH];
	// stop is set, under lock, when the window goes; commands run under
	// lock, so none touches a window that is gone. The Injector outlives
	// the window, and its thread frees it.
	BLocker lock;
	bool stop = false;
};

void PostMouse(GioWindow *win, uint32 what, float x, float y, int32 buttons, int32 clicks) {
	if (!win->LockLooper())
		return;
	BPoint where = win->fView->ConvertToScreen(BPoint(x, y));
	win->UnlockLooper();
	BMessage msg(what);
	msg.AddInt64("when", system_time());
	msg.AddPoint("screen_where", where);
	msg.AddInt32("buttons", buttons);
	msg.AddInt32("modifiers", 0);
	if (what == B_MOUSE_DOWN)
		msg.AddInt32("clicks", clicks);
	if (what == B_MOUSE_MOVED)
		msg.AddInt32("be:transit", B_INSIDE_VIEW);
	win->PostMessage(&msg, win->fView);
}

void PostKey(GioWindow *win, const char *bytes, int32 raw, int32 modifiers = 0) {
	for (uint32 what : {(uint32)B_KEY_DOWN, (uint32)B_KEY_UP}) {
		BMessage msg(what);
		msg.AddInt64("when", system_time());
		msg.AddInt32("modifiers", modifiers);
		msg.AddInt32("key", raw);
		msg.AddInt32("raw_char", (uint8)bytes[0]);
		msg.AddString("bytes", bytes);
		for (int32 i = 0; bytes[i] != 0; i++)
			msg.AddInt8("byte", bytes[i]);
		win->PostMessage(&msg, win->fView);
	}
}

int32 RunInjector(void *data) {
	Injector *in = (Injector *)data;
	for (;;) {
		snooze(100000);
		in->lock.Lock();
		bool stop = in->stop;
		in->lock.Unlock();
		if (stop) {
			delete in;
			return 0;
		}
		char taken[B_PATH_NAME_LENGTH + 8];
		snprintf(taken, sizeof(taken), "%s.run", in->path);
		// Renamed first, so a writer's next file is not lost to the delete.
		if (rename(in->path, taken) != 0)
			continue;
		FILE *f = fopen(taken, "r");
		if (f == NULL)
			continue;
		in->lock.Lock();
		if (in->stop) {
			in->lock.Unlock();
			fclose(f);
			delete in;
			return 0;
		}
		char line[256];
		while (fgets(line, sizeof(line), f) != NULL) {
			float x = 0, y = 0;
			int32 b = 1, code = 0, mods = 0;
			char text[200] = "";
			if (sscanf(line, "move %f %f", &x, &y) == 2) {
				PostMouse(in->win, B_MOUSE_MOVED, x, y, 0, 0);
			} else if (sscanf(line, "down %f %f %d", &x, &y, &b) >= 2) {
				PostMouse(in->win, B_MOUSE_DOWN, x, y, b, 1);
			} else if (sscanf(line, "up %f %f", &x, &y) == 2) {
				PostMouse(in->win, B_MOUSE_UP, x, y, 0, 0);
			} else if (sscanf(line, "click %f %f", &x, &y) == 2) {
				PostMouse(in->win, B_MOUSE_MOVED, x, y, 0, 0);
				PostMouse(in->win, B_MOUSE_DOWN, x, y, 1, 1);
				snooze(50000);
				PostMouse(in->win, B_MOUSE_UP, x, y, 0, 0);
			} else if (sscanf(line, "wheel %f %f", &x, &y) == 2) {
				BMessage msg(B_MOUSE_WHEEL_CHANGED);
				msg.AddInt64("when", system_time());
				msg.AddFloat("be:wheel_delta_x", x);
				msg.AddFloat("be:wheel_delta_y", y);
				in->win->PostMessage(&msg, in->win->fView);
			} else if (sscanf(line, "raw %i %i %199s", &code, &mods, text) >= 2) {
				PostKey(in->win, text, code, mods);
			} else if (sscanf(line, "key %199[^\n]", text) == 1) {
				if (strcmp(text, "\\b") == 0)
					PostKey(in->win, "\b", 0x1e);
				else if (strcmp(text, "\\n") == 0)
					PostKey(in->win, "\n", 0x47);
				else {
					// One message a character, as typing makes.
					for (const char *p = text; *p != 0;) {
						int32 n = 1;
						while ((p[n] & 0xc0) == 0x80)
							n++;
						char ch[8];
						memcpy(ch, p, n);
						ch[n] = 0;
						PostKey(in->win, ch, 0);
						p += n;
					}
				}
			}
		}
		in->lock.Unlock();
		fclose(f);
		unlink(taken);
	}
}

void StartInjector(GioWindow *win) {
	const char *path = getenv("GIO_HAIKU_INPUT");
	if (path == NULL || path[0] == 0)
		return;
	Injector *in = new Injector;
	win->fInjector = in;
	in->win = win;
	strlcpy(in->path, path, sizeof(in->path));
	thread_id t = spawn_thread(RunInjector, "gio input", B_NORMAL_PRIORITY, in);
	if (t >= 0)
		resume_thread(t);
}
} // namespace

extern "C" {

int32_t gh_abi(void) { return GH_ABI; }

// OwnSignature reads the signature the program's file carries in its
// resources (an rdef's app_signature, put there by xres), into sig, which
// holds B_MIME_TYPE_LENGTH bytes.
static bool OwnSignature(char *sig) {
	image_info info;
	int32 cookie = 0;
	while (get_next_image_info(B_CURRENT_TEAM, &cookie, &info) == B_OK) {
		if (info.type != B_APP_IMAGE)
			continue;
		BFile file(info.name, B_READ_ONLY);
		BAppFileInfo appInfo(&file);
		return appInfo.InitCheck() == B_OK && appInfo.GetSignature(sig) == B_OK && sig[0] != 0;
	}
	return false;
}

int32_t gh_init(const char *signature) {
	// The file's own signature comes first: a BApplication of another one
	// than its file's is told of on the terminal, and the roster keeps the
	// file's icon and flags under the file's signature.
	char own[B_MIME_TYPE_LENGTH];
	if (gSignature == NULL && OwnSignature(own))
		gSignature = strdup(own);
	if (gSignature == NULL)
		gSignature = strdup(signature != NULL && signature[0] ? signature : "application/x-vnd.gio-app");
	pthread_once(&gInitOnce, InitOnce);
	return gInitStatus;
}

void *gh_launch_args(int32_t *len) {
	gLaunchArgsLock.Lock();
	void *out = NULL;
	*len = 0;
	if (!gLaunchArgs.empty() && (out = malloc(gLaunchArgs.size())) != NULL) {
		memcpy(out, gLaunchArgs.data(), gLaunchArgs.size());
		*len = (int32_t)gLaunchArgs.size();
	}
	gLaunchArgsLock.Unlock();
	return out;
}

float gh_ui_scale(void) {
	float size = be_plain_font != NULL ? be_plain_font->Size() : 12.0f;
	return size / 12.0f;
}

void *gh_window_create(int32_t width, int32_t height, const char *title, int32_t decorated) {
	BRect frame(100, 100, 100 + width - 1, 100 + height - 1);
	GioWindow *w = new GioWindow(frame, title, decorated != 0);
	// A window larger than the screen opened with its edges off it.
	Fit(w, width, height, true);
	StartInjector(w);
	return w;
}

void gh_window_destroy(void *w) {
	// The window quits in its own thread. BWindow::Quit from another thread
	// waits for that thread to end, and a window whose thread was busy kept
	// Gio's goroutine waiting before the program could exit: the window was
	// gone, the program stayed in the Deskbar.
	GioWindow *win = Win(w);
	if (win->fInjector != NULL) {
		win->fInjector->lock.Lock();
		win->fInjector->stop = true;
		win->fInjector->lock.Unlock();
	}
	atomic_set(&win->fClosing, 1);
	win->PostMessage(B_QUIT_REQUESTED);
}

int32_t gh_window_next_event(void *w, gh_event *ev, int64_t timeout) {
	return Win(w)->fQueue.Pop(ev, timeout) ? 1 : 0;
}

void gh_window_wake(void *w) { Win(w)->fQueue.Push(NewEvent(GH_EV_WAKE)); }

void gh_window_size(void *w, int32_t *width, int32_t *height) {
	*width = 0;
	*height = 0;
	Locked(w, [&](GioWindow *win) {
		BRect b = win->fView->Bounds();
		*width = (int32_t)b.Width() + 1;
		*height = (int32_t)b.Height() + 1;
	});
}

void gh_window_set_title(void *w, const char *title) {
	Locked(w, [&](GioWindow *win) { win->SetTitle(title); });
}

void gh_window_set_size(void *w, int32_t width, int32_t height) {
	Locked(w, [&](GioWindow *win) { Fit(win, width, height, false); });
}

void gh_window_set_limits(void *w, int32_t minw, int32_t minh, int32_t maxw, int32_t maxh) {
	Locked(w, [&](GioWindow *win) {
		float maxW = maxw > 0 ? maxw - 1 : 32768, maxH = maxh > 0 ? maxh - 1 : 32768;
		float minW = minw > 0 ? minw - 1 : 0, minH = minh > 0 ? minh - 1 : 0;
		win->SetSizeLimits(minW, maxW, minH, maxH);
	});
}

void gh_window_set_decorated(void *w, int32_t decorated) {
	Locked(w, [&](GioWindow *win) { win->SetLook(decorated ? B_TITLED_WINDOW_LOOK : B_NO_BORDER_WINDOW_LOOK); });
}

void gh_window_set_mode(void *w, int32_t mode) {
	Locked(w, [&](GioWindow *win) {
		if (mode != GH_MODE_FULLSCREEN && win->fFullscreen) {
			win->SetLook(B_TITLED_WINDOW_LOOK);
			win->MoveTo(win->fWindowed.LeftTop());
			win->ResizeTo(win->fWindowed.Width(), win->fWindowed.Height());
			win->fFullscreen = false;
		}
		switch (mode) {
		case GH_MODE_MINIMIZED:
			win->Minimize(true);
			break;
		case GH_MODE_MAXIMIZED:
			if (win->IsMinimized())
				win->Minimize(false);
			if (!win->fZoomed)
				win->BWindow::Zoom();
			break;
		case GH_MODE_FULLSCREEN: {
			if (win->fFullscreen)
				break;
			win->fWindowed = win->Frame();
			BRect screen = BScreen(win).Frame();
			win->SetLook(B_NO_BORDER_WINDOW_LOOK);
			win->MoveTo(screen.LeftTop());
			win->ResizeTo(screen.Width(), screen.Height());
			win->fFullscreen = true;
			break;
		}
		default:
			if (win->IsMinimized())
				win->Minimize(false);
			if (win->fZoomed)
				win->BWindow::Zoom();
		}
	});
}

void gh_window_center(void *w) {
	Locked(w, [&](GioWindow *win) { win->CenterOnScreen(); });
}

void gh_window_raise(void *w) {
	Locked(w, [&](GioWindow *win) {
		if (win->IsMinimized())
			win->Minimize(false);
		win->Activate(true);
	});
}

void gh_window_show(void *w) {
	GioWindow *win = Win(w);
	win->Show();
}

void gh_window_set_cursor(void *w, int32_t cursor) {
	Locked(w, [&](GioWindow *win) { win->fView->SetCursorShape(cursor); });
}

void *gh_window_drop_paths(void *w, int32_t *len) {
	return Win(w)->fView->DropPaths(len);
}

int32_t gh_gl_lock(void *w, int32_t width, int32_t height) {
	GioWindow *win = Win(w);
	if (width <= 0 || height <= 0)
		return B_BAD_VALUE;
	if (win->fContext == NULL) {
		const int attribs[] = {
			OSMESA_FORMAT, OSMESA_BGRA,
			OSMESA_DEPTH_BITS, 0,
			OSMESA_STENCIL_BITS, 0,
			OSMESA_ACCUM_BITS, 0,
			OSMESA_PROFILE, OSMESA_CORE_PROFILE,
			OSMESA_CONTEXT_MAJOR_VERSION, 3,
			OSMESA_CONTEXT_MINOR_VERSION, 3,
			0,
		};
		win->fContext = OSMesaCreateContextAttribs(attribs, NULL);
		if (win->fContext == NULL)
			return B_ERROR;
	}
	if (width != win->fWidth || height != win->fHeight) {
		void *buf = realloc(win->fBuffer, (size_t)width * height * 4);
		if (buf == NULL)
			return B_NO_MEMORY;
		win->fBuffer = buf;
		win->fWidth = width;
		win->fHeight = height;
	}
	if (!OSMesaMakeCurrent(win->fContext, win->fBuffer, GL_UNSIGNED_BYTE, width, height))
		return B_ERROR;
	// Rows from the top, as a BBitmap has them.
	OSMesaPixelStore(OSMESA_Y_UP, 0);
	return B_OK;
}

void gh_gl_unlock(void *w) {
	OSMesaMakeCurrent(NULL, NULL, GL_UNSIGNED_BYTE, 0, 0);
}

void gh_gl_swap(void *w) {
	GioWindow *win = Win(w);
	if (win->fBuffer == NULL)
		return;
	glFinish();
	BRect bounds(0, 0, win->fWidth - 1, win->fHeight - 1);
	win->fFrame.lock.Lock();
	BBitmap *bm = win->fFrame.bitmap;
	if (bm == NULL || bm->Bounds() != bounds) {
		delete bm;
		bm = new BBitmap(bounds, B_RGB32);
		win->fFrame.bitmap = bm;
	}
	int32 rowBytes = win->fWidth * 4;
	uint8 *dst = (uint8 *)bm->Bits();
	const uint8 *src = (const uint8 *)win->fBuffer;
	if (bm->BytesPerRow() == rowBytes) {
		memcpy(dst, src, (size_t)rowBytes * win->fHeight);
	} else {
		for (int32 y = 0; y < win->fHeight; y++)
			memcpy(dst + y * bm->BytesPerRow(), src + y * rowBytes, rowBytes);
	}
	win->fFrame.lock.Unlock();
	if (atomic_get_and_set(&win->fFrame.pending, 1) == 0)
		win->PostMessage(kShowFrame);
}

void gh_gl_release(void *w) {
	GioWindow *win = Win(w);
	if (win->fContext != NULL) {
		OSMesaDestroyContext(win->fContext);
		win->fContext = NULL;
	}
	free(win->fBuffer);
	win->fBuffer = NULL;
	win->fWidth = win->fHeight = 0;
	// Without a frame, the view asks Gio for one when it is shown.
	win->fFrame.lock.Lock();
	delete win->fFrame.bitmap;
	win->fFrame.bitmap = NULL;
	win->fFrame.lock.Unlock();
}

int32_t gh_clipboard_write(const char *mime, const void *data, int32_t len) {
	if (!be_clipboard->Lock())
		return B_ERROR;
	be_clipboard->Clear();
	BMessage *clip = be_clipboard->Data();
	status_t err = B_ERROR;
	if (clip != NULL) {
		err = clip->AddData(mime, B_MIME_TYPE, data, len);
		if (err == B_OK)
			err = be_clipboard->Commit();
	}
	be_clipboard->Unlock();
	return err;
}

void *gh_clipboard_read(const char *mime, int32_t *len) {
	*len = 0;
	if (!be_clipboard->Lock())
		return NULL;
	void *out = NULL;
	BMessage *clip = be_clipboard->Data();
	const void *data;
	ssize_t size;
	if (clip != NULL && clip->FindData(mime, B_MIME_TYPE, &data, &size) == B_OK) {
		out = malloc(size > 0 ? size : 1);
		if (out != NULL) {
			memcpy(out, data, size);
			*len = (int32_t)size;
		}
	}
	be_clipboard->Unlock();
	return out;
}

void gh_free(void *p) { free(p); }

} // extern "C"
