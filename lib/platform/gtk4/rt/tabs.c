/* A `ui.tabs` on GTK for a program written in C: a box holding a
 * GtkNotebook. A page appended or inserted into the box is a page of the
 * notebook, and removing one takes it out; the box itself holds only the
 * notebook. */
#include "sngl_gtk.h"

typedef struct SnglTabs SnglTabs;

typedef struct {
    char *title;
    bool disabled;
    SnglFn on_shown;
    void *ctx;
    GDestroyNotify release;
    SnglTabs *host;
} SnglTabPage;

struct SnglTabs {
    GtkWidget *notebook;
    GPtrArray *pages;
    /* selected is the program's, which the notebook shows. */
    int selected;
    /* quiet is set while rt changes the page shown, which GTK reports as it
     * would the user's. */
    bool quiet;
};

static SnglTabs *sngl_tabs_of(GtkWidget *box) {
    return box ? g_object_get_data(G_OBJECT(box), "sngl-tabs") : NULL;
}

static SnglTabPage *sngl_tab_page_of(GtkWidget *page) {
    return page ? g_object_get_data(G_OBJECT(page), "sngl-tab-page") : NULL;
}

static void sngl_tabs_show(SnglTabs *t) {
    if (t->selected < 0 || (guint)t->selected >= t->pages->len) return;
    GtkWidget *page = g_ptr_array_index(t->pages, t->selected);
    t->quiet = true;
    gtk_notebook_set_current_page(GTK_NOTEBOOK(t->notebook), gtk_notebook_page_num(GTK_NOTEBOOK(t->notebook), page));
    t->quiet = false;
}

/* A disabled page's tab is insensitive. GTK still lets the user click it,
 * and sngl_tabs_shown puts the program's page back. */
static void sngl_tabs_sensitive(SnglTabs *t) {
    for (guint i = 0; i < t->pages->len; i++) {
        GtkWidget *page = g_ptr_array_index(t->pages, i);
        SnglTabPage *pg = sngl_tab_page_of(page);
        GtkWidget *l = gtk_notebook_get_tab_label(GTK_NOTEBOOK(t->notebook), page);
        if (l) gtk_widget_set_sensitive(l, !(pg && pg->disabled));
    }
}

static void sngl_tabs_title(SnglTabs *t, GtkWidget *page, SnglTabPage *pg) {
    gtk_notebook_set_tab_label_text(GTK_NOTEBOOK(t->notebook), page, pg->title ? pg->title : "");
}

/* The notebook showing another page: the user's choice, unless it is a
 * disabled page's, which the program's own page replaces. A notebook its box
 * let go of is being torn down, and the pages it drops as it goes are no
 * one's choice. */
static void sngl_tabs_shown(GObject *o, GParamSpec *ps, gpointer data) {
    SnglTabs *t = data;
    if (t->quiet || gtk_widget_get_parent(GTK_WIDGET(o)) == NULL) return;
    int k = gtk_notebook_get_current_page(GTK_NOTEBOOK(t->notebook));
    GtkWidget *shown = k < 0 ? NULL : gtk_notebook_get_nth_page(GTK_NOTEBOOK(t->notebook), k);
    guint at;
    if (shown == NULL || !g_ptr_array_find(t->pages, shown, &at) || (int)at == t->selected) return;
    SnglTabPage *pg = sngl_tab_page_of(shown);
    if (pg == NULL || pg->disabled) {
        sngl_tabs_show(t);
        return;
    }
    t->selected = (int)at;
    if (pg->on_shown) pg->on_shown(pg->ctx);
}

static void sngl_tabs_free(gpointer data) {
    SnglTabs *t = data;
    for (guint i = 0; i < t->pages->len; i++) {
        SnglTabPage *pg = sngl_tab_page_of(g_ptr_array_index(t->pages, i));
        if (pg) pg->host = NULL;
    }
    g_ptr_array_unref(t->pages);
    g_free(t);
}

