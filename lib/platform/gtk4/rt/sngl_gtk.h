/* gtk4's runtime, for a program in either language: a widget's look, a
 * window's toplevel, the application they run in. A C program compiles it
 * beside its own; Go reaches it through lib/platform/gtk4/cgo. */
#ifndef SNGL_GTK_H
#define SNGL_GTK_H

#include <gtk/gtk.h>
#include <stdbool.h>

#include "sngl_http.h"

/* The function types a callback crosses as: the closure's dispatcher, which
 * finds it through ctx. */
typedef void (*SnglFn)(void *ctx);
typedef void (*SnglBoolFn)(int v, void *ctx);
typedef void (*SnglStrFn)(const char *s, void *ctx);
typedef void (*SnglDrawFn)(cairo_t *cr, void *data);

/* sngl_post runs fn from the main loop when it is next idle, then release
 * with its ctx: how work another thread finished reaches GTK, which owns its
 * widgets from the loop's thread. */
void sngl_post(SnglFn fn, void *ctx, GDestroyNotify release);

/* sngl_contents_new is the box an instance of a component holds its widgets
 * in, which the program never wrote: a child growing in it grows the box
 * too, in the box it sits in, as CSS's display: contents makes its children
 * that box's items (look.c). */
GtkWidget *sngl_contents_new(void);

/* sngl_set_look gives w what a gtk4 Look holds (look.c). */
void sngl_set_look(GtkWidget *w, const char *css, int width, int height, double grow,
                   int cross, int align, int lines, int wrap, int hidden, const char *cursor,
                   int justify, int spaced, int spacing, int grid, int columns);

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
 * anything in it (keys.c). While it runs, sngl_key_heard is the key's name
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

/* An image is a box holding the source it shows (image.c), a GtkPicture
 * showing a picture or what a drawing paints, which reports to the image
 * whether it arrived. A picture is what a URL names, decoded once and
 * counted by its holders; sngl_picture_paint paints one into a drawing. A
 * file a program carries is one of the table its entry hands sngl_gtk_carry,
 * ending at a NULL name, or one sngl_gtk_carry_file was handed, as a Go
 * program hands each it embeds. sngl_image_adopt and sngl_image_unadopt are a
 * window's append and remove of a source, its icon, and are whether they
 * were. */
typedef struct SnglPicture SnglPicture;
typedef struct {
    const char *name;
    const unsigned char *data;
    size_t len;
} SnglAsset;
extern const SnglAsset sngl_assets[];
void sngl_gtk_carry(const SnglAsset *assets);
void sngl_gtk_carry_file(const char *name, const char *data, int len);
GtkWidget *sngl_image_new(void);
void sngl_image_set_fit(GtkWidget *box, const char *fit);
void sngl_image_on_click(GtkWidget *box, SnglFn fn, void *ctx, GDestroyNotify release);
void sngl_image_on_load(GtkWidget *box, SnglFn fn, void *ctx, GDestroyNotify release);
void sngl_image_on_failed(GtkWidget *box, SnglFn fn, void *ctx, GDestroyNotify release);
const char *sngl_image_failure(GtkWidget *box);
void sngl_image_click(GtkWidget *box);
GtkWidget *sngl_image_source_new(void);
void sngl_image_source_set_url(GtkWidget *w, const char *url);
void sngl_image_source_set_picture(GtkWidget *w, SnglPicture *pic);
void sngl_drawing_set_width(GtkWidget *w, double width);
void sngl_drawing_set_height(GtkWidget *w, double height);
void sngl_drawing_on_paint(GtkWidget *w, SnglDrawFn draw, void *ctx, GDestroyNotify release);
void sngl_drawing_redraw(GtkWidget *w);
SnglPicture *sngl_picture_load(const char *url);
SnglPicture *sngl_picture_ref(SnglPicture *pic);
void sngl_picture_unref(SnglPicture *pic);
void sngl_picture_paint(cairo_t *cr, SnglPicture *pic, double x, double y, double w, double h);
bool sngl_image_adopt(GtkWidget *box, GtkWidget *child);
bool sngl_image_unadopt(GtkWidget *box, GtkWidget *child);
void sngl_toplevel_icon_refresh(GtkWidget *toplevel);

