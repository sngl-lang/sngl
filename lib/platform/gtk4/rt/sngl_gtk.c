#include "sngl_gtk.h"

/* A toplevel's state, held on its box. */
typedef struct {
    GtkWidget *box;
    char *title;
    bool visible;
    bool attached;
    GtkWindow *win;
    SnglFn on_closed;
    void *closed_ctx;
    GDestroyNotify closed_release;
    SnglBoolFn on_visible;
    void *visible_ctx;
    GDestroyNotify visible_release;
} SnglToplevel;

static GtkApplication *sngl_app;
static GPtrArray *sngl_attached;
static bool sngl_started;

static void sngl_toplevel_free(gpointer data) {
    SnglToplevel *t = data;
    if (t->closed_release) t->closed_release(t->closed_ctx);
    if (t->visible_release) t->visible_release(t->visible_ctx);
    if (sngl_attached) g_ptr_array_remove(sngl_attached, t);
    g_free(t->title);
    g_free(t);
}

static SnglToplevel *sngl_top(GtkWidget *box) {
    return box ? g_object_get_data(G_OBJECT(box), "sngl-toplevel") : NULL;
}

GtkWidget *sngl_toplevel_new(void) {
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 6);
    SnglToplevel *t = g_new0(SnglToplevel, 1);
    t->box = box;
    t->visible = true;
    g_object_set_data_full(G_OBJECT(box), "sngl-toplevel", t, sngl_toplevel_free);
    return box;
}

void sngl_toplevel_set_title(GtkWidget *box, const char *title) {
    SnglToplevel *t = sngl_top(box);
    if (!t) return;
    g_free(t->title);
    t->title = g_strdup(title);
    if (t->win) gtk_window_set_title(t->win, title);
}

void sngl_toplevel_set_visible(GtkWidget *box, bool visible) {
    SnglToplevel *t = sngl_top(box);
    if (!t || t->visible == visible) return;
    t->visible = visible;
    if (!t->win) return;
    if (visible) {
        gtk_window_present(t->win);
    } else {
        gtk_widget_set_visible(GTK_WIDGET(t->win), FALSE);
    }
}

void sngl_toplevel_on_closed(GtkWidget *box, SnglFn fn, void *ctx, GDestroyNotify release) {
    SnglToplevel *t = sngl_top(box);
    if (!t) return;
    if (t->closed_release) t->closed_release(t->closed_ctx);
    t->on_closed = fn;
    t->closed_ctx = ctx;
    t->closed_release = release;
}

void sngl_toplevel_on_visible(GtkWidget *box, SnglBoolFn fn, void *ctx, GDestroyNotify release) {
    SnglToplevel *t = sngl_top(box);
    if (!t) return;
    if (t->visible_release) t->visible_release(t->visible_ctx);
    t->on_visible = fn;
    t->visible_ctx = ctx;
    t->visible_release = release;
}

void sngl_toplevel_append(GtkWidget *box, GtkWidget *child) {
    gtk_box_append(GTK_BOX(box), child);
}

/* Whether any attached toplevel has a window on screen. */
static bool sngl_any_on_screen(void) {
    for (guint i = 0; sngl_attached && i < sngl_attached->len; i++) {
        SnglToplevel *t = g_ptr_array_index(sngl_attached, i);
        if (t->win && gtk_widget_get_visible(GTK_WIDGET(t->win))) return true;
    }
    return false;
}

/* The window manager's close: reported as the window going off screen, then
 * as `@closed`; a close that leaves nothing on screen ends the program.
 * TRUE stops GTK destroying the window, which the program's tree decides. */
static gboolean sngl_close_request(GtkWindow *win, gpointer data) {
    SnglToplevel *t = data;
    gtk_widget_set_visible(GTK_WIDGET(win), FALSE);
    if (t->visible) {
        t->visible = false;
        if (t->on_visible) t->on_visible(false, t->visible_ctx);
    }
    if (t->on_closed) t->on_closed(t->closed_ctx);
    if (!sngl_any_on_screen()) g_application_quit(G_APPLICATION(sngl_app));
    return TRUE;
}

static void sngl_toplevel_create(SnglToplevel *t) {
    if (t->win) return;
    t->win = GTK_WINDOW(gtk_application_window_new(sngl_app));
    gtk_window_set_default_size(t->win, 480, 640);
    if (t->title) gtk_window_set_title(t->win, t->title);
    gtk_window_set_child(t->win, t->box);
    g_signal_connect(t->win, "close-request", G_CALLBACK(sngl_close_request), t);
}

