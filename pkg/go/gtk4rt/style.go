//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>

extern void snglToplevelGone(gpointer data, GObject *where);

static GtkCssProvider *sngl_style_provider(void) {
    static GtkCssProvider *provider = NULL;
    if (provider == NULL) {
        provider = gtk_css_provider_new();
        gtk_style_context_add_provider_for_display(gdk_display_get_default(),
            GTK_STYLE_PROVIDER(provider), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
    }
    return provider;
}
static void sngl_style_load(const char *css) {
    gtk_css_provider_load_from_string(sngl_style_provider(), css);
}
static void sngl_style_watch(GObject *o, int idx) {
    g_object_weak_ref(o, snglToplevelGone, GINT_TO_POINTER(idx));
}
static int sngl_style_is_box(GtkWidget *w) { return GTK_IS_BOX(w); }

// An image frame sized by its style is that size, whatever its picture's own:
// a size request is only the least a widget is, and a picture's natural size
// is its texture's. The frame's children -- its source, its placeholder, a
// render slot's anchor -- are each laid out over the whole of it.
static void sngl_sized_measure(GtkWidget *w, GtkOrientation o, int for_size,
        int *min, int *nat, int *min_base, int *nat_base) {
    (void)min_base; (void)nat_base;
    int want = GPOINTER_TO_INT(g_object_get_data(G_OBJECT(w),
        o == GTK_ORIENTATION_HORIZONTAL ? "sngl-width" : "sngl-height"));
    if (want > 0) {
        *min = *nat = want;
        return;
    }
    // The other axis alone is set: the children's size for it.
    int other = GPOINTER_TO_INT(g_object_get_data(G_OBJECT(w),
        o == GTK_ORIENTATION_HORIZONTAL ? "sngl-height" : "sngl-width"));
    if (other > 0) for_size = other;
    *min = *nat = 0;
    for (GtkWidget *c = gtk_widget_get_first_child(w); c != NULL; c = gtk_widget_get_next_sibling(c)) {
        if (!gtk_widget_should_layout(c)) continue;
        int cmin = 0, cnat = 0;
        gtk_widget_measure(c, o, for_size, &cmin, &cnat, NULL, NULL);
        if (cmin > *min) *min = cmin;
        if (cnat > *nat) *nat = cnat;
    }
}
static void sngl_sized_allocate(GtkWidget *w, int width, int height, int baseline) {
    for (GtkWidget *c = gtk_widget_get_first_child(w); c != NULL; c = gtk_widget_get_next_sibling(c)) {
        if (gtk_widget_should_layout(c)) gtk_widget_allocate(c, width, height, baseline, NULL);
    }
}
static GtkSizeRequestMode sngl_sized_mode(GtkWidget *w) {
    (void)w;
    return GTK_SIZE_REQUEST_HEIGHT_FOR_WIDTH;
}
static void sngl_sized_set(GtkWidget *w, int width, int height) {
    g_object_set_data(G_OBJECT(w), "sngl-width", GINT_TO_POINTER(width));
    g_object_set_data(G_OBJECT(w), "sngl-height", GINT_TO_POINTER(height));
    if (!GTK_IS_CUSTOM_LAYOUT(gtk_widget_get_layout_manager(w))) {
        gtk_widget_set_layout_manager(w,
            gtk_custom_layout_new(sngl_sized_mode, sngl_sized_measure, sngl_sized_allocate));
    }
    gtk_widget_queue_resize(w);
}

// A label sized by its style is that size too, its words wrapping inside the
// width as html's do: a size request let a long line widen it. It is measured
// and allocated by GtkLabel's own code, given the width it is.
static GtkWidgetClass *sngl_label_class(void) {
    return GTK_WIDGET_CLASS(g_type_class_peek(GTK_TYPE_LABEL));
}
static void sngl_label_measure(GtkWidget *w, GtkOrientation o, int for_size,
        int *min, int *nat, int *min_base, int *nat_base) {
    int width = GPOINTER_TO_INT(g_object_get_data(G_OBJECT(w), "sngl-width"));
    int height = GPOINTER_TO_INT(g_object_get_data(G_OBJECT(w), "sngl-height"));
    if (o == GTK_ORIENTATION_VERTICAL && width > 0) for_size = width;
    sngl_label_class()->measure(w, o, for_size, min, nat, min_base, nat_base);
    int want = o == GTK_ORIENTATION_HORIZONTAL ? width : height;
    if (want > 0) *min = *nat = want;
}
static void sngl_label_allocate(GtkWidget *w, int width, int height, int baseline) {
    sngl_label_class()->size_allocate(w, width, height, baseline);
}
static void sngl_label_sized_set(GtkWidget *w, int width, int height, gboolean wrap) {
    g_object_set_data(G_OBJECT(w), "sngl-width", GINT_TO_POINTER(width));
    g_object_set_data(G_OBJECT(w), "sngl-height", GINT_TO_POINTER(height));
    if (!GTK_IS_CUSTOM_LAYOUT(gtk_widget_get_layout_manager(w))) {
        gtk_widget_set_layout_manager(w,
            gtk_custom_layout_new(sngl_sized_mode, sngl_label_measure, sngl_label_allocate));
    }
    gtk_label_set_wrap(GTK_LABEL(w), wrap);
    gtk_widget_queue_resize(w);
}
static int sngl_style_is_label(GtkWidget *w) { return GTK_IS_LABEL(w); }
static gboolean sngl_label_wraps(GtkWidget *w) { return gtk_label_get_wrap(GTK_LABEL(w)); }
static float sngl_label_xalign(GtkWidget *w) { return gtk_label_get_xalign(GTK_LABEL(w)); }
static GtkJustification sngl_label_justify(GtkWidget *w) { return gtk_label_get_justify(GTK_LABEL(w)); }
static void sngl_label_align(GtkWidget *w, float x, GtkJustification j) {
    gtk_label_set_xalign(GTK_LABEL(w), x);
    gtk_label_set_justify(GTK_LABEL(w), j);
}

// A box's alignItems is where each of its children sits across it, kept on
// the box so that a child inserted later -- a render slot's -- sits there too.
// 0 is no alignment the style named, which leaves the child as it was built.
static void sngl_align_put(GtkWidget *box, GtkWidget *c, GtkAlign a) {
    if (gtk_orientable_get_orientation(GTK_ORIENTABLE(box)) == GTK_ORIENTATION_VERTICAL) {
        // A baseline across a column is its start, as in CSS.
        if (a != GTK_ALIGN_START && a != GTK_ALIGN_CENTER && a != GTK_ALIGN_END && a != GTK_ALIGN_FILL) {
            a = GTK_ALIGN_START;
        }
        gtk_widget_set_halign(c, a);
    } else {
        gtk_widget_set_valign(c, a);
    }
}
static void sngl_align_child(GtkWidget *box, GtkWidget *c) {
    int a = GPOINTER_TO_INT(g_object_get_data(G_OBJECT(box), "sngl-align"));
    if (a != 0) sngl_align_put(box, c, a - 1);
}
// A box that stops naming an alignment stretches its children again.
static void sngl_align_set(GtkWidget *box, int a) {
    g_object_set_data(G_OBJECT(box), "sngl-align", GINT_TO_POINTER(a));
    for (GtkWidget *c = gtk_widget_get_first_child(box); c != NULL; c = gtk_widget_get_next_sibling(c)) {
        sngl_align_put(box, c, a == 0 ? GTK_ALIGN_FILL : a - 1);
    }
}
static int sngl_align_baseline(void) {
#if GTK_CHECK_VERSION(4, 12, 0)
    return GTK_ALIGN_BASELINE_FILL;
#else
    return GTK_ALIGN_BASELINE;
#endif
}
*/
import "C"

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/pkg/go/cbind"
	"duckfam.us/sngl/pkg/go/snglcolor"
)

