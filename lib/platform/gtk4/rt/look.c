/* What of a widget's look gtk4.sngl cannot write itself (its "A widget's
 * look"): a widget kept without a reference, and the CSS provider of a
 * widget's own, attached through the style context GTK deprecated and
 * loaded as the GTK it is built against takes it. Compiled into every gtk4
 * program: the Go wrappers include it, and a C program compiles it beside
 * its own. */
#include "sngl_gtk.h"

// A widget kept on o under key without a reference, which reads as NULL
// once it is finalized, and is let go as key is set to NULL.
static void sngl_weak_free(gpointer data) {
    GtkWidget **slot = data;
    if (*slot) g_object_remove_weak_pointer(G_OBJECT(*slot), (gpointer *)slot);
    g_free(slot);
}

void sngl_weak_set(GObject *o, const char *key, GtkWidget *w) {
    GtkWidget **slot = g_new(GtkWidget *, 1);
    *slot = w;
    g_object_add_weak_pointer(G_OBJECT(w), (gpointer *)slot);
    g_object_set_data_full(o, key, slot, sngl_weak_free);
}

GtkWidget *sngl_weak(GObject *o, const char *key) {
    GtkWidget **slot = g_object_get_data(o, key);
    return slot ? *slot : NULL;
}

// A provider of w's own, which restyles w alone. gtk_style_context_add_provider
// is deprecated since 4.10 in favor of style classes, which would put every
// distinct look in one display-wide sheet and restyle every widget whenever
// a look changed.
void sngl_style_add(GtkWidget *w, GtkCssProvider *css) {
    G_GNUC_BEGIN_IGNORE_DEPRECATIONS
    gtk_style_context_add_provider(gtk_widget_get_style_context(w), GTK_STYLE_PROVIDER(css), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
    G_GNUC_END_IGNORE_DEPRECATIONS
}

void sngl_style_remove(GtkWidget *w, GtkCssProvider *css) {
    G_GNUC_BEGIN_IGNORE_DEPRECATIONS
    gtk_style_context_remove_provider(gtk_widget_get_style_context(w), GTK_STYLE_PROVIDER(css));
    G_GNUC_END_IGNORE_DEPRECATIONS
}

void sngl_css_load(GtkCssProvider *css, const char *rule) {
#if GTK_CHECK_VERSION(4, 12, 0)
    gtk_css_provider_load_from_string(css, rule);
#else
    gtk_css_provider_load_from_data(css, rule, -1);
#endif
}
