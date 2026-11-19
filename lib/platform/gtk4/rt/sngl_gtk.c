#include "sngl_gtk.h"

#include <string.h>

#ifdef G_OS_UNIX
#include <glib-unix.h>
#include <signal.h>
#endif

guint sngl_signal_watch(int which, GSourceFunc fn, gpointer data) {
#ifdef G_OS_UNIX
    return g_unix_signal_add(which == 0 ? SIGINT : SIGTERM, fn, data);
#else
    (void)which;
    (void)fn;
    (void)data;
    return 0;
#endif
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

/* A tick that keeps a blocking iteration from parking past a deadline. */
static gboolean sngl_capture_tick(gpointer data) {
    (void)data;
    return G_SOURCE_CONTINUE;
}

bool sngl_draw(GtkWidget *win, int width, int height, unsigned char **rgba, int *w, int *h, const char **why) {
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
    return ok;
}

int sngl_draw_file(GtkWidget *win, const char *path, int width, int height) {
    unsigned char *rgba = NULL;
    int w = 0, h = 0;
    const char *why = "nothing is shown";
    if (win == NULL || !sngl_draw(win, width, height, &rgba, &w, &h, &why)) {
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
