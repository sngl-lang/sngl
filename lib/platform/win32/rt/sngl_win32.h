/* sngl_win32.h is sngl:platform/win32's runtime: what a program on Win32
 * needs that is no single Win32 call. It keeps a record beside each control
 * (sngl_win32.c), holding the handlers the program connected and what the
 * layout reads, and lays a box's children out itself, since Win32 places a
 * child window where it is told and moves it for nothing.
 *
 * Every node is a window: a control, a box (a window of the runtime's own
 * class that lays out its children), or a toplevel. A node is made hidden,
 * parented to a window no one sees, and moved under its parent when it is
 * appended. Removing a node destroys it and everything in it.
 *
 * A handler is a function C calls with the context it was connected with,
 * which the runtime releases when the window it was connected to goes. */
#ifndef SNGL_WIN32_H
#define SNGL_WIN32_H

#include <windows.h>

typedef void (*sngl_win32_fn)(void *ctx);
typedef void (*sngl_win32_bool_fn)(int value, void *ctx);
typedef void (*sngl_win32_release)(void *ctx);

/* sngl_win32_init registers the runtime's window classes and makes the
 * hidden window every node starts under. Each sngl_win32_* below calls it
 * first, so a program need not. */
void sngl_win32_init(void);

/* sngl_win32_run runs the message loop until sngl_win32_quit, or until the
 * last toplevel shown is closed. */
void sngl_win32_run(void);
void sngl_win32_quit(void);

/* sngl_win32_pump handles every message waiting and returns, the test agent's
 * way of letting the program settle between steps; sngl_win32_step handles
 * those waiting, or waits up to a millisecond for one. */
void sngl_win32_pump(void);
void sngl_win32_step(void);

/* --- Nodes --- */

HWND sngl_win32_toplevel(void);
HWND sngl_win32_box(int vertical);
/* sngl_win32_set_vertical is a box's axis, or a divider's. */
void sngl_win32_set_vertical(HWND box, int vertical);
/* sngl_win32_set_mode is how a box lays its children out: 0 along its axis,
 * 1 in a grid of sngl_win32_set_columns columns, 2 in rows that wrap, 3
 * one over another, each the box's whole room, and 4 along its axis in
 * equal shares. */
void sngl_win32_set_mode(HWND box, int mode);
void sngl_win32_set_columns(HWND box, int columns);
/* sngl_win32_set_framed draws an etched edge inside a box's bounds. */
void sngl_win32_set_framed(HWND box, int framed);
HWND sngl_win32_text(void);
HWND sngl_win32_button(void);
HWND sngl_win32_edit(void);
HWND sngl_win32_checkbox(void);
HWND sngl_win32_spacer(void);
/* sngl_win32_progress is a progress bar, sngl_win32_textarea a multi-line
 * edit and sngl_win32_select a drop-down list. */
HWND sngl_win32_progress(void);
HWND sngl_win32_textarea(void);
HWND sngl_win32_select(void);
/* sngl_win32_divider is an etched line, sngl_win32_link a text that opens
 * its href as it is clicked, and sngl_win32_radio a radio button, which
 * unchecks the others in its box as it is checked. */
HWND sngl_win32_divider(void);
HWND sngl_win32_link(void);
HWND sngl_win32_radio(void);

/* sngl_win32_append makes child the last of parent's children, and
 * sngl_win32_insert puts it before before, which is one of them. Either lays
 * the window they are in out again. */
void sngl_win32_append(HWND parent, HWND child);
void sngl_win32_insert(HWND parent, HWND child, HWND before);

/* sngl_win32_remove destroys child, which is one of parent's children. */
void sngl_win32_remove(HWND parent, HWND child);

/* sngl_win32_attach shows a toplevel the program holds, or any other node
 * in a toplevel of its own, and sngl_win32_detach destroys the toplevel. */
void sngl_win32_attach(HWND top);
void sngl_win32_detach(HWND top);

/* --- Props --- */

/* sngl_win32_set_text sets a window's text -- a toplevel's title, a
 * control's caption, an edit's content -- and lays it out again. */
void sngl_win32_set_text(HWND w, const unsigned short *text);

/* sngl_win32_get_text is a window's text as UTF-8, in a buffer the next call
 * overwrites. */
const char *sngl_win32_get_text(HWND w);

/* sngl_win32_set_edit_text sets an edit's text without reporting a change,
 * and only where it differs, so the caret stays where it is. */
void sngl_win32_set_edit_text(HWND w, const unsigned short *text);

/* sngl_win32_set_rows is the lines a multi-line edit is measured to show, 0
 * for the runtime's three. */
void sngl_win32_set_rows(HWND w, int rows);

/* sngl_win32_set_fraction fills a progress bar to fraction, 0 to 1, and a
 * negative one makes it indeterminate. sngl_win32_set_show_text writes its
 * text over it, or how far along it is where it has none. */
