#include "sngl_gtk.h"

#include <string.h>

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
    SnglKeyFn on_key;
    void *key_ctx;
    GDestroyNotify key_release;
} SnglToplevel;

static GtkApplication *sngl_app;
static GPtrArray *sngl_attached;
static bool sngl_started;

static void sngl_toplevel_free(gpointer data) {
    SnglToplevel *t = data;
    if (t->closed_release) t->closed_release(t->closed_ctx);
    if (t->visible_release) t->visible_release(t->visible_ctx);
    if (t->key_release) t->key_release(t->key_ctx);
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

static bool sngl_top_hear(const char *key, bool ctrl, bool alt, bool shift, bool meta, bool editing, void *data);

/* An Escape nothing took goes back while the window can. */
static bool sngl_top_back(void *data) {
    SnglToplevel *t = data;
    return sngl_window_back(t->box);
}

static void sngl_toplevel_create(SnglToplevel *t) {
    if (t->win) return;
    t->win = GTK_WINDOW(gtk_application_window_new(sngl_app));
    gtk_window_set_default_size(t->win, 480, 640);
    if (t->title) gtk_window_set_title(t->win, t->title);
    gtk_window_set_child(t->win, t->box);
    g_signal_connect(t->win, "close-request", G_CALLBACK(sngl_close_request), t);
    sngl_keys_watch(GTK_WIDGET(t->win), sngl_top_hear, sngl_top_back, t);
    sngl_menu_apply(t->box);
    sngl_toplevel_icon_refresh(t->box);
}

GtkWindow *sngl_toplevel_window(GtkWidget *box) {
    SnglToplevel *t = sngl_top(box);
    return t ? t->win : NULL;
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

typedef struct {
    int w, h;
    const char *mode;
    SnglDrawFn draw;
    void *data;
} SnglCanvas;

/* The drawing's own coordinate space mapped onto the size GTK is drawing
 * at, so the shapes are rasterized where they land rather than drawn at one
 * size and stretched from it, as gtk4rt's _snglCairoScale does. */
static void sngl_canvas_draw(GtkDrawingArea *area, cairo_t *cr, int pw, int ph, gpointer p) {
    SnglCanvas *c = p;
    if (c->w > 0 && c->h > 0 && c->mode[0] != '\0') {
        double sx = (double)pw / c->w, sy = (double)ph / c->h;
        if (strcmp(c->mode, "stretch") == 0) {
        } else if (strcmp(c->mode, "fill") == 0) {
            if (sy > sx) sx = sy;
            sy = sx;
        } else {
            if (sy < sx) sx = sy;
            sy = sx;
        }
        cairo_scale(cr, sx, sy);
    }
    c->draw(cr, c->data);
}

GtkWidget *sngl_canvas_new(int w, int h, const char *mode, SnglDrawFn draw, void *data) {
    GtkWidget *a = gtk_drawing_area_new();
    gtk_drawing_area_set_content_width(GTK_DRAWING_AREA(a), w);
    gtk_drawing_area_set_content_height(GTK_DRAWING_AREA(a), h);
    if (mode[0] != '\0' && strcmp(mode, "center") != 0) {
        gtk_widget_set_hexpand(a, TRUE);
        gtk_widget_set_vexpand(a, TRUE);
    }
    SnglCanvas *c = g_new0(SnglCanvas, 1);
    c->w = w;
    c->h = h;
    c->mode = mode;
    c->draw = draw;
    c->data = data;
    gtk_drawing_area_set_draw_func(GTK_DRAWING_AREA(a), sngl_canvas_draw, c, g_free);
    return a;
}

typedef struct {
    SnglFn fn;
    void *ctx;
    GDestroyNotify release;
} SnglHandler;

static void sngl_handler_free(gpointer data, GClosure *closure) {
    SnglHandler *h = data;
    if (h->release) h->release(h->ctx);
    g_free(h);
}

static void sngl_click_pressed(GtkGestureClick *g, int n, double x, double y, gpointer data) {
    SnglHandler *h = data;
    h->fn(h->ctx);
}

void sngl_canvas_on_click(GtkWidget *w, SnglFn fn, void *ctx, GDestroyNotify release) {
    GtkGesture *g = gtk_gesture_click_new();
    SnglHandler *h = g_new0(SnglHandler, 1);
    h->fn = fn;
    h->ctx = ctx;
    h->release = release;
    g_signal_connect_data(g, "pressed", G_CALLBACK(sngl_click_pressed), h, sngl_handler_free, 0);
    gtk_widget_add_controller(w, GTK_EVENT_CONTROLLER(g));
    g_object_set_data(G_OBJECT(w), "sngl-click", h);
}

void sngl_canvas_click(GtkWidget *w) {
    SnglHandler *h = g_object_get_data(G_OBJECT(w), "sngl-click");
    if (h) h->fn(h->ctx);
}

static gboolean sngl_idle_run(gpointer data) {
    SnglHandler *h = data;
    h->fn(h->ctx);
    return G_SOURCE_REMOVE;
}

static void sngl_idle_free(gpointer data) {
    SnglHandler *h = data;
    if (h->release) h->release(h->ctx);
    g_free(h);
}

void sngl_idle(SnglFn fn, void *ctx, GDestroyNotify release) {
    SnglHandler *h = g_new0(SnglHandler, 1);
    h->fn = fn;
    h->ctx = ctx;
    h->release = release;
    g_idle_add_full(G_PRIORITY_DEFAULT_IDLE, sngl_idle_run, h, sngl_idle_free);
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
        sngl_bar_forget(box);
        g_object_unref(box);
    }
}

/* sngl_slot_host is the box a slot rendering into w puts its entries in:
 * w, a box, or for a container holding one child -- a frame, a button, a
 * scroll, a window -- a box it holds as that child, made the first time. */
static GtkWidget *sngl_slot_host(GtkWidget *w) {
    if (w == SNGL_APP || GTK_IS_BOX(w)) return w;
    GtkWidget *b = g_object_get_data(G_OBJECT(w), "sngl-slot-box");
    if (b == NULL) {
        b = gtk_box_new(GTK_ORIENTATION_VERTICAL, 6);
        g_object_set_data_full(G_OBJECT(w), "sngl-slot-box", g_object_ref_sink(b), g_object_unref);
    }
    if (gtk_widget_get_parent(b) != NULL) return b;
    if (GTK_IS_SCROLLED_WINDOW(w)) {
        sngl_scroll_set_child(w, b);
    } else if (GTK_IS_FRAME(w)) {
        gtk_frame_set_child(GTK_FRAME(w), b);
    } else if (GTK_IS_BUTTON(w)) {
        gtk_button_set_child(GTK_BUTTON(w), b);
    } else if (GTK_IS_WINDOW(w)) {
        gtk_window_set_child(GTK_WINDOW(w), b);
    } else {
        g_critical("a slot renders into a %s, which holds no box", G_OBJECT_TYPE_NAME(w));
        return w;
    }
    return b;
}

void sngl_slot_anchor(GtkWidget *box, GtkWidget **anchor) {
    if (box == SNGL_APP) {
        sngl_hold(anchor, NULL);
        return;
    }
    box = sngl_slot_host(box);
    if (*anchor != NULL && gtk_widget_get_parent(*anchor) == box) return;
    GtkWidget *w = gtk_label_new(NULL);
    gtk_widget_set_visible(w, FALSE);
    gtk_box_append(GTK_BOX(box), w);
    sngl_hold(anchor, w);
}

void sngl_box_append(GtkWidget *box, GtkWidget *child) {
    if (box == SNGL_APP) {
        sngl_app_attach(child);
        return;
    }
    if (sngl_tabs_add(box, child) || sngl_image_adopt(box, child)) return;
    box = sngl_slot_host(box);
    gtk_box_append(GTK_BOX(box), child);
    sngl_menu_moved(box, child);
}

void sngl_box_insert_before(GtkWidget *box, GtkWidget *anchor, GtkWidget *child) {
    box = sngl_slot_host(box);
    if (box == SNGL_APP || anchor == NULL || gtk_widget_get_parent(anchor) != box) {
        sngl_box_append(box, child);
        return;
    }
    if (sngl_tabs_add(box, child) || sngl_image_adopt(box, child)) return;
    gtk_box_insert_child_after(GTK_BOX(box), child, gtk_widget_get_prev_sibling(anchor));
    sngl_menu_moved(box, child);
}

void sngl_box_remove(GtkWidget *box, GtkWidget *child) {
    if (box == SNGL_APP) {
        sngl_app_detach(child);
        return;
    }
    /* What the box let go of may be gone once it has, and the bar and the
     * tabs ask what it was. */
    g_object_ref(child);
    if (!sngl_tabs_remove(box, child) && !sngl_image_unadopt(box, child)) {
        box = sngl_slot_host(box);
        gtk_box_remove(GTK_BOX(box), child);
        sngl_menu_moved(box, child);
    }
    g_object_unref(child);
}

void sngl_hold(GtkWidget **slot, GtkWidget *w) {
    if (*slot == w) return;
    if (*slot) g_object_remove_weak_pointer(G_OBJECT(*slot), (gpointer *)slot);
    *slot = w;
    if (w) g_object_add_weak_pointer(G_OBJECT(w), (gpointer *)slot);
}

void sngl_copy_drop(GHashTable *copies, GtkWidget *w) {
    if (copies == NULL) return;
    GHashTableIter it;
    gpointer k, v;
    g_hash_table_iter_init(&it, copies);
    while (g_hash_table_iter_next(&it, &k, &v)) {
        if (v == w) g_hash_table_iter_remove(&it);
    }
}

/* A widget gone is in no table, whatever let go of it. */
static void sngl_copy_gone(gpointer copies, GObject *w) {
    sngl_copy_drop(copies, (GtkWidget *)w);
}

void sngl_copy_put(GHashTable **copies, const char *key, GtkWidget *w) {
    if (*copies == NULL) *copies = g_hash_table_new_full(g_str_hash, g_str_equal, g_free, NULL);
    sngl_copy_drop(*copies, w);
    if (g_object_get_data(G_OBJECT(w), "sngl-copies") != *copies) {
        g_object_set_data(G_OBJECT(w), "sngl-copies", *copies);
        g_object_weak_ref(G_OBJECT(w), sngl_copy_gone, *copies);
    }
    g_hash_table_insert(*copies, g_strdup(key), w);
}

GtkWidget *sngl_copy_get(GHashTable *copies, const char *key) {
    return copies ? g_hash_table_lookup(copies, key) : NULL;
}

GtkWidget *sngl_flow_new(void) {
    GtkWidget *l = gtk_label_new("");
    gtk_label_set_wrap(GTK_LABEL(l), TRUE);
    gtk_label_set_xalign(GTK_LABEL(l), 0);
    return l;
}

void sngl_editable_set_text_quiet(GtkWidget *e, const char *t) {
    guint id = g_signal_lookup("changed", GTK_TYPE_EDITABLE);
    g_signal_handlers_block_matched(e, G_SIGNAL_MATCH_ID, id, 0, NULL, NULL, NULL);
    gtk_editable_set_text(GTK_EDITABLE(e), t);
    g_signal_handlers_unblock_matched(e, G_SIGNAL_MATCH_ID, id, 0, NULL, NULL, NULL);
}

/* What a stop the handler calls does: notes the key stopped, and calls the
 * test's own stop where a test pressed it. */
typedef struct {
    bool stopped;
    SnglFn then;
    void *then_ctx;
} SnglStop;

static void sngl_stop(void *data) {
    SnglStop *s = data;
    s->stopped = true;
    if (s->then) s->then(s->then_ctx);
}

/* sngl_hear runs the window's `@key`, and is whether its handler stopped the
 * key. */
static bool sngl_hear(SnglToplevel *t, const char *key, bool ctrl, bool alt, bool shift, bool meta, bool editing,
        SnglFn then, void *then_ctx) {
    if (!t || !t->on_key) return false;
    SnglStop s = {false, then, then_ctx};
    t->on_key(key, ctrl, alt, shift, meta, editing, sngl_stop, &s, t->key_ctx);
    return s.stopped;
}

static bool sngl_top_hear(const char *key, bool ctrl, bool alt, bool shift, bool meta, bool editing, void *data) {
    return sngl_hear(data, key, ctrl, alt, shift, meta, editing, NULL, NULL);
}

void sngl_toplevel_on_key(GtkWidget *box, SnglKeyFn fn, void *ctx, GDestroyNotify release) {
    SnglToplevel *t = sngl_top(box);
    if (!t) {
        if (release) release(ctx);
        return;
    }
    if (t->key_release) t->key_release(t->key_ctx);
    t->on_key = fn;
    t->key_ctx = ctx;
    t->key_release = release;
}

/* An Escape the handler leaves goes back while the window can. */
void sngl_toplevel_key(GtkWidget *box, const char *key, bool ctrl, bool alt, bool shift, bool meta, bool editing,
        SnglFn stop, void *stop_ctx, GDestroyNotify stop_release) {
    SnglToplevel *t = sngl_top(box);
    bool stopped = sngl_hear(t, key, ctrl, alt, shift, meta, editing, stop, stop_ctx);
    if (stop_release) stop_release(stop_ctx);
    if (stopped || !t || ctrl || alt || meta) return;
    if (strcmp(key, "Escape") == 0) {
        sngl_window_back(box);
        return;
    }
    if (!t->win) return;
    sngl_keys_go_on(t->win, key, shift);
}

void sngl_flip(GtkWidget *w, bool v, const char *signal, const char *prop) {
    bool on = GTK_IS_SWITCH(w) ? gtk_switch_get_active(GTK_SWITCH(w)) : gtk_check_button_get_active(GTK_CHECK_BUTTON(w));
    if (on != v) {
        if (GTK_IS_SWITCH(w)) {
            gtk_switch_set_active(GTK_SWITCH(w), v);
        } else {
            gtk_check_button_set_active(GTK_CHECK_BUTTON(w), v);
        }
    } else if (prop != NULL) {
        g_object_notify(G_OBJECT(w), prop);
    } else {
        g_signal_emit_by_name(w, signal);
    }
}
