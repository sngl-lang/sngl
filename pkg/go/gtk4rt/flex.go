//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>

GtkOrientation sngl_box_axis(GtkWidget *box);

// A flexing widget expands along the axis of the box it is in, and only that
// one: GTK hands a box's spare room to the children that expand along it, and
// an expand on the other axis would reach the box's own parent instead, which
// a `flex` in CSS does not.
static void sngl_flex_apply(GtkWidget *w) {
    GtkWidget *parent = gtk_widget_get_parent(w);
    if (parent == NULL || !GTK_IS_BOX(parent)) return;
    GtkOrientation o = sngl_box_axis(parent);
    if (o == GTK_ORIENTATION_VERTICAL) {
        gtk_widget_set_vexpand(w, TRUE);
    } else {
        gtk_widget_set_hexpand(w, TRUE);
    }
}
static void sngl_flex_parent(GObject *o, GParamSpec *ps, gpointer data) {
    (void)ps; (void)data;
    sngl_flex_apply(GTK_WIDGET(o));
}
static void sngl_flex(GtkWidget *w) {
    if (g_object_get_data(G_OBJECT(w), "sngl-flex") == NULL) {
        g_object_set_data(G_OBJECT(w), "sngl-flex", GINT_TO_POINTER(1));
        g_signal_connect(w, "notify::parent", G_CALLBACK(sngl_flex_parent), NULL);
    }
    sngl_flex_apply(w);
}
*/
import "C"

// Flex makes w take a share of the spare room of the box it is in, which is
// what `style.flex` asks for. Which way it expands is the box's axis, and the
// box may not be there yet -- a render slot's content is built before it is
// inserted -- so it is read whenever w lands in one. GTK carries the expand up
// to the box's own ancestors, so a flexing scroll in a page fills the window.
func Flex(w Handle) {
	C.sngl_flex(widget(w))
}
