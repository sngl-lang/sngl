/* gtk4's runtime, for a program in either language: a widget's look, a
 * window's toplevel, the application they run in. A C program compiles it
 * beside its own; Go reaches it through lib/platform/gtk4/cgo. */
#ifndef SNGL_GTK_H
#define SNGL_GTK_H

#include <gtk/gtk.h>
#include <stdbool.h>
#include <unistd.h>

#include "sngl_http.h"

/* The function types a callback crosses as: the closure's dispatcher, which
 * finds it through ctx. */
typedef void (*SnglFn)(void *ctx);
typedef void (*SnglBoolFn)(int v, void *ctx);
typedef void (*SnglStrFn)(const char *s, void *ctx);

/* sngl_post runs fn from the main loop when it is next idle, then release
 * with its ctx: how work another thread finished reaches GTK, which owns its
 * widgets from the loop's thread. */
void sngl_post(SnglFn fn, void *ctx, GDestroyNotify release);

/* What of a widget's look gtk4.sngl does not write itself (look.c): a
 * widget kept on o under key without a reference, NULL once it is
 * finalized; and a CSS provider of w's own, attached through the deprecated
 * style context and loaded as the GTK built against takes it. */
void sngl_weak_set(GObject *o, const char *key, GtkWidget *w);
GtkWidget *sngl_weak(GObject *o, const char *key);
void sngl_style_add(GtkWidget *w, GtkCssProvider *css);
void sngl_style_remove(GtkWidget *w, GtkCssProvider *css);
void sngl_css_load(GtkCssProvider *css, const char *rule);

/* A toplevel is a ui.window: a box its content is appended to, which the
 * application shows in a window of its own once it is attached and the
 * application runs. */
GtkWidget *sngl_toplevel_new(void);
void sngl_toplevel_set_title(GtkWidget *box, const char *title);
void sngl_toplevel_set_visible(GtkWidget *box, int visible);
void sngl_toplevel_on_closed(GtkWidget *box, SnglFn fn, void *ctx, GDestroyNotify release);
void sngl_toplevel_on_visible(GtkWidget *box, SnglBoolFn fn, void *ctx, GDestroyNotify release);
void sngl_toplevel_append(GtkWidget *box, GtkWidget *child);
void sngl_toplevel_insert_before(GtkWidget *box, GtkWidget *anchor, GtkWidget *child);

/* A window's `@key`: fn hears each key pressed in the window before
 * anything in it (gtk4.sngl's controllers). While it runs, sngl_key_heard is the key's name
 * and sngl_key_held whether a modifier was held -- 0 Control, 1 Alt, 2
 * Shift, 3 Super or Meta -- or, at 4, whether the focused field was being
 * edited; sngl_key_stop keeps the key from going further. Neither is
 * anything once fn returns. sngl_toplevel_key presses a key as a test
 * describes it: fn hears it, stop is called with stop_ctx by the handler's
 * stop and released after, and a key the handler leaves goes on where it
 * would have gone. */
void sngl_toplevel_on_key(GtkWidget *box, SnglFn fn, void *ctx, GDestroyNotify release);
const char *sngl_key_heard(void);
int sngl_key_held(int which);
void sngl_key_stop(void);
void sngl_toplevel_key(GtkWidget *box, const char *key, int ctrl, int alt, int shift, int meta, int editing,
                       SnglFn stop, void *stop_ctx, GDestroyNotify stop_release);
void sngl_gtk_init(void);
/* What an entry whose platform node wrote `@run` calls: sngl_gtk_hold makes
 * GTK ready to build in and holds the windows the build attaches hidden as
 * the loop starts, sngl_gtk_present has the loop show them after all, and
 * sngl_gtk_run runs the application over the tree already built. */
void sngl_gtk_hold(void);
void sngl_gtk_present(void);
int sngl_gtk_run(void);
/* sngl_gtk_quit ends the application's loop, once it has one. */
void sngl_gtk_quit(void);
void sngl_gtk_step(void);
/* sngl_gtk_reset takes down every toplevel attached, its window destroyed:
 * what a test agent does before the next test builds afresh. */
void sngl_gtk_reset(void);
int sngl_gtk_main(SnglFn build, void *ctx, GDestroyNotify release);
/* A widget's signal and a property's notification, connected through
 * trampolines that hand fn only its context. */
void sngl_signal_connect(GtkWidget *w, const char *signal, SnglFn fn, void *ctx, GDestroyNotify release);
void sngl_notify_connect(GtkWidget *w, const char *signal, SnglFn fn, void *ctx, GDestroyNotify release);
/* A GTK property with no setter of its own, set by name through GObject. */
void sngl_set_prop_string(void *obj, const char *name, const char *v);
void sngl_set_prop_bool(void *obj, const char *name, int v);
void sngl_set_prop_int(void *obj, const char *name, int v);
void sngl_set_prop_double(void *obj, const char *name, double v);
void sngl_app_attach(GtkWidget *box);
/* sngl_toplevel_window is the window a toplevel is shown in, or NULL while
 * it has none. */
GtkWindow *sngl_toplevel_window(GtkWidget *box);

void sngl_app_detach(GtkWidget *box);

/* sngl_capture draws root -- or, where it is NULL, the content of the
 * toplevel attached last -- in a window of width by height, as a test's
 * snapshot sees it: into *rgba, which the caller frees, *w by *h pixels,
 * red, green, blue and alpha, from the top. False, with *why set, where
 * there is nothing to draw. The widget leaves the window as it came. */