// Style is what this platform reads of a `ui.Style`, every length in pixels.
// A field at its zero is unset, as it is in the Style it is read from, and
// leaves the widget as GTK draws it.
type Style struct {
	Width, MinWidth, Height float64
	Gap                     float64

	Padding, PaddingX, PaddingY                          float64
	PaddingTop, PaddingRight, PaddingBottom, PaddingLeft float64
	Margin, MarginX, MarginY                             float64
	MarginTop, MarginRight, MarginBottom, MarginLeft     float64

	// AlignItems is where a box's children sit across it, TextAlign where a
	// label's lines sit in it: each a member's name, "" for none.
	AlignItems string
	TextAlign  string

	FontSize   float64
	FontWeight string
	FontStyle  string
	Color      snglcolor.Color

	Background   snglcolor.Color
	BorderRadius float64
}

// styled is what SetStyle last did to a widget, so that a style written again
// -- a run-time value changing -- undoes what the new one leaves unset.
type styled struct {
	class   string
	sized   bool
	spacing C.int
	spaced  bool
	aligned bool
	// A label's own wrap and alignment, read the first time it is styled,
	// which a style that stops sizing or aligning it gives back.
	own         bool
	wrap        C.gboolean
	xalign      C.float
	justify     C.GtkJustification
	textAligned bool
}

