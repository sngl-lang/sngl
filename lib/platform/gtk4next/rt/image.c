/* A `ui.image` on GTK for a program written in C, as gtk4rt's image.go is
 * for Go. An image is a box holding the source it shows, fitted as the image
 * says, or the placeholder where it holds none. A source is a GtkPicture
 * showing the texture its picture or its drawing make, and reporting to the
 * image holding it whether the picture arrived. Appended to a window, a
 * source is the window's icon. */
#include "sngl_gtk.h"

#include <math.h>
#include <string.h>
#include <unistd.h>

typedef struct {
    GtkContentFit fit;
    SnglFn on_click;
    void *click_ctx;
    GDestroyNotify click_release;
    SnglFn on_load;
    void *load_ctx;
    GDestroyNotify load_release;
    SnglFn on_failed;
    void *failed_ctx;
    GDestroyNotify failed_release;
    /* failure is why the last picture the image was told of failed. */
    char *failure;
} SnglImage;

typedef struct {
    GtkWidget *pic;
    SnglPicture *picture;
    bool ready;
    char *err;
    GdkTexture *tex;
    /* frame is the image last told, compared and never read through: the
     * image is the source's parent while it holds it. */
    GtkWidget *frame;
    bool told;
    /* icon is the toplevel whose icon the source is, or NULL. */
    GtkWidget *icon;
    /* What a drawing paints its picture with, width by height: the
     * program's shapes, as the handler of its paint. */
    int dw, dh;
    SnglDrawFn draw;
    void *draw_ctx;
    GDestroyNotify draw_release;
} SnglSource;

/* A picture is what a URL names, decoded once: the program's `ui.file` and
 * `ui.remote` derive one from the URL and hold it as long as the URL stands,
 * so a source shows it and a drawing paints it without reading or decoding
 * it again. A carried file is there at once; a fetched one arrives later,
 * and the sources showing it are told. */
struct SnglPicture {
    char *url;
    bool arrived;
    char *err;
    GdkTexture *tex;
    /* surface is the texture's pixels for cairo, and scaled those at the
     * last size a drawing painted them. */
    cairo_surface_t *surface, *scaled;
    int sw, sh;
    /* watching is each source showing the picture, told when it arrives. */
    GPtrArray *watching;
    /* refs is how many hold the picture; pending is a fetch still holding
     * it, which frees it on arriving after the last holder let go. */
    int refs;
    bool released, pending;
};

static SnglImage *sngl_image_of(GtkWidget *w) {
    return w ? g_object_get_data(G_OBJECT(w), "sngl-image") : NULL;
}

static SnglSource *sngl_source_of(GtkWidget *w) {
    return w ? g_object_get_data(G_OBJECT(w), "sngl-source") : NULL;
}

static void sngl_image_free(gpointer data) {
    SnglImage *img = data;
    if (img->click_release) img->click_release(img->click_ctx);
    if (img->load_release) img->load_release(img->load_ctx);
    if (img->failed_release) img->failed_release(img->failed_ctx);
    g_free(img->failure);
    g_free(img);
}

static void sngl_image_pressed(GtkGestureClick *g, int n, double x, double y, gpointer box) {
    sngl_image_click(box);
}

/* Horizontal: a GtkPicture measures its height for the width it is given,
 * and a vertical box gives each child its own whole width, so the picture
 * asked for as much height as that width implies. A horizontal box measures
 * it at its own width. */
GtkWidget *sngl_image_new(void) {
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0);
    SnglImage *img = g_new0(SnglImage, 1);
    img->fit = GTK_CONTENT_FIT_CONTAIN;
    g_object_set_data_full(G_OBJECT(box), "sngl-image", img, sngl_image_free);
    GtkGesture *g = gtk_gesture_click_new();
    g_signal_connect(g, "pressed", G_CALLBACK(sngl_image_pressed), box);
    gtk_widget_add_controller(box, GTK_EVENT_CONTROLLER(g));
    return box;
}

/* `none` is `scaleDown`: GTK has no picture that keeps its own size whatever
 * room it is given. */
