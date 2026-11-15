/* A window's menu and its way back on GTK, for a program in either language.
 * A menu entry is a hidden box among the
 * window's content, where it takes no room, holding its submenu's entries as
 * its children; the window builds its menu from the entries it holds -- a
 * GMenu behind the header bar's menu button -- whenever one comes, goes or
 * changes. A back is a hidden widget among the content, which has the window
 * show a back button that presses it while one is there. */
#include "sngl_gtk.h"

typedef struct {
    char *label;
    bool disabled;
    bool checked;
    SnglFn on_select;
    void *ctx;
    GDestroyNotify release;
} SnglMenuEntry;

typedef struct {
    SnglFn on_press;
    void *ctx;
    GDestroyNotify release;
} SnglBack;

/* A window's bar: the header bar holding the back button and the menu
 * button, decided once as the window is made, since GTK sets a title bar
 * only on a window not yet shown. */
typedef struct {
    bool decided;
    GtkWidget *bar, *back_button, *menu_button;
} SnglBar;

static SnglMenuEntry *sngl_entry_of(GtkWidget *w) {
    return w ? g_object_get_data(G_OBJECT(w), "sngl-menu-entry") : NULL;
}

static SnglBack *sngl_back_of(GtkWidget *w) {
    return w ? g_object_get_data(G_OBJECT(w), "sngl-back") : NULL;
}

static void sngl_entry_free(gpointer data) {
    SnglMenuEntry *e = data;
    if (e->release) e->release(e->ctx);
    g_free(e->label);
    g_free(e);
}

GtkWidget *sngl_menu_entry_new(void) {
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    gtk_widget_set_visible(box, FALSE);
    g_object_set_data_full(G_OBJECT(box), "sngl-menu-entry", g_new0(SnglMenuEntry, 1), sngl_entry_free);
    return box;
}

/* The window whose menu w is in, through the entries holding it, or NULL. */
static GtkWidget *sngl_menu_host(GtkWidget *w) {
    for (GtkWidget *p = gtk_widget_get_parent(w); p; p = gtk_widget_get_parent(p)) {
        if (g_object_get_data(G_OBJECT(p), "sngl-toplevel")) return p;
        if (!sngl_entry_of(p)) return NULL;
    }
    return NULL;
}

static void sngl_entry_changed(GtkWidget *w) {
    GtkWidget *host = sngl_menu_host(w);
    if (host) sngl_menu_apply(host);
}

void sngl_menu_entry_set_label(GtkWidget *w, const char *label) {
    SnglMenuEntry *e = sngl_entry_of(w);
    if (!e || g_strcmp0(e->label, label) == 0) return;
    g_free(e->label);
    e->label = g_strdup(label);
    sngl_entry_changed(w);
}

void sngl_menu_entry_set_disabled(GtkWidget *w, int v) {
    SnglMenuEntry *e = sngl_entry_of(w);
    if (!e || e->disabled == v) return;
    e->disabled = v;
    sngl_entry_changed(w);
}

void sngl_menu_entry_set_checked(GtkWidget *w, int v) {
    SnglMenuEntry *e = sngl_entry_of(w);
    if (!e || e->checked == v) return;
    e->checked = v;
    sngl_entry_changed(w);
}

void sngl_menu_entry_on_select(GtkWidget *w, SnglFn fn, void *ctx, GDestroyNotify release) {
    SnglMenuEntry *e = sngl_entry_of(w);
    if (!e) {
        if (release) release(ctx);
        return;
    }
    if (e->release) e->release(e->ctx);
    e->on_select = fn;
    e->ctx = ctx;
    e->release = release;
}

void sngl_menu_entry_select(GtkWidget *w) {
    SnglMenuEntry *e = sngl_entry_of(w);
    if (e && !e->disabled && e->on_select) e->on_select(e->ctx);
}

static void sngl_back_free(gpointer data) {
    SnglBack *b = data;
    if (b->release) b->release(b->ctx);
    g_free(b);
}

