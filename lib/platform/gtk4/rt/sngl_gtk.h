/* gtk4's runtime for a program written in C: what pkg/go/gtk4rt does for Go
 * that a cgo build still calls -- a widget's look, a window's toplevel, the
 * application they run in. Compiled beside the program from sngl_gtk.c. */
#ifndef SNGL_GTK_H
#define SNGL_GTK_H

#include <gtk/gtk.h>
#include <stdbool.h>

/* The function types a callback crosses as: the closure's dispatcher, which
 * finds it through ctx. */
typedef void (*SnglFn)(void *ctx);
typedef void (*SnglBoolFn)(bool v, void *ctx);

/* sngl_set_look gives w what a gtk4rt.Look holds (look.c). */
void sngl_set_look(GtkWidget *w, const char *css, int width, int height, double grow,
                   int cross, int align, int lines, int wrap, int hidden, const char *cursor,
                   int justify, int spaced, int spacing, int grid, int columns);

/* A toplevel is a ui.window: a box its content is appended to, which the
 * application shows in a window of its own once it is attached and the
 * application runs. */
GtkWidget *sngl_toplevel_new(void);
void sngl_toplevel_set_title(GtkWidget *box, const char *title);
void sngl_toplevel_set_visible(GtkWidget *box, bool visible);
void sngl_toplevel_on_closed(GtkWidget *box, SnglFn fn, void *ctx, GDestroyNotify release);
void sngl_toplevel_on_visible(GtkWidget *box, SnglBoolFn fn, void *ctx, GDestroyNotify release);
void sngl_toplevel_append(GtkWidget *box, GtkWidget *child);

/* A window's `@key`: fn hears each key pressed in the window before
 * anything in it (keys.c), with the stop that keeps the key from going
 * further, which it calls with stop_ctx. sngl_toplevel_key presses a key as
 * a test describes it: fn hears it, stop is called with stop_ctx by the
 * handler's stop and released after, and a key the handler leaves goes on
 * where it would have gone. */
typedef void (*SnglKeyFn)(const char *key, bool ctrl, bool alt, bool shift, bool meta, bool editing,
                          SnglFn stop, void *stop_ctx, void *ctx);
void sngl_toplevel_on_key(GtkWidget *box, SnglKeyFn fn, void *ctx, GDestroyNotify release);
void sngl_toplevel_key(GtkWidget *box, const char *key, bool ctrl, bool alt, bool shift, bool meta, bool editing,
                       SnglFn stop, void *stop_ctx, GDestroyNotify stop_release);
void sngl_app_attach(GtkWidget *box);

void sngl_app_detach(GtkWidget *box);

/* SNGL_APP is the application as a box: a slot at the package's root
 * renders its windows into it, which the box calls below attach and
 * detach, in no order a window shows. */
#define SNGL_APP ((GtkWidget *)NULL)

/* A slot's children sit in its parent box before its anchor, a hidden
 * widget sngl_slot_anchor makes there once and answers again while it is
 * still the box's: what keeps a slot's children in their place among the
 * box's others as it renders again. */
GtkWidget *sngl_slot_anchor(GtkWidget *box, GtkWidget *anchor);
void sngl_box_insert_before(GtkWidget *box, GtkWidget *anchor, GtkWidget *child);
void sngl_box_remove(GtkWidget *box, GtkWidget *child);

/* A canvas is a GtkDrawingArea of w by h, whose drawing draw paints with
 * data. Where the area GTK lays it out in is another size, mode says what the
 * drawing's coordinates are scaled by: "stretch" each axis to fit, "fill"
 * the larger scale on both, and else the smaller, which "" leaves alone. A
 * canvas that scales other than "center" expands to take the room. */
typedef void (*SnglDrawFn)(cairo_t *cr, void *data);
GtkWidget *sngl_canvas_new(int w, int h, const char *mode, SnglDrawFn draw, void *data);
/* sngl_canvas_on_click runs fn with ctx on a press of w, which a drawing
 * area has no signal of its own for: a GtkGestureClick's, which releases
 * ctx with the gesture. */
void sngl_canvas_on_click(GtkWidget *w, SnglFn fn, void *ctx, GDestroyNotify release);
/* sngl_canvas_click is a press of w, as a test makes one. */
void sngl_canvas_click(GtkWidget *w);

/* A timer's schedule: fn runs with ctx every ms milliseconds, on the main
 * loop's thread, until sngl_schedule_cancel removes it, which releases ctx.
 * A period of 0 or less arms nothing and answers NULL, which cancels as a
 * no-op, as does a schedule never armed. */
typedef struct SnglSchedule SnglSchedule;
SnglSchedule *sngl_every(int ms, SnglFn fn, void *ctx, GDestroyNotify release);
void sngl_schedule_cancel(SnglSchedule *s);

/* A scroll is a GtkScrolledWindow moving along a direction, "horizontal",
 * "both" or else vertical, that runs its reach-end handler as the end of
 * its content comes within half a viewport of showing (scroll.c). Its
 * content is set through sngl_scroll_set_child, which leaves content it
 * already holds where it is. sngl_scroll_reach_end runs the handler, as a
 * test does. */
GtkWidget *sngl_scroll_new(void);
void sngl_scroll_set_child(GtkWidget *sw, GtkWidget *child);
void sngl_scroll_set_direction(GtkWidget *sw, const char *dir);
void sngl_scroll_on_reach_end(GtkWidget *sw, SnglFn fn, void *ctx, GDestroyNotify release);
void sngl_scroll_reach_end(GtkWidget *sw);

/* A window's keys (keys.c). sngl_key_name is what a window's `@key` calls
 * the key keyval is with state held -- a name, or the character it types,
 * written into buf -- or NULL for one it does not report: a modifier on its
 * own, a control character, a key no browser names. With a shortcut's
 * modifier held a letter is the key's own, lowercase unless Shift is held.
 *
 * sngl_keys_watch gives win the controllers that hear its keys: hear, with
 * data, on each key pressed in it before anything in it, which is whether
 * the handler stopped the key; and back on an Escape nothing took, which is
 * whether it went back. back may be NULL.
 *
 * sngl_keys_go_on is a key nothing stopped going where it would have gone,
 * as a keyboard's would, for a test pressing it by name: typed into a
 * focused field, pressing a focused button, or moving focus. The caller
 * answers Escape, and a key held with a shortcut's modifier. */
typedef bool (*SnglKeyHear)(const char *key, bool ctrl, bool alt, bool shift, bool meta, bool editing, void *data);
typedef bool (*SnglKeyBack)(void *data);
const char *sngl_key_name(guint keyval, GdkModifierType state, char buf[8]);
void sngl_keys_watch(GtkWidget *win, SnglKeyHear hear, SnglKeyBack back, void *data);
void sngl_keys_go_on(GtkWindow *win, const char *key, bool shift);

/* sngl_editable_set_text_quiet writes an entry's text with its "changed"
 * blocked, which an override does when the program's value moves, so the
 * write does not report itself back as an edit. */
void sngl_editable_set_text_quiet(GtkWidget *e, const char *t);

/* sngl_app_run runs the application: build makes the program's tree, its
 * toplevels attached, once GTK is up, and the windows of the visible ones
 * are put on screen. The status is the application's. */
int sngl_app_run(void (*build)(void));

#endif