void sngl_image_set_fit(GtkWidget *box, const char *fit) {
    SnglImage *img = sngl_image_of(box);
    if (!img) return;
    if (strcmp(fit, "cover") == 0) {
        img->fit = GTK_CONTENT_FIT_COVER;
    } else if (strcmp(fit, "fill") == 0) {
        img->fit = GTK_CONTENT_FIT_FILL;
    } else if (strcmp(fit, "none") == 0 || strcmp(fit, "scaleDown") == 0) {
        img->fit = GTK_CONTENT_FIT_SCALE_DOWN;
    } else {
        img->fit = GTK_CONTENT_FIT_CONTAIN;
    }
    for (GtkWidget *c = gtk_widget_get_first_child(box); c; c = gtk_widget_get_next_sibling(c)) {
        if (sngl_source_of(c)) gtk_picture_set_content_fit(GTK_PICTURE(c), img->fit);
    }
}

void sngl_image_on_click(GtkWidget *box, SnglFn fn, void *ctx, GDestroyNotify release) {
    SnglImage *img = sngl_image_of(box);
    if (!img) {
        if (release) release(ctx);
        return;
    }
    if (img->click_release) img->click_release(img->click_ctx);
    img->on_click = fn;
    img->click_ctx = ctx;
    img->click_release = release;
}

void sngl_image_on_load(GtkWidget *box, SnglFn fn, void *ctx, GDestroyNotify release) {
    SnglImage *img = sngl_image_of(box);
    if (!img) {
        if (release) release(ctx);
        return;
    }
    if (img->load_release) img->load_release(img->load_ctx);
    img->on_load = fn;
    img->load_ctx = ctx;
    img->load_release = release;
}

void sngl_image_on_failed(GtkWidget *box, SnglFn fn, void *ctx, GDestroyNotify release) {
    SnglImage *img = sngl_image_of(box);
    if (!img) {
        if (release) release(ctx);
        return;
    }
    if (img->failed_release) img->failed_release(img->failed_ctx);
    img->on_failed = fn;
    img->failed_ctx = ctx;
    img->failed_release = release;
}

const char *sngl_image_failure(GtkWidget *box) {
    SnglImage *img = sngl_image_of(box);
    return img && img->failure ? img->failure : "";
}

void sngl_image_click(GtkWidget *box) {
    SnglImage *img = sngl_image_of(box);
    if (img && img->on_click) img->on_click(img->click_ctx);
}

/* report tells the image holding the source, once per picture, whether it
 * arrived. */
static void sngl_source_report(SnglSource *s) {
    GtkWidget *parent = gtk_widget_get_parent(s->pic);
    SnglImage *img = sngl_image_of(parent);
    if (!img || !s->ready || s->told) return;
    s->told = true;
    if (s->err) {
        g_free(img->failure);
        img->failure = g_strdup(s->err);
        if (img->on_failed) img->on_failed(img->failed_ctx);
    } else if (!s->err && img->on_load) {
        img->on_load(img->load_ctx);
    }
}

static void sngl_icon_apply(GtkWidget *toplevel, GdkTexture *tex);

/* set_texture shows tex, which the source holds a reference of its own on. */
static void sngl_source_set_texture(SnglSource *s, GdkTexture *tex) {
    if (s->tex) g_object_unref(s->tex);
    s->tex = tex;
    gtk_picture_set_paintable(GTK_PICTURE(s->pic), GDK_PAINTABLE(tex));
    s->ready = true;
    s->told = false;
    sngl_source_report(s);
    if (s->icon) sngl_icon_apply(s->icon, s->tex);
}

/* take shows the picture as it arrived. */
static void sngl_source_take(SnglSource *s, SnglPicture *pic) {
    if (pic->tex) g_object_ref(pic->tex);
    g_free(s->err);
    s->err = g_strdup(pic->err);
    sngl_source_set_texture(s, pic->tex);
}