var (
	styledWidgets = map[Handle]*styled{}
	// classes is the class carrying each distinct style's CSS: one rule per
	// style rather than per widget, so a thousand cards alike are one rule.
	// A class no widget holds is dropped, rule and all: a style computed from
	// a value that keeps changing -- an animated width or color -- left a
	// rule per value it passed through, each reloaded with every new one.
	classes = map[string]*styleClassRule{}
	// rules is the live classes in the order they were made, which is the
	// order the stylesheet lists them in.
	rules  []*styleClassRule
	classN int
)

// styleClassRule is one class's rule and how many widgets hold it.
type styleClassRule struct {
	css, class string
	holders    int
}

// SetStyle applies s to w, a constant style once and a run-time one each time
// it changes. A width and a height are a size request, except on an image and
// a label, which are that size; a gap is a box's spacing and alignItems where
// it puts its children; textAlign is a label's alignment; the rest is CSS, a
// class per distinct style on a provider for the whole display.
func SetStyle(w Handle, s Style) {
	st := styledWidgets[w]
	if st == nil {
		st = &styled{}
		styledWidgets[w] = st
		gone := cbind.Register(func() {
			if st := styledWidgets[w]; st != nil {
				releaseClass(st.class)
			}
			delete(styledWidgets, w)
		})
		C.sngl_style_watch((*C.GObject)(p(w)), C.int(gone))
	}
	label := C.sngl_style_is_label(widget(w)) != 0
	if label && !st.own {
		st.own = true
		st.wrap = C.sngl_label_wraps(widget(w))
		st.xalign, st.justify = C.sngl_label_xalign(widget(w)), C.sngl_label_justify(widget(w))
	}
	width, height := max(s.Width, s.MinWidth), s.Height
	switch {
	case images[w] != nil && (s.Width > 0 || s.Height > 0 || st.sized):
		C.sngl_sized_set(widget(w), C.int(s.Width), C.int(s.Height))
		st.sized = s.Width > 0 || s.Height > 0
	case label && (s.Width > 0 || s.Height > 0 || st.sized):
		wrap := st.wrap
		if s.Width > 0 {
			wrap = 1
		}
		C.sngl_label_sized_set(widget(w), C.int(s.Width), C.int(s.Height), wrap)
		// A minWidth is still only the least a label is.
		C.gtk_widget_set_size_request(widget(w), sizeRequest(s.MinWidth), -1)
		st.sized = s.Width > 0 || s.Height > 0
	case width > 0 || height > 0 || st.sized:
		C.gtk_widget_set_size_request(widget(w), sizeRequest(width), sizeRequest(height))
		st.sized = width > 0 || height > 0
	}
	if C.sngl_style_is_box(widget(w)) != 0 && images[w] == nil {
		box := (*C.GtkBox)(p(w))
		if !st.spaced {
			st.spacing, st.spaced = C.gtk_box_get_spacing(box), true
		}
		spacing := st.spacing
		if s.Gap > 0 {
			spacing = C.int(s.Gap)
		}
		C.gtk_box_set_spacing(box, spacing)
		if a := alignment(s.AlignItems); a != 0 || st.aligned {
			C.sngl_align_set(widget(w), a)
			st.aligned = a != 0
		}
	}
	if label && (s.TextAlign != "" || st.textAligned) {
		x, j := st.xalign, st.justify
		switch s.TextAlign {
		case "left":
			x, j = 0, C.GTK_JUSTIFY_LEFT
		case "center":
			x, j = 0.5, C.GTK_JUSTIFY_CENTER
		case "right":
			x, j = 1, C.GTK_JUSTIFY_RIGHT
		case "justify":
			x, j = 0, C.GTK_JUSTIFY_FILL
		}
		C.sngl_label_align(widget(w), x, j)
		st.textAligned = s.TextAlign != ""
	}
	css := s.CSS()
	if old := classes[css]; old != nil && old.class == st.class {
		return
	}
	if css == "" && st.class == "" {
		return
	}
	class := holdClass(css)
	if st.class != "" {
		cs, free := cstr(st.class)
		C.gtk_widget_remove_css_class(widget(w), cs)
		free()
	}
	if class != "" {
		cs, free := cstr(class)
		C.gtk_widget_add_css_class(widget(w), cs)
		free()
	}
	released := releaseClass(st.class)
	st.class = class
	if class != "" && classes[css].holders == 1 || released {
		loadRules()
	}
}

