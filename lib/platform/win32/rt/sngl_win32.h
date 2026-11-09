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
 * way of letting the program settle between steps. */
void sngl_win32_pump(void);

/* --- Nodes --- */

HWND sngl_win32_toplevel(void);
HWND sngl_win32_box(int vertical);
void sngl_win32_set_vertical(HWND box, int vertical);
HWND sngl_win32_text(void);
HWND sngl_win32_button(void);
HWND sngl_win32_edit(void);
HWND sngl_win32_checkbox(void);
HWND sngl_win32_spacer(void);

/* sngl_win32_append makes child the last of parent's children, and
 * sngl_win32_insert puts it before before, which is one of them. Either lays
 * the window they are in out again. */
void sngl_win32_append(HWND parent, HWND child);
void sngl_win32_insert(HWND parent, HWND child, HWND before);

/* sngl_win32_remove destroys child, which is one of parent's children. */
void sngl_win32_remove(HWND parent, HWND child);

/* sngl_win32_attach shows a toplevel the program holds, and
 * sngl_win32_detach destroys one. */
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

void sngl_win32_set_checked(HWND w, int checked);
int sngl_win32_checked(HWND w);
void sngl_win32_set_visible(HWND top, int visible);

/* sngl_win32_look is what a node's style asks of the layout and the paint:
 * how much of a box's spare room it takes (flex), the gap a box leaves
 * between its children, the room inside its edges, its least size, and its
 * colors and font size. A color is 0xRRGGBB, or -1 for the system's; a size
 * of 0 is the system's. Its fields are named as sngl:platform/win32's Look,
 * which crosses as one. */
typedef struct sngl_win32_look {
	double flex;
	int gap;
	int padTop, padRight, padBottom, padLeft;
	int minWidth, minHeight;
	int color, background;
	int fontPx;
} sngl_win32_look;

void sngl_win32_set_look(HWND w, sngl_win32_look look);

/* --- Events --- */

/* sngl_win32_on_click calls fn when a button or a checkbox is pressed, and
 * sngl_win32_on_change when an edit's text changes, which a set does not
 * report. sngl_win32_on_closed calls fn when a toplevel's close is asked
 * for, after hiding it. */
void sngl_win32_on_click(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release);
void sngl_win32_on_change(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release);
/* sngl_win32_on_activate calls fn when Enter is pressed in an edit. */
void sngl_win32_on_activate(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release);
void sngl_win32_on_closed(HWND top, sngl_win32_fn fn, void *ctx, sngl_win32_release release);
/* sngl_win32_on_visible calls fn with 0 when a toplevel the window manager
 * closes is hidden, ahead of its closed handler. */
void sngl_win32_on_visible(HWND top, sngl_win32_bool_fn fn, void *ctx, sngl_win32_release release);

/* sngl_win32_click presses a button or a checkbox as a user does, through
 * its own BM_CLICK: the test agent's trigger. */
void sngl_win32_click(HWND w);

#endif
