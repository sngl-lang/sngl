//go:build unix

package gtkprobe

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>

static GtkWidget *probe_focus(GtkWidget *win) {
    return gtk_root_get_focus(GTK_ROOT(win));
}

// probe_click is a click on w as GTK hands one to it: "pressed" and
// "released" on each of its click gestures, whose handlers are what focus a
// button a click lands on and press it. It is the widget's own handlers that
// run; no event is made.
static void probe_click(GtkWidget *w) {
    GListModel *controllers = gtk_widget_observe_controllers(w);
    guint n = g_list_model_get_n_items(controllers);
    for (guint i = 0; i < n; i++) {
        GObject *c = g_list_model_get_item(controllers, i);
        if (GTK_IS_GESTURE_CLICK(c)) {
            g_signal_emit_by_name(c, "pressed", 1, 1.0, 1.0);
            g_signal_emit_by_name(c, "released", 1, 1.0, 1.0);
        }
        g_object_unref(c);
    }
    g_object_unref(controllers);
}
*/
import "C"

import "unsafe"

// Focus is the widget focus is on in the window win, or nil.
func Focus(win unsafe.Pointer) unsafe.Pointer {
	return unsafe.Pointer(C.probe_focus((*C.GtkWidget)(win)))
}

// GrabFocus focuses w where it can take focus, and reports whether it did.
func GrabFocus(w unsafe.Pointer) bool {
	return C.gtk_widget_grab_focus((*C.GtkWidget)(w)) != 0
}

// IsAncestor reports whether w is a or is inside it: an entry's focus is on
// the text widget it holds.
func IsAncestor(w, a unsafe.Pointer) bool {
	return w == a || (w != nil && C.gtk_widget_is_ancestor((*C.GtkWidget)(w), (*C.GtkWidget)(a)) != 0)
}

// Click is a click landing on w, as its click gestures see one.
func Click(w unsafe.Pointer) {
	C.probe_click((*C.GtkWidget)(w))
}
