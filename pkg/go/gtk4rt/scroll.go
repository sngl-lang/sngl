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
// The probe is the scroll's child, and holds the content: a box laying its
// one child out over the whole of it, which measures the content's natural
// height each time it is allocated and reports it changing. The adjustment
// alone cannot say so, since its upper is never less than the page: content
// shorter than the viewport grew and nothing heard of it, so a short first
// page asked for one more and no further.
static void sngl_probe_measure(GtkWidget *w, GtkOrientation o, int for_size,
        int *min, int *nat, int *min_base, int *nat_base) {
    (void)min_base; (void)nat_base;
    *min = *nat = 0;
    GtkWidget *c = gtk_widget_get_first_child(w);
    if (c != NULL && gtk_widget_should_layout(c)) gtk_widget_measure(c, o, for_size, min, nat, NULL, NULL);
}
static void sngl_probe_allocate(GtkWidget *w, int width, int height, int baseline) {
    GtkWidget *c = gtk_widget_get_first_child(w);
    if (c == NULL || !gtk_widget_should_layout(c)) return;
    gtk_widget_allocate(c, width, height, baseline, NULL);
    int nat = 0;
    gtk_widget_measure(c, GTK_ORIENTATION_VERTICAL, width, NULL, &nat, NULL, NULL);
    if (nat != GPOINTER_TO_INT(g_object_get_data(G_OBJECT(w), "sngl-content"))) {
        g_object_set_data(G_OBJECT(w), "sngl-content", GINT_TO_POINTER(nat));
        snglRtDispatch(GPOINTER_TO_INT(g_object_get_data(G_OBJECT(w), "sngl-changed")));
    }
}
static GtkSizeRequestMode sngl_probe_mode(GtkWidget *w) {
    GtkWidget *c = gtk_widget_get_first_child(w);
    return c != NULL ? gtk_widget_get_request_mode(c) : GTK_SIZE_REQUEST_CONSTANT_SIZE;
}
static int sngl_scroll_holds(GtkWidget *sw, GtkWidget *c) {
    GtkWidget *probe = g_object_get_data(G_OBJECT(sw), "sngl-probe");
    return probe != NULL && c != NULL && gtk_widget_get_parent(c) == probe;
}
static int sngl_probe_content(GtkWidget *sw) {
    GtkWidget *probe = g_object_get_data(G_OBJECT(sw), "sngl-probe");
    return probe != NULL ? GPOINTER_TO_INT(g_object_get_data(G_OBJECT(probe), "sngl-content")) : -1;
}
// The content is laid out at its natural height, as a scroll's is in CSS.
// GTK's default is its minimum, and a picture's minimum is nothing: content
// taller than the viewport squeezed every picture in it to no height at all.
// Its width stays the viewport's, which is CSS's too. The viewport the
// scrolled window wraps its child in is the scrollable whose policy says so,
// and it is made as the child is set.
//
// A child set that is not the probe is the content, and goes in the probe,
// which becomes the child: generated code sets a scroll's child through GTK's
// own call.
static void sngl_scroll_child(GObject *o, GParamSpec *ps, gpointer data) {
    (void)ps; (void)data;
    GtkScrolledWindow *sw = GTK_SCROLLED_WINDOW(o);
    GtkWidget *probe = g_object_get_data(o, "sngl-probe");
    GtkWidget *c = gtk_scrolled_window_get_child(sw);
    if (c != NULL && !GTK_IS_SCROLLABLE(c)) c = gtk_widget_get_parent(c);
    if (c != NULL && GTK_IS_SCROLLABLE(c)) {
        gtk_scrollable_set_vscroll_policy(GTK_SCROLLABLE(c), GTK_SCROLL_NATURAL);
    }
    GtkWidget *content = gtk_scrolled_window_get_child(sw);
    if (probe == NULL || content == NULL || content == probe) return;
    if (GTK_IS_VIEWPORT(content)) {
        GtkWidget *inner = gtk_viewport_get_child(GTK_VIEWPORT(content));
        if (inner == probe) return;
        content = inner;
    }
    if (content == NULL) return;
    g_object_ref(content);
    for (GtkWidget *k = gtk_widget_get_first_child(probe); k != NULL; k = gtk_widget_get_first_child(probe)) {
        gtk_box_remove(GTK_BOX(probe), k);
    }
    g_object_set_data(G_OBJECT(probe), "sngl-content", GINT_TO_POINTER(-1));
    gtk_scrolled_window_set_child(sw, probe);
    gtk_box_append(GTK_BOX(probe), content);
    g_object_unref(content);
}
// "changed" is the viewport changing size, "value-changed" a scroll, and the
// probe reports the content changing size. The scrolled window keeps its
// vertical adjustment when it wraps its child in a viewport, so connecting to
// it once is enough.
static GtkWidget *sngl_scroll_new(int idx) {
    GtkWidget *sw = gtk_scrolled_window_new();
    GtkWidget *probe = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    gtk_widget_set_layout_manager(probe,
        gtk_custom_layout_new(sngl_probe_mode, sngl_probe_measure, sngl_probe_allocate));
    g_object_set_data(G_OBJECT(probe), "sngl-changed", GINT_TO_POINTER(idx));
    g_object_set_data(G_OBJECT(probe), "sngl-content", GINT_TO_POINTER(-1));
    g_object_set_data_full(G_OBJECT(sw), "sngl-probe", g_object_ref_sink(probe), g_object_unref);
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
	// content is the content's natural height when last measured, which the
	// probe reports: -1 before it has been.
	content int
	// asked says a report is posted and has not run: the adjustment changes
	// several times in one allocation, and one report answers them all.
	asked bool
}

var scrolls = map[Handle]*scroll{}

func ScrollNew() Handle {
	s := &scroll{content: -1}
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

// scrollHolds reports whether child is already the content of sw, a scroll
// ScrollNew made, which holds it in its probe rather than as its child.
func scrollHolds(sw, child Handle) bool {
	return C.sngl_scroll_holds(widget(sw), widget(child)) != 0
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
	content := int(C.sngl_probe_content(widget(h)))
	grew := content != s.content
	fire := near && (!s.near || grew)
	s.near, s.content = near, content
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