/* watch makes pic the picture the source is told about, or none. */
static void sngl_source_watch(SnglSource *s, SnglPicture *pic) {
    if (s->picture && s->picture->watching) g_ptr_array_remove(s->picture->watching, s);
    s->picture = pic;
    if (pic && pic->watching) g_ptr_array_add(pic->watching, s);
}

static void sngl_source_free(gpointer data) {
    SnglSource *s = data;
    sngl_source_watch(s, NULL);
    if (s->tex) g_object_unref(s->tex);
    if (s->draw_release) s->draw_release(s->draw_ctx);
    g_free(s->err);
    g_free(s);
}

/* The source having been put somewhere: it fits itself to the image it is
 * in, and reports to it. */
static void sngl_source_parented(GObject *o, GParamSpec *ps, gpointer data) {
    SnglSource *s = data;
    GtkWidget *parent = gtk_widget_get_parent(s->pic);
    SnglImage *img = sngl_image_of(parent);
    if (!img) {
        s->frame = NULL;
        return;
    }
    gtk_picture_set_content_fit(GTK_PICTURE(s->pic), img->fit);
    if (s->frame != parent) {
        s->frame = parent;
        s->told = false;
    }
    sngl_source_report(s);
}

GtkWidget *sngl_image_source_new(void) {
    GtkWidget *pic = gtk_picture_new();
    SnglSource *s = g_new0(SnglSource, 1);
    s->pic = pic;
    g_object_set_data_full(G_OBJECT(pic), "sngl-source", s, sngl_source_free);
    g_signal_connect(pic, "notify::parent", G_CALLBACK(sngl_source_parented), s);
    return pic;
}

/* The URL a source says it shows, kept on it for what reads it: it shows the
 * picture it is handed, not one it fetches itself. */
void sngl_image_source_set_url(GtkWidget *w, const char *url) {
    g_object_set_data_full(G_OBJECT(w), "sngl-url", g_strdup(url), g_free);
}

void sngl_image_source_set_picture(GtkWidget *w, SnglPicture *pic) {
    SnglSource *s = sngl_source_of(w);
    if (!s) return;
    sngl_source_watch(s, pic);
    if (pic == NULL) {
        g_free(s->err);
        s->err = NULL;
        sngl_source_set_texture(s, NULL);
        return;
    }
    if (pic->arrived) sngl_source_take(s, pic);
}

/* A drawing paints its picture again whenever it is asked, once it knows
 * its size and what to paint: a texture of what the handler draws on a
 * surface of that size. GDK_MEMORY_DEFAULT is cairo's ARGB32, so the
 * surface's pixels are the texture's as they stand. */
static void sngl_drawing_paint(SnglSource *s) {
    if (!s->draw || s->dw <= 0 || s->dh <= 0) return;
    cairo_surface_t *surface = cairo_image_surface_create(CAIRO_FORMAT_ARGB32, s->dw, s->dh);
    cairo_t *cr = cairo_create(surface);
    s->draw(cr, s->draw_ctx);
    cairo_destroy(cr);
    cairo_surface_flush(surface);
    int stride = cairo_image_surface_get_stride(surface);
    GBytes *px = g_bytes_new(cairo_image_surface_get_data(surface), (gsize)stride * s->dh);
    GdkTexture *tex = gdk_memory_texture_new(s->dw, s->dh, GDK_MEMORY_DEFAULT, px, stride);
    g_bytes_unref(px);
    cairo_surface_destroy(surface);
    sngl_source_watch(s, NULL);
    g_free(s->err);
    s->err = NULL;
    sngl_source_set_texture(s, tex);
}

void sngl_drawing_set_width(GtkWidget *w, double width) {
    SnglSource *s = sngl_source_of(w);
    if (!s) return;
    s->dw = (int)ceil(width);
    sngl_drawing_paint(s);
}

void sngl_drawing_set_height(GtkWidget *w, double height) {
    SnglSource *s = sngl_source_of(w);
    if (!s) return;
    s->dh = (int)ceil(height);
    sngl_drawing_paint(s);
}

