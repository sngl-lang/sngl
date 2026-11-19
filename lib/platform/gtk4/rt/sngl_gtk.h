/* What of gtk4's runtime is C, for a program in either language: a window's
 * toplevel and the application it runs in, the timer, the posted work and
 * loop wait the C language runtime calls, a snapshot's capture, and what of
 * a look, a calendar and a carried file SNGL cannot write. A C program
 * compiles it beside its own; Go reaches it through lib/platform/gtk4/cgo. */
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
/* A toplevel attached to the application, its window shown once the
 * application runs, and taken off it, its window destroyed: what
 * gtk4.sngl's box calls end in for a window. */
void sngl_toplevel_attach(GtkWidget *box);
void sngl_toplevel_detach(GtkWidget *box);
/* sngl_toplevel_window is the window a toplevel is shown in, or NULL while
 * it has none. */
GtkWindow *sngl_toplevel_window(GtkWidget *box);

/* sngl_capture draws root -- or, where it is NULL, the content of the
 * toplevel attached last -- in a window of width by height, as a test's
 * snapshot sees it: into *rgba, which the caller frees, *w by *h pixels,
 * red, green, blue and alpha, from the top. False, with *why set, where
 * there is nothing to draw. The widget leaves the window as it came. */
bool sngl_capture(GtkWidget *root, int width, int height, unsigned char **rgba, int *w, int *h, const char **why);
int sngl_gtk_capture_file(const char *path, int width, int height);

/* A window's menu entries and its way back: hidden widgets among its
 * content, from which the window builds its header bar's menu and back
 * button, gtk4.sngl's. The toplevel hands them to the functions
 * sngl_menu_hooks was last handed: apply builds a window's menu afresh,
 * back presses the first way back among a window's content and answers
 * whether there was one, and forget drops the bar of a window destroyed. */
typedef void (*SnglMenuHook)(GtkWidget *box);
typedef int (*SnglMenuBackHook)(GtkWidget *box);
void sngl_menu_hooks(SnglMenuHook apply, SnglMenuBackHook back, SnglMenuHook forget);

/* An image is a box holding the source it shows, a GtkPicture showing a
 * picture or what a drawing paints: gtk4.sngl's, which reads the bytes a
 * URL names. A file a program carries is one of the table its entry hands
 * sngl_gtk_carry, ending at a NULL name, or one sngl_gtk_carry_file was
 * handed, as a Go program hands each it embeds, which sngl_carried finds
 * by name (image.c). A window made has the function sngl_icon_hook was
 * last handed read its icon, gtk4.sngl's. */
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
void sngl_icon_hook(SnglIconHook refresh);

/* A timer's schedule: fn runs with ctx every ms milliseconds, on the main
 * loop's thread, until sngl_schedule_cancel removes it, which releases ctx.
 * A period of 0 or less arms nothing and answers NULL, which cancels as a
 * no-op, as does a schedule never armed. */
typedef struct SnglSchedule SnglSchedule;
SnglSchedule *sngl_every(int ms, SnglFn fn, void *ctx, GDestroyNotify release);
void sngl_schedule_cancel(SnglSchedule *s);

/* A window's keys are gtk4.sngl's, which sngl_keys_hooks hands the
 * function that gives a window made the controllers that hear them. */
typedef void (*SnglKeysWatch)(GtkWidget *win);
void sngl_keys_hooks(SnglKeysWatch watch);

/* sngl_calendar_select_date selects day on w, a calendar, as
 * gtk_calendar_select_day does, which GTK 4.20 deprecates (choose.c). */
void sngl_calendar_select_date(GtkWidget *w, GDateTime *day);

/* sngl_app_run runs the application: build makes the program's tree, its
 * toplevels attached, once GTK is up, and the windows of the visible ones
 * are put on screen. The status is the application's. */
int sngl_app_run(void (*build)(void));


#endif
