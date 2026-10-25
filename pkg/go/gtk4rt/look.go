//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <stdlib.h>

typedef struct {
    GtkCssProvider *css;
    int width, height;
    gboolean sized;
    int width0, height0;
    double grow;
    int grow_axis;
    gboolean expand0, expand_set0;
    GtkWidget *grow_box;
    int growers;
    gboolean box_expand0, box_expand_set0;
    gboolean watching_parent;
    gboolean aligned;
    int align_axis;
    GtkAlign align0;
    int cross;
    GListModel *kids;
    gulong kids_id;
    gboolean label_sized;
    gboolean text_saved;
    float xalign0;
    GtkJustification justify0;
    gboolean wrap0;
    int lines0;
    PangoEllipsizeMode ellipsize0;
    gboolean hidden, visible0;
    gboolean cursor;
} SnglLook;

static void sngl_unpin(SnglLook *l, int axis);

static void sngl_look_free(gpointer data) {
    SnglLook *l = data;
    sngl_unpin(l, l->grow_axis);
    if (l->css) g_object_unref(l->css);
    if (l->kids) {
        g_signal_handler_disconnect(l->kids, l->kids_id);
        g_object_unref(l->kids);
    }
    g_free(l);
}

static SnglLook *sngl_look_of(GtkWidget *w) {
    return g_object_get_data(G_OBJECT(w), "sngl-look");
}

static SnglLook *sngl_look_ensure(GtkWidget *w) {
    SnglLook *l = sngl_look_of(w);
    if (!l) {
        l = g_new0(SnglLook, 1);
        l->grow_axis = -1;
        g_object_set_data_full(G_OBJECT(w), "sngl-look", l, sngl_look_free);
    }
    return l;
}

static gboolean sngl_expand_set(GtkWidget *w, int axis) {
    return axis == 0 ? gtk_widget_get_hexpand_set(w) : gtk_widget_get_vexpand_set(w);
}

static void sngl_set_expand(GtkWidget *w, int axis, gboolean on) {
    if (axis == 0) gtk_widget_set_hexpand(w, on); else gtk_widget_set_vexpand(w, on);
}

// A growing child pins the box it is in, which counts it, so that GTK hands
// the child's expand no further up: a CSS flex item grows no box. The last
// one out gives the box back what it had.
static void sngl_pin(GtkWidget *box, SnglLook *l, int axis) {
    SnglLook *bl = sngl_look_ensure(box);
    if (bl->growers++ == 0) {
        bl->box_expand0 = axis == 0 ? gtk_widget_get_hexpand(box) : gtk_widget_get_vexpand(box);
        bl->box_expand_set0 = sngl_expand_set(box, axis);
        if (!bl->box_expand_set0) sngl_set_expand(box, axis, FALSE);
    }
    l->grow_box = box;
    g_object_add_weak_pointer(G_OBJECT(box), (gpointer *)&l->grow_box);
}

static void sngl_unpin(SnglLook *l, int axis) {
    GtkWidget *box = l->grow_box;
    if (!box) return;
    g_object_remove_weak_pointer(G_OBJECT(box), (gpointer *)&l->grow_box);
    l->grow_box = NULL;
    SnglLook *bl = sngl_look_of(box);
    if (!bl || --bl->growers > 0) return;
    sngl_set_expand(box, axis, bl->box_expand0);
    if (axis == 0) gtk_widget_set_hexpand_set(box, bl->box_expand_set0);
    else gtk_widget_set_vexpand_set(box, bl->box_expand_set0);
}

static void sngl_restore_expand(GtkWidget *w, SnglLook *l) {
    if (l->grow_axis < 0) return;
    sngl_unpin(l, l->grow_axis);
    sngl_set_expand(w, l->grow_axis, l->expand0);
    if (l->grow_axis == 0) gtk_widget_set_hexpand_set(w, l->expand_set0);
    else gtk_widget_set_vexpand_set(w, l->expand_set0);
    l->grow_axis = -1;
}

static GtkAlign sngl_cross_align(int cross) {
    switch (cross) {
    case 2: return GTK_ALIGN_CENTER;
    case 3: return GTK_ALIGN_END;
#if GTK_CHECK_VERSION(4, 12, 0)
    case 4: return GTK_ALIGN_BASELINE_FILL;
#else
    case 4: return GTK_ALIGN_BASELINE;
#endif
    }
    return GTK_ALIGN_FILL;
}

static GtkAlign sngl_get_align(GtkWidget *w, int axis) {
    return axis == 0 ? gtk_widget_get_halign(w) : gtk_widget_get_valign(w);
}

static void sngl_set_align(GtkWidget *w, int axis, GtkAlign a) {
    if (axis == 0) gtk_widget_set_halign(w, a); else gtk_widget_set_valign(w, a);
}

