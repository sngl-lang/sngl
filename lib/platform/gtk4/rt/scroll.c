/* A scroll is what a `ui.scroll` is on gtk4: a GtkScrolledWindow that reports
 * the end of its content coming within half a viewport of showing -- as it
 * arrives, and again when the content changes size while it is there. */
#include "sngl_gtk.h"

#include <string.h>

typedef struct {
    GtkWidget *sw;
    SnglFn fn;
    void *ctx;
    GDestroyNotify release;
    bool near;
    /* content is the content's natural length along the axis the end is
     * read on when last measured: -1 before it has been. */
    int content;
    /* asked says a report is posted and has not run. */
    guint asked;
} SnglScroll;

static SnglScroll *sngl_scroll_of(GtkWidget *sw) {
    return g_object_get_data(G_OBJECT(sw), "sngl-scroll");
}

static void sngl_scroll_free(gpointer data) {
    SnglScroll *s = data;
    if (s->asked) g_source_remove(s->asked);
    if (s->release) s->release(s->ctx);
    g_free(s);
}

static int sngl_scroll_axis(GtkWidget *sw) {
    return GPOINTER_TO_INT(g_object_get_data(G_OBJECT(sw), "sngl-axis"));
}

static void sngl_scroll_measure(GtkWidget *sw, double *upper, double *value, double *page) {
    GtkAdjustment *a = sngl_scroll_axis(sw) == 1
        ? gtk_scrolled_window_get_hadjustment(GTK_SCROLLED_WINDOW(sw))
        : gtk_scrolled_window_get_vadjustment(GTK_SCROLLED_WINDOW(sw));
    *upper = gtk_adjustment_get_upper(a);
    *value = gtk_adjustment_get_value(a);
    *page = gtk_adjustment_get_page_size(a);
}

static int sngl_probe_content(GtkWidget *sw) {
    GtkWidget *probe = g_object_get_data(G_OBJECT(sw), "sngl-probe");
    return probe != NULL ? GPOINTER_TO_INT(g_object_get_data(G_OBJECT(probe), "sngl-content")) : -1;
}

static gboolean sngl_scroll_report(gpointer data) {
    SnglScroll *s = data;
    s->asked = 0;
    if (s->fn) s->fn(s->ctx);
    return G_SOURCE_REMOVE;
}

/* The handler runs on the next idle tick rather than here: the adjustment
 * changes while GTK allocates, and a handler adding rows then would change
 * the tree mid-allocation. */
static void sngl_scroll_changed(SnglScroll *s) {
    double upper, value, page;
    sngl_scroll_measure(s->sw, &upper, &value, &page);
    if (page <= 0) return;
    bool near = upper - (value + page) <= page / 2;
    int content = sngl_probe_content(s->sw);
    bool grew = content > s->content;
    bool fire = near && (!s->near || grew);
    s->near = near;
    s->content = content;
    if (!fire || s->asked) return;
    s->asked = g_idle_add(sngl_scroll_report, s);
}

/* data is the scrolled window, which the connection lives no longer than. */
static void sngl_scroll_adj(GtkAdjustment *a, gpointer data) {
    (void)a;
    SnglScroll *s = sngl_scroll_of(data);
    if (s != NULL) sngl_scroll_changed(s);
}

/* The probe's layout is gtk4.sngl's: a custom layout whose mode, measure
 * and allocate are SNGL functions GTK calls. What they keep on the probe is
 * read and written here: sngl_probe_across is whether the scroll's end is
 * read along x, and sngl_probe_measured takes the content's natural length
 * along that axis as the probe was allocated, telling the scroll when it
 * changed. */
int sngl_probe_across(GtkWidget *w) {
    return g_object_get_data(G_OBJECT(w), "sngl-across") != NULL;
}

void sngl_probe_measured(GtkWidget *w, int nat) {
    if (nat != GPOINTER_TO_INT(g_object_get_data(G_OBJECT(w), "sngl-content"))) {
        g_object_set_data(G_OBJECT(w), "sngl-content", GINT_TO_POINTER(nat));
        SnglScroll *s = g_object_get_data(G_OBJECT(w), "sngl-changed");
        if (s != NULL) sngl_scroll_changed(s);
    }
}