void sngl_app_attach(GtkWidget *box) {
    SnglToplevel *t = sngl_top(box);
    if (!t || t->attached) return;
    t->attached = true;
    if (!sngl_attached) sngl_attached = g_ptr_array_new();
    g_ptr_array_add(sngl_attached, t);
    if (sngl_started) {
        sngl_toplevel_create(t);
        if (t->visible) gtk_window_present(t->win);
    }
}

/* What builds the program's tree, which C cannot hand GLib as its data. */
static void (*sngl_build)(void);

static void sngl_activate(GtkApplication *app, gpointer data) {
    g_application_hold(G_APPLICATION(app));
    sngl_build();
    sngl_started = true;
    for (guint i = 0; sngl_attached && i < sngl_attached->len; i++) {
        SnglToplevel *t = g_ptr_array_index(sngl_attached, i);
        sngl_toplevel_create(t);
        if (t->visible) gtk_window_present(t->win);
    }
}

int sngl_app_run(void (*build)(void)) {
    sngl_app = gtk_application_new(NULL, G_APPLICATION_NON_UNIQUE);
    sngl_build = build;
    g_signal_connect(sngl_app, "activate", G_CALLBACK(sngl_activate), NULL);
    int status = g_application_run(G_APPLICATION(sngl_app), 0, NULL);
    g_object_unref(sngl_app);
    return status;
}

struct SnglSchedule {
    guint id;
    SnglFn fn;
    void *ctx;
    GDestroyNotify release;
};

static gboolean sngl_tick(gpointer data) {
    SnglSchedule *s = data;
    s->fn(s->ctx);
    return G_SOURCE_CONTINUE;
}

/* The source's own release, once it is removed: the callback's context,
 * then the schedule. */
static void sngl_schedule_free(gpointer data) {
    SnglSchedule *s = data;
    if (s->release) s->release(s->ctx);
    g_free(s);
}

SnglSchedule *sngl_every(int ms, SnglFn fn, void *ctx, GDestroyNotify release) {
    if (ms <= 0) {
        if (release) release(ctx);
        return NULL;
    }
    SnglSchedule *s = g_new0(SnglSchedule, 1);
    s->fn = fn;
    s->ctx = ctx;
    s->release = release;
    s->id = g_timeout_add_full(G_PRIORITY_DEFAULT, (guint)ms, sngl_tick, s, sngl_schedule_free);
    return s;
}

void sngl_schedule_cancel(SnglSchedule *s) {
    if (s) g_source_remove(s->id);
}

/* sngl_app_detach takes a toplevel off the application, its window
 * destroyed: a slot at the package's root no longer renders it. */
void sngl_app_detach(GtkWidget *box) {
    SnglToplevel *t = sngl_top(box);
    if (!t || !t->attached) return;
    t->attached = false;
    g_ptr_array_remove(sngl_attached, t);
    if (t->win) {
        GtkWindow *win = t->win;
        t->win = NULL;
        /* A reference across the unparent, so the box is freed by its last
         * holder rather than mid-call. */
        g_object_ref(box);
        gtk_window_set_child(win, NULL);
        gtk_window_destroy(win);
        g_object_unref(box);
    }
}

GtkWidget *sngl_slot_anchor(GtkWidget *box, GtkWidget *anchor) {
    if (box == SNGL_APP) return NULL;
    if (anchor != NULL && gtk_widget_get_parent(anchor) == box) return anchor;
    GtkWidget *w = gtk_label_new(NULL);
    gtk_widget_set_visible(w, FALSE);
    gtk_box_append(GTK_BOX(box), w);
    return w;
}

void sngl_box_insert_before(GtkWidget *box, GtkWidget *anchor, GtkWidget *child) {
    if (box == SNGL_APP) {
        sngl_app_attach(child);
    } else if (anchor == NULL || gtk_widget_get_parent(anchor) != box) {
        gtk_box_append(GTK_BOX(box), child);
    } else {
        gtk_box_insert_child_after(GTK_BOX(box), child, gtk_widget_get_prev_sibling(anchor));
    }
}

void sngl_box_remove(GtkWidget *box, GtkWidget *child) {
    if (box == SNGL_APP) {
        sngl_app_detach(child);
    } else {
        gtk_box_remove(GTK_BOX(box), child);
    }
}