// alignment is the GtkAlign an `alignItems` member puts a child at, plus one,
// so that 0 is none named. A stretched child fills the box, as it does when a
// style names none.
func alignment(member string) C.int {
	switch member {
	case "start":
		return C.GTK_ALIGN_START + 1
	case "center":
		return C.GTK_ALIGN_CENTER + 1
	case "end":
		return C.GTK_ALIGN_END + 1
	case "stretch":
		return C.GTK_ALIGN_FILL + 1
	case "baseline":
		return C.sngl_align_baseline() + 1
	}
	return 0
}

// aligned puts child where box's alignItems says, as it is inserted.
func aligned(box, child Handle) {
	if C.sngl_style_is_box(widget(box)) != 0 {
		C.sngl_align_child(widget(box), widget(child))
	}
}

func sizeRequest(px float64) C.int {
	if px <= 0 {
		return -1
	}
	return C.int(px)
}

// holdClass is the class carrying css, made the first time it is asked for,
// held once more; "" for no CSS at all. The caller loads the rules when it is
// new.
func holdClass(css string) string {
	if css == "" {
		return ""
	}
	r := classes[css]
	if r == nil {
		r = &styleClassRule{css: css, class: "sngl-style-" + strconv.Itoa(classN)}
		classN++
		classes[css] = r
		rules = append(rules, r)
	}
	r.holders++
	return r.class
}

// releaseClass lets go of one hold on class, and drops its rule when nothing
// holds it: it reports whether it did, so the caller loads the rules again.
func releaseClass(class string) bool {
	if class == "" {
		return false
	}
	for i, r := range rules {
		if r.class != class {
			continue
		}
		if r.holders--; r.holders > 0 {
			return false
		}
		delete(classes, r.css)
		rules = append(rules[:i], rules[i+1:]...)
		return true
	}
	return false
}

// loadRules puts every live class's rule on the display's provider.
func loadRules() {
	var b strings.Builder
	for _, r := range rules {
		b.WriteString("." + r.class + " { " + r.css + " }\n")
	}
	all, free := cstr(b.String())
	defer free()
	C.sngl_style_load(all)
}

// CSS is the declarations GTK's CSS draws of s, in a fixed order: the same
// style is the same text, and so the same class.
func (s Style) CSS() string {
	var b strings.Builder
	decl := func(prop, value string) {
		fmt.Fprintf(&b, "%s: %s; ", prop, value)
	}
	box := func(prop string, all, x, y, top, right, bottom, left float64) {
		side := func(own, axis float64) float64 {
			switch {
			case own > 0:
				return own
			case axis > 0:
				return axis
			}
			return all
		}
		t, r, bt, l := side(top, y), side(right, x), side(bottom, y), side(left, x)
		if t > 0 || r > 0 || bt > 0 || l > 0 {
			decl(prop, px(t)+" "+px(r)+" "+px(bt)+" "+px(l))
		}
	}
	box("padding", s.Padding, s.PaddingX, s.PaddingY, s.PaddingTop, s.PaddingRight, s.PaddingBottom, s.PaddingLeft)
	box("margin", s.Margin, s.MarginX, s.MarginY, s.MarginTop, s.MarginRight, s.MarginBottom, s.MarginLeft)
	if s.FontSize > 0 {
		decl("font-size", px(s.FontSize))
	}
	switch s.FontWeight {
	case "bold", "lighter", "bolder":
		decl("font-weight", s.FontWeight)
	}
	switch s.FontStyle {
	case "italic", "oblique":
		decl("font-style", s.FontStyle)
	}
	if s.Color.A > 0 {
		decl("color", s.Color.String())
	}
	if s.Background.A > 0 {
		// A theme draws a button's face as an image over its color.
		decl("background", "none")
		decl("background-color", s.Background.String())
	}
	if s.BorderRadius > 0 {
		decl("border-radius", px(s.BorderRadius))
	}
	return strings.TrimSpace(b.String())
}

func px(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64) + "px"
}