static void sngl_unalign(GtkWidget *w, SnglLook *l) {
    if (!l || !l->aligned) return;
    sngl_set_align(w, l->align_axis, l->align0);
    l->aligned = FALSE;
}

static void sngl_look_watch_parent(GtkWidget *w, SnglLook *l);

// What the box a widget sits in reads off it: a share along the box's axis,
// and an alignment across it from the box's alignItems, or the start where a
// stretching box holds a child of fixed size, as CSS places one.
static void sngl_look_place(GtkWidget *w) {
    SnglLook *l = sngl_look_of(w);
    GtkWidget *parent = gtk_widget_get_parent(w);
    int main_axis = -1;
    if (parent && GTK_IS_ORIENTABLE(parent))
        main_axis = gtk_orientable_get_orientation(GTK_ORIENTABLE(parent)) == GTK_ORIENTATION_HORIZONTAL ? 0 : 1;
    if (l) {
        if (l->grow_axis >= 0 && (l->grow <= 0 || l->grow_axis != main_axis))
            sngl_restore_expand(w, l);
        if (l->grow > 0 && main_axis >= 0 && l->grow_axis < 0) {
            l->grow_axis = main_axis;
            l->expand0 = main_axis == 0 ? gtk_widget_get_hexpand(w) : gtk_widget_get_vexpand(w);
            l->expand_set0 = sngl_expand_set(w, main_axis);
            sngl_set_expand(w, main_axis, TRUE);
            sngl_pin(parent, l, main_axis);
        }
    }
    if (main_axis < 0) {
        sngl_unalign(w, l);
        return;
    }
    SnglLook *pl = sngl_look_of(parent);
    int cross = pl ? pl->cross : 0;
    gboolean fixed = l && (main_axis == 1 ? l->width > 0 : l->height > 0);
    if (cross == 0 && !fixed) {
        sngl_unalign(w, l);
        return;
    }
    int axis = main_axis == 1 ? 0 : 1;
    if (!l) l = sngl_look_ensure(w);
    sngl_look_watch_parent(w, l);
    if (l->aligned && l->align_axis != axis) sngl_unalign(w, l);
    if (!l->aligned) {
        l->aligned = TRUE;
        l->align_axis = axis;
        l->align0 = sngl_get_align(w, axis);
    }
    sngl_set_align(w, axis, cross == 0 ? GTK_ALIGN_START : sngl_cross_align(cross));
}

static void sngl_look_parent_changed(GObject *o, GParamSpec *ps, gpointer data) {
    (void)ps; (void)data;
    sngl_look_place(GTK_WIDGET(o));
}

static void sngl_look_watch_parent(GtkWidget *w, SnglLook *l) {
    if (l->watching_parent) return;
    l->watching_parent = TRUE;
    g_signal_connect(w, "notify::parent", G_CALLBACK(sngl_look_parent_changed), NULL);
}

static void sngl_look_place_kids(GtkWidget *box) {
    for (GtkWidget *c = gtk_widget_get_first_child(box); c; c = gtk_widget_get_next_sibling(c))
        sngl_look_place(c);
}

static void sngl_look_kids_changed(GListModel *m, guint pos, guint removed, guint added, gpointer box) {
    (void)m; (void)pos; (void)removed;
    if (added) sngl_look_place_kids(GTK_WIDGET(box));
}

// GTK tells a box nothing when a child is added, so a box aligning its
// children watches its own list of them. GTK warns that the list costs
// bookkeeping on every change, which only a box with an alignItems pays.
static void sngl_look_set_cross(GtkWidget *w, SnglLook *l, int cross) {
    if (cross == l->cross) return;
    l->cross = cross;
    if (cross != 0 && !l->kids) {
        l->kids = gtk_widget_observe_children(w);
        l->kids_id = g_signal_connect(l->kids, "items-changed", G_CALLBACK(sngl_look_kids_changed), w);
    } else if (cross == 0 && l->kids) {
        g_signal_handler_disconnect(l->kids, l->kids_id);
        g_object_unref(l->kids);
        l->kids = NULL;
    }
    sngl_look_place_kids(w);
}

