/* What a widget's look is on GTK: sngl:ui's Style as gtk4.sngl translates it
 * (style.sngl), applied by sngl_set_look -- its CSS, size, flex, alignment,
 * spacing and a box's layout. Compiled into every gtk4 program: the Go
 * wrappers include it, and a C program compiles it beside its own. */
#include "sngl_gtk.h"

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
    int justify;
    gboolean spaced;
    int spacing;
    // GTK's own layout of a box, kept while the runtime's lays the box out.
    GtkLayoutManager *box_layout;
    // A box that is no box of the program's: its grow is its children's.
    gboolean contents;
    // A box that is sngl:ui's grid, laid out a row at a time: `columns` to a
    // row, or flowing where it is zero.
    gboolean grid;
    int columns;
    // The arguments sngl_set_look was last given: a row a list sets again
    // with what it already has, as every row is each time the list's
    // value changes, then costs a comparison rather than a relayout.
    char *given;
} SnglLook;

static void sngl_unpin(SnglLook *l, int axis);
static void sngl_box_update(GtkWidget *box);
static void sngl_contents_update(GtkWidget *box);

static void sngl_look_free(gpointer data) {
    SnglLook *l = data;
    sngl_unpin(l, l->grow_axis);
    if (l->css) g_object_unref(l->css);
    if (l->box_layout) g_object_unref(l->box_layout);
    g_free(l->given);
    if (l->kids) {
        g_signal_handler_disconnect(l->kids, l->kids_id);
        g_object_unref(l->kids);
    }
    g_free(l);
}

static SnglLook *sngl_look_of(GtkWidget *w) {
    return g_object_get_data(G_OBJECT(w), "sngl-look");
}

// The axis of a box. GtkBox keeps its orientation in its layout manager, so
// once the runtime has given a box a layout of its own, the box answers
// through one that is not a GtkBoxLayout -- horizontal, with a critical on
// stderr -- and its own, kept aside, is what knows.
GtkOrientation sngl_box_axis(GtkWidget *box) {
    SnglLook *l = sngl_look_of(box);
    if (l && l->box_layout) return gtk_orientable_get_orientation(GTK_ORIENTABLE(l->box_layout));
    return gtk_orientable_get_orientation(GTK_ORIENTABLE(box));
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
    sngl_box_update(box);
    sngl_contents_update(box);
}

