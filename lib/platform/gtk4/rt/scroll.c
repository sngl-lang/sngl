/* What a `ui.scroll` on gtk4 needs that only C can write: the probe, a box
 * laid out around the scroll's content that says when the content's length
 * along the scroll changes. Its layout manager answers GTK through
 * out-parameters, which no function SNGL hands C can fill. The scroll itself
 * -- its window, the adjustments it watches, when it reports its end -- is
 * gtk4.sngl's. */
#include "sngl_gtk.h"

/* The content's natural length along the axis the probe reads, -1 before it
 * was measured, and whether that axis is across. */
typedef struct {
    int content;
    bool across;
    struct {
        SnglFn fn;
        void *ctx;
        GDestroyNotify release;
    } changed;
} SnglProbe;

static SnglProbe *sngl_probe_of(GtkWidget *probe) {
    return probe != NULL ? g_object_get_data(G_OBJECT(probe), "sngl-probe-state") : NULL;
}

static void sngl_probe_free(gpointer data) {
    SnglProbe *p = data;
    if (p->changed.release) p->changed.release(p->changed.ctx);
    g_free(p);
}

static void sngl_probe_measure(GtkWidget *w, GtkOrientation o, int for_size,
        int *min, int *nat, int *min_base, int *nat_base) {
    (void)min_base; (void)nat_base;
    *min = *nat = 0;
    GtkWidget *c = gtk_widget_get_first_child(w);
    if (c != NULL && gtk_widget_should_layout(c)) gtk_widget_measure(c, o, for_size, min, nat, NULL, NULL);
}

static void sngl_probe_allocate(GtkWidget *w, int width, int height, int baseline) {
    GtkWidget *c = gtk_widget_get_first_child(w);
    SnglProbe *p = sngl_probe_of(w);
    if (c == NULL || p == NULL || !gtk_widget_should_layout(c)) return;
    gtk_widget_allocate(c, width, height, baseline, NULL);
    int nat = 0;
    if (p->across) {
        gtk_widget_measure(c, GTK_ORIENTATION_HORIZONTAL, height, NULL, &nat, NULL, NULL);
    } else {
        gtk_widget_measure(c, GTK_ORIENTATION_VERTICAL, width, NULL, &nat, NULL, NULL);
    }
    if (nat != p->content) {
        p->content = nat;
        if (p->changed.fn) p->changed.fn(p->changed.ctx);
    }
}

static GtkSizeRequestMode sngl_probe_mode(GtkWidget *w) {
    GtkWidget *c = gtk_widget_get_first_child(w);
    return c != NULL ? gtk_widget_get_request_mode(c) : GTK_SIZE_REQUEST_CONSTANT_SIZE;
}

GtkWidget *sngl_scroll_probe(GtkWidget *sw) {
    GtkWidget *probe = g_object_get_data(G_OBJECT(sw), "sngl-probe");
    if (probe != NULL) return probe;
    probe = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    gtk_widget_set_layout_manager(probe,
        gtk_custom_layout_new(sngl_probe_mode, sngl_probe_measure, sngl_probe_allocate));
    SnglProbe *p = g_new0(SnglProbe, 1);
    p->content = -1;
    g_object_set_data_full(G_OBJECT(probe), "sngl-probe-state", p, sngl_probe_free);
    g_object_set_data_full(G_OBJECT(sw), "sngl-probe", g_object_ref_sink(probe), g_object_unref);
    return probe;
}

int sngl_probe_content(GtkWidget *probe) {
    SnglProbe *p = sngl_probe_of(probe);
    return p != NULL ? p->content : -1;
}

void sngl_probe_reset(GtkWidget *probe, int across) {
    SnglProbe *p = sngl_probe_of(probe);
    if (p == NULL) return;
    p->across = across != 0;
    p->content = -1;
    gtk_widget_queue_allocate(probe);
}

void sngl_probe_on_change(GtkWidget *probe, SnglFn fn, void *ctx, GDestroyNotify release) {
    SnglProbe *p = sngl_probe_of(probe);
    if (p == NULL) {
        if (release) release(ctx);
        return;
    }
    if (p->changed.release) p->changed.release(p->changed.ctx);
    p->changed.fn = fn;
    p->changed.ctx = ctx;
    p->changed.release = release;
}

/* content, a child the scroll's window was given, goes in the probe, which
 * becomes the window's child in its place. */
void sngl_probe_hold(GtkWidget *sw, GtkWidget *probe, GtkWidget *content) {
    SnglProbe *p = sngl_probe_of(probe);
    if (p == NULL) return;
    g_object_ref(content);
    for (GtkWidget *k = gtk_widget_get_first_child(probe); k != NULL; k = gtk_widget_get_first_child(probe)) {
        gtk_box_remove(GTK_BOX(probe), k);
    }
    p->content = -1;
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(sw), probe);
    gtk_box_append(GTK_BOX(probe), content);
    g_object_unref(content);
}

void sngl_scroll_set_child(GtkWidget *sw, GtkWidget *child) {
    GtkWidget *probe = g_object_get_data(G_OBJECT(sw), "sngl-probe");
    if (probe != NULL && child != NULL && gtk_widget_get_parent(child) == probe) return;
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(sw), child);
}