void sngl_drawing_on_paint(GtkWidget *w, SnglDrawFn draw, void *ctx, GDestroyNotify release) {
    SnglSource *s = sngl_source_of(w);
    if (!s) {
        if (release) release(ctx);
        return;
    }
    if (s->draw_release) s->draw_release(s->draw_ctx);
    s->draw = draw;
    s->draw_ctx = ctx;
    s->draw_release = release;
    sngl_drawing_paint(s);
}

void sngl_drawing_redraw(GtkWidget *w) {
    SnglSource *s = sngl_source_of(w);
    if (s) sngl_drawing_paint(s);
}

/* GdkPixbuf rather than gdk_texture_new_from_bytes: its loaders read SVG as
 * well as PNG and JPEG. */
static GdkTexture *sngl_texture(const guchar *data, gsize len) {
    GdkPixbufLoader *l = gdk_pixbuf_loader_new();
    gboolean ok = gdk_pixbuf_loader_write(l, data, len, NULL);
    ok = gdk_pixbuf_loader_close(l, NULL) && ok;
    GdkPixbuf *pb = ok ? gdk_pixbuf_loader_get_pixbuf(l) : NULL;
    GdkTexture *t = NULL;
    if (pb) {
        GBytes *px = gdk_pixbuf_read_pixel_bytes(pb);
        t = gdk_memory_texture_new(gdk_pixbuf_get_width(pb), gdk_pixbuf_get_height(pb),
            gdk_pixbuf_get_has_alpha(pb) ? GDK_MEMORY_R8G8B8A8 : GDK_MEMORY_R8G8B8,
            px, gdk_pixbuf_get_rowstride(pb));
        g_bytes_unref(px);
    }
    g_object_unref(l);
    return t;
}

static void sngl_picture_free(SnglPicture *pic) {
    if (pic->scaled) cairo_surface_destroy(pic->scaled);
    if (pic->surface) cairo_surface_destroy(pic->surface);
    if (pic->tex) g_object_unref(pic->tex);
    if (pic->watching) g_ptr_array_unref(pic->watching);
    g_free(pic->err);
    g_free(pic->url);
    g_free(pic);
}

static void sngl_picture_arrive(SnglPicture *pic, const guchar *data, gsize len, const char *err) {
    if (err) {
        pic->err = g_strdup_printf("%s: %s", pic->url, err);
    } else if (len == 0) {
        pic->err = g_strdup_printf("%s: empty", pic->url);
    } else {
        pic->tex = sngl_texture(data, len);
        if (!pic->tex) pic->err = g_strdup_printf("%s: not an image GTK can read", pic->url);
    }
    pic->arrived = true;
    if (!pic->watching) return;
    /* Telling a source runs the program's @load or @error, which may give
     * the picture back or show another in a source: the picture is held
     * until each is told, and each is told from a copy of the list, if it
     * is still watching. */
    sngl_picture_ref(pic);
    GPtrArray *told = g_ptr_array_copy(pic->watching, NULL, NULL);
    for (guint i = 0; i < told->len; i++) {
        SnglSource *s = g_ptr_array_index(told, i);
        if (!pic->released && g_ptr_array_find(pic->watching, s, NULL)) {
            sngl_source_take(s, pic);
        }
    }
    g_ptr_array_unref(told);
    sngl_picture_unref(pic);
}

/* A fetch is an HTTP/1.0 GET over a GSocketClient, TLS for https, read to
 * the connection's close: GIO's own http is GVfs's, which a host -- or a
 * test's session bus -- may not run. A redirect is followed five deep. */
typedef struct {
    SnglPicture *pic;
    char *url;
    int redirects;
    GSocketConnection *conn;
    GByteArray *got;
    char *request;
} SnglFetch;

static void sngl_fetch_start(SnglFetch *f);

static void sngl_fetch_free(SnglFetch *f) {
    if (f->conn) g_object_unref(f->conn);
    if (f->got) g_byte_array_unref(f->got);
    g_free(f->request);
    g_free(f->url);
    g_free(f);
}