bool sngl_capture(GtkWidget *root, int width, int height, unsigned char **rgba, int *w, int *h, const char **why);
int sngl_gtk_capture_file(const char *path, int width, int height);

/* SNGL_APP is the application as a box: a slot at the package's root
 * renders its windows into it, which the box calls below attach and
 * detach, in no order a window shows. */
#define SNGL_APP ((GtkWidget *)NULL)

/* A slot's children sit in its parent box before its anchor, a hidden
 * widget sngl_slot_anchor makes at the box's end and hands back while it is
 * still the box's: what keeps a slot's children in their place among the
 * box's others as it renders again. The slot's var holds a reference on it,
 * so one its box let go of is still there to ask, and given back as the
 * next is made, or as a record destroyed asks with no box (SNGL_APP). NULL
 * where the slot renders into the application. */
GtkWidget *sngl_slot_anchor(GtkWidget *box, GtkWidget *anchor);
void sngl_box_insert_before(GtkWidget *box, GtkWidget *anchor, GtkWidget *child);
void sngl_box_remove(GtkWidget *box, GtkWidget *child);
/* sngl_box_append is a child put at the end of a box, which a box of rt's
 * own takes as it takes children: a tabs as a page, a window a source as its
 * icon and an entry or a way back into its bar. */
void sngl_box_append(GtkWidget *box, GtkWidget *child);

/* A tabs is a box holding a GtkNotebook, whose pages are the tab pages
 * appended to the box: gtk4.sngl's. The box calls hand a tabs's append and
 * remove to the two functions sngl_tabs_hooks was last handed, each taking
 * the box and the page and answering whether the box is a tabs. */
typedef int (*SnglTabsHook)(GtkWidget *box, GtkWidget *page);
void sngl_tabs_hooks(SnglTabsHook add, SnglTabsHook remove);

/* A window's menu entries and its way back: hidden widgets among its
 * content, from which the window builds its header bar's menu and back
 * button, gtk4.sngl's. The box calls and the window hand them to the
 * functions sngl_menu_hooks was last handed: apply builds a window's menu
 * afresh, moved tells box an entry or a way back came or went, back presses
 * the first way back among a window's content and answers whether there
 * was one, and forget drops the bar of a window destroyed. */
typedef void (*SnglMenuHook)(GtkWidget *box);
typedef void (*SnglMenuMovedHook)(GtkWidget *box, GtkWidget *child);
typedef int (*SnglMenuBackHook)(GtkWidget *box);
void sngl_menu_hooks(SnglMenuHook apply, SnglMenuMovedHook moved, SnglMenuBackHook back, SnglMenuHook forget);

/* An image is a box holding the source it shows, a GtkPicture showing a
 * picture or what a drawing paints: gtk4.sngl's, which reads the bytes a
 * URL names. A file a program carries is one of the table its entry hands
 * sngl_gtk_carry, ending at a NULL name, or one sngl_gtk_carry_file was
 * handed, as a Go program hands each it embeds, which sngl_carried finds
 * by name (image.c).
 *
 * The box calls hand a window's append and remove of a source, its icon, to
 * the functions sngl_image_hooks was last handed, which answer whether they
 * were, and a window made has refresh read its icon. */
typedef struct {
    const char *name;
    const unsigned char *data;
    size_t len;
} SnglAsset;
extern const SnglAsset sngl_assets[];
void sngl_gtk_carry(const SnglAsset *assets);
void sngl_gtk_carry_file(const char *name, const char *data, int len);
const SnglAsset *sngl_carried(const char *name);
/* What sngl_http_start hands a fetch's answer to (gtk4.sngl's picture). */
typedef void (*SnglHttpDone)(sngl_http_answer *a, void *ctx);
typedef void (*SnglIconHook)(GtkWidget *box);
void sngl_image_hooks(SnglTabsHook adopt, SnglTabsHook unadopt, SnglIconHook refresh);

/* A timer's schedule: fn runs with ctx every ms milliseconds, on the main
 * loop's thread, until sngl_schedule_cancel removes it, which releases ctx.
 * A period of 0 or less arms nothing and answers NULL, which cancels as a
 * no-op, as does a schedule never armed. */
typedef struct SnglSchedule SnglSchedule;
SnglSchedule *sngl_every(int ms, SnglFn fn, void *ctx, GDestroyNotify release);
void sngl_schedule_cancel(SnglSchedule *s);

/* A window's keys are gtk4.sngl's, handed over through sngl_keys_hooks:
 * watch gives a window made the controllers that hear its keys, and go_on
 * takes a key a test pressed that nothing stopped where it would have
 * gone. sngl_toplevel_hear has the toplevel box's `@key` hear a key, and
 * is whether its handler stopped it. */
typedef void (*SnglKeysWatch)(GtkWidget *win);
typedef void (*SnglKeysGoOn)(GtkWidget *win, const char *key, int shift);
void sngl_keys_hooks(SnglKeysWatch watch, SnglKeysGoOn go_on);
int sngl_toplevel_hear(GtkWidget *box, const char *key, int ctrl, int alt, int shift, int meta, int editing);

/* sngl_calendar_select_date selects day on w, a calendar, as
 * gtk_calendar_select_day does, which GTK 4.20 deprecates (choose.c). */
void sngl_calendar_select_date(GtkWidget *w, GDateTime *day);

/* sngl_app_run runs the application: build makes the program's tree, its
 * toplevels attached, once GTK is up, and the windows of the visible ones
 * are put on screen. The status is the application's. */
int sngl_app_run(void (*build)(void));


#endif
