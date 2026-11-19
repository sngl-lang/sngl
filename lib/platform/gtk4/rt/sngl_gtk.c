#include "sngl_gtk.h"

#include <string.h>

#ifdef G_OS_UNIX
#include <glib-unix.h>
#include <signal.h>
#endif

static SnglMenuHook sngl_menu_apply_hook, sngl_bar_forget_hook;
static SnglMenuBackHook sngl_window_back_hook;

void sngl_menu_hooks(SnglMenuHook apply, SnglMenuBackHook back, SnglMenuHook forget) {
    sngl_menu_apply_hook = apply;
    sngl_window_back_hook = back;
    sngl_bar_forget_hook = forget;
}

/* A window's menu and its way back are gtk4.sngl's, which hands them over
 * as the first window, entry or way back is made: before, there is none. */
static void sngl_menu_apply(GtkWidget *box) {
    if (sngl_menu_apply_hook != NULL) sngl_menu_apply_hook(box);
}

static bool sngl_window_back(GtkWidget *box) {
    return sngl_window_back_hook != NULL && sngl_window_back_hook(box);
}

static void sngl_bar_forget(GtkWidget *box) {
    if (sngl_bar_forget_hook != NULL) sngl_bar_forget_hook(box);
}

/* A window made shows the icon its source holds, gtk4.sngl's. */
static SnglIconHook sngl_icon_refresh_hook;

void sngl_icon_hook(SnglIconHook refresh) {
    sngl_icon_refresh_hook = refresh;
}

static void sngl_toplevel_icon_refresh(GtkWidget *box) {
    if (sngl_icon_refresh_hook != NULL) sngl_icon_refresh_hook(box);
}

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
/* Whether the windows are held hidden as the loop starts: an `@run` handler
 * that has not called run. */
static bool sngl_holding;

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

/* A window's keys are gtk4.sngl's, which hands them over as each toplevel
 * is made: watching the window made, and a key a test pressed that nothing
 * stopped. */
static SnglKeysWatch sngl_keys_watch_hook;

void sngl_keys_hooks(SnglKeysWatch watch) {
    sngl_keys_watch_hook = watch;
}

static void sngl_toplevel_create(SnglToplevel *t) {
    if (t->win) return;
    /* A test draws a window no application runs. */
    t->win = GTK_WINDOW(sngl_app ? gtk_application_window_new(sngl_app) : gtk_window_new());
    gtk_window_set_default_size(t->win, 480, 640);
    if (t->title) gtk_window_set_title(t->win, t->title);
    gtk_window_set_child(t->win, t->box);
    g_signal_connect(t->win, "close-request", G_CALLBACK(sngl_close_request), t);
    if (sngl_keys_watch_hook != NULL) sngl_keys_watch_hook(GTK_WIDGET(t->win));
    sngl_menu_apply(t->box);
    sngl_toplevel_icon_refresh(t->box);
}

GtkWindow *sngl_toplevel_window(GtkWidget *box) {
    SnglToplevel *t = sngl_top(box);
    return t ? t->win : NULL;
}

void sngl_toplevel_attach(GtkWidget *box) {
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

typedef struct {
    SnglFn fn;
    void *ctx;
    GDestroyNotify release;
} SnglHandler;

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

/* sngl_loop_wait (sngl_http.h) runs what the main loop has, waiting for
 * something where it has nothing: a request's answer is posted to it. */
bool sngl_loop_wait(void) {
    g_main_context_iteration(NULL, TRUE);
    return true;
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

/* sngl_toplevel_detach takes a toplevel off the application, its window
 * destroyed: a slot at the package's root no longer renders it. */
void sngl_toplevel_detach(GtkWidget *box) {
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
        sngl_toplevel_detach(t->box);
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
            gtk_box_remove(GTK_BOX(wrapper), root);
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
        gtk_box_append(GTK_BOX(wrapper), root);
        g_object_unref(root);
    }
    return ok;
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