GtkWidget *sngl_back_new(void) {
    GtkWidget *w = gtk_label_new(NULL);
    gtk_widget_set_visible(w, FALSE);
    g_object_set_data_full(G_OBJECT(w), "sngl-back", g_new0(SnglBack, 1), sngl_back_free);
    return w;
}

void sngl_back_on_press(GtkWidget *w, SnglFn fn, void *ctx, GDestroyNotify release) {
    SnglBack *b = sngl_back_of(w);
    if (!b) {
        if (release) release(ctx);
        return;
    }
    if (b->release) b->release(b->ctx);
    b->on_press = fn;
    b->ctx = ctx;
    b->release = release;
}

void sngl_back_press(GtkWidget *w) {
    SnglBack *b = sngl_back_of(w);
    if (b && b->on_press) b->on_press(b->ctx);
}

/* The first way back among a window's content, or NULL. */
static GtkWidget *sngl_back_in(GtkWidget *box) {
    for (GtkWidget *c = gtk_widget_get_first_child(box); c; c = gtk_widget_get_next_sibling(c)) {
        if (sngl_back_of(c)) return c;
    }
    return NULL;
}

bool sngl_window_back(GtkWidget *box) {
    GtkWidget *b = sngl_back_in(box);
    if (!b) return false;
    sngl_back_press(b);
    return true;
}

static void sngl_back_clicked(GtkButton *button, gpointer box) {
    sngl_window_back(box);
}

static guint sngl_entries_in(GtkWidget *box, GtkWidget **out, guint max) {
    guint n = 0;
    for (GtkWidget *c = gtk_widget_get_first_child(box); c; c = gtk_widget_get_next_sibling(c)) {
        if (sngl_entry_of(c)) {
            if (n < max) out[n] = c;
            n++;
        }
    }
    return n;
}

static SnglBar *sngl_bar_of(GtkWidget *box) {
    SnglBar *b = g_object_get_data(G_OBJECT(box), "sngl-bar");
    if (!b) {
        b = g_new0(SnglBar, 1);
        g_object_set_data_full(G_OBJECT(box), "sngl-bar", b, g_free);
    }
    return b;
}

/* A window destroyed takes its bar with it, and a back button in it: the
 * next window decides its own. One over the content stays there. */
void sngl_bar_forget(GtkWidget *box) {
    SnglBar *b = g_object_get_data(G_OBJECT(box), "sngl-bar");
    if (!b) return;
    if (b->bar) b->back_button = NULL;
    b->bar = b->menu_button = NULL;
    b->decided = false;
}

/* The back button goes in the bar, or over the content of a window made
 * without one, and shows while a way back is among the content. */
void sngl_bar_apply(GtkWidget *box) {
    GtkWindow *win = sngl_toplevel_window(box);
    if (!win) return;
    SnglBar *b = sngl_bar_of(box);
    guint entries = sngl_entries_in(box, NULL, 0);
    bool can_back = sngl_back_in(box) != NULL;
    if (!b->decided) {
        b->decided = true;
        if (entries > 0 || can_back) {
            b->bar = gtk_header_bar_new();
            gtk_window_set_titlebar(win, b->bar);
            b->menu_button = gtk_menu_button_new();
            gtk_menu_button_set_icon_name(GTK_MENU_BUTTON(b->menu_button), "open-menu-symbolic");
            gtk_header_bar_pack_end(GTK_HEADER_BAR(b->bar), b->menu_button);
        }
    }
    if (!b->back_button && can_back) {
        b->back_button = gtk_button_new_from_icon_name("go-previous-symbolic");
        g_signal_connect(b->back_button, "clicked", G_CALLBACK(sngl_back_clicked), box);
        if (b->bar) {
            gtk_header_bar_pack_start(GTK_HEADER_BAR(b->bar), b->back_button);
        } else {
            gtk_widget_set_halign(b->back_button, GTK_ALIGN_START);
            gtk_box_prepend(GTK_BOX(box), b->back_button);
        }
    }
    if (b->back_button) gtk_widget_set_visible(b->back_button, can_back);
    if (b->menu_button) gtk_widget_set_visible(b->menu_button, entries > 0);
}