/* A canvas is a GtkDrawingArea whose drawing is the handler of its paint,
 * handed the context, in a space of width by height that the scaling mode
 * -- `draw.ScalingMode`'s ordinal -- maps onto the room it is given. A
 * redraw asks it to paint again. */
GtkWidget *sngl_canvas_new(void);
void sngl_canvas_set_width(GtkWidget *w, int width);
void sngl_canvas_set_height(GtkWidget *w, int height);
void sngl_canvas_set_scaling(GtkWidget *w, int mode);
void sngl_canvas_on_paint(GtkWidget *w, SnglDrawFn draw, void *data, GDestroyNotify release);
void sngl_canvas_redraw(GtkWidget *w);
void sngl_cairo_line_cap(cairo_t *cr, int cap);
void sngl_cairo_line_join(cairo_t *cr, int join);
void sngl_cairo_font(cairo_t *cr, const char *family, double size);
/* sngl_on_click runs fn with ctx on a press of w, which a drawing area or
 * a label has no signal of its own for: a GtkGestureClick's, which releases
 * ctx with the gesture, and a second call replaces the first's handler. */
void sngl_on_click(GtkWidget *w, SnglFn fn, void *ctx, GDestroyNotify release);
/* sngl_click is a press of w, as a test makes one. */
void sngl_click(GtkWidget *w);
GtkWidget *sngl_text_new(void);
void sngl_text_set_value(GtkWidget *w, const char *value);


/* A timer's schedule: fn runs with ctx every ms milliseconds, on the main
 * loop's thread, until sngl_schedule_cancel removes it, which releases ctx.
 * A period of 0 or less arms nothing and answers NULL, which cancels as a
 * no-op, as does a schedule never armed. */
typedef struct SnglSchedule SnglSchedule;
SnglSchedule *sngl_every(int ms, SnglFn fn, void *ctx, GDestroyNotify release);
void sngl_schedule_cancel(SnglSchedule *s);

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

/* sngl_calendar_select_date selects day on w, a calendar, as
 * gtk_calendar_select_day does, which GTK 4.20 deprecates (choose.c). */
void sngl_calendar_select_date(GtkWidget *w, GDateTime *day);

/* sngl_hold makes *slot w, which it reads NULL once w is gone: what a test
 * asks of a widget a branch took away. */
void sngl_hold(GtkWidget **slot, GtkWidget *w);

/* A node loops render is held by the key of its copy (ir.CopyKey), which a
 * test reaching one copy finds it by: sngl_copy_put holds w under key in
 * *copies, made on first use, and takes it from any key it held before;
 * sngl_copy_get is the widget key holds, or NULL; sngl_copy_drop takes w
 * from every key, as w's finalizing does. */
void sngl_copy_put(GHashTable **copies, const char *key, GtkWidget *w);
void sngl_copy_drop(GHashTable *copies, GtkWidget *w);
GtkWidget *sngl_copy_get(GHashTable *copies, const char *key);

/* sngl_flow_new is the label a flow of rich text is: wrapped, and starting
 * at the left as a paragraph does. Its spans' words are its markup. */
GtkWidget *sngl_flow_new(void);

/* sngl_editable_set_text_quiet writes an entry's text with its "changed"
 * blocked, which an override does when the program's value moves, so the
 * write does not report itself back as an edit. */
void sngl_editable_set_text_quiet(GtkWidget *e, const char *t);

/* sngl_app_run runs the application: build makes the program's tree, its
 * toplevels attached, once GTK is up, and the windows of the visible ones
 * are put on screen. The status is the application's. */
int sngl_app_run(void (*build)(void));


#endif
