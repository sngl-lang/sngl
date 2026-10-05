//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <stdlib.h>

extern void snglGoDispatch(int idx);
extern void snglToplevelsActivate(GtkApplication *app, gpointer data);
extern void snglToplevelGone(gpointer data, GObject *where);

// close-request is (window, user_data) -> gboolean. TRUE stops GTK's own
// close, which would destroy the window: whether the window still exists is
// the program's answer, reached through its `visible` and `@closed`.
static gboolean sngl_top_close_request(GtkWindow *win, gpointer data) {
    (void)win;
    snglGoDispatch(GPOINTER_TO_INT(data));
    return TRUE;
}
static void sngl_top_connect_close(GtkWindow *win, int idx) {
    g_signal_connect(win, "close-request", G_CALLBACK(sngl_top_close_request), GINT_TO_POINTER(idx));
}
static void sngl_top_connect_activate(GtkApplication *app) {
    g_signal_connect(app, "activate", G_CALLBACK(snglToplevelsActivate), NULL);
}
static void sngl_top_watch(GObject *box, int idx) {
    g_object_weak_ref(box, snglToplevelGone, GINT_TO_POINTER(idx));
}
*/
import "C"

import (
	"os"
	"runtime"
	"slices"
	"unsafe"

	"git.duckfam.us/jonathan/sngl/pkg/go/cbind"
)

// A Toplevel is what a `ui.window` is on gtk4: a node whose handle is the box
// its content is appended to, and which the program attaches to the
// application (App) like any node to its parent. Attaching one creates the
// GtkWindow around the box and puts it on screen when it is visible;
// detaching one destroys the window, and the box with it unless something --
// a record keeping the instance across renders -- holds a reference on it.
//
// So the window lives exactly as long as the node does in the tree: a window
// under `if details` is created when the condition turns true and destroyed,
// content and all, when it turns false. Hiding without destroying is
// `visible`.
type toplevel struct {
	box     Handle
	title   string
	visible bool
	// attached is whether the node is the application's child.
	attached  bool
	win       *C.GtkWindow
	onVisible func(bool)
	onClosed  func()
	gone      int
}

// App is the parent a node at the root of the package body is attached to.
// It is no widget: every container call below that is handed it answers for
// the application instead.
var App = Handle(unsafe.Pointer(&appSentinel))

var appSentinel byte

var (
	toplevels = map[Handle]*toplevel{}
	// attachedTops is the application's children, in the order attached.
	attachedTops []*toplevel
	topsApp      *C.GtkApplication
	// topsStarted is whether the application has activated: a toplevel needs
	// one, so a window attached before that is created on activate.
	topsStarted bool
	// topsHidden is a start with no window on screen -- `@run` never called
	// `run` -- which a window's close does not end.
	topsHidden bool
)

// ToplevelNew makes a toplevel's content box. The window is made when the
// node is attached to the application and the loop is running.
func ToplevelNew() Handle {
	box := BoxNew(OrientationVertical, 6)
	t := &toplevel{box: box, visible: true}
	toplevels[box] = t
	idx := cbind.Register(func() {
		delete(toplevels, box)
	})
	t.gone = idx
	C.sngl_top_watch((*C.GObject)(p(box)), C.int(idx))
	return box
}

//export snglToplevelGone
func snglToplevelGone(data C.gpointer, _ *C.GObject) {
	idx := int(uintptr(data))
	cbind.DispatchOnce(idx)
}

func topOf(h Handle) *toplevel {
	return toplevels[h]
}

// ToplevelSetTitle sets the title the window shows.
func ToplevelSetTitle(h Handle, title string) {
	t := topOf(h)
	if t == nil {
		return
	}
	t.title = title
	if t.win != nil {
		cs := C.CString(title)
		C.gtk_window_set_title(t.win, cs)
		C.free(unsafe.Pointer(cs))
	}
}

// ToplevelSetVisible shows or hides the window. The program writing the
// value the window already has -- its own report coming back through the
// binding -- changes nothing.
func ToplevelSetVisible(h Handle, v bool) {
	t := topOf(h)
	if t == nil || t.visible == v {
		return
	}
	t.visible = v
	if t.win == nil {
		return
	}
	if v {
		C.gtk_window_present(t.win)
	} else {
		C.gtk_widget_set_visible((*C.GtkWidget)(unsafe.Pointer(t.win)), 0)
	}
}

// ToplevelOnVisible is where the host reports a change of `visible` it made
// itself: the window manager's close, or a start with nothing on screen.
func ToplevelOnVisible(h Handle, fn func(bool)) {
	if t := topOf(h); t != nil {
		t.onVisible = fn
	}
}

// ToplevelOnClosed is the window's `@closed`, run after a window manager's
// close has been reported as `visible = false`.
func ToplevelOnClosed(h Handle, fn func()) {
	if t := topOf(h); t != nil {
		t.onClosed = fn
	}
}

// AppAttach makes a toplevel a child of the application. Before the loop
// starts it is created on activate; after, it is created at once, and put on
// screen when it is visible.
func AppAttach(h Handle) {
	t := topOf(h)
	if t == nil || t.attached {
		return
	}
	t.attached = true
	attachedTops = append(attachedTops, t)
	if topsStarted {
		t.create()
		if t.visible {
			C.gtk_window_present(t.win)
		}
	}
}

