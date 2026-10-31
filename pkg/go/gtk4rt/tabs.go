//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>

extern void snglRtDispatch(int idx);
extern void snglToplevelGone(gpointer data, GObject *where);

// A stack its box let go of is being torn down, and the pages it drops as it
// goes are no one's choice.
static void sngl_tabs_shown(GObject *o, GParamSpec *ps, gpointer data) {
    (void)ps;
    if (gtk_widget_get_parent(GTK_WIDGET(o)) == NULL) return;
    snglRtDispatch(GPOINTER_TO_INT(data));
}
static GtkWidget *sngl_tabs_new(int shown, GtkWidget **stack, GtkWidget **switcher) {
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    *switcher = gtk_stack_switcher_new();
    *stack = gtk_stack_new();
    gtk_stack_switcher_set_stack(GTK_STACK_SWITCHER(*switcher), GTK_STACK(*stack));
    gtk_box_append(GTK_BOX(box), *switcher);
    gtk_box_append(GTK_BOX(box), *stack);
    g_signal_connect(*stack, "notify::visible-child", G_CALLBACK(sngl_tabs_shown), GINT_TO_POINTER(shown));
    return box;
}
static void sngl_tabs_watch(GObject *o, int idx) {
    g_object_weak_ref(o, snglToplevelGone, GINT_TO_POINTER(idx));
}
static void sngl_tabs_title(GtkWidget *stack, GtkWidget *page, const char *title) {
    gtk_stack_page_set_title(gtk_stack_get_page(GTK_STACK(stack), page), title);
}
static const char *sngl_tabs_title_of(GtkWidget *stack, GtkWidget *page) {
    return gtk_stack_page_get_title(gtk_stack_get_page(GTK_STACK(stack), page));
}
// The switcher's buttons are its children, one per page in the stack's order.
static GtkWidget *sngl_tabs_button(GtkWidget *switcher, int i) {
    GtkWidget *c = gtk_widget_get_first_child(switcher);
    for (; c != NULL && i > 0; i--) c = gtk_widget_get_next_sibling(c);
    return c;
}
*/
import "C"

import (
	"slices"
	"unsafe"

	"duckfam.us/sngl/pkg/go/cbind"
)

// A tabs is a `ui.tabs` on gtk4: a box of a GtkStackSwitcher over a GtkStack.
// A child appended or inserted into the box is a page of the stack, and
// removing one takes it out; the box itself holds only the two, and a render
// slot's anchor.
type tabs struct {
	stack, switcher Handle
	pages           []Handle
	// selected is the program's, which the stack shows.
	selected int
	// quiet is set while this package changes the page shown, which GTK
	// reports as it would the user's.
	quiet bool
}

// A tabPage is one tab's page: a box holding its panel, titled with its
// label.
type tabPage struct {
	title    string
	disabled bool
	onShown  func()
	host     *tabs
}

var (
	tabsOf   = map[Handle]*tabs{}
	tabPages = map[Handle]*tabPage{}
)

func TabsNew() Handle {
	t := &tabs{}
	shown := cbind.Register(t.shown)
	var stack, sw *C.GtkWidget
	box := Handle(unsafe.Pointer(C.sngl_tabs_new(C.int(shown), &stack, &sw)))
	t.stack, t.switcher = Handle(unsafe.Pointer(stack)), Handle(unsafe.Pointer(sw))
	tabsOf[box] = t
	gone := cbind.Register(func() {
		delete(tabsOf, box)
		cbind.Release(shown)
	})
	C.sngl_tabs_watch((*C.GObject)(p(box)), C.int(gone))
	return box
}

// TabsSetSelected is the page shown, by its index among the pages.
func TabsSetSelected(h Handle, k int) {
	if t := tabsOf[h]; t != nil {
		t.selected = k
		t.show()
	}
}

func TabPageNew() Handle {
	box := BoxNew(OrientationVertical, 0)
	tabPages[box] = &tabPage{}
	gone := cbind.Register(func() { delete(tabPages, box) })
	C.sngl_tabs_watch((*C.GObject)(p(box)), C.int(gone))
	return box
}

