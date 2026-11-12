#include "sngl_gtk.h"

#include <string.h>

#ifdef G_OS_UNIX
#include <glib-unix.h>
#include <signal.h>
#endif

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
    SnglFn on_key;
    void *key_ctx;
    GDestroyNotify key_release;
} SnglToplevel;

static GtkApplication *sngl_app;
static GPtrArray *sngl_attached;
static bool sngl_started;
/* Whether the windows are held hidden as the loop starts: an `@run` handler
 * that has not called run. */
static bool sngl_holding;

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

void sngl_toplevel_set_visible(GtkWidget *box, int visible) {
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

/* A toplevel's content is a box's, whose append hands the window an entry
 * of its menu or its way back. */
void sngl_toplevel_append(GtkWidget *box, GtkWidget *child) {
    sngl_box_append(box, child);
}

void sngl_toplevel_insert_before(GtkWidget *box, GtkWidget *anchor, GtkWidget *child) {
    sngl_box_insert_before(box, anchor, child);
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
    if (!sngl_any_on_screen() && sngl_app) g_application_quit(G_APPLICATION(sngl_app));
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
    /* A test draws a window no application runs. */
    t->win = GTK_WINDOW(sngl_app ? gtk_application_window_new(sngl_app) : gtk_window_new());
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

/* The toplevel a node attached that is none is shown in: a test's component,
 * a program with no window. Made the first time it is attached. */
static GtkWidget *sngl_wrapper(GtkWidget *box, bool make) {
    GtkWidget *top = g_object_get_data(G_OBJECT(box), "sngl-wrapper");
    if (top == NULL && make) {
        top = sngl_toplevel_new();
        sngl_toplevel_append(top, box);
        g_object_set_data(G_OBJECT(box), "sngl-wrapper", top);
        g_object_set_data(G_OBJECT(top), "sngl-wraps", box);
    }
    return top;
}

/* The windows a box holds: what a component renders at its top beside, or
 * instead of, its other nodes, which is no child of the box but a toplevel
 * of the application, attached as it is added and detached as it is
 * removed. The box keeps them, so that its own attach and detach, a root
 * component's or a test's, reach them. */
static GPtrArray *sngl_windows_of(GtkWidget *box, bool make) {
    if (box == SNGL_APP) return NULL;
    GPtrArray *ws = g_object_get_data(G_OBJECT(box), "sngl-windows");
    if (ws == NULL && make) {
        ws = g_ptr_array_new_with_free_func(g_object_unref);
        g_object_set_data_full(G_OBJECT(box), "sngl-windows", ws, (GDestroyNotify)g_ptr_array_unref);
    }
    return ws;
}

static bool sngl_window_add(GtkWidget *box, GtkWidget *child) {
    if (box == SNGL_APP || !sngl_top(child)) return false;
    g_ptr_array_add(sngl_windows_of(box, true), g_object_ref_sink(child));
    sngl_app_attach(child);
    return true;
}

static bool sngl_window_remove(GtkWidget *box, GtkWidget *child) {
    GPtrArray *ws = sngl_windows_of(box, false);
    if (ws == NULL || !sngl_top(child) || !g_ptr_array_find(ws, child, NULL)) return false;
    sngl_app_detach(child);
    g_ptr_array_remove(ws, child);
    return true;
}

void sngl_app_attach(GtkWidget *box) {
    GPtrArray *ws = sngl_top(box) ? NULL : sngl_windows_of(box, false);
    if (ws != NULL) {
        /* A box of windows is shown as they are, in no wrapper, and kept
         * while it is attached. */
        if (g_object_get_data(G_OBJECT(box), "sngl-app-holds") == NULL) {
            g_object_set_data(G_OBJECT(box), "sngl-app-holds", g_object_ref_sink(box));
        }
        for (guint i = 0; i < ws->len; i++) sngl_app_attach(g_ptr_array_index(ws, i));
        return;
    }
    if (!sngl_top(box)) box = sngl_wrapper(box, true);
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
        if (t->visible && sngl_holding) {
            /* Held, which the window's `visible` hears. */
            t->visible = false;
            if (t->on_visible) t->on_visible(false, t->visible_ctx);
        }
        if (t->visible) gtk_window_present(t->win);
    }
}

#ifdef G_OS_UNIX
/* SIGINT and SIGTERM end the loop, so the application tears down as it does
 * when its last window closes: ctrl+c, or a process asked to stop. */
static gboolean sngl_on_signal(gpointer data) {
    sngl_gtk_quit();
    return G_SOURCE_CONTINUE;
}
#endif

int sngl_app_run(void (*build)(void)) {
    sngl_app = gtk_application_new(NULL, G_APPLICATION_NON_UNIQUE);
    sngl_build = build;
    g_signal_connect(sngl_app, "activate", G_CALLBACK(sngl_activate), NULL);
#ifdef G_OS_UNIX
    guint sigint = g_unix_signal_add(SIGINT, sngl_on_signal, NULL);
    guint sigterm = g_unix_signal_add(SIGTERM, sngl_on_signal, NULL);
#endif
    int status = g_application_run(G_APPLICATION(sngl_app), 0, NULL);
#ifdef G_OS_UNIX
    g_source_remove(sigint);
    g_source_remove(sigterm);
#endif
    g_object_unref(sngl_app);
    sngl_app = NULL;
    return status;
}

void sngl_gtk_hold(void) {
    sngl_holding = true;
    gtk_init();
}

void sngl_gtk_present(void) {
    sngl_holding = false;
}

static void sngl_built(void) {
}

int sngl_gtk_run(void) {
    return sngl_app_run(sngl_built);
}

void sngl_gtk_quit(void) {
    if (sngl_app) g_application_quit(G_APPLICATION(sngl_app));
}

/* What a language whose build is a closure runs the application with: its
 * build and the context it closes over, released once the loop ends. */
static SnglFn sngl_main_fn;
static void *sngl_main_ctx;

static void sngl_main_build(void) {
    sngl_main_fn(sngl_main_ctx);
}

void sngl_gtk_init(void) {
    gtk_init();
}

/* Runs what the loop has ready, or waits up to a millisecond for it: how a
 * Go test lets the loop run while it waits. */
void sngl_gtk_step(void) {
    if (!g_main_context_iteration(NULL, FALSE)) g_usleep(1000);
}

int sngl_gtk_main(SnglFn build, void *ctx, GDestroyNotify release) {
    sngl_main_fn = build;
    sngl_main_ctx = ctx;
    int status = sngl_app_run(sngl_main_build);
    if (release) release(ctx);
    return status;
}

/* A canvas's drawing is the handler its paint is: the program's shapes, as
 * the calls that paint them, handed the context. */
typedef struct {
    int w, h;
    int mode;
    SnglDrawFn draw;
    void *data;
    GDestroyNotify release;
} SnglCanvas;

static SnglCanvas *sngl_canvas_of(GtkWidget *w) {
    return w ? g_object_get_data(G_OBJECT(w), "sngl-canvas") : NULL;
}

static void sngl_canvas_free(gpointer p) {
    SnglCanvas *c = p;
    if (c->release) c->release(c->data);
    g_free(c);
}

/* The drawing's own coordinate space mapped onto the size GTK is drawing
 * at, so the shapes are rasterized where they land rather than drawn at one
 * size and stretched from it. `center` and `fit` scale it evenly until the
 * whole of it is inside -- a centered canvas is given its own size, so that
 * is no scale -- `fill` until none of the room is left over, and `stretch`
 * each axis to the edges. */
static void sngl_canvas_draw(GtkDrawingArea *area, cairo_t *cr, int pw, int ph, gpointer p) {
    SnglCanvas *c = p;
    if (!c->draw) return;
    if (c->w > 0 && c->h > 0) {
        double sx = (double)pw / c->w, sy = (double)ph / c->h;
        if (c->mode == 3) {
        } else if (c->mode == 2) {
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

GtkWidget *sngl_canvas_new(void) {
    GtkWidget *a = gtk_drawing_area_new();
    SnglCanvas *c = g_new0(SnglCanvas, 1);
    g_object_set_data_full(G_OBJECT(a), "sngl-canvas", c, sngl_canvas_free);
    gtk_drawing_area_set_draw_func(GTK_DRAWING_AREA(a), sngl_canvas_draw, c, NULL);
    return a;
}

void sngl_canvas_set_width(GtkWidget *w, int width) {
    SnglCanvas *c = sngl_canvas_of(w);
    if (!c) return;
    c->w = width;
    gtk_drawing_area_set_content_width(GTK_DRAWING_AREA(w), width);
}

void sngl_canvas_set_height(GtkWidget *w, int height) {
    SnglCanvas *c = sngl_canvas_of(w);
    if (!c) return;
    c->h = height;
    gtk_drawing_area_set_content_height(GTK_DRAWING_AREA(w), height);
}

/* A canvas that scales other than `center` expands to take the room. */
void sngl_canvas_set_scaling(GtkWidget *w, int mode) {
    SnglCanvas *c = sngl_canvas_of(w);
    if (!c) return;
    c->mode = mode;
    gtk_widget_set_hexpand(w, mode != 0);
    gtk_widget_set_vexpand(w, mode != 0);
    gtk_widget_queue_draw(w);
}

void sngl_canvas_on_paint(GtkWidget *w, SnglDrawFn draw, void *data, GDestroyNotify release) {
    SnglCanvas *c = sngl_canvas_of(w);
    if (!c) {
        if (release) release(data);
        return;
    }
    if (c->release) c->release(c->data);
    c->draw = draw;
    c->data = data;
    c->release = release;
    gtk_widget_queue_draw(w);
}

void sngl_canvas_redraw(GtkWidget *w) {
    gtk_widget_queue_draw(w);
}

/* What the stdlib's shapes set that cairo takes as an enum, or as a face
 * and a size: a line's cap and join in `LineCap` and `LineJoin`'s order,
 * which is cairo's, and a font of the normal slant and weight. */
void sngl_cairo_line_cap(cairo_t *cr, int cap) {
    cairo_set_line_cap(cr, (cairo_line_cap_t)cap);
}

void sngl_cairo_line_join(cairo_t *cr, int join) {
    cairo_set_line_join(cr, (cairo_line_join_t)join);
}

void sngl_cairo_font(cairo_t *cr, const char *family, double size) {
    cairo_select_font_face(cr, family, CAIRO_FONT_SLANT_NORMAL, CAIRO_FONT_WEIGHT_NORMAL);
    cairo_set_font_size(cr, size);
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

/* A GLib signal's handler and a property's notification, each handed only
 * its context: every signal a widget declares an event for is
 * (instance, user_data), and a notification (instance, pspec, user_data). */
static void sngl_signal_tramp(gpointer self, gpointer data) {
    SnglHandler *h = data;
    h->fn(h->ctx);
}

static void sngl_notify_tramp(gpointer self, GParamSpec *pspec, gpointer data) {
    SnglHandler *h = data;
    h->fn(h->ctx);
}

static void sngl_connect_with(GtkWidget *w, const char *signal, GCallback tramp, SnglFn fn, void *ctx,
                              GDestroyNotify release) {
    SnglHandler *h = g_new0(SnglHandler, 1);
    h->fn = fn;
    h->ctx = ctx;
    h->release = release;
    g_signal_connect_data(w, signal, tramp, h, sngl_handler_free, 0);
}

void sngl_signal_emit(GtkWidget *w, const char *signal) {
    g_signal_emit_by_name(w, signal);
}

void sngl_signal_connect(GtkWidget *w, const char *signal, SnglFn fn, void *ctx, GDestroyNotify release) {
    sngl_connect_with(w, signal, G_CALLBACK(sngl_signal_tramp), fn, ctx, release);
}

void sngl_notify_connect(GtkWidget *w, const char *signal, SnglFn fn, void *ctx, GDestroyNotify release) {
    sngl_connect_with(w, signal, G_CALLBACK(sngl_notify_tramp), fn, ctx, release);
}

static void sngl_click_pressed(GtkGestureClick *g, int n, double x, double y, gpointer data) {
    SnglHandler *h = data;
    h->fn(h->ctx);
}

void sngl_on_click(GtkWidget *w, SnglFn fn, void *ctx, GDestroyNotify release) {
    SnglHandler *h = g_object_get_data(G_OBJECT(w), "sngl-click");
    if (h) {
        if (h->release) h->release(h->ctx);
        h->fn = fn;
        h->ctx = ctx;
        h->release = release;
        return;
    }
    GtkGesture *g = gtk_gesture_click_new();
    h = g_new0(SnglHandler, 1);
    h->fn = fn;
    h->ctx = ctx;
    h->release = release;
    g_signal_connect_data(g, "pressed", G_CALLBACK(sngl_click_pressed), h, sngl_handler_free, 0);
    gtk_widget_add_controller(w, GTK_EVENT_CONTROLLER(g));
    g_object_set_data(G_OBJECT(w), "sngl-click", h);
}

void sngl_click(GtkWidget *w) {
    SnglHandler *h = g_object_get_data(G_OBJECT(w), "sngl-click");
    if (h) h->fn(h->ctx);
}

/* A text is a GtkLabel starting at the left of its box, as html's does: a
 * label centers its words in whatever room it is given. */
GtkWidget *sngl_text_new(void) {
    GtkWidget *l = gtk_label_new("");
    gtk_label_set_xalign(GTK_LABEL(l), 0.0);
    return l;
}

void sngl_text_set_value(GtkWidget *w, const char *value) {
    gtk_label_set_label(GTK_LABEL(w), value);
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

/* sngl_post (sngl.h) runs from the main loop when it is next idle. */
void sngl_post(void (*fn)(void *ctx), void *ctx, void (*release)(void *ctx)) {
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
    GPtrArray *ws = sngl_top(box) ? NULL : sngl_windows_of(box, false);
    if (ws != NULL) {
        for (guint i = 0; i < ws->len; i++) sngl_app_detach(g_ptr_array_index(ws, i));
        if (g_object_steal_data(G_OBJECT(box), "sngl-app-holds") != NULL) g_object_unref(box);
        return;
    }
    if (!sngl_top(box)) {
        box = sngl_wrapper(box, false);
        if (box == NULL) return;
    }
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

void sngl_gtk_reset(void) {
    while (sngl_attached && sngl_attached->len > 0) {
        SnglToplevel *t = g_ptr_array_index(sngl_attached, 0);
        sngl_app_detach(t->box);
        if (sngl_attached->len > 0 && g_ptr_array_index(sngl_attached, 0) == t) g_ptr_array_remove_index(sngl_attached, 0);
    }
}

/* A tick that keeps a blocking iteration from parking past a deadline. */
static gboolean sngl_capture_tick(gpointer data) {
    (void)data;
    return G_SOURCE_CONTINUE;
}

bool sngl_capture(GtkWidget *root, int width, int height, unsigned char **rgba, int *w, int *h, const char **why) {
    /* What is shown is the first window attached, as gtk4's BuildUI shows
     * the first toplevel: a root component's first, or the package body's.
     * A node shown in a toplevel of its own, a test's component, is drawn
     * as the window's whole content, as it is where it is a window's only
     * child, and put back after. A window of the program's own is drawn as
     * it is shown, its bar and all, made now where nothing showed it yet: a
     * test's. */
    GtkWidget *wrapper = NULL;
    SnglToplevel *top = NULL;
    if (root == NULL && sngl_attached && sngl_attached->len > 0) {
        SnglToplevel *t = g_ptr_array_index(sngl_attached, 0);
        root = t->box;
        GtkWidget *wrapped = g_object_get_data(G_OBJECT(root), "sngl-wraps");
        if (wrapped != NULL && gtk_widget_get_parent(root) == NULL) {
            wrapper = root;
            root = g_object_ref(wrapped);
            sngl_box_remove(wrapper, root);
        } else if (wrapped == NULL) {
            top = t;
        }
    }
    if (root == NULL) {
        *why = "nothing is shown";
        return false;
    }
    GtkWidget *win;
    if (top != NULL) {
        if (top->win == NULL) {
            sngl_toplevel_create(top);
            gtk_window_set_default_size(top->win, width, height);
        }
        win = GTK_WIDGET(top->win);
    } else {
        if (gtk_widget_get_parent(root) != NULL) {
            *why = "what is shown is in a window already";
            return false;
        }
        /* What made it may hold it only weakly, and the window's leaving
         * would free it: the capture keeps it, once. */
        if (g_object_is_floating(root)) {
            g_object_ref_sink(root);
        }
        win = gtk_window_new();
        gtk_window_set_default_size(GTK_WINDOW(win), width, height);
        gtk_window_set_child(GTK_WINDOW(win), root);
    }
    gtk_window_present(GTK_WINDOW(win));
    /* Mapped is not yet laid out: the compositor's map arrives before GTK
     * allocates, and a widget of no width draws nothing. */
    gint64 deadline = g_get_monotonic_time() + 2 * G_USEC_PER_SEC;
    guint tick = g_timeout_add(5, sngl_capture_tick, NULL);
    while (!(gtk_widget_get_mapped(win) && gtk_widget_get_width(win) > 0) && g_get_monotonic_time() < deadline) {
        g_main_context_iteration(NULL, TRUE);
    }
    while (g_main_context_iteration(NULL, FALSE)) {
    }
    bool ok = false;
    /* Nor is laid out drawn: a window the compositor has mapped may have
     * had no frame yet, and draws nothing until the loop gives it one. */
    GdkPaintable *paintable = gtk_widget_paintable_new(win);
    GskRenderNode *node = NULL;
    for (;;) {
        GtkSnapshot *snap = gtk_snapshot_new();
        gdk_paintable_snapshot(paintable, GDK_SNAPSHOT(snap), width, height);
        node = gtk_snapshot_free_to_node(snap);
        if (node != NULL || g_get_monotonic_time() >= deadline) break;
        g_main_context_iteration(NULL, TRUE);
    }
    g_source_remove(tick);
    GskRenderer *renderer = gsk_cairo_renderer_new();
#if GTK_CHECK_VERSION(4, 14, 0)
    bool realized = gsk_renderer_realize_for_display(renderer, gdk_display_get_default(), NULL);
#else
    bool realized = gsk_renderer_realize(renderer, NULL, NULL);
#endif
    if (node == NULL) {
        *why = "the window drew nothing";
    } else if (!realized) {
        *why = "the cairo renderer would not start";
    } else {
        graphene_rect_t bounds = GRAPHENE_RECT_INIT(0, 0, (float)width, (float)height);
        GdkTexture *tex = gsk_renderer_render_texture(renderer, node, &bounds);
        int tw = gdk_texture_get_width(tex), th = gdk_texture_get_height(tex);
        *rgba = g_malloc((size_t)tw * (size_t)th * 4);
#if GTK_CHECK_VERSION(4, 10, 0)
        GdkTextureDownloader *dl = gdk_texture_downloader_new(tex);
        gdk_texture_downloader_set_format(dl, GDK_MEMORY_R8G8B8A8);
        gdk_texture_downloader_download_into(dl, *rgba, (gsize)tw * 4);
        gdk_texture_downloader_free(dl);
#else
        /* Cairo's ARGB32, a word a pixel, premultiplied, which a window's
         * opaque pixels are unchanged by. */
        gdk_texture_download(tex, *rgba, (gsize)tw * 4);
        for (size_t i = 0; i < (size_t)tw * (size_t)th; i++) {
            guint32 px;
            memcpy(&px, *rgba + i * 4, 4);
            (*rgba)[i * 4] = (unsigned char)(px >> 16);
            (*rgba)[i * 4 + 1] = (unsigned char)(px >> 8);
            (*rgba)[i * 4 + 2] = (unsigned char)px;
            (*rgba)[i * 4 + 3] = (unsigned char)(px >> 24);
        }
#endif
        *w = tw;
        *h = th;
        g_object_unref(tex);
        ok = true;
    }
    if (realized) {
        gsk_renderer_unrealize(renderer);
    }
    g_object_unref(renderer);
    if (node) {
        gsk_render_node_unref(node);
    }
    g_object_unref(paintable);
    if (top == NULL) {
        gtk_window_set_child(GTK_WINDOW(win), NULL);
        gtk_window_destroy(GTK_WINDOW(win));
    }
    if (wrapper != NULL) {
        sngl_toplevel_append(wrapper, root);
        g_object_unref(root);
    }
    return ok;
}

/* sngl_slot_host is the box a slot rendering into w puts its entries in:
 * w, a box or a stack, or for a container holding one child -- a frame, a
 * button, a scroll, a window -- a box it holds as that child, made the first
 * time. */
static GtkWidget *sngl_slot_host(GtkWidget *w) {
    if (w == SNGL_APP || GTK_IS_BOX(w) || GTK_IS_STACK(w)) return w;
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

/* sngl_stack_put puts child into box ahead of before, or last where before
 * is NULL, where box is a stack, whose children are its layers: a slot
 * directly inside one renders its entries as layers of their own. */
static bool sngl_stack_put(GtkWidget *box, GtkWidget *child, GtkWidget *before) {
    if (!GTK_IS_STACK(box)) return false;
    if (gtk_widget_get_parent(child) != box) gtk_stack_add_child(GTK_STACK(box), child);
    if (child != before) gtk_widget_insert_before(child, box, before);
    return true;
}

GtkWidget *sngl_slot_anchor(GtkWidget *box, GtkWidget *anchor) {
    if (box == SNGL_APP) return NULL;
    box = sngl_slot_host(box);
    if (anchor != NULL && gtk_widget_get_parent(anchor) == box) return anchor;
    /* The slot's var held the last one, which its container let go of. */
    if (anchor != NULL) g_object_unref(anchor);
    if (!GTK_IS_BOX(box) && !GTK_IS_STACK(box)) return NULL;
    GtkWidget *w = g_object_ref_sink(gtk_label_new(NULL));
    gtk_widget_set_visible(w, FALSE);
    if (!sngl_stack_put(box, w, NULL)) gtk_box_append(GTK_BOX(box), w);
    return w;
}

/* sngl_moved_from is the box or stack child is in other than box, taken out
 * of it, and child referenced until the caller has put it in box: an
 * instance a render keeps goes into the container the render built afresh,
 * as the DOM's appendChild moves a node, and left in the old one it went with
 * it when that was destroyed. NULL where child is in no other. */
static GtkWidget *sngl_moved_from(GtkWidget *box, GtkWidget *child) {
    GtkWidget *old = gtk_widget_get_parent(child);
    if (old == NULL || old == box || old == g_object_get_data(G_OBJECT(box), "sngl-slot-box")) return NULL;
    /* A box or stack of the program's: a tab page sits in a GtkNotebook's own
     * stack, which the tabs move pages through themselves. */
    if (!GTK_IS_BOX(old) && !GTK_IS_STACK(old)) return NULL;
    if (gtk_widget_get_ancestor(old, GTK_TYPE_NOTEBOOK) != NULL && !GTK_IS_BOX(old)) return NULL;
    g_object_ref(child);
    if (GTK_IS_STACK(old)) {
        gtk_stack_remove(GTK_STACK(old), child);
    } else {
        gtk_box_remove(GTK_BOX(old), child);
        sngl_menu_moved(old, child);
    }
    return child;
}

static void sngl_box_append_to(GtkWidget *box, GtkWidget *child);
static void sngl_box_insert_before_in(GtkWidget *box, GtkWidget *anchor, GtkWidget *child);

void sngl_box_append(GtkWidget *box, GtkWidget *child) {
    GtkWidget *moved = box == SNGL_APP ? NULL : sngl_moved_from(box, child);
    sngl_box_append_to(box, child);
    if (moved) g_object_unref(moved);
}

void sngl_box_insert_before(GtkWidget *box, GtkWidget *anchor, GtkWidget *child) {
    GtkWidget *moved = box == SNGL_APP ? NULL : sngl_moved_from(box, child);
    sngl_box_insert_before_in(box, anchor, child);
    if (moved) g_object_unref(moved);
}

static void sngl_box_append_to(GtkWidget *box, GtkWidget *child) {
    if (box == SNGL_APP) {
        sngl_app_attach(child);
        return;
    }
    if (sngl_window_add(box, child) || sngl_tabs_add(box, child) || sngl_image_adopt(box, child)) return;
    box = sngl_slot_host(box);
    if (sngl_stack_put(box, child, NULL)) return;
    if (gtk_widget_get_parent(child) == box) {
        GtkWidget *last = gtk_widget_get_last_child(box);
        if (child != last) gtk_box_reorder_child_after(GTK_BOX(box), child, last);
    } else {
        gtk_box_append(GTK_BOX(box), child);
    }
    sngl_menu_moved(box, child);
}

static void sngl_box_insert_before_in(GtkWidget *box, GtkWidget *anchor, GtkWidget *child) {
    if (sngl_window_add(box, child)) return;
    box = sngl_slot_host(box);
    if (box == SNGL_APP || anchor == NULL || gtk_widget_get_parent(anchor) != box) {
        sngl_box_append_to(box, child);
        return;
    }
    if (sngl_tabs_add(box, child) || sngl_image_adopt(box, child) || sngl_stack_put(box, child, anchor)) return;
    /* A child the box already holds moves, as the DOM's insertBefore moves
     * one: a keyed loop putting a row it kept in its new place. */
    if (gtk_widget_get_parent(child) == box) {
        GtkWidget *after = gtk_widget_get_prev_sibling(anchor);
        if (child != anchor && child != after) gtk_box_reorder_child_after(GTK_BOX(box), child, after);
    } else {
        gtk_box_insert_child_after(GTK_BOX(box), child, gtk_widget_get_prev_sibling(anchor));
    }
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
    if (!sngl_window_remove(box, child) && !sngl_tabs_remove(box, child) && !sngl_image_unadopt(box, child)) {
        box = sngl_slot_host(box);
        if (GTK_IS_STACK(box)) {
            gtk_stack_remove(GTK_STACK(box), child);
        } else {
            gtk_box_remove(GTK_BOX(box), child);
            sngl_menu_moved(box, child);
        }
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

/* The key a window's `@key` handler is hearing, while it runs: its name,
 * what was held, and whether the handler has stopped it. A test's press
 * also hands the test's own stop, which a stop calls. */
typedef struct {
    const char *key;
    bool held[5];
    bool stopped;
    SnglFn then;
    void *then_ctx;
} SnglHeard;

static SnglHeard *sngl_heard;

const char *sngl_key_heard(void) {
    return sngl_heard ? sngl_heard->key : "";
}

int sngl_key_held(int which) {
    return sngl_heard && which >= 0 && which < 5 && sngl_heard->held[which];
}

void sngl_key_stop(void) {
    if (!sngl_heard) return;
    sngl_heard->stopped = true;
    if (sngl_heard->then) sngl_heard->then(sngl_heard->then_ctx);
}

/* sngl_hear runs the window's `@key`, and is whether its handler stopped the
 * key. */
static bool sngl_hear(SnglToplevel *t, const char *key, bool ctrl, bool alt, bool shift, bool meta, bool editing,
        SnglFn then, void *then_ctx) {
    if (!t || !t->on_key) return false;
    SnglHeard h = {key, {ctrl, alt, shift, meta, editing}, false, then, then_ctx};
    SnglHeard *outer = sngl_heard;
    sngl_heard = &h;
    t->on_key(t->key_ctx);
    sngl_heard = outer;
    return h.stopped;
}

static bool sngl_top_hear(const char *key, bool ctrl, bool alt, bool shift, bool meta, bool editing, void *data) {
    return sngl_hear(data, key, ctrl, alt, shift, meta, editing, NULL, NULL);
}

void sngl_toplevel_on_key(GtkWidget *box, SnglFn fn, void *ctx, GDestroyNotify release) {
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
void sngl_toplevel_key(GtkWidget *box, const char *key, int ctrl, int alt, int shift, int meta, int editing,
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

// A GTK property with no setter function of its own is set by name through
// GObject. g_object_set is variadic, which cgo cannot call at all, so the
// value goes through g_object_set_property — which takes a GValue of the
// property's own GType. Only the four scalar GValues below are built here;
// g_value_transform lands each on whatever the class's GParamSpec declares,
// which is how an enum, a float or an unsigned property is reached from one
// of them. A construct-only property fails inside GTK with a warning, since
// only the class knows which those are.
static void sngl_set_prop(void *obj, const char *name, GValue *src) {
    GObject *o = G_OBJECT(obj);
    GParamSpec *ps = g_object_class_find_property(G_OBJECT_GET_CLASS(o), name);
    if (ps == NULL) {
        g_warning("sngl: %s has no property \"%s\"", G_OBJECT_TYPE_NAME(o), name);
    } else if (g_value_type_compatible(G_VALUE_TYPE(src), ps->value_type)) {
        g_object_set_property(o, name, src);
    } else {
        GValue dst = G_VALUE_INIT;
        g_value_init(&dst, ps->value_type);
        if (g_value_transform(src, &dst)) {
            g_object_set_property(o, name, &dst);
        } else {
            g_warning("sngl: cannot set %s.%s from a %s", G_OBJECT_TYPE_NAME(o), name, G_VALUE_TYPE_NAME(src));
        }
        g_value_unset(&dst);
    }
    g_value_unset(src);
}
void sngl_set_prop_string(void *obj, const char *name, const char *v) {
    GValue src = G_VALUE_INIT;
    g_value_init(&src, G_TYPE_STRING);
    g_value_set_string(&src, v);
    sngl_set_prop(obj, name, &src);
}
void sngl_set_prop_bool(void *obj, const char *name, int v) {
    GValue src = G_VALUE_INIT;
    g_value_init(&src, G_TYPE_BOOLEAN);
    g_value_set_boolean(&src, v ? TRUE : FALSE);
    sngl_set_prop(obj, name, &src);
}
void sngl_set_prop_int(void *obj, const char *name, int v) {
    GValue src = G_VALUE_INIT;
    g_value_init(&src, G_TYPE_INT);
    g_value_set_int(&src, v);
    sngl_set_prop(obj, name, &src);
}
void sngl_set_prop_double(void *obj, const char *name, double v) {
    GValue src = G_VALUE_INIT;
    g_value_init(&src, G_TYPE_DOUBLE);
    g_value_set_double(&src, v);
    sngl_set_prop(obj, name, &src);
}

/* sngl_gtk_capture_file is sngl_capture of what was shown last, written to
 * path as its width and height, each four bytes little-endian, and then its
 * pixels: how a Go test agent, which takes no pointer back, reads one. Not
 * zero where nothing could be drawn. */
int sngl_gtk_capture_file(const char *path, int width, int height) {
    unsigned char *rgba = NULL;
    int w = 0, h = 0;
    const char *why = NULL;
    if (!sngl_capture(NULL, width, height, &rgba, &w, &h, &why)) {
        g_printerr("sngl: capture: %s\n", why);
        return 1;
    }
    FILE *f = fopen(path, "wb");
    if (f == NULL) {
        g_free(rgba);
        return 1;
    }
    unsigned char head[8];
    for (int i = 0; i < 4; i++) {
        head[i] = (unsigned char)((unsigned)w >> (8 * i));
        head[4 + i] = (unsigned char)((unsigned)h >> (8 * i));
    }
    size_t n = (size_t)w * (size_t)h * 4;
    bool ok = fwrite(head, 1, 8, f) == 8 && fwrite(rgba, 1, n, f) == n;
    ok = fclose(f) == 0 && ok;
    g_free(rgba);
    return ok ? 0 : 1;
}