// AppDetach takes a toplevel out of the application: its window is
// destroyed. The box goes with it unless a reference outlives the window.
func AppDetach(h Handle) {
	t := topOf(h)
	if t == nil || !t.attached {
		return
	}
	t.attached = false
	attachedTops = slices.DeleteFunc(attachedTops, func(x *toplevel) bool { return x == t })
	if t.win != nil {
		win := t.win
		t.win = nil
		// A reference across the unparent, so the box is freed by its last
		// holder rather than mid-call.
		C.g_object_ref(C.gpointer(p(t.box)))
		C.gtk_window_set_child(win, nil)
		C.gtk_window_destroy(win)
		C.g_object_unref(C.gpointer(p(t.box)))
	}
}

// ToplevelWindow puts a toplevel's content in a window of app's, for a
// harness that shows one window's content outside a running program: a
// snapshot or a test. The window is the toplevel's from then on.
func ToplevelWindow(app, h Handle) Handle {
	win := C.gtk_application_window_new((*C.GtkApplication)(p(app)))
	C.gtk_window_set_default_size((*C.GtkWindow)(unsafe.Pointer(win)), 480, 640)
	t := topOf(h)
	if t == nil {
		return Handle(unsafe.Pointer(win))
	}
	if t.title != "" {
		cs := C.CString(t.title)
		C.gtk_window_set_title((*C.GtkWindow)(unsafe.Pointer(win)), cs)
		C.free(unsafe.Pointer(cs))
	}
	C.gtk_window_set_child((*C.GtkWindow)(unsafe.Pointer(win)), widget(t.box))
	t.win = (*C.GtkWindow)(unsafe.Pointer(win))
	return Handle(unsafe.Pointer(win))
}

// RequestClose closes the window the way the window manager's close button
// does: GTK asks the window, and the toplevel's report answers.
func ToplevelRequestClose(h Handle) {
	if t := topOf(h); t != nil && t.win != nil {
		C.gtk_window_close(t.win)
	}
}

// onScreen is whether the toplevel has a window and it is visible.
func (t *toplevel) onScreen() bool {
	return t.win != nil && C.gtk_widget_get_visible((*C.GtkWidget)(unsafe.Pointer(t.win))) != 0
}

func (t *toplevel) create() {
	if t.win != nil {
		return
	}
	t.win = (*C.GtkWindow)(unsafe.Pointer(C.gtk_application_window_new(topsApp)))
	C.gtk_window_set_default_size(t.win, 480, 640)
	if t.title != "" {
		cs := C.CString(t.title)
		C.gtk_window_set_title(t.win, cs)
		C.free(unsafe.Pointer(cs))
	}
	C.gtk_window_set_child(t.win, widget(t.box))
	idx := cbind.Register(t.closeRequested)
	C.sngl_top_connect_close(t.win, C.int(idx))
}

// report is the host changing `visible` itself.
func (t *toplevel) report(v bool) {
	if t.visible == v {
		return
	}
	t.visible = v
	if t.onVisible != nil {
		t.onVisible(v)
	}
}

// closeRequested is the window manager's close. It is reported as the window
// going off screen, and then as `@closed`; what the handlers do decides what
// exists. A close that leaves nothing on screen ends the program, unless it
// started with nothing on screen.
func (t *toplevel) closeRequested() {
	if t.win != nil {
		C.gtk_widget_set_visible((*C.GtkWidget)(unsafe.Pointer(t.win)), 0)
	}
	t.report(false)
	if t.onClosed != nil {
		t.onClosed()
	}
	if topsHidden {
		return
	}
	for _, x := range attachedTops {
		if x.onScreen() {
			return
		}
	}
	C.g_application_quit((*C.GApplication)(unsafe.Pointer(topsApp)))
}

//export snglToplevelsActivate
func snglToplevelsActivate(app *C.GtkApplication, _ C.gpointer) {
	// Held, so the application outlives its last window: one the tree
	// destroys is not the program ending, and a start with none on screen
	// has none to keep it alive.
	C.g_application_hold((*C.GApplication)(unsafe.Pointer(app)))
	topsStarted = true
	for _, t := range slices.Clone(attachedTops) {
		t.create()
		switch {
		case !topsHidden && t.visible:
			C.gtk_window_present(t.win)
		case topsHidden:
			// Started with nothing on screen, which the host reports: a
			// window's `visible` says false until something opens it.
			t.report(false)
		}
	}
}

// RunWindows runs the application over the toplevels attached so far and
// those attached later. show puts the visible ones on screen at start;
// without it the loop runs with nothing on screen until a window is opened or
// attached. Returns the application's exit status.
func RunWindows(show bool) int {
	runtime.LockOSThread()
	topsHidden = !show
	topsApp = C.gtk_application_new(nil, C.G_APPLICATION_NON_UNIQUE)
	C.sngl_top_connect_activate(topsApp)
	status := int(C.g_application_run((*C.GApplication)(unsafe.Pointer(topsApp)), 0, nil))
	if status != 0 {
		os.Stderr.WriteString("gtk: application exited with a non-zero status\n")
	}
	return status
}