// One rule naming the widget's own node, so a node it draws inside itself (a
// check button's check) is not restyled with it; a popover draws its body on
// its contents node. gtk_style_context_add_provider is deprecated since 4.10
// in favor of style classes, which would put every distinct look in one
// display-wide sheet and restyle every widget whenever a look changed; this
// restyles one.
static void sngl_look_set_css(GtkWidget *w, SnglLook *l, const char *decls) {
    G_GNUC_BEGIN_IGNORE_DEPRECATIONS
    GtkStyleContext *ctx = gtk_widget_get_style_context(w);
    if (!*decls) {
        if (l->css) {
            gtk_style_context_remove_provider(ctx, GTK_STYLE_PROVIDER(l->css));
            g_object_unref(l->css);
            l->css = NULL;
        }
    } else if (!l->css) {
        l->css = gtk_css_provider_new();
        gtk_style_context_add_provider(ctx, GTK_STYLE_PROVIDER(l->css), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
    }
    G_GNUC_END_IGNORE_DEPRECATIONS
    if (!*decls) return;
    char *rule = GTK_IS_POPOVER(w) ? g_strdup_printf("popover > contents { %s }", decls)
                                   : g_strdup_printf("%s { %s }", gtk_widget_get_css_name(w), decls);
#if GTK_CHECK_VERSION(4, 12, 0)
    gtk_css_provider_load_from_string(l->css, rule);
#else
    gtk_css_provider_load_from_data(l->css, rule, -1);
#endif
    g_free(rule);
}

// The widget a widget's text is in: its own, or a button's label, which every
// override sets before the look.
static GtkWidget *sngl_text_widget(GtkWidget *w) {
    if (GTK_IS_LABEL(w) || GTK_IS_EDITABLE(w)) return w;
    if (!GTK_IS_BUTTON(w) && !GTK_IS_CHECK_BUTTON(w)) return NULL;
    for (GtkWidget *c = gtk_widget_get_first_child(w); c; c = gtk_widget_get_next_sibling(c))
        if (GTK_IS_LABEL(c)) return c;
    return NULL;
}

static void sngl_look_set_text(GtkWidget *w, SnglLook *l, int align, int lines, int wrap) {
    GtkWidget *t = sngl_text_widget(w);
    if (!t) return;
    if (!l->text_saved) {
        if (!align && !lines && !wrap) return;
        l->text_saved = TRUE;
        if (GTK_IS_LABEL(t)) {
            GtkLabel *lb = GTK_LABEL(t);
            l->xalign0 = gtk_label_get_xalign(lb);
            l->justify0 = gtk_label_get_justify(lb);
            l->wrap0 = gtk_label_get_wrap(lb);
            l->lines0 = gtk_label_get_lines(lb);
            l->ellipsize0 = gtk_label_get_ellipsize(lb);
        } else {
            l->xalign0 = gtk_editable_get_alignment(GTK_EDITABLE(t));
        }
    }
    float x = align == 1 ? 0.5f : align == 2 ? 1.0f : align == 3 ? 0.0f : l->xalign0;
    if (!GTK_IS_LABEL(t)) {
        gtk_editable_set_alignment(GTK_EDITABLE(t), x);
        return;
    }
    GtkLabel *lb = GTK_LABEL(t);
    gtk_label_set_xalign(lb, x);
    gtk_label_set_justify(lb, align == 1 ? GTK_JUSTIFY_CENTER : align == 2 ? GTK_JUSTIFY_RIGHT : align == 3 ? GTK_JUSTIFY_FILL : l->justify0);
    gtk_label_set_lines(lb, lines > 0 ? lines : l->lines0);
    gtk_label_set_ellipsize(lb, lines > 0 ? PANGO_ELLIPSIZE_END : l->ellipsize0);
    gtk_label_set_wrap(lb, lines > 0 || wrap == 2 ? TRUE : wrap == 1 ? FALSE : l->wrap0);
}

// A label sized by its look is that size, its words wrapping inside the
// width as html's do: a size request is only the least a widget is, and a
// long line widened it. It is measured and allocated by GtkLabel's own code,
// given the width it is.
static GtkWidgetClass *sngl_label_class(void) {
    return GTK_WIDGET_CLASS(g_type_class_peek(GTK_TYPE_LABEL));
}
static void sngl_label_measure(GtkWidget *w, GtkOrientation o, int for_size,
        int *min, int *nat, int *min_base, int *nat_base) {
    SnglLook *l = sngl_look_of(w);
    int width = l ? l->width : 0, height = l ? l->height : 0;
    if (o == GTK_ORIENTATION_VERTICAL && width > 0) for_size = width;
    sngl_label_class()->measure(w, o, for_size, min, nat, min_base, nat_base);
    int want = o == GTK_ORIENTATION_HORIZONTAL ? width : height;
    if (want > 0) *min = *nat = want;
}
static void sngl_label_allocate(GtkWidget *w, int width, int height, int baseline) {
    sngl_label_class()->size_allocate(w, width, height, baseline);
}
static GtkSizeRequestMode sngl_label_mode(GtkWidget *w) {
    return GTK_SIZE_REQUEST_HEIGHT_FOR_WIDTH;
}
static void sngl_look_size_label(GtkWidget *w, SnglLook *l, int wrap) {
    if (!GTK_IS_LABEL(w) || (!l->width && !l->height && !l->label_sized)) return;
    if (!l->label_sized) {
        l->label_sized = TRUE;
        gtk_widget_set_layout_manager(w,
            gtk_custom_layout_new(sngl_label_mode, sngl_label_measure, sngl_label_allocate));
    }
    if (l->width > 0 && wrap != 1) gtk_label_set_wrap(GTK_LABEL(w), TRUE);
    gtk_widget_queue_resize(w);
}

static void sngl_set_look(GtkWidget *w, const char *css, int width, int height, double grow,
                          int cross, int align, int lines, int wrap, int hidden, const char *cursor) {
    SnglLook *l = sngl_look_of(w);
    if (!l && !*css && !width && !height && grow <= 0 && !cross && !align && !lines && !wrap && !hidden && !*cursor)
        return;
    l = sngl_look_ensure(w);
    sngl_look_set_css(w, l, css);
    if ((width || height) && !l->sized) {
        l->sized = TRUE;
        gtk_widget_get_size_request(w, &l->width0, &l->height0);
    }
    if (l->sized)
        gtk_widget_set_size_request(w, width > 0 ? width : l->width0, height > 0 ? height : l->height0);
    l->width = width;
    l->height = height;
    l->grow = grow;
    if (grow > 0 || width || height) sngl_look_watch_parent(w, l);
    sngl_look_place(w);
    sngl_look_set_cross(w, l, cross);
    sngl_look_set_text(w, l, align, lines, wrap);
    sngl_look_size_label(w, l, wrap);
    if (hidden && !l->hidden) {
        l->visible0 = gtk_widget_get_visible(w);
        gtk_widget_set_visible(w, FALSE);
    } else if (!hidden && l->hidden) {
        gtk_widget_set_visible(w, l->visible0);
    }
    l->hidden = hidden;
    if (*cursor || l->cursor) gtk_widget_set_cursor_from_name(w, *cursor ? cursor : NULL);
    l->cursor = *cursor != 0;
}
*/
import "C"

import (
	"math"
	"unsafe"
)

// Look is what a ui.Style asks of a GTK widget, built by gtk4's style.sngl.
// Zero is unset in every field, and a field a later Look leaves unset gives
// the widget back what it had when a Look first set it.
type Look struct {
	// Css is declarations, without a selector.
	Css string
	// Width and Height are the size the widget is requested at, in pixels.
	// GTK may allocate more, never less.
	Width, Height float64
	// Grow is the widget's flex: above zero it expands along the axis of the
	// box it sits in.
	Grow float64
	// Cross is a box's alignItems, which it puts on each child: 2 center,
	// 3 end, 4 baseline; zero stretches.
	Cross int
	// TextAlign is a label's, an entry's or a button's: 1 center, 2 right,
	// 3 justify.
	TextAlign int
	// Lines ellipsizes a label at that many lines.
	Lines int
	// Wrap is a label's: 1 one line, 2 wrapped.
	Wrap   int
	Hidden bool
	Cursor string
}

// SetLook gives w what l asks for, and back what an earlier Look took that l
// leaves unset.
func SetLook(w Handle, l Look) {
	css, freeCSS := cstr(l.Css)
	defer freeCSS()
	cursor, freeCursor := cstr(l.Cursor)
	defer freeCursor()
	C.sngl_set_look(widget(w), css, C.int(math.Round(l.Width)), C.int(math.Round(l.Height)), C.double(l.Grow),
		C.int(l.Cross), C.int(l.TextAlign), C.int(l.Lines), C.int(l.Wrap), C.int(bool01(l.Hidden)), cursor)
}

func bool01(b bool) int {
	if b {
		return 1
	}
	return 0
}

// The probes below are look_test.go's: a _test.go file cannot use cgo.

func expands(w Handle) (h, v bool) {
	return C.gtk_widget_get_hexpand(widget(w)) != 0, C.gtk_widget_get_vexpand(widget(w)) != 0
}

func aligns(w Handle) (h, v C.GtkAlign) {
	return C.gtk_widget_get_halign(widget(w)), C.gtk_widget_get_valign(widget(w))
}

func labelXAlign(w Handle) float64 {
	return float64(C.gtk_label_get_xalign((*C.GtkLabel)(p(w))))
}

func shown(w Handle) bool { return C.gtk_widget_get_visible(widget(w)) != 0 }

func setShown(w Handle, v bool) { C.gtk_widget_set_visible(widget(w), gbool(v)) }

func setHAlign(w Handle, a C.GtkAlign) { C.gtk_widget_set_halign(widget(w), a) }

func computesVExpand(w Handle) bool {
	return C.gtk_widget_compute_expand(widget(w), C.GTK_ORIENTATION_VERTICAL) != 0
}

func firstChild(w Handle) Handle {
	return Handle(unsafe.Pointer(C.gtk_widget_get_first_child(widget(w))))
}

func setVExpand(w Handle, v bool) { C.gtk_widget_set_vexpand(widget(w), gbool(v)) }
