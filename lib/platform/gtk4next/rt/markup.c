/* Rich text on GTK: a flow is one GtkLabel, and the spans in it are its
 * Pango markup. Pango nests as the spans do -- `<b><i>x</i></b>` is what
 * `bold { italic { … } }` means -- so the tree a program builds is the tree
 * the markup is written from, and Pango resolves the cascade.
 *
 * A span is no widget: it is a SnglSpan, an object holding its words, its
 * link, the tags its style opens and closes, and the spans in it. It is
 * handed about as every node is, as a GtkWidget pointer, and is one only to
 * the functions here. A flow, or a span, holds the spans put in it; taking
 * one out lets it go. Any change sets its flow's markup again, whole. */
#include "sngl_gtk.h"

#include <string.h>

#define SNGL_TYPE_SPAN (sngl_span_get_type())
G_DECLARE_FINAL_TYPE(SnglSpan, sngl_span, SNGL, SPAN, GInitiallyUnowned)

struct _SnglSpan {
    GInitiallyUnowned parent_instance;
    /* The span or the flow it is in, which it does not hold. */
    gpointer parent;
    char *words;
    char *href;
    char *open;
    char *close;
    GPtrArray *kids;
};

G_DEFINE_FINAL_TYPE(SnglSpan, sngl_span, G_TYPE_INITIALLY_UNOWNED)

static void sngl_span_finalize(GObject *o) {
    SnglSpan *s = SNGL_SPAN(o);
    for (guint i = 0; i < s->kids->len; i++) {
        SNGL_SPAN(g_ptr_array_index(s->kids, i))->parent = NULL;
    }
    g_ptr_array_unref(s->kids);
    g_free(s->words);
    g_free(s->href);
    g_free(s->open);
    g_free(s->close);
    G_OBJECT_CLASS(sngl_span_parent_class)->finalize(o);
}

static void sngl_span_class_init(SnglSpanClass *c) {
    G_OBJECT_CLASS(c)->finalize = sngl_span_finalize;
}

static void sngl_span_init(SnglSpan *s) {
    s->kids = g_ptr_array_new_with_free_func(g_object_unref);
}

/* Lets go of the spans a flow held, which are in it no longer. */
static void sngl_flow_kids_free(gpointer data) {
    GPtrArray *kids = data;
    for (guint i = 0; i < kids->len; i++) {
        SNGL_SPAN(g_ptr_array_index(kids, i))->parent = NULL;
    }
    g_ptr_array_unref(kids);
}

/* The spans a flow holds, made on first use and let go with the label. */
static GPtrArray *sngl_flow_kids(GtkWidget *flow) {
    GPtrArray *kids = g_object_get_data(G_OBJECT(flow), "sngl-spans");
    if (kids == NULL) {
        kids = g_ptr_array_new_with_free_func(g_object_unref);
        g_object_set_data_full(G_OBJECT(flow), "sngl-spans", kids, sngl_flow_kids_free);
    }
    return kids;
}

static void sngl_span_write(GString *out, SnglSpan *s) {
    if (s->href != NULL && s->href[0] != '\0') {
        char *href = g_markup_escape_text(s->href, -1);
        g_string_append_printf(out, "<a href=\"%s\">", href);
        g_free(href);
    }
    if (s->open != NULL) g_string_append(out, s->open);
    if (s->words != NULL && s->words[0] != '\0') {
        char *words = g_markup_escape_text(s->words, -1);
        g_string_append(out, words);
        g_free(words);
    }
    for (guint i = 0; i < s->kids->len; i++) {
        sngl_span_write(out, g_ptr_array_index(s->kids, i));
    }
    if (s->close != NULL) g_string_append(out, s->close);
    if (s->href != NULL && s->href[0] != '\0') g_string_append(out, "</a>");
}

/* Sets the markup of the flow s is in, if it is in one yet. */
static void sngl_span_changed(gpointer at) {
    while (at != NULL && SNGL_IS_SPAN(at)) {
        at = SNGL_SPAN(at)->parent;
    }
    if (at == NULL) return;
    GtkWidget *flow = at;
    GPtrArray *kids = sngl_flow_kids(flow);
    GString *out = g_string_new(NULL);
    for (guint i = 0; i < kids->len; i++) {
        sngl_span_write(out, g_ptr_array_index(kids, i));
    }
    gtk_label_set_markup(GTK_LABEL(flow), out->str);
    g_string_free(out, TRUE);
}

GtkWidget *sngl_span_new(void) {
    return (GtkWidget *)g_object_new(SNGL_TYPE_SPAN, NULL);
}

/* What parent holds: a span's kids, or a flow's. */
static GPtrArray *sngl_span_kids_of(GtkWidget *parent) {
    return SNGL_IS_SPAN(parent) ? SNGL_SPAN(parent)->kids : sngl_flow_kids(parent);
}