/* done hands the picture what arrived, or why nothing did. */
static void sngl_fetch_done(SnglFetch *f, const guchar *data, gsize len, const char *err) {
    SnglPicture *pic = f->pic;
    pic->pending = false;
    if (pic->released) {
        sngl_picture_free(pic);
    } else {
        sngl_picture_arrive(pic, data, len, err);
    }
    sngl_fetch_free(f);
}

static void sngl_fetch_answered(SnglFetch *f) {
    const guchar *d = f->got->data;
    gsize n = f->got->len;
    const guchar *end = NULL;
    for (gsize i = 0; i + 3 < n; i++) {
        if (d[i] == '\r' && d[i + 1] == '\n' && d[i + 2] == '\r' && d[i + 3] == '\n') {
            end = d + i;
            break;
        }
    }
    if (!end) {
        sngl_fetch_done(f, NULL, 0, "no HTTP response");
        return;
    }
    char *head = g_strndup((const char *)d, end - d);
    const guchar *body = end + 4;
    gsize blen = n - (body - d);
    char **lines = g_strsplit(head, "\r\n", -1);
    int status = 0;
    if (lines[0]) sscanf(lines[0], "HTTP/%*s %d", &status);
    if (status >= 300 && status < 400 && f->redirects < 5) {
        for (int i = 1; lines[i]; i++) {
            if (g_ascii_strncasecmp(lines[i], "location:", 9) == 0) {
                char *to = g_uri_resolve_relative(f->url, g_strstrip(lines[i] + 9), G_URI_FLAGS_NONE, NULL);
                g_strfreev(lines);
                g_free(head);
                if (to) {
                    g_free(f->url);
                    f->url = to;
                    f->redirects++;
                    g_clear_object(&f->conn);
                    g_byte_array_set_size(f->got, 0);
                    sngl_fetch_start(f);
                    return;
                }
                sngl_fetch_done(f, NULL, 0, "a redirect to nowhere");
                return;
            }
        }
    }
    g_strfreev(lines);
    g_free(head);
    if (status < 200 || status >= 300) {
        char *why = g_strdup_printf("HTTP %d", status);
        sngl_fetch_done(f, NULL, 0, why);
        g_free(why);
        return;
    }
    sngl_fetch_done(f, body, blen, NULL);
}

static void sngl_fetch_read(GObject *o, GAsyncResult *res, gpointer data) {
    SnglFetch *f = data;
    GError *err = NULL;
    GBytes *b = g_input_stream_read_bytes_finish(G_INPUT_STREAM(o), res, &err);
    if (!b) {
        sngl_fetch_done(f, NULL, 0, err->message);
        g_error_free(err);
        return;
    }
    gsize n = 0;
    const guchar *p = g_bytes_get_data(b, &n);
    if (n == 0) {
        g_bytes_unref(b);
        sngl_fetch_answered(f);
        return;
    }
    g_byte_array_append(f->got, p, n);
    g_bytes_unref(b);
    g_input_stream_read_bytes_async(G_INPUT_STREAM(o), 65536, G_PRIORITY_DEFAULT, NULL, sngl_fetch_read, f);
}

static void sngl_fetch_sent(GObject *o, GAsyncResult *res, gpointer data) {
    SnglFetch *f = data;
    GError *err = NULL;
    if (!g_output_stream_write_all_finish(G_OUTPUT_STREAM(o), res, NULL, &err)) {
        sngl_fetch_done(f, NULL, 0, err->message);
        g_error_free(err);
        return;
    }
    GInputStream *in = g_io_stream_get_input_stream(G_IO_STREAM(f->conn));
    g_input_stream_read_bytes_async(in, 65536, G_PRIORITY_DEFAULT, NULL, sngl_fetch_read, f);
}

