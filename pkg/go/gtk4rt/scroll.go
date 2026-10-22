//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>

extern void snglRtDispatch(int idx);
extern void snglToplevelGone(gpointer data, GObject *where);

static void sngl_scroll_adj(GtkAdjustment *a, gpointer data) {
    (void)a;
    snglRtDispatch(GPOINTER_TO_INT(data));
}
// The content is laid out at its natural height, as a scroll's is in CSS.
// GTK's default is its minimum, and a picture's minimum is nothing: content
// taller than the viewport squeezed every picture in it to no height at all.
// Its width stays the viewport's, which is CSS's too. The viewport the
// scrolled window wraps its child in is the scrollable whose policy says so,
// and it is made as the child is set.
static void sngl_scroll_child(GObject *o, GParamSpec *ps, gpointer data) {
    (void)ps; (void)data;
    GtkWidget *c = gtk_scrolled_window_get_child(GTK_SCROLLED_WINDOW(o));
    if (c != NULL && !GTK_IS_SCROLLABLE(c)) c = gtk_widget_get_parent(c);
    if (c == NULL || !GTK_IS_SCROLLABLE(c)) return;
    gtk_scrollable_set_vscroll_policy(GTK_SCROLLABLE(c), GTK_SCROLL_NATURAL);
}
// "changed" is the content or the viewport changing size, "value-changed" a
// scroll. The scrolled window keeps its vertical adjustment when it wraps its
// child in a viewport, so connecting to it once is enough.
static GtkWidget *sngl_scroll_new(int idx) {
    GtkWidget *sw = gtk_scrolled_window_new();
    g_signal_connect(sw, "notify::child", G_CALLBACK(sngl_scroll_child), NULL);
    GtkAdjustment *a = gtk_scrolled_window_get_vadjustment(GTK_SCROLLED_WINDOW(sw));
    g_signal_connect(a, "changed", G_CALLBACK(sngl_scroll_adj), GINT_TO_POINTER(idx));
    g_signal_connect(a, "value-changed", G_CALLBACK(sngl_scroll_adj), GINT_TO_POINTER(idx));
    return sw;
}
static void sngl_scroll_watch(GObject *o, int idx) {
    g_object_weak_ref(o, snglToplevelGone, GINT_TO_POINTER(idx));
}
static void sngl_scroll_measure(GtkWidget *sw, double *upper, double *value, double *page) {
    GtkAdjustment *a = gtk_scrolled_window_get_vadjustment(GTK_SCROLLED_WINDOW(sw));
    *upper = gtk_adjustment_get_upper(a);
    *value = gtk_adjustment_get_value(a);
    *page = gtk_adjustment_get_page_size(a);
}
*/
import "C"

import (
	"unsafe"

	"duckfam.us/sngl/pkg/go/cbind"
)

// A scroll is what a `ui.scroll` is on gtk4: a GtkScrolledWindow that reports
// the end of its content coming within half a viewport of showing -- as it
// arrives, and again when the content changes size while it is there.
type scroll struct {
	onReachEnd func()
	near       bool
	upper      float64
	// asked says a report is posted and has not run: the adjustment changes
	// several times in one allocation, and one report answers them all.
	asked bool
}

var scrolls = map[Handle]*scroll{}

func ScrollNew() Handle {
	s := &scroll{upper: -1}
	var h Handle
	changed := cbind.Register(func() { s.changed(h) })
	h = Handle(unsafe.Pointer(C.sngl_scroll_new(C.int(changed))))
	scrolls[h] = s
	gone := cbind.Register(func() {
		delete(scrolls, h)
		cbind.Release(changed)
	})
	C.sngl_scroll_watch((*C.GObject)(p(h)), C.int(gone))
	return h
}

// ScrollOnReachEnd is the handler a `@reachEnd` attaches.
func ScrollOnReachEnd(h Handle, fn func()) {
	if s := scrolls[h]; s != nil {
		s.onReachEnd = fn
	}
}

// ScrollReachEnd fires the handler as the end arriving would; for tests.
func ScrollReachEnd(h Handle) {
	if s := scrolls[h]; s != nil && s.onReachEnd != nil {
		s.onReachEnd()
	}
}

// changed asks again after the adjustment moved. The handler runs on the next
// idle tick rather than here: the adjustment changes while GTK allocates, and
// a handler adding rows then would change the tree mid-allocation.
func (s *scroll) changed(h Handle) {
	var upper, value, page C.double
	C.sngl_scroll_measure(widget(h), &upper, &value, &page)
	if page <= 0 {
		// Not allocated yet: nothing is shown, so no end is.
		return
	}
	near := float64(upper)-float64(value+page) <= float64(page)/2
	grew := float64(upper) != s.upper
	fire := near && (!s.near || grew)
	s.near, s.upper = near, float64(upper)
	if !fire || s.asked {
		return
	}
	s.asked = true
	Post(func() {
		s.asked = false
		if s.onReachEnd != nil && scrolls[h] == s {
			s.onReachEnd()
		}
	})
}