static void sngl_unpin(SnglLook *l, int axis) {
    GtkWidget *box = l->grow_box;
    if (!box) return;
    g_object_remove_weak_pointer(G_OBJECT(box), (gpointer *)&l->grow_box);
    l->grow_box = NULL;
    SnglLook *bl = sngl_look_of(box);
    if (!bl) return;
    sngl_box_update(box);
    // The contents gives its own grow back before the box its expand, which
    // the grow was set over.
    bl->growers--;
    sngl_contents_update(box);
    if (bl->growers > 0) return;
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

// --- A box laid out by the runtime ---
//
// A box whose look justifies, or whose growing children ask for unequal
// shares, and a grid, are laid out by gtk4.sngl's functions (its "A box laid
// out by the runtime"), which read what the look says through the
// functions below; which box takes them, and gives GTK's own layout back
// when it asks for neither, is decided here.

int sngl_box_spacing(GtkWidget *box) {
    SnglLook *l = sngl_look_of(box);
    if (l && l->spaced) return l->spacing;
    if (l && l->box_layout) return gtk_box_layout_get_spacing(GTK_BOX_LAYOUT(l->box_layout));
    return gtk_box_get_spacing(GTK_BOX(box));
}

// The share of the spare room a child takes: its look's flex, or one share
// where it expands itself, as a GIR widget given `hexpand` does. An expand
// GTK carries up from something the child holds is no share: a CSS flex item
// grows by its own flex, never by what is inside it, so a box holding such a
// widget is its content's size.
double sngl_look_weight(GtkWidget *c, GtkOrientation o) {
    SnglLook *cl = sngl_look_of(c);
    if (cl && cl->grow > 0) return cl->grow;
    gboolean own = o == GTK_ORIENTATION_HORIZONTAL ? gtk_widget_get_hexpand(c) : gtk_widget_get_vexpand(c);
    return own ? 1 : 0;
}

// Whether the children's shares are not what GTK's box would give them: two
// weights that differ, or a weight beside a child GTK would grow for what it
// holds. A box with no weight at all is left to GTK's, carried expands and
// all.
static gboolean sngl_weights_uneven(GtkWidget *box) {
    GtkOrientation o = sngl_box_axis(box);
    double first = 0;
    gboolean carried = FALSE;
    for (GtkWidget *c = gtk_widget_get_first_child(box); c; c = gtk_widget_get_next_sibling(c)) {
        if (!gtk_widget_should_layout(c)) continue;
        double w = sngl_look_weight(c, o);
        if (w <= 0) {
            carried = carried || gtk_widget_compute_expand(c, o);
            continue;
        }
        if (first == 0) first = w;
        else if (w != first) return TRUE;
    }
    return carried && first > 0;
}

// The functions a box the runtime lays out and a grid are laid out by:
// gtk4.sngl's, which hands them over before the first look is set.
static GtkCustomRequestModeFunc sngl_flex_mode, sngl_grid_mode;
static GtkCustomMeasureFunc sngl_flex_measure, sngl_grid_measure;
static GtkCustomAllocateFunc sngl_flex_allocate, sngl_grid_allocate;

void sngl_layout_hooks(GtkCustomRequestModeFunc flex_mode, GtkCustomMeasureFunc flex_measure, GtkCustomAllocateFunc flex_allocate,
                       GtkCustomRequestModeFunc grid_mode, GtkCustomMeasureFunc grid_measure, GtkCustomAllocateFunc grid_allocate) {
    sngl_flex_mode = flex_mode;
    sngl_flex_measure = flex_measure;
    sngl_flex_allocate = flex_allocate;
    sngl_grid_mode = grid_mode;
    sngl_grid_measure = grid_measure;
    sngl_grid_allocate = grid_allocate;
}

int sngl_look_justify(GtkWidget *box) {
    SnglLook *l = sngl_look_of(box);
    return l ? l->justify : 0;
}

int sngl_look_columns(GtkWidget *box) {
    SnglLook *l = sngl_look_of(box);
    return l ? l->columns : 0;
}

int sngl_look_width(GtkWidget *box) {
    SnglLook *l = sngl_look_of(box);
    return l ? l->width : 0;
}

// Lays a box out as a grid, its own layout kept aside for its axis and its
// spacing.
static void sngl_look_set_grid(GtkWidget *box, SnglLook *l, int grid, int columns) {
    if (!grid || !GTK_IS_BOX(box) || sngl_grid_measure == NULL) return;
    gboolean changed = l->columns != columns;
    l->columns = columns;
    if (!l->grid) {
        l->grid = TRUE;
        GtkLayoutManager *own = gtk_widget_get_layout_manager(box);
        if (l->box_layout) {
            // The flex layout had it, and kept GTK's aside already.
        } else if (GTK_IS_BOX_LAYOUT(own)) {
            l->box_layout = g_object_ref(own);
        }
        gtk_widget_set_layout_manager(box, gtk_custom_layout_new(sngl_grid_mode, sngl_grid_measure, sngl_grid_allocate));
    } else if (changed) {
        gtk_widget_queue_resize(box);
    }
}

// Gives a box GTK's own layout back, spaced as its look says.
static void sngl_box_release(GtkWidget *box, SnglLook *l) {
    GtkLayoutManager *own = l->box_layout;
    l->box_layout = NULL;
    if (l->spaced) gtk_box_layout_set_spacing(GTK_BOX_LAYOUT(own), l->spacing);
    gtk_widget_set_layout_manager(box, own);
}

static void sngl_box_update(GtkWidget *box) {
    if (!box || !GTK_IS_BOX(box)) return;
    SnglLook *l = sngl_look_of(box);
    if (l && l->grid) {
        gtk_widget_queue_resize(box);
        return;
    }
    gboolean ours = l && l->box_layout;
    gboolean need = (l && l->justify != 0) || sngl_weights_uneven(box);
    if (need && !ours) {
        GtkLayoutManager *own = gtk_widget_get_layout_manager(box);
        if (!GTK_IS_BOX_LAYOUT(own) || sngl_flex_measure == NULL) return;
        l = sngl_look_ensure(box);
        l->box_layout = g_object_ref(own);
        gtk_widget_set_layout_manager(box, gtk_custom_layout_new(sngl_flex_mode, sngl_flex_measure, sngl_flex_allocate));
    } else if (!need && ours) {
        sngl_box_release(box, l);
    } else if (ours) {
        gtk_widget_queue_resize(box);
    }
}

// A box's spacing, which its look holds while the runtime lays the box out.
static void sngl_look_set_spacing(GtkWidget *w, SnglLook *l, int spaced, int spacing) {
    if (!spaced || !GTK_IS_BOX(w)) return;
    l->spaced = TRUE;
    l->spacing = spacing;
    if (l->box_layout) gtk_widget_queue_resize(w);
    else gtk_box_set_spacing(GTK_BOX(w), spacing);
}

static GtkAlign sngl_cross_align(int cross) {
    switch (cross) {
    case 1: return GTK_ALIGN_START;
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
        main_axis = sngl_box_axis(parent) == GTK_ORIENTATION_HORIZONTAL ? 0 : 1;
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

// A contents grows by the most any child growing in it asks, in the box it
// sits in, as CSS's display: contents makes its children that box's items.
// The child still grows in the contents, which is then all of its room.
static void sngl_contents_update(GtkWidget *box) {
    SnglLook *l = sngl_look_of(box);
    if (!l || !l->contents) return;
    double grow = 0;
    for (GtkWidget *c = gtk_widget_get_first_child(box); c; c = gtk_widget_get_next_sibling(c)) {
        SnglLook *cl = sngl_look_of(c);
        if (cl && cl->grow_box == box && cl->grow > grow) grow = cl->grow;
    }
    if (grow == l->grow) return;
    l->grow = grow;
    if (grow > 0) sngl_look_watch_parent(box, l);
    sngl_look_place(box);
    if (l->grow_box) sngl_box_update(l->grow_box);
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
// check button's check) is not restyled with it. gtk_style_context_add_provider
// is deprecated since 4.10 in favor of style classes, which would put every
// distinct look in one display-wide sheet and restyle every widget whenever a
// look changed; this restyles one.
//
// A provider reaches the nodes of its own widget and not another's. A popover
// draws its body on its contents, a widget of its own, and its arrow on a node
// of the popover's, so its rule is on both and names both, as the theme's does.
static GtkWidget *sngl_popover_contents(GtkWidget *w) {
    if (!GTK_IS_POPOVER(w)) return NULL;
    for (GtkWidget *c = gtk_widget_get_first_child(w); c; c = gtk_widget_get_next_sibling(c))
        if (!strcmp(gtk_widget_get_css_name(c), "contents")) return c;
    return NULL;
}

static void sngl_look_set_css(GtkWidget *w, SnglLook *l, const char *decls) {
    GtkWidget *contents = sngl_popover_contents(w);
    G_GNUC_BEGIN_IGNORE_DEPRECATIONS
    GtkStyleContext *ctx = gtk_widget_get_style_context(w);
    if (!*decls) {
        if (l->css) {
            gtk_style_context_remove_provider(ctx, GTK_STYLE_PROVIDER(l->css));
            if (contents)
                gtk_style_context_remove_provider(gtk_widget_get_style_context(contents), GTK_STYLE_PROVIDER(l->css));
            g_object_unref(l->css);
            l->css = NULL;
        }
    } else if (!l->css) {
        l->css = gtk_css_provider_new();
        gtk_style_context_add_provider(ctx, GTK_STYLE_PROVIDER(l->css), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
        if (contents)
            gtk_style_context_add_provider(gtk_widget_get_style_context(contents), GTK_STYLE_PROVIDER(l->css),
                                           GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
    }
    G_GNUC_END_IGNORE_DEPRECATIONS
    if (!*decls) return;
    const char *name = gtk_widget_get_css_name(w);
    GString *rule = g_string_new(NULL);
    if (GTK_IS_POPOVER(w)) g_string_printf(rule, "popover > contents, popover > arrow { %s }", decls);
    else g_string_printf(rule, "%s { %s }", name, decls);
    // The rule outranks the theme's in every state, so a button given a
    // background shades it itself when hovered and pressed, as the theme
    // would. style.sngl writes `background` only for a Style's.
    if (GTK_IS_BUTTON(w) && strstr(decls, "background"))
        g_string_append_printf(rule,
            " %s:hover { background-image: image(alpha(currentColor, 0.1)); }"
            " %s:active, %s:checked { background-image: image(alpha(currentColor, 0.2)); }",
            name, name, name);
#if GTK_CHECK_VERSION(4, 12, 0)
    gtk_css_provider_load_from_string(l->css, rule->str);
#else
    gtk_css_provider_load_from_data(l->css, rule->str, -1);
#endif
    g_string_free(rule, TRUE);
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
    float x = align == 1 ? 0.5f : align == 2 ? 1.0f : align == 3 || align == 4 ? 0.0f : l->xalign0;
    if (!GTK_IS_LABEL(t)) {
        gtk_editable_set_alignment(GTK_EDITABLE(t), x);
        return;
    }
    GtkLabel *lb = GTK_LABEL(t);
    gtk_label_set_xalign(lb, x);
    gtk_label_set_justify(lb, align == 1 ? GTK_JUSTIFY_CENTER : align == 2 ? GTK_JUSTIFY_RIGHT : align == 3 ? GTK_JUSTIFY_FILL : align == 4 ? GTK_JUSTIFY_LEFT : l->justify0);
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

// A contents is shown only while a child of it is: one holding nothing a
// reader sees is no item of the box it sits in, as CSS's display: contents
// adds none, and takes no gap there. A component whose body asks and shows
// nothing -- a query per row -- put a gap per row above what came after.
static void sngl_contents_shown(GtkWidget *box) {
    SnglLook *l = sngl_look_of(box);
    if (!l || l->hidden) return;
    gboolean any = FALSE;
    for (GtkWidget *c = gtk_widget_get_first_child(box); c && !any; c = gtk_widget_get_next_sibling(c))
        any = gtk_widget_get_visible(c);
    if (gtk_widget_get_visible(box) != any) gtk_widget_set_visible(box, any);
}

static void sngl_contents_child_shown(GObject *c, GParamSpec *ps, gpointer box) {
    (void)c; (void)ps;
    sngl_contents_shown(GTK_WIDGET(box));
}

static void sngl_contents_kids(GListModel *m, guint pos, guint removed, guint added, gpointer box) {
    (void)removed;
    for (guint i = pos; i < pos + added; i++) {
        GObject *c = g_list_model_get_item(m, i);
        g_signal_connect_object(c, "notify::visible", G_CALLBACK(sngl_contents_child_shown), box, 0);
        g_object_unref(c);
    }
    sngl_contents_shown(GTK_WIDGET(box));
}

GtkWidget *sngl_contents_new(void) {
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 6);
    sngl_look_ensure(box)->contents = 1;
    GListModel *kids = gtk_widget_observe_children(box);
    g_signal_connect_object(kids, "items-changed", G_CALLBACK(sngl_contents_kids), box, 0);
    g_object_set_data_full(G_OBJECT(box), "sngl-contents-kids", kids, g_object_unref);
    gtk_widget_set_visible(box, FALSE);
    return box;
}

void sngl_set_look(GtkWidget *w, const char *css, int width, int height, double grow,
                   int cross, int align, int lines, int wrap, int hidden, const char *cursor,
                          int justify, int spaced, int spacing, int grid, int columns) {
    SnglLook *l = sngl_look_of(w);
    if (!l && !*css && !width && !height && grow <= 0 && !cross && !align && !lines && !wrap && !hidden && !*cursor && !justify && !grid) {
        if (spaced && GTK_IS_BOX(w)) gtk_box_set_spacing(GTK_BOX(w), spacing);
        return;
    }
    l = sngl_look_ensure(w);
    char *given = g_strdup_printf("%s\x1f%d %d %.17g %d %d %d %d %d %d %d %d %d %d\x1f%s", css, width, height, grow, cross, align, lines, wrap,
                                  hidden, justify, spaced, spacing, grid, columns, cursor);
    if (g_strcmp0(l->given, given) == 0) {
        g_free(given);
        return;
    }
    g_free(l->given);
    l->given = given;
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
    if (l->grow_box) sngl_box_update(l->grow_box);
    sngl_look_set_spacing(w, l, spaced, spacing);
    l->justify = justify;
    sngl_look_set_grid(w, l, grid, columns);
    sngl_box_update(w);
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