static void sngl_fetch_connected(GObject *o, GAsyncResult *res, gpointer data) {
    SnglFetch *f = data;
    GError *err = NULL;
    f->conn = g_socket_client_connect_to_uri_finish(G_SOCKET_CLIENT(o), res, &err);
    g_object_unref(o);
    if (!f->conn) {
        sngl_fetch_done(f, NULL, 0, err->message);
        g_error_free(err);
        return;
    }
    GOutputStream *out = g_io_stream_get_output_stream(G_IO_STREAM(f->conn));
    g_output_stream_write_all_async(out, f->request, strlen(f->request), G_PRIORITY_DEFAULT, NULL, sngl_fetch_sent, f);
}

static void sngl_fetch_start(SnglFetch *f) {
    GUri *uri = g_uri_parse(f->url, G_URI_FLAGS_NONE, NULL);
    if (!uri || !g_uri_get_host(uri)) {
        if (uri) g_uri_unref(uri);
        sngl_fetch_done(f, NULL, 0, "not a URL");
        return;
    }
    bool tls = g_ascii_strcasecmp(g_uri_get_scheme(uri), "https") == 0;
    const char *path = g_uri_get_path(uri);
    const char *query = g_uri_get_query(uri);
    int port = g_uri_get_port(uri);
    char *host = port > 0 ? g_strdup_printf("%s:%d", g_uri_get_host(uri), port) : g_strdup(g_uri_get_host(uri));
    g_free(f->request);
    f->request = g_strdup_printf("GET %s%s%s HTTP/1.0\r\nHost: %s\r\nUser-Agent: sngl\r\nConnection: close\r\n\r\n",
                                 path && *path ? path : "/", query ? "?" : "", query ? query : "", host);
    g_free(host);
    g_uri_unref(uri);
    GSocketClient *c = g_socket_client_new();
    g_socket_client_set_tls(c, tls);
    g_socket_client_connect_to_uri_async(c, f->url, tls ? 443 : 80, NULL, sngl_fetch_connected, f);
}

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

/* A file the program carries is read at once, from those it was handed
 * (sngl_gtk_carry) or else beside it; one fetched arrives from the main
 * loop, through the runtime's fetch. */
SnglPicture *sngl_picture_load(const char *url) {
    SnglPicture *pic = g_new0(SnglPicture, 1);
    pic->url = g_strdup(url);
    pic->watching = g_ptr_array_new();
    pic->refs = 1;
    if (g_str_has_prefix(url, "http://") || g_str_has_prefix(url, "https://")) {
        pic->pending = true;
        SnglFetch *f = g_new0(SnglFetch, 1);
        f->pic = pic;
        f->url = g_strdup(url);
        f->got = g_byte_array_new();
        sngl_fetch_start(f);
        return pic;
    }
    const char *name = url[0] == '/' ? url + 1 : url;
    for (const SnglAsset *a = sngl_carried; a && a->name; a++) {
        if (strcmp(a->name, name) == 0) {
            sngl_picture_arrive(pic, a->data, a->len, NULL);
            return pic;
        }
    }
    for (size_t i = 0; i < sngl_ncarried_files; i++) {
        const SnglAsset *a = &sngl_carried_files[i];
        if (strcmp(a->name, name) == 0) {
            sngl_picture_arrive(pic, a->data, a->len, NULL);
            return pic;
        }
    }
    char *bytes = NULL;
    gsize len = 0;
    GError *err = NULL;
    g_file_get_contents(name, &bytes, &len, &err);
    sngl_picture_arrive(pic, (const guchar *)bytes, len, err ? err->message : NULL);
    g_clear_error(&err);
    g_free(bytes);
    return pic;
}

SnglPicture *sngl_picture_ref(SnglPicture *pic) {
    if (pic && !pic->released) pic->refs++;
    return pic;
}

/* The sources showing a picture given back keep what they show. */
void sngl_picture_unref(SnglPicture *pic) {
    if (!pic || pic->released || --pic->refs > 0) return;
    pic->released = true;
    for (guint i = 0; i < pic->watching->len; i++) {
        SnglSource *s = g_ptr_array_index(pic->watching, i);
        s->picture = NULL;
    }
    g_ptr_array_set_size(pic->watching, 0);
    if (!pic->pending) sngl_picture_free(pic);
}