void sngl_win32_set_fraction(HWND w, double fraction);
void sngl_win32_set_show_text(HWND w, int show);

/* sngl_win32_offer gives a drop-down the choices labels holds, one a line,
 * and selects the one labeled chosen, or none; it reports neither.
 * sngl_win32_selected is the label of its choice selected, or "", as UTF-8 in
 * a buffer the next call overwrites. */
void sngl_win32_offer(HWND w, const unsigned short *labels, const unsigned short *chosen);
const char *sngl_win32_selected(HWND w);

/* sngl_win32_set_shown hides a node, which then takes no room in its box,
 * or shows it again; sngl_win32_is_shown is whether it is shown. */
void sngl_win32_set_shown(HWND w, int shown);
int sngl_win32_is_shown(HWND w);
void sngl_win32_set_enabled(HWND w, int enabled);
/* sngl_win32_set_href is what a link opens. */
void sngl_win32_set_href(HWND w, const unsigned short *href);

void sngl_win32_set_checked(HWND w, int checked);
int sngl_win32_checked(HWND w);
void sngl_win32_set_visible(HWND top, int visible);
/* sngl_win32_shown is the toplevel shown last, while it is, or NULL. */
HWND sngl_win32_shown(void);

/* sngl_win32_capture draws the client area of the toplevel shown last into
 * *rgba, which the caller frees: *width by *height pixels, four bytes each,
 * red, green, blue and alpha, row by row from the top. Zero, with *why set,
 * where there is none to draw. A layout a change posted runs first. */
int sngl_win32_capture(unsigned char **rgba, int *width, int *height, const char **why);

/* sngl_win32_snapshot sizes the toplevel shown last to a client area of
 * width by height, where both are positive, and writes what it draws to
 * path: its width and height, four bytes each, little-endian, then its
 * pixels as sngl_win32_capture has them. `sngl snapshot`'s entry; zero where
 * it wrote them. */
int sngl_win32_snapshot(const char *path, int width, int height);

/* sngl_win32_look is what a node's style asks of the layout and the paint:
 * how much of a box's spare room it takes (flex), the gap a box leaves
 * between its children, the room inside its edges, its least size, and its
 * colors and font size, and whether its style's display hides it. A color
 * is 0xRRGGBB, or -1 for the system's; a size of 0 is the system's. Its
 * fields are named as sngl:platform/win32's Look, which crosses as one. */
typedef struct sngl_win32_look {
	double flex;
	int gap;
	int padTop, padRight, padBottom, padLeft;
	int minWidth, minHeight;
	int color, background;
	int fontPx;
	int hidden;
} sngl_win32_look;

void sngl_win32_set_look(HWND w, sngl_win32_look look);

/* --- Events --- */

/* sngl_win32_on_click calls fn when a button or a checkbox is pressed, and
 * sngl_win32_on_change when an edit's text changes, which a set does not
 * report. sngl_win32_on_closed calls fn when a toplevel's close is asked
 * for, after hiding it. */
void sngl_win32_on_click(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release);
void sngl_win32_on_change(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release);
/* sngl_win32_on_activate calls fn when Enter is pressed in an edit, or as a
 * multi-line edit loses the focus. A drop-down's change is a choice the user
 * selects. */
void sngl_win32_on_activate(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release);
void sngl_win32_on_closed(HWND top, sngl_win32_fn fn, void *ctx, sngl_win32_release release);
/* sngl_win32_on_visible calls fn with 0 when a toplevel the window manager
 * closes is hidden, ahead of its closed handler. */
void sngl_win32_on_visible(HWND top, sngl_win32_bool_fn fn, void *ctx, sngl_win32_release release);

/* --- A test's events ---
 *
 * Each puts what the test hands the event where Win32 keeps it and raises
 * the notification the control would, which reaches the handler the program
 * connected through the window the control is in, as the user's does. */

/* sngl_win32_fire_click is a button's press, or a check box's, which is
 * checked first. */
void sngl_win32_fire_click(HWND w);
void sngl_win32_fire_check(HWND w, int checked);
/* sngl_win32_fire_change is text typed into an edit, and
 * sngl_win32_fire_activate Enter pressed in it after. */
void sngl_win32_fire_change(HWND w, const unsigned short *text);
void sngl_win32_fire_activate(HWND w, const unsigned short *text);
/* sngl_win32_fire_select is the choice labeled label picked from a
 * drop-down. */
void sngl_win32_fire_select(HWND w, const unsigned short *label);
/* sngl_win32_fire_close is the window manager's close of a toplevel. */
void sngl_win32_fire_close(HWND top);

/* --- Timers --- */

/* sngl_win32_every calls fn every ms milliseconds from the loop until
 * sngl_win32_cancel is given the id it returns, which releases ctx. */
int sngl_win32_every(int ms, sngl_win32_fn fn, void *ctx, sngl_win32_release release);
void sngl_win32_cancel(int id);

#endif
