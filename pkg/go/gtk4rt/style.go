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
}

var (
	styledWidgets = map[Handle]*styled{}
	// classes is the class carrying each distinct style's CSS: one rule per
	// style rather than per widget, so a thousand cards alike are one rule.
	classes = map[string]string{}
	rules   []string
)

// SetStyle applies s to w, a constant style once and a run-time one each time
// it changes. A width and a height are a size request, except on an image,
// which is that size; a gap is a box's spacing; the rest is CSS, a class per
// distinct style on a provider for the whole display.
func SetStyle(w Handle, s Style) {
	st := styledWidgets[w]
	if st == nil {
		st = &styled{}
		styledWidgets[w] = st
		gone := cbind.Register(func() { delete(styledWidgets, w) })
		C.sngl_style_watch((*C.GObject)(p(w)), C.int(gone))
	}
	width, height := max(s.Width, s.MinWidth), s.Height
	switch {
	case images[w] != nil && (s.Width > 0 || s.Height > 0 || st.sized):
		C.sngl_sized_set(widget(w), C.int(s.Width), C.int(s.Height))
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
	}
	class := styleClass(s.CSS())
	if class == st.class {
		return
	}
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
	st.class = class
}

func sizeRequest(px float64) C.int {
	if px <= 0 {
		return -1
	}
	return C.int(px)
}

// styleClass is the class carrying css, its rule added to the display's
// provider the first time it is asked for; "" for no CSS at all.
func styleClass(css string) string {
	if css == "" {
		return ""
	}
	if class, ok := classes[css]; ok {
		return class
	}
	class := "sngl-style-" + strconv.Itoa(len(rules))
	classes[css] = class
	rules = append(rules, "."+class+" { "+css+" }")
	all, free := cstr(strings.Join(rules, "\n"))
	defer free()
	C.sngl_style_load(all)
	return class
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