/* The notebook reports the page it shows through its `page` property, after
 * the change, where `switch-page` fires before it. */
GtkWidget *sngl_tabs_new(void) {
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    SnglTabs *t = g_new0(SnglTabs, 1);
    t->notebook = gtk_notebook_new();
    t->pages = g_ptr_array_new();
    gtk_notebook_set_scrollable(GTK_NOTEBOOK(t->notebook), TRUE);
    gtk_box_append(GTK_BOX(box), t->notebook);
    g_signal_connect(t->notebook, "notify::page", G_CALLBACK(sngl_tabs_shown), t);
    g_object_set_data_full(G_OBJECT(box), "sngl-tabs", t, sngl_tabs_free);
    return box;
}

void sngl_tabs_set_selected(GtkWidget *box, int k) {
    SnglTabs *t = sngl_tabs_of(box);
    if (!t) return;
    t->selected = k;
    sngl_tabs_show(t);
}

bool sngl_tabs_add(GtkWidget *box, GtkWidget *page) {
    SnglTabs *t = sngl_tabs_of(box);
    if (!t) return false;
    SnglTabPage *pg = sngl_tab_page_of(page);
    if (pg == NULL || g_ptr_array_find(t->pages, page, NULL)) return true;
    t->quiet = true;
    gtk_notebook_append_page(GTK_NOTEBOOK(t->notebook), page, NULL);
    t->quiet = false;
    g_ptr_array_add(t->pages, page);
    pg->host = t;
    sngl_tabs_title(t, page, pg);
    sngl_tabs_sensitive(t);
    sngl_tabs_show(t);
    return true;
}

bool sngl_tabs_remove(GtkWidget *box, GtkWidget *page) {
    SnglTabs *t = sngl_tabs_of(box);
    if (!t) return false;
    if (!g_ptr_array_remove(t->pages, page)) return true;
    t->quiet = true;
    gtk_notebook_remove_page(GTK_NOTEBOOK(t->notebook), gtk_notebook_page_num(GTK_NOTEBOOK(t->notebook), page));
    t->quiet = false;
    SnglTabPage *pg = sngl_tab_page_of(page);
    if (pg) pg->host = NULL;
    sngl_tabs_sensitive(t);
    sngl_tabs_show(t);
    return true;
}

static void sngl_tab_page_free(gpointer data) {
    SnglTabPage *pg = data;
    if (pg->release) pg->release(pg->ctx);
    g_free(pg->title);
    g_free(pg);
}

GtkWidget *sngl_tab_page_new(void) {
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    g_object_set_data_full(G_OBJECT(box), "sngl-tab-page", g_new0(SnglTabPage, 1), sngl_tab_page_free);
    return box;
}

void sngl_tab_page_set_title(GtkWidget *page, const char *title) {
    SnglTabPage *pg = sngl_tab_page_of(page);
    if (!pg) return;
    g_free(pg->title);
    pg->title = g_strdup(title);
    if (pg->host) sngl_tabs_title(pg->host, page, pg);
}

void sngl_tab_page_set_disabled(GtkWidget *page, int disabled) {
    SnglTabPage *pg = sngl_tab_page_of(page);
    if (!pg) return;
    pg->disabled = disabled;
    if (pg->host) sngl_tabs_sensitive(pg->host);
}

void sngl_tab_page_on_shown(GtkWidget *page, SnglFn fn, void *ctx, GDestroyNotify release) {
    SnglTabPage *pg = sngl_tab_page_of(page);
    if (!pg) {
        if (release) release(ctx);
        return;
    }
    if (pg->release) pg->release(pg->ctx);
    pg->on_shown = fn;
    pg->ctx = ctx;
    pg->release = release;
}

void sngl_tab_page_show(GtkWidget *page) {
    SnglTabPage *pg = sngl_tab_page_of(page);
    if (pg && !pg->disabled && pg->on_shown) pg->on_shown(pg->ctx);
}