static void sngl_scroll_policies(GtkScrolledWindow *sw) {
    int axis = sngl_scroll_axis(GTK_WIDGET(sw));
    gtk_scrolled_window_set_policy(sw, axis == 0 ? GTK_POLICY_EXTERNAL : GTK_POLICY_AUTOMATIC,
        axis == 1 ? GTK_POLICY_NEVER : GTK_POLICY_AUTOMATIC);
    gtk_scrolled_window_set_propagate_natural_width(sw, axis == 0);
    GtkWidget *c = gtk_scrolled_window_get_child(sw);
    if (c != NULL && !GTK_IS_SCROLLABLE(c)) c = gtk_widget_get_parent(c);
    if (c != NULL && GTK_IS_SCROLLABLE(c)) {
        gtk_scrollable_set_vscroll_policy(GTK_SCROLLABLE(c), axis != 1 ? GTK_SCROLL_NATURAL : GTK_SCROLL_MINIMUM);
        gtk_scrollable_set_hscroll_policy(GTK_SCROLLABLE(c), axis != 0 ? GTK_SCROLL_NATURAL : GTK_SCROLL_MINIMUM);
    }
}

/* A child set that is not the probe is the content, and goes in the probe,
 * which becomes the child. */
static void sngl_scroll_child(GObject *o, GParamSpec *ps, gpointer data) {
    (void)ps; (void)data;
    GtkScrolledWindow *sw = GTK_SCROLLED_WINDOW(o);
    GtkWidget *probe = g_object_get_data(o, "sngl-probe");
    sngl_scroll_policies(sw);
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

GtkWidget *sngl_scroll_new(GtkLayoutManager *layout) {
    GtkWidget *sw = gtk_scrolled_window_new();
    SnglScroll *s = g_new0(SnglScroll, 1);
    s->sw = sw;
    s->content = -1;
    g_object_set_data_full(G_OBJECT(sw), "sngl-scroll", s, sngl_scroll_free);
    GtkWidget *probe = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    gtk_widget_set_layout_manager(probe, layout);
    g_object_set_data(G_OBJECT(probe), "sngl-changed", s);
    g_object_set_data(G_OBJECT(probe), "sngl-content", GINT_TO_POINTER(-1));
    g_object_set_data_full(G_OBJECT(sw), "sngl-probe", g_object_ref_sink(probe), g_object_unref);
    g_signal_connect(sw, "notify::child", G_CALLBACK(sngl_scroll_child), NULL);
    GtkAdjustment *adj[2] = {
        gtk_scrolled_window_get_vadjustment(GTK_SCROLLED_WINDOW(sw)),
        gtk_scrolled_window_get_hadjustment(GTK_SCROLLED_WINDOW(sw)),
    };
    for (int i = 0; i < 2; i++) {
        g_signal_connect_object(adj[i], "changed", G_CALLBACK(sngl_scroll_adj), sw, 0);
        g_signal_connect_object(adj[i], "value-changed", G_CALLBACK(sngl_scroll_adj), sw, 0);
    }
    sngl_scroll_policies(GTK_SCROLLED_WINDOW(sw));
    return sw;
}

void sngl_scroll_set_child(GtkWidget *sw, GtkWidget *child) {
    GtkWidget *probe = g_object_get_data(G_OBJECT(sw), "sngl-probe");
    if (probe != NULL && child != NULL && gtk_widget_get_parent(child) == probe) return;
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(sw), child);
}

void sngl_scroll_set_direction(GtkWidget *sw, const char *dir) {
    int axis = 0;
    if (strcmp(dir, "horizontal") == 0) {
        axis = 1;
    } else if (strcmp(dir, "both") == 0) {
        axis = 2;
    }
    g_object_set_data(G_OBJECT(sw), "sngl-axis", GINT_TO_POINTER(axis));
    sngl_scroll_policies(GTK_SCROLLED_WINDOW(sw));
    SnglScroll *s = sngl_scroll_of(sw);
    if (s != NULL) {
        s->near = false;
        s->content = -1;
    }
    GtkWidget *probe = g_object_get_data(G_OBJECT(sw), "sngl-probe");
    if (probe == NULL) return;
    g_object_set_data(G_OBJECT(probe), "sngl-across", axis == 1 ? GINT_TO_POINTER(1) : NULL);
    g_object_set_data(G_OBJECT(probe), "sngl-content", GINT_TO_POINTER(-1));
    gtk_widget_queue_allocate(probe);
}

void sngl_scroll_on_reach_end(GtkWidget *sw, SnglFn fn, void *ctx, GDestroyNotify release) {
    SnglScroll *s = sngl_scroll_of(sw);
    if (s == NULL) {
        if (release) release(ctx);
        return;
    }
    if (s->release) s->release(s->ctx);
    s->fn = fn;
    s->ctx = ctx;
    s->release = release;
}

void sngl_scroll_reach_end(GtkWidget *sw) {
    SnglScroll *s = sngl_scroll_of(sw);
    if (s != NULL && s->fn) s->fn(s->ctx);
}