static void sngl_menu_activate(GSimpleAction *a, GVariant *param, gpointer w) {
    sngl_menu_entry_select(w);
}

static void sngl_menu_add(GMenu *menu, GSimpleActionGroup *group, int *n, GtkWidget *w, bool checkable) {
    SnglMenuEntry *e = sngl_entry_of(w);
    char name[32];
    g_snprintf(name, sizeof name, "e%d", (*n)++);
    GSimpleAction *a = checkable
        ? g_simple_action_new_stateful(name, NULL, g_variant_new_boolean(e->checked))
        : g_simple_action_new(name, NULL);
    g_simple_action_set_enabled(a, !e->disabled);
    g_signal_connect(a, "activate", G_CALLBACK(sngl_menu_activate), w);
    g_action_map_add_action(G_ACTION_MAP(group), G_ACTION(a));
    g_object_unref(a);
    char *detailed = g_strconcat("sngl.", name, NULL);
    g_menu_append(menu, e->label ? e->label : "", detailed);
    g_free(detailed);
}

/* A submenu per top-level entry holding entries, or the entry itself where
 * it holds none. A submenu where one entry is checked draws them all as
 * checks. */
void sngl_menu_apply(GtkWidget *box) {
    sngl_bar_apply(box);
    GtkWindow *win = sngl_toplevel_window(box);
    SnglBar *b = g_object_get_data(G_OBJECT(box), "sngl-bar");
    if (!win || !b || !b->menu_button) return;
    GMenu *menu = g_menu_new();
    GSimpleActionGroup *group = g_simple_action_group_new();
    int n = 0;
    for (GtkWidget *c = gtk_widget_get_first_child(box); c; c = gtk_widget_get_next_sibling(c)) {
        SnglMenuEntry *e = sngl_entry_of(c);
        if (!e) continue;
        if (sngl_entries_in(c, NULL, 0) == 0) {
            sngl_menu_add(menu, group, &n, c, e->checked);
            continue;
        }
        bool checkable = false;
        for (GtkWidget *k = gtk_widget_get_first_child(c); k; k = gtk_widget_get_next_sibling(k)) {
            SnglMenuEntry *ke = sngl_entry_of(k);
            checkable = checkable || (ke && ke->checked);
        }
        GMenu *sub = g_menu_new();
        for (GtkWidget *k = gtk_widget_get_first_child(c); k; k = gtk_widget_get_next_sibling(k)) {
            if (sngl_entry_of(k)) sngl_menu_add(sub, group, &n, k, checkable);
        }
        g_menu_append_submenu(menu, e->label ? e->label : "", G_MENU_MODEL(sub));
        g_object_unref(sub);
    }
    gtk_widget_insert_action_group(GTK_WIDGET(win), "sngl", G_ACTION_GROUP(group));
    /* A popover of its own each time: one handed a new model keeps each
     * submenu's page of the last and adds another of the same name. */
    gtk_menu_button_set_popover(GTK_MENU_BUTTON(b->menu_button), gtk_popover_menu_new_from_model(G_MENU_MODEL(menu)));
    g_object_unref(group);
    g_object_unref(menu);
}

void sngl_menu_moved(GtkWidget *box, GtkWidget *child) {
    if (sngl_back_of(child)) {
        if (g_object_get_data(G_OBJECT(box), "sngl-toplevel")) sngl_bar_apply(box);
    } else if (sngl_entry_of(child)) {
        if (g_object_get_data(G_OBJECT(box), "sngl-toplevel")) {
            sngl_menu_apply(box);
        } else if (sngl_entry_of(box)) {
            sngl_entry_changed(box);
        }
    }
}
