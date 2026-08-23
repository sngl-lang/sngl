package gtk4

// gtk4SnapshotCgo is the shared cgo preamble for both single-doc and
// batch snapshot harnesses. It declares sngl_snapshot (widget→PNG via
// GskCairoRenderer) and sngl_pump_idle (drains the GLib main context
// so layout/realize/draw settle before snapshotting).
const gtk4SnapshotCgo = `/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <gdk/gdk.h>
#include <gsk/gsk.h>
#include <graphene.h>
#include <stdlib.h>

// sngl_snapshot wraps the target widget in a GdkPaintable, snapshots
// it into a render node, and rasterises via the cairo GSK renderer
// (which needs no GdkSurface — works offscreen). The PNG is written
// to ` + "`path`" + `; returns 0 on success, non-zero on failure.
static int sngl_snapshot(GtkWidget *widget, int width, int height, const char *path) {
    if (widget == NULL) return 1;
    GdkPaintable *paintable = gtk_widget_paintable_new(widget);
    if (paintable == NULL) return 2;

    GtkSnapshot *snap = gtk_snapshot_new();
    gdk_paintable_snapshot(paintable, GDK_SNAPSHOT(snap), (double)width, (double)height);
    GskRenderNode *node = gtk_snapshot_free_to_node(snap);
    if (node == NULL) {
        g_object_unref(paintable);
        return 3;
    }

    GskRenderer *renderer = gsk_cairo_renderer_new();
    GError *err = NULL;
    if (!gsk_renderer_realize(renderer, NULL, &err)) {
        if (err) g_error_free(err);
        gsk_render_node_unref(node);
        g_object_unref(paintable);
        g_object_unref(renderer);
        return 4;
    }

    graphene_rect_t bounds = GRAPHENE_RECT_INIT(0, 0, (float)width, (float)height);
    GdkTexture *tex = gsk_renderer_render_texture(renderer, node, &bounds);
    if (tex == NULL) {
        gsk_renderer_unrealize(renderer);
        g_object_unref(renderer);
        gsk_render_node_unref(node);
        g_object_unref(paintable);
        return 5;
    }

    gboolean ok = gdk_texture_save_to_png(tex, path);

    g_object_unref(tex);
    gsk_renderer_unrealize(renderer);
    g_object_unref(renderer);
    gsk_render_node_unref(node);
    g_object_unref(paintable);
    return ok ? 0 : 6;
}

// Waits for map *and* a non-zero allocation: the compositor sends the map
// event before GTK has run size-allocate, and a zero-width widget snapshots to
// an empty (NULL) render node.
//
// The wait is a wall-clock deadline over blocking iterations because a
// non-blocking poll is not a wait at all — 1000 rounds of it returned in under
// 2ms on a quiet socket, so under load the allocation had simply not arrived.
// The tick source only keeps a blocking iteration from parking past the
// deadline.
static gboolean sngl_pump_tick(gpointer data) { return G_SOURCE_CONTINUE; }

static void sngl_pump_until_mapped(GtkWidget *widget, int timeoutMs) {
    gint64 deadline = g_get_monotonic_time() + (gint64)timeoutMs * 1000;
    guint tick = g_timeout_add(5, sngl_pump_tick, NULL);
    while (!(gtk_widget_get_mapped(widget) && gtk_widget_get_width(widget) > 0)) {
        if (g_get_monotonic_time() >= deadline) break;
        g_main_context_iteration(NULL, TRUE);
    }
    g_source_remove(tick);
    for (int i = 0; i < 1000; i++) {
        if (!g_main_context_iteration(NULL, FALSE)) break;
    }
}

// Legacy name retained for generated snapshot harnesses that have not been
// regenerated yet. Drains without a widget-mapped check.
static void sngl_pump_idle(int maxIter) {
    g_main_context_iteration(NULL, TRUE);
    for (int i = 0; i < maxIter; i++) {
        if (!g_main_context_iteration(NULL, FALSE)) return;
    }
}
*/
import "C"
`