/* The picture is scaled to the box once and painted from that until the box
 * changes size; one still on its way paints nothing. */
void sngl_picture_paint(cairo_t *cr, SnglPicture *pic, double x, double y, double w, double h) {
    if (!pic || !pic->tex || w <= 0 || h <= 0) return;
    int sw = (int)ceil(w), sh = (int)ceil(h);
    if (!pic->scaled || pic->sw != sw || pic->sh != sh) {
        if (!pic->surface) {
            int tw = gdk_texture_get_width(pic->tex), th = gdk_texture_get_height(pic->tex);
            if (tw <= 0 || th <= 0) return;
            pic->surface = cairo_image_surface_create(CAIRO_FORMAT_ARGB32, tw, th);
            gdk_texture_download(pic->tex, cairo_image_surface_get_data(pic->surface), cairo_image_surface_get_stride(pic->surface));
            cairo_surface_mark_dirty(pic->surface);
        }
        if (pic->scaled) cairo_surface_destroy(pic->scaled);
        int tw = cairo_image_surface_get_width(pic->surface), th = cairo_image_surface_get_height(pic->surface);
        pic->scaled = cairo_image_surface_create(CAIRO_FORMAT_ARGB32, sw, sh);
        cairo_t *sc = cairo_create(pic->scaled);
        cairo_scale(sc, (double)sw / tw, (double)sh / th);
        cairo_set_source_surface(sc, pic->surface, 0, 0);
        cairo_paint(sc);
        cairo_destroy(sc);
        pic->sw = sw;
        pic->sh = sh;
    }
    cairo_save(cr);
    cairo_set_source_surface(cr, pic->scaled, x, y);
    cairo_paint(cr);
    cairo_restore(cr);
}

/* GTK 4 takes a window's icon by name only, so the picture is written into a
 * directory of the icon theme's search path under a name of its own, a new
 * one each time it changes. */
static void sngl_icon_apply(GtkWidget *toplevel, GdkTexture *tex) {
    static char *dir;
    static int n;
    GtkWindow *win = sngl_toplevel_window(toplevel);
    if (!win || !tex) return;
    if (!dir) {
        dir = g_dir_make_tmp("sngl-icons-XXXXXX", NULL);
        if (!dir) return;
        gtk_icon_theme_add_search_path(gtk_icon_theme_get_for_display(gdk_display_get_default()), dir);
    }
    char *name = g_strdup_printf("sngl-icon-%d-%d", (int)getpid(), ++n);
    char *file = g_strdup_printf("%s.png", name);
    char *path = g_build_filename(dir, file, NULL);
    if (gdk_texture_save_to_png(tex, path)) gtk_window_set_icon_name(win, name);
    g_free(path);
    g_free(file);
    g_free(name);
}

void sngl_toplevel_icon_refresh(GtkWidget *toplevel) {
    SnglSource *s = g_object_get_data(G_OBJECT(toplevel), "sngl-icon");
    if (s) sngl_icon_apply(toplevel, s->tex);
}

/* A window takes a source appended to it as its icon rather than as a
 * widget. */
bool sngl_image_adopt(GtkWidget *box, GtkWidget *child) {
    SnglSource *s = sngl_source_of(child);
    if (!s || !g_object_get_data(G_OBJECT(box), "sngl-toplevel")) return false;
    g_object_ref(child);
    g_object_set_data_full(G_OBJECT(box), "sngl-icon-widget", child, g_object_unref);
    g_object_set_data(G_OBJECT(box), "sngl-icon", s);
    s->icon = box;
    sngl_icon_apply(box, s->tex);
    return true;
}

bool sngl_image_unadopt(GtkWidget *box, GtkWidget *child) {
    SnglSource *s = sngl_source_of(child);
    if (!s || g_object_get_data(G_OBJECT(box), "sngl-icon") != s) return false;
    s->icon = NULL;
    g_object_set_data(G_OBJECT(box), "sngl-icon", NULL);
    g_object_set_data(G_OBJECT(box), "sngl-icon-widget", NULL);
    return true;
}
