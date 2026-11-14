/* A window's menu and its way back are gtk4.sngl's -- its entries, the bar
 * and the GMenu behind its menu button, built from SNGL calling GTK -- and so
 * is an image. What the runtime's window, box and image sources ask of them
 * goes through the one hook the SNGL side installs, which each window,
 * entry, way back and image made installs anew. */
#include "sngl_gtk.h"

static struct {
    SnglWindowHook fn;
    void *ctx;
    GDestroyNotify release;
} sngl_hook;

void sngl_window_hook(SnglWindowHook fn, void *ctx, GDestroyNotify release) {
    GDestroyNotify old = sngl_hook.release;
    void *old_ctx = sngl_hook.ctx;
    sngl_hook.fn = fn;
    sngl_hook.ctx = ctx;
    sngl_hook.release = release;
    if (old) old(old_ctx);
}

/* Nothing installed is no window with a menu or a way back to ask. */
static bool sngl_hook_call(int op, GtkWidget *box, GtkWidget *child) {
    return sngl_hook.fn != NULL && sngl_hook.fn(op, box, child, sngl_hook.ctx);
}

bool sngl_window_back(GtkWidget *box) {
    return sngl_hook_call(SNGL_HOOK_BACK, box, box);
}

void sngl_menu_apply(GtkWidget *box) {
    sngl_hook_call(SNGL_HOOK_APPLY, box, box);
}

void sngl_bar_forget(GtkWidget *box) {
    sngl_hook_call(SNGL_HOOK_FORGET, box, box);
}

void sngl_menu_moved(GtkWidget *box, GtkWidget *child) {
    sngl_hook_call(SNGL_HOOK_MOVED, box, child);
}

bool sngl_image_placed(GtkWidget *box, GtkWidget *source) {
    return sngl_hook_call(SNGL_HOOK_IMAGE_PLACED, box, source);
}

bool sngl_image_report(GtkWidget *box, GtkWidget *source) {
    return sngl_hook_call(SNGL_HOOK_IMAGE_REPORT, box, source);
}
