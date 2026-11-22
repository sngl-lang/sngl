/* What of gtk4's runtime is C, for a program in either language: the
 * timer, the posted work and loop wait the C language runtime calls, the
 * watch on SIGINT and SIGTERM, a snapshot's drawing, and what of a look, a
 * calendar and a carried file SNGL cannot write. The toplevel, the
 * application and its loop are gtk4.sngl's. A C program compiles it beside
 * its own; Go reaches it through lib/platform/gtk4/cgo. */
#ifndef SNGL_GTK_H
#define SNGL_GTK_H

#include <gtk/gtk.h>
#include <stdbool.h>
#include <unistd.h>

#include "sngl_http.h"

/* The function type a callback crosses as: the closure's dispatcher, which
 * finds it through ctx. */
typedef void (*SnglFn)(void *ctx);

/* sngl_post runs fn from the main loop when it is next idle, then release
 * with its ctx: how work another thread finished reaches GTK, which owns its
 * widgets from the loop's thread. */
void sngl_post(SnglFn fn, void *ctx, GDestroyNotify release);

/* What of a widget's look gtk4.sngl does not write itself: a
 * widget kept on o under key without a reference, NULL once it is
 * finalized; and a CSS provider loaded as the GTK built against takes it. */
void sngl_weak_set(GObject *o, const char *key, GtkWidget *w);
GtkWidget *sngl_weak(GObject *o, const char *key);
void sngl_css_load(GtkCssProvider *css, const char *rule);

/* sngl_signal_watch has fn called with data on the main loop as the process
 * is sent SIGINT (which 0) or SIGTERM (which 1), until the source it answers
 * is removed; 0 where the system sends neither. */
guint sngl_signal_watch(int which, GSourceFunc fn, gpointer data);

/* sngl_draw draws win, a window gtk4.sngl has presented, at width by height,
 * as a test's snapshot sees it, once it is laid out: into *rgba, which the
 * caller frees, *w by *h pixels, red, green, blue and alpha, from the top.
 * False, with *why set, where it drew nothing. sngl_draw_file writes what it
 * draws to path as its width and height, each four bytes little-endian, and
 * then its pixels, and is not zero, with why on stderr, where win is NULL or
 * nothing could be drawn. */
bool sngl_draw(GtkWidget *win, int width, int height, unsigned char **rgba, int *w, int *h, const char **why);
int sngl_draw_file(GtkWidget *win, const char *path, int width, int height);

/* An image is a box holding the source it shows, a GtkPicture showing a
 * picture or what a drawing paints: gtk4.sngl's, which reads the bytes a
 * URL names. A file a program carries is one of the table its entry hands
 * sngl_gtk_carry, ending at a NULL name, or one sngl_gtk_carry_file was
 * handed, as a Go program hands each it embeds, which sngl_carried finds
 * by name. */
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

/* A timer's schedule: fn runs with ctx every ms milliseconds, on the main
 * loop's thread, until sngl_schedule_cancel removes it, which releases ctx.
 * A period of 0 or less arms nothing and answers NULL, which cancels as a
 * no-op, as does a schedule never armed. */
typedef struct SnglSchedule SnglSchedule;
SnglSchedule *sngl_every(int ms, SnglFn fn, void *ctx, GDestroyNotify release);
void sngl_schedule_cancel(SnglSchedule *s);

/* sngl_calendar_select_date selects day on w, a calendar, as
 * gtk_calendar_select_day does, which GTK 4.20 deprecates. */
void sngl_calendar_select_date(GtkWidget *w, GDateTime *day);

#endif