void sngl_span_insert(GtkWidget *parent, GtkWidget *child, GtkWidget *before) {
    SnglSpan *s = SNGL_SPAN(child);
    GPtrArray *kids = sngl_span_kids_of(parent);
    guint at;
    /* A span parent already holds moves, as a keyed loop's kept row does. */
    if (s->parent == parent && g_ptr_array_find(kids, s, &at)) {
        g_object_ref(s);
        g_ptr_array_remove_index(kids, at);
    } else {
        g_object_ref_sink(s);
        if (s->parent != NULL) {
            GPtrArray *old = sngl_span_kids_of(s->parent);
            if (g_ptr_array_find(old, s, &at)) {
                g_object_ref(s);
                g_ptr_array_remove_index(old, at);
                g_object_unref(s);
            }
        }
    }
    s->parent = parent;
    if (before != NULL && g_ptr_array_find(kids, before, &at)) {
        g_ptr_array_insert(kids, (gint)at, s);
    } else {
        g_ptr_array_add(kids, s);
    }
    sngl_span_changed(parent);
}

void sngl_span_append(GtkWidget *parent, GtkWidget *child) {
    sngl_span_insert(parent, child, NULL);
}

void sngl_span_remove(GtkWidget *parent, GtkWidget *child) {
    SnglSpan *s = SNGL_SPAN(child);
    GPtrArray *kids = sngl_span_kids_of(parent);
    guint at;
    if (!g_ptr_array_find(kids, s, &at)) return;
    s->parent = NULL;
    g_ptr_array_remove_index(kids, at);
    sngl_span_changed(parent);
}

void sngl_span_set_words(GtkWidget *w, const char *words) {
    SnglSpan *s = SNGL_SPAN(w);
    g_free(s->words);
    s->words = g_strdup(words);
    sngl_span_changed(s);
}

void sngl_span_set_href(GtkWidget *w, const char *href) {
    SnglSpan *s = SNGL_SPAN(w);
    g_free(s->href);
    s->href = g_strdup(href);
    sngl_span_changed(s);
}

/* A style as the Pango a run's words are wrapped in: Pango's own elements
 * for a weight, a slant, a line and a monospace family, and a span's
 * attributes for the rest. What the style leaves alone writes nothing, so
 * the run inherits it from the span around it, as the cascade asks. The
 * weights and slants are markup's, inherit first. */
void sngl_span_set_style(GtkWidget *w, int r, int g, int b, int a, double size, int weight, int slant,
                         int underline, int strike, const char *family) {
    enum { inherit, normal, bold, lighter, bolder };
    enum { slant_inherit, slant_normal, italic, oblique };
    SnglSpan *s = SNGL_SPAN(w);
    bool mono = family != NULL && strcmp(family, "monospace") == 0;
    GString *attrs = g_string_new(NULL);
    if (weight == normal || weight == lighter) g_string_append(attrs, " weight=\"normal\"");
    if (slant == slant_normal) g_string_append(attrs, " style=\"normal\"");
    if (family != NULL && family[0] != '\0' && !mono) {
        char *f = g_markup_escape_text(family, -1);
        g_string_append_printf(attrs, " font_family=\"%s\"", f);
        g_free(f);
    }
    if (a != 0) g_string_append_printf(attrs, " foreground=\"#%02X%02X%02X\"", r & 0xff, g & 0xff, b & 0xff);
    if (size > 0) {
        char px[G_ASCII_DTOSTR_BUF_SIZE];
        g_string_append_printf(attrs, " font_desc=\"%spx\"", g_ascii_dtostr(px, sizeof px, size));
    }
    bool b_ = weight == bold || weight == bolder, i_ = slant == italic || slant == oblique;
    GString *open = g_string_new(NULL), *close = g_string_new(NULL);
    if (b_) g_string_append(open, "<b>");
    if (i_) g_string_append(open, "<i>");
    if (underline) g_string_append(open, "<u>");
    if (strike) g_string_append(open, "<s>");
    if (mono) g_string_append(open, "<tt>");
    if (attrs->len > 0) {
        g_string_append_printf(open, "<span%s>", attrs->str);
        g_string_append(close, "</span>");
    }
    if (mono) g_string_append(close, "</tt>");
    if (strike) g_string_append(close, "</s>");
    if (underline) g_string_append(close, "</u>");
    if (i_) g_string_append(close, "</i>");
    if (b_) g_string_append(close, "</b>");
    g_free(s->open);
    g_free(s->close);
    s->open = g_string_free(open, FALSE);
    s->close = g_string_free(close, FALSE);
    g_string_free(attrs, TRUE);
    sngl_span_changed(s);
}
