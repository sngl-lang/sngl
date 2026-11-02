//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>

extern void snglRtDispatch(int idx);
extern void snglToplevelGone(gpointer data, GObject *where);

// A notebook its box let go of is being torn down, and the pages it drops as
// it goes are no one's choice.
static void sngl_tabs_shown(GObject *o, GParamSpec *ps, gpointer data) {
    (void)ps;
    if (gtk_widget_get_parent(GTK_WIDGET(o)) == NULL) return;
    snglRtDispatch(GPOINTER_TO_INT(data));
}
// The notebook reports the page it shows through its `page` property, after
// the change, where `switch-page` fires before it.
static GtkWidget *sngl_tabs_new(int shown, GtkWidget **notebook) {
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    *notebook = gtk_notebook_new();
    gtk_notebook_set_scrollable(GTK_NOTEBOOK(*notebook), TRUE);
    gtk_box_append(GTK_BOX(box), *notebook);
    g_signal_connect(*notebook, "notify::page", G_CALLBACK(sngl_tabs_shown), GINT_TO_POINTER(shown));
    return box;
}
static void sngl_tabs_watch(GObject *o, int idx) {
    g_object_weak_ref(o, snglToplevelGone, GINT_TO_POINTER(idx));
}
static void sngl_tabs_title(GtkWidget *notebook, GtkWidget *page, const char *title) {
    gtk_notebook_set_tab_label_text(GTK_NOTEBOOK(notebook), page, title);
}
static const char *sngl_tabs_title_of(GtkWidget *notebook, GtkWidget *page) {
    return gtk_notebook_get_tab_label_text(GTK_NOTEBOOK(notebook), page);
}
static GtkWidget *sngl_tabs_label(GtkWidget *notebook, GtkWidget *page) {
    return gtk_notebook_get_tab_label(GTK_NOTEBOOK(notebook), page);
}
static void sngl_tabs_show(GtkWidget *notebook, GtkWidget *page) {
    gtk_notebook_set_current_page(GTK_NOTEBOOK(notebook), gtk_notebook_page_num(GTK_NOTEBOOK(notebook), page));
}
static GtkWidget *sngl_tabs_shown_page(GtkWidget *notebook) {
    int k = gtk_notebook_get_current_page(GTK_NOTEBOOK(notebook));
    return k < 0 ? NULL : gtk_notebook_get_nth_page(GTK_NOTEBOOK(notebook), k);
}
static void sngl_tabs_remove(GtkWidget *notebook, GtkWidget *page) {
    gtk_notebook_remove_page(GTK_NOTEBOOK(notebook), gtk_notebook_page_num(GTK_NOTEBOOK(notebook), page));
}
// Probes for tabs_test.go.
static int sngl_is_notebook(GtkWidget *w) { return GTK_IS_NOTEBOOK(w); }
*/
import "C"

import (
	"slices"
	"unsafe"

	"duckfam.us/sngl/pkg/go/cbind"
)

// A tabs is a `ui.tabs` on gtk4: a box holding a GtkNotebook. A child
// appended or inserted into the box is a page of the notebook, and removing
// one takes it out; the box itself holds only the notebook, and a render
// slot's anchor.
type tabs struct {
	notebook Handle
	pages    []Handle
	// selected is the program's, which the notebook shows.
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
	var nb *C.GtkWidget
	box := Handle(unsafe.Pointer(C.sngl_tabs_new(C.int(shown), &nb)))
	t.notebook = Handle(unsafe.Pointer(nb))
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
	C.gtk_notebook_append_page((*C.GtkNotebook)(p(t.notebook)), widget(page), nil)
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
	C.sngl_tabs_remove(widget(t.notebook), widget(page))
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
	C.sngl_tabs_show(widget(t.notebook), widget(t.pages[t.selected]))
	t.quiet = false
}

// shown answers the notebook showing another page: the user's choice, unless it
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
	C.sngl_tabs_title(widget(t.notebook), widget(page), c)
}

// A disabled page's tab is insensitive. GTK still lets the user click it,
// and shown puts the program's page back.
func (t *tabs) applySensitive() {
	for _, page := range t.pages {
		on := C.gboolean(1)
		if pg := tabPages[page]; pg != nil && pg.disabled {
			on = 0
		}
		if l := C.sngl_tabs_label(widget(t.notebook), widget(page)); l != nil {
			C.gtk_widget_set_sensitive(l, on)
		}
	}
}

func tabsShown(t *tabs) Handle {
	return Handle(unsafe.Pointer(C.sngl_tabs_shown_page(widget(t.notebook))))
}

// tabsChoose shows page as clicking its tab does.
func tabsChoose(t *tabs, page Handle) {
	C.sngl_tabs_show(widget(t.notebook), widget(page))
}

func tabsTitles(t *tabs) []string {
	var out []string
	for _, page := range t.pages {
		out = append(out, C.GoString(C.sngl_tabs_title_of(widget(t.notebook), widget(page))))
	}
	return out
}

func tabsSensitive(t *tabs) []bool {
	var out []bool
	for _, page := range t.pages {
		l := C.sngl_tabs_label(widget(t.notebook), widget(page))
		out = append(out, l != nil && C.gtk_widget_get_sensitive(l) != 0)
	}
	return out
}

func isNotebook(w Handle) bool { return C.sngl_is_notebook(widget(w)) != 0 }

func notebookPages(w Handle) int {
	return int(C.gtk_notebook_get_n_pages((*C.GtkNotebook)(p(w))))
}
