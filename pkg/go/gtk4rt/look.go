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
    gboolean clipped;
    GtkOverflow overflow0;
    gboolean hidden, visible0;
    gboolean cursor;
    int justify;
    gboolean spaced;
    int spacing;
    // GTK's own layout of a box, kept while gtk4rt's lays the box out.
    GtkLayoutManager *box_layout;
} SnglLook;

static void sngl_unpin(SnglLook *l, int axis);
static void sngl_box_update(GtkWidget *box);

static void sngl_look_free(gpointer data) {
    SnglLook *l = data;
    sngl_unpin(l, l->grow_axis);
    if (l->css) g_object_unref(l->css);
    if (l->box_layout) g_object_unref(l->box_layout);
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
// once gtk4rt has given a box a layout of its own, the box answers through
// one that is not a GtkBoxLayout -- horizontal, with a critical on stderr --
// and its own, kept aside, is what knows. flex.go asks too.
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
}

static void sngl_unpin(SnglLook *l, int axis) {
    GtkWidget *box = l->grow_box;
    if (!box) return;
    g_object_remove_weak_pointer(G_OBJECT(box), (gpointer *)&l->grow_box);
    l->grow_box = NULL;
    SnglLook *bl = sngl_look_of(box);
    if (!bl) return;
    sngl_box_update(box);
    if (--bl->growers > 0) return;
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

// --- A box laid out by gtk4rt ---
//
// A GtkBox packs its children from the start and splits its spare room evenly
// between the ones that expand. CSS places them as justify-content says and
// shares the room by each one's flex, so a box whose look justifies, or whose
// growing children ask for unequal shares, is laid out here instead, and
// given GTK's own layout back when it asks for neither. Its spacing is then
// its look's, which is why the overrides hand a box's spacing to the look:
// GtkBox's own setter would reach a layout that is not GTK's.

static int sngl_box_spacing(GtkWidget *box) {
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
static double sngl_weight(GtkWidget *c, GtkOrientation o) {
    SnglLook *cl = sngl_look_of(c);
    if (cl && cl->grow > 0) return cl->grow;
    gboolean own = o == GTK_ORIENTATION_HORIZONTAL ? gtk_widget_get_hexpand(c) : gtk_widget_get_vexpand(c);
    return own ? 1 : 0;
}

static GPtrArray *sngl_flex_kids(GtkWidget *box) {
    GPtrArray *kids = g_ptr_array_new();
    for (GtkWidget *c = gtk_widget_get_first_child(box); c; c = gtk_widget_get_next_sibling(c))
        if (gtk_widget_should_layout(c)) g_ptr_array_add(kids, c);
    return kids;
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
        double w = sngl_weight(c, o);
        if (w <= 0) {
            carried = carried || gtk_widget_compute_expand(c, o);
            continue;
        }
        if (first == 0) first = w;
        else if (w != first) return TRUE;
    }
    return carried && first > 0;
}

// Each child's extent along the box, of along in all, at across: its
// minimum, then up to its natural size, then the spare room's share its
// weight asks for. What no child takes is handed back for justifying.
static int sngl_flex_sizes(GtkWidget *box, GtkOrientation o, int along, int across, GPtrArray *kids, int *sizes) {
    int n = kids->len;
    GtkRequestedSize *req = g_new(GtkRequestedSize, n);
    int spare = along - sngl_box_spacing(box) * (n - 1);
    double weights = 0;
    for (int i = 0; i < n; i++) {
        GtkWidget *c = g_ptr_array_index(kids, i);
        req[i].data = c;
        gtk_widget_measure(c, o, across, &req[i].minimum_size, &req[i].natural_size, NULL, NULL);
        spare -= req[i].minimum_size;
        weights += sngl_weight(c, o);
    }
    spare = gtk_distribute_natural_allocation(MAX(spare, 0), n, req);
    for (int i = 0; i < n; i++) {
        sizes[i] = req[i].minimum_size;
        if (weights <= 0) continue;
        double w = sngl_weight(g_ptr_array_index(kids, i), o);
        if (w <= 0) continue;
        // The last grower takes what rounding left.
        int share = w == weights ? spare : (int)(spare * w / weights + 0.5);
        sizes[i] += share;
        spare -= share;
        weights -= w;
    }
    g_free(req);
    return weights > 0 ? 0 : spare;
}

static GtkSizeRequestMode sngl_flex_mode(GtkWidget *box) {
    (void)box;
    return GTK_SIZE_REQUEST_HEIGHT_FOR_WIDTH;
}

static void sngl_flex_measure(GtkWidget *box, GtkOrientation o, int for_size,
        int *min, int *nat, int *min_base, int *nat_base) {
    GtkOrientation axis = sngl_box_axis(box);
    GPtrArray *kids = sngl_flex_kids(box);
    int n = kids->len;
    *min = *nat = 0;
    *min_base = *nat_base = -1;
    if (o == axis) {
        for (int i = 0; i < n; i++) {
            int cmin, cnat;
            gtk_widget_measure(g_ptr_array_index(kids, i), o, for_size, &cmin, &cnat, NULL, NULL);
            *min += cmin;
            *nat += cnat;
        }
        if (n > 1) {
            *min += sngl_box_spacing(box) * (n - 1);
            *nat += sngl_box_spacing(box) * (n - 1);
        }
    } else {
        int *sizes = NULL;
        if (for_size >= 0 && n > 0) {
            sizes = g_new(int, n);
            sngl_flex_sizes(box, axis, for_size, -1, kids, sizes);
        }
        for (int i = 0; i < n; i++) {
            int cmin, cnat;
            gtk_widget_measure(g_ptr_array_index(kids, i), o, sizes ? sizes[i] : -1, &cmin, &cnat, NULL, NULL);
            *min = MAX(*min, cmin);
            *nat = MAX(*nat, cnat);
        }
        g_free(sizes);
    }
    g_ptr_array_unref(kids);
}

// Each child along the box, across all of it: GTK aligns a child within
// what it is allocated, as its halign and valign say.
static void sngl_flex_allocate(GtkWidget *box, int width, int height, int baseline) {
    (void)baseline;
    GtkOrientation axis = sngl_box_axis(box);
    gboolean row = axis == GTK_ORIENTATION_HORIZONTAL;
    int along = row ? width : height;
    GPtrArray *kids = sngl_flex_kids(box);
    int n = kids->len;
    if (n == 0) {
        g_ptr_array_unref(kids);
        return;
    }
    int *sizes = g_new(int, n);
    int spare = sngl_flex_sizes(box, axis, along, row ? height : width, kids, sizes);
    SnglLook *l = sngl_look_of(box);
    int lead = 0, between = 0, rest = 0;
    switch (l ? l->justify : 0) {
    case 1: lead = spare / 2; break;
    case 2: lead = spare; break;
    case 3: if (n > 1) { between = spare / (n - 1); rest = spare % (n - 1); } break;
    case 4: lead = spare / (2 * n); between = spare / n; break;
    case 5: lead = between = spare / (n + 1); break;
    }
    gboolean rtl = row && gtk_widget_get_direction(box) == GTK_TEXT_DIR_RTL;
    int spacing = sngl_box_spacing(box), pos = lead;
    for (int i = 0; i < n; i++) {
        if (i > 0) pos += spacing + between + (i <= rest ? 1 : 0);
        int at = rtl ? along - pos - sizes[i] : pos;
        GtkAllocation a = row ? (GtkAllocation){at, 0, sizes[i], height} : (GtkAllocation){0, at, width, sizes[i]};
        gtk_widget_size_allocate(g_ptr_array_index(kids, i), &a, -1);
        pos += sizes[i];
    }
    g_free(sizes);
    g_ptr_array_unref(kids);
}

static void sngl_box_update(GtkWidget *box) {
    if (!box || !GTK_IS_BOX(box)) return;
    SnglLook *l = sngl_look_of(box);
    gboolean ours = l && l->box_layout;
    gboolean need = (l && l->justify != 0) || sngl_weights_uneven(box);
    if (need && !ours) {
        GtkLayoutManager *own = gtk_widget_get_layout_manager(box);
        if (!GTK_IS_BOX_LAYOUT(own)) return;
        l = sngl_look_ensure(box);
        l->box_layout = g_object_ref(own);
        gtk_widget_set_layout_manager(box, gtk_custom_layout_new(sngl_flex_mode, sngl_flex_measure, sngl_flex_allocate));
    } else if (!need && ours) {
        GtkLayoutManager *own = l->box_layout;
        l->box_layout = NULL;
        if (l->spaced) gtk_box_layout_set_spacing(GTK_BOX_LAYOUT(own), l->spacing);
        gtk_widget_set_layout_manager(box, own);
    } else if (ours) {
        gtk_widget_queue_resize(box);
    }
}

// A box's spacing, which its look holds while gtk4rt lays the box out.
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

static void sngl_set_look(GtkWidget *w, const char *css, int width, int height, double grow,
                          int cross, int align, int lines, int wrap, int clip, int hidden, const char *cursor,
                          int justify, int spaced, int spacing) {
    SnglLook *l = sngl_look_of(w);
    if (!l && !*css && !width && !height && grow <= 0 && !cross && !align && !lines && !wrap && !clip && !hidden && !*cursor && !justify) {
        if (spaced && GTK_IS_BOX(w)) gtk_box_set_spacing(GTK_BOX(w), spacing);
        return;
    }
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
    if (l->grow_box) sngl_box_update(l->grow_box);
    sngl_look_set_spacing(w, l, spaced, spacing);
    l->justify = justify;
    sngl_box_update(w);
    sngl_look_set_cross(w, l, cross);
    sngl_look_set_text(w, l, align, lines, wrap);
    sngl_look_size_label(w, l, wrap);
    if (clip && !l->clipped) {
        l->clipped = TRUE;
        l->overflow0 = gtk_widget_get_overflow(w);
    }
    if (l->clipped) gtk_widget_set_overflow(w, clip ? GTK_OVERFLOW_HIDDEN : l->overflow0);
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
// Probes for look_test.go and flex_test.go.
static GtkAllocation sngl_allocation(GtkWidget *w) {
    GtkAllocation a;
    G_GNUC_BEGIN_IGNORE_DEPRECATIONS
    gtk_widget_get_allocation(w, &a);
    G_GNUC_END_IGNORE_DEPRECATIONS
    return a;
}
static int sngl_is_box_layout(GtkWidget *w) {
    return GTK_IS_BOX_LAYOUT(gtk_widget_get_layout_manager(w));
}
static gboolean sngl_probe_tick(gpointer data) { (void)data; return G_SOURCE_CONTINUE; }
static void sngl_probe_pump(int ms) {
    gint64 deadline = g_get_monotonic_time() + (gint64)ms * 1000;
    guint tick = g_timeout_add(5, sngl_probe_tick, NULL);
    while (g_get_monotonic_time() < deadline) g_main_context_iteration(NULL, TRUE);
    g_source_remove(tick);
}
// What GTK paints for w, shown in a window of its own and in state, as
// premultiplied ARGB; a popover is popped up from a button the window holds.
static guint32 *sngl_probe_paint(GtkWidget *w, GtkStateFlags state, int *width_out, int *height_out) {
    g_object_set(gtk_settings_get_default(), "gtk-enable-animations", FALSE, NULL);
    if (!gtk_widget_get_root(w)) {
        GtkWidget *win = gtk_window_new();
        gtk_window_set_default_size(GTK_WINDOW(win), 200, 200);
        if (GTK_IS_POPOVER(w)) {
            GtkWidget *anchor = gtk_button_new();
            gtk_widget_set_valign(anchor, GTK_ALIGN_START);
            gtk_window_set_child(GTK_WINDOW(win), anchor);
            gtk_widget_set_parent(w, anchor);
            gtk_popover_set_position(GTK_POPOVER(w), GTK_POS_BOTTOM);
        } else {
            gtk_window_set_child(GTK_WINDOW(win), w);
        }
        gtk_window_present(GTK_WINDOW(win));
        sngl_probe_pump(150);
        if (GTK_IS_POPOVER(w)) {
            gtk_popover_popup(GTK_POPOVER(w));
            sngl_probe_pump(150);
        }
    }
    gtk_widget_set_state_flags(w, state, TRUE);
    gtk_widget_queue_draw(w);
    sngl_probe_pump(50);
    int width = gtk_widget_get_width(w), height = gtk_widget_get_height(w);
    *width_out = width;
    *height_out = height;
    if (width <= 0 || height <= 0) return NULL;
    GdkPaintable *paintable = gtk_widget_paintable_new(w);
    GtkSnapshot *snap = gtk_snapshot_new();
    gdk_paintable_snapshot(paintable, GDK_SNAPSHOT(snap), width, height);
    GskRenderNode *node = gtk_snapshot_free_to_node(snap);
    g_object_unref(paintable);
    if (!node) return NULL;
    GskRenderer *r = gsk_cairo_renderer_new();
    gsk_renderer_realize(r, NULL, NULL);
    graphene_rect_t bounds = GRAPHENE_RECT_INIT(0, 0, width, height);
    GdkTexture *tex = gsk_renderer_render_texture(r, node, &bounds);
    guint32 *px = g_new(guint32, width * height);
    gdk_texture_download(tex, (guchar *)px, width * 4);
    g_object_unref(tex);
    gsk_renderer_unrealize(r);
    g_object_unref(r);
    gsk_render_node_unref(node);
    return px;
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
	// Cross is a box's alignItems, which it puts on each child: 1 start,
	// 2 center, 3 end, 4 baseline; zero stretches.
	Cross int
	// TextAlign is a label's, an entry's or a button's: 1 center, 2 right,
	// 3 justify, 4 left; zero leaves the widget's own.
	TextAlign int
	// Lines ellipsizes a label at that many lines.
	Lines int
	// Wrap is a label's: 1 one line, 2 wrapped.
	Wrap   int
	Clip   bool
	Hidden bool
	Cursor string
	// Justify is a box's justifyContent: 1 center, 2 end, 3 space between,
	// 4 around, 5 evenly; zero packs its children from the start.
	Justify int
	// Spacing is a box's, where Spaced says the look holds one.
	Spaced  bool
	Spacing int
}

// SetLook gives w what l asks for, and back what an earlier Look took that l
// leaves unset.
func SetLook(w Handle, l Look) {
	css, freeCSS := cstr(l.Css)
	defer freeCSS()
	cursor, freeCursor := cstr(l.Cursor)
	defer freeCursor()
	C.sngl_set_look(widget(w), css, C.int(math.Round(l.Width)), C.int(math.Round(l.Height)), C.double(l.Grow),
		C.int(l.Cross), C.int(l.TextAlign), C.int(l.Lines), C.int(l.Wrap), C.int(bool01(l.Clip)), C.int(bool01(l.Hidden)), cursor,
		C.int(l.Justify), C.int(bool01(l.Spaced)), C.int(l.Spacing))
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

func setHExpand(w Handle, v bool) { C.gtk_widget_set_hexpand(widget(w), gbool(v)) }

func allocate(w Handle, width, height int) {
	var min, nat C.int
	C.gtk_widget_measure(widget(w), C.GTK_ORIENTATION_HORIZONTAL, -1, &min, &nat, nil, nil)
	C.gtk_widget_measure(widget(w), C.GTK_ORIENTATION_VERTICAL, C.int(width), &min, &nat, nil, nil)
	a := C.GtkAllocation{width: C.int(width), height: C.int(height)}
	C.gtk_widget_size_allocate(widget(w), &a, -1)
}

func allocation(w Handle) C.GtkAllocation {
	return C.sngl_allocation(widget(w))
}

func allocatedX(w Handle) int { return int(allocation(w).x) }

func allocatedWidth(w Handle) int { return int(allocation(w).width) }

func allocatedHeight(w Handle) int { return int(allocation(w).height) }

func boxLaidOutByGTK(w Handle) bool {
	return C.sngl_is_box_layout(widget(w)) != 0
}

// State flags painted takes, as GtkStateFlags has them.
const (
	stateActive  = 1 << 0
	stateHover   = 1 << 1
	stateChecked = 1 << 11
)

// painted is what GTK paints for w in state, a row at a time, each pixel
// 0xAARRGGBB.
func painted(w Handle, state uint32) [][]uint32 {
	var width, height C.int
	px := C.sngl_probe_paint(widget(w), C.GtkStateFlags(state), &width, &height)
	if px == nil {
		return nil
	}
	defer C.g_free(C.gpointer(px))
	flat := unsafe.Slice((*uint32)(unsafe.Pointer(px)), int(width)*int(height))
	rows := make([][]uint32, height)
	for y := range rows {
		rows[y] = append([]uint32(nil), flat[y*int(width):(y+1)*int(width)]...)
	}
	return rows
}
