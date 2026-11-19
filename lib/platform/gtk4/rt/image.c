/* What of a `ui.image` on GTK is C's (the rest is gtk4.sngl's "An image,
 * with no struct of its own"): the files a program carries, and reading the
 * bytes a URL names, which reach a picture through the function it hands
 * sngl_picture_read. SNGL cannot walk the table of carried files, read a
 * GError's message or read an answer sngl_http_start hands its callback. */
#include "sngl_gtk.h"

#include <string.h>

/* The files the program carries: its entry's table, and each Go hands. */
static const SnglAsset *sngl_carried;
static SnglAsset *sngl_carried_files;
static size_t sngl_ncarried_files;

void sngl_gtk_carry(const SnglAsset *assets) {
    sngl_carried = assets;
}

void sngl_gtk_carry_file(const char *name, const char *data, int len) {
    size_t n = len > 0 ? (size_t)len : 0;
    unsigned char *copy = g_malloc(n ? n : 1);
    memcpy(copy, data, n);
    sngl_carried_files = g_renew(SnglAsset, sngl_carried_files, sngl_ncarried_files + 1);
    sngl_carried_files[sngl_ncarried_files++] = (SnglAsset){g_strdup(name), copy, n};
}

typedef struct {
    GObject *pic;
    SnglPictureRead done;
    sngl_http_request *req;
} SnglRead;

/* A picture fetched arrives through the runtime's request (sngl_http.h) on
 * the main loop: one whose status is no success failed, by its status. */
static void sngl_picture_fetched(sngl_http_answer *a, void *data) {
    SnglRead *r = data;
    sngl_http_cancel(r->req);
    if (a->failure != SNGL_HTTP_ANSWERED) {
        r->done(r->pic, NULL, a->message);
    } else if (a->status < 200 || a->status >= 300) {
        char *why = g_strdup_printf("HTTP %d", a->status);
        r->done(r->pic, NULL, why);
        g_free(why);
    } else {
        GBytes *b = g_bytes_new(a->body, a->body_len);
        r->done(r->pic, b, "");
        g_bytes_unref(b);
    }
    g_object_unref(r->pic);
    g_free(r);
}

static void sngl_picture_carried(GObject *pic, SnglPictureRead done, const SnglAsset *a) {
    GBytes *b = g_bytes_new_static(a->data, a->len);
    done(pic, b, "");
    g_bytes_unref(b);
}

void sngl_picture_read(const char *url, GObject *pic, SnglPictureRead done) {
    if (g_str_has_prefix(url, "http://") || g_str_has_prefix(url, "https://")) {
        SnglRead *r = g_new0(SnglRead, 1);
        r->pic = g_object_ref(pic);
        r->done = done;
        r->req = sngl_http_start("GET", url, 0, NULL, NULL, NULL, 0, sngl_picture_fetched, r);
        if (r->req == NULL) {
            g_object_unref(r->pic);
            g_free(r);
            done(pic, NULL, "out of memory");
        }
        return;
    }
    const char *name = url[0] == '/' ? url + 1 : url;
    for (const SnglAsset *a = sngl_carried; a && a->name; a++) {
        if (strcmp(a->name, name) == 0) {
            sngl_picture_carried(pic, done, a);
            return;
        }
    }
    for (size_t i = 0; i < sngl_ncarried_files; i++) {
        if (strcmp(sngl_carried_files[i].name, name) == 0) {
            sngl_picture_carried(pic, done, &sngl_carried_files[i]);
            return;
        }
    }
    char *bytes = NULL;
    gsize len = 0;
    GError *err = NULL;
    if (g_file_get_contents(name, &bytes, &len, &err)) {
        GBytes *b = g_bytes_new_take(bytes, len);
        done(pic, b, "");
        g_bytes_unref(b);
    } else {
        done(pic, NULL, err->message);
        g_clear_error(&err);
    }
}

const char *sngl_icon_dir(void) {
    static char *dir;
    if (!dir) {
        dir = g_dir_make_tmp("sngl-icons-XXXXXX", NULL);
        if (!dir) return "";
        gtk_icon_theme_add_search_path(gtk_icon_theme_get_for_display(gdk_display_get_default()), dir);
    }
    return dir;
}
