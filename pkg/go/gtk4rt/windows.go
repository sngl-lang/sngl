//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>

extern void snglGoDispatch(int idx);
extern void snglWindowsActivate(GtkApplication *app, gpointer data);

// close-request is (window, user_data) -> gboolean. TRUE stops GTK's own
// close, which would destroy the window: whether the window still exists is
// the program's answer, reached through the handler.
static gboolean sngl_close_request(GtkWindow *win, gpointer data) {
    (void)win;
    snglGoDispatch(GPOINTER_TO_INT(data));
    return TRUE;
}
static void sngl_connect_close(GtkWindow *win, int idx) {
    g_signal_connect(win, "close-request", G_CALLBACK(sngl_close_request), GINT_TO_POINTER(idx));
}
static void sngl_connect_windows_activate(GtkApplication *app) {
    g_signal_connect(app, "activate", G_CALLBACK(snglWindowsActivate), NULL);
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

// Window is one window of a program: the toplevel the host shows, around a
// widget tree the program builds once.
//
// The tree outlives the toplevel. Mount creates a GtkWindow around Content
// and Unmount destroys it, while Content is retained and keeps receiving the
// program's updates in between -- so nothing the program writes ever reaches
// a freed widget, and a window mounted again shows the state as it now is.
type Window struct {
	Title   string
	Content Handle
	// OnClose is the window's `@close`, run when the window manager closes
	// it. Nil hides the window.
	OnClose func()

	win      *C.GtkWindow
	mounted  bool
	open     bool
	retained bool
}

var (
	windowsApp *C.GtkApplication
	// windowsStarted is whether the application has activated: a toplevel
	// needs one, so a window mounted before that is created on activate.
	windowsStarted bool
	// windowsHidden is a start with no window on screen -- `@run` never
	// called `run` -- which a window's close does not end.
	windowsHidden bool
	mountedWins   []*Window
)

// Mount says the window exists. Before the loop starts it is created on
// activate; after, it is created and put on screen at once, since a window
// the tree gains while the program runs is one the program asked for.
func (w *Window) Mount() {
	if w.mounted {
		return
	}
	w.mounted = true
	mountedWins = append(mountedWins, w)
	if windowsStarted {
		w.create()
		w.present()
	}
}

// Unmount destroys the toplevel. The content is kept for a later Mount.
func (w *Window) Unmount() {
	if !w.mounted {
		return
	}
	w.mounted = false
	w.open = false
	mountedWins = slices.DeleteFunc(mountedWins, func(x *Window) bool { return x == w })
	if w.win != nil {
		C.gtk_window_set_child(w.win, nil)
		C.gtk_window_destroy(w.win)
		w.win = nil
	}
}

// Open puts a mounted window on screen and in front.
func (w *Window) Open() {
	w.open = true
	if w.win != nil {
		w.present()
	}
}

// Close takes the window off screen; Open brings it back as it was.
func (w *Window) Close() {
	w.open = false
	if w.win != nil {
		C.gtk_widget_set_visible((*C.GtkWidget)(unsafe.Pointer(w.win)), 0)
	}
}

// RequestClose closes the window the way the window manager's close button
// does: GTK asks the window, and the window's @close answers.
func (w *Window) RequestClose() {
	if w.win != nil {
		C.gtk_window_close(w.win)
	}
}

// onScreen is whether the window has a toplevel and it is visible.
func (w *Window) onScreen() bool {
	return w.win != nil && C.gtk_widget_get_visible((*C.GtkWidget)(unsafe.Pointer(w.win))) != 0
}

func (w *Window) create() {
	if w.win != nil {
		return
	}
	w.win = (*C.GtkWindow)(unsafe.Pointer(C.gtk_application_window_new(windowsApp)))
	C.gtk_window_set_default_size(w.win, 480, 640)
	if w.Title != "" {
		cs := C.CString(w.Title)
		C.gtk_window_set_title(w.win, cs)
		C.free(unsafe.Pointer(cs))
	}
	if w.Content != nil {
		if !w.retained {
			// The toplevel's destroy would free the tree; this reference is
			// what lets it outlive one.
			C.g_object_ref_sink(C.gpointer(p(w.Content)))
			w.retained = true
		}
		C.gtk_window_set_child(w.win, widget(w.Content))
	}
	idx := cbind.Register(w.closeRequested)
	C.sngl_connect_close(w.win, C.int(idx))
}

func (w *Window) present() {
	w.open = true
	C.gtk_window_present(w.win)
}

// closeRequested is the window manager's close. The handler decides what it
// means; with none, the window hides. Either way a close that leaves nothing
// on screen ends the program, unless it started with nothing on screen.
func (w *Window) closeRequested() {
	if w.OnClose != nil {
		w.OnClose()
	} else {
		w.Close()
	}
	if windowsHidden {
		return
	}
	for _, x := range mountedWins {
		if x.onScreen() {
			return
		}
	}
	C.g_application_quit((*C.GApplication)(unsafe.Pointer(windowsApp)))
}

//export snglWindowsActivate
func snglWindowsActivate(app *C.GtkApplication, _ C.gpointer) {
	// Held, so the application outlives its last window: one the tree
	// destroys is not the program ending, and a start with none on screen
	// has none to keep it alive.
	C.g_application_hold((*C.GApplication)(unsafe.Pointer(app)))
	windowsStarted = true
	for _, w := range slices.Clone(mountedWins) {
		w.create()
		if !windowsHidden || w.open {
			w.present()
		}
	}
}

// RunWindows runs the application over the windows mounted so far and those
// mounted later. show puts the mounted ones on screen at start; without it
// the loop runs with nothing on screen until a window is opened or mounted.
// Returns the application's exit status.
func RunWindows(show bool) int {
	runtime.LockOSThread()
	windowsHidden = !show
	windowsApp = C.gtk_application_new(nil, C.G_APPLICATION_NON_UNIQUE)
	C.sngl_connect_windows_activate(windowsApp)
	status := int(C.g_application_run((*C.GApplication)(unsafe.Pointer(windowsApp)), 0, nil))
	if status != 0 {
		os.Stderr.WriteString("gtk: application exited with a non-zero status\n")
	}
	return status
}
