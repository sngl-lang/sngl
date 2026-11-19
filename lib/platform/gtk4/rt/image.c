/* What of a `ui.image` on GTK is C's (the rest is gtk4.sngl's "An image,
 * with no struct of its own"): the tables of files a program carries,
 * which SNGL cannot walk, and reading a file beside it, which GLib answers
 * a GError ** out for, which SNGL cannot hand it. */
#include "sngl_gtk.h"

#include <string.h>

/* The files the program carries: its entry's table, and each Go hands. */
static const SnglAsset *sngl_carried_table;
static SnglAsset *sngl_carried_files;
static size_t sngl_ncarried_files;

void sngl_gtk_carry(const SnglAsset *assets) {
    sngl_carried_table = assets;
}

void sngl_gtk_carry_file(const char *name, const char *data, int len) {
    size_t n = len > 0 ? (size_t)len : 0;
    unsigned char *copy = g_malloc(n ? n : 1);
    memcpy(copy, data, n);
    sngl_carried_files = g_renew(SnglAsset, sngl_carried_files, sngl_ncarried_files + 1);
    sngl_carried_files[sngl_ncarried_files++] = (SnglAsset){g_strdup(name), copy, n};
}

/* The file the program carries name names, from its entry's table or
 * those Go handed, or NULL. */
const SnglAsset *sngl_carried(const char *name) {
    for (const SnglAsset *a = sngl_carried_table; a && a->name; a++) {
        if (strcmp(a->name, name) == 0) return a;
    }
    for (size_t i = 0; i < sngl_ncarried_files; i++) {
        if (strcmp(sngl_carried_files[i].name, name) == 0) return &sngl_carried_files[i];
    }
    return NULL;
}

/* The bytes of the file name names, or NULL with why GLib's message. */
GBytes *sngl_file_read(const char *name, char **why) {
    char *bytes = NULL;
    gsize len = 0;
    GError *err = NULL;
    if (!g_file_get_contents(name, &bytes, &len, &err)) {
        *why = g_strdup(err->message);
        g_clear_error(&err);
        return NULL;
    }
    return g_bytes_new_take(bytes, len);
}