func TabPageSetTitle(h Handle, title string) {
	pg := tabPages[h]
	if pg == nil {
		return
	}
	pg.title = title
	if pg.host != nil {
		pg.host.applyTitle(h, pg)
	}
}

func TabPageSetDisabled(h Handle, disabled bool) {
	pg := tabPages[h]
	if pg == nil {
		return
	}
	pg.disabled = disabled
	if pg.host != nil {
		pg.host.applySensitive()
	}
}

// TabPageOnShown is the handler the user choosing the page runs.
func TabPageOnShown(h Handle, fn func()) {
	if pg := tabPages[h]; pg != nil {
		pg.onShown = fn
	}
}

// TabPageShow runs the page's handler as the user choosing it does; for
// tests.
func TabPageShow(h Handle) {
	if pg := tabPages[h]; pg != nil && !pg.disabled && pg.onShown != nil {
		pg.onShown()
	}
}

func (t *tabs) add(page Handle) {
	pg := tabPages[page]
	if pg == nil || slices.Contains(t.pages, page) {
		return
	}
	t.quiet = true
	C.gtk_stack_add_child((*C.GtkStack)(p(t.stack)), widget(page))
	t.quiet = false
	t.pages = append(t.pages, page)
	pg.host = t
	t.applyTitle(page, pg)
	t.applySensitive()
	t.show()
}

func (t *tabs) remove(page Handle) {
	i := slices.Index(t.pages, page)
	if i < 0 {
		return
	}
	t.quiet = true
	C.gtk_stack_remove((*C.GtkStack)(p(t.stack)), widget(page))
	t.quiet = false
	t.pages = slices.Delete(t.pages, i, i+1)
	if pg := tabPages[page]; pg != nil {
		pg.host = nil
	}
	t.applySensitive()
	t.show()
}

func (t *tabs) show() {
	if t.selected < 0 || t.selected >= len(t.pages) {
		return
	}
	t.quiet = true
	C.gtk_stack_set_visible_child((*C.GtkStack)(p(t.stack)), widget(t.pages[t.selected]))
	t.quiet = false
}

// shown answers the stack showing another page: the user's choice, unless it
// is a disabled page's, which the program's own page replaces.
func (t *tabs) shown() {
	if t.quiet {
		return
	}
	k := slices.Index(t.pages, tabsShown(t))
	if k < 0 || k == t.selected {
		return
	}
	pg := tabPages[t.pages[k]]
	if pg == nil || pg.disabled {
		t.show()
		return
	}
	t.selected = k
	if pg.onShown != nil {
		pg.onShown()
	}
}

func (t *tabs) applyTitle(page Handle, pg *tabPage) {
	c, free := cstr(pg.title)
	defer free()
	C.sngl_tabs_title(widget(t.stack), widget(page), c)
}

func (t *tabs) applySensitive() {
	for i, page := range t.pages {
		on := C.gboolean(1)
		if pg := tabPages[page]; pg != nil && pg.disabled {
			on = 0
		}
		if b := C.sngl_tabs_button(widget(t.switcher), C.int(i)); b != nil {
			C.gtk_widget_set_sensitive(b, on)
		}
	}
}

func tabsShown(t *tabs) Handle {
	return Handle(unsafe.Pointer(C.gtk_stack_get_visible_child((*C.GtkStack)(p(t.stack)))))
}

// tabsChoose shows page as the switcher's button for it does.
func tabsChoose(t *tabs, page Handle) {
	C.gtk_stack_set_visible_child((*C.GtkStack)(p(t.stack)), widget(page))
}

func tabsTitles(t *tabs) []string {
	var out []string
	for _, page := range t.pages {
		out = append(out, C.GoString(C.sngl_tabs_title_of(widget(t.stack), widget(page))))
	}
	return out
}

func tabsSensitive(t *tabs) []bool {
	var out []bool
	for i := range t.pages {
		b := C.sngl_tabs_button(widget(t.switcher), C.int(i))
		out = append(out, b != nil && C.gtk_widget_get_sensitive(b) != 0)
	}
	return out
}
