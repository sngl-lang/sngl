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
 * last toplevel shown is closed; started with none shown, only the quit ends
 * it. */
void sngl_win32_run(void);
void sngl_win32_quit(void);

/* An @run handler's start: sngl_win32_hold keeps what the program attaches
 * hidden, its :visible told so, until sngl_win32_present shows it, which
 * the handler's run does before the loop. sngl_win32_refuse_run_again ends
 * the process where a handler calls run a second time. */
void sngl_win32_hold(void);
void sngl_win32_present(void);
void sngl_win32_refuse_run_again(const char *pos);

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
/* sngl_win32_tabs is a tab control, which shows a tab for each page
 * appended to it, titled with the page's text, and the page of the one
 * selected; a page, sngl_win32_tab_page, is a box. */
HWND sngl_win32_tabs(void);
HWND sngl_win32_tab_page(void);
/* sngl_win32_scroll is a box that scrolls what it holds along the axes
 * sngl_win32_set_scroll gives it, vertically where it is given none. */
HWND sngl_win32_scroll(void);
/* sngl_win32_modal is a node taking no room in its box, whose children are
 * shown in a window of their own over the window it is in, which is
 * disabled while the modal is open. */
HWND sngl_win32_modal(void);
/* sngl_win32_entry is a menu entry, its label its text: a node taking no
 * room, which the toplevel it is in shows in its menu bar, wherever in it
 * it is, and an entry holding entries as their submenu. Its click handler
 * is its command. sngl_win32_set_entry_* gray it or check it.
 * sngl_win32_menu_items is how many items the menu bar of the toplevel w
 * is in holds, and sngl_win32_choose chooses the entry labeled label in it
 * as the user does, answering whether there was one to choose. */
HWND sngl_win32_entry(void);

/* sngl_win32_image is an image: a node holding the picture it shows, which
 * fills it, painted as sngl_win32_set_fit says -- 0 contain, 1 cover, 2
 * fill, 3 none, 4 scaleDown -- and measured at the picture's size. A
 * picture, sngl_win32_picture, shows what sngl_win32_set_url names: a file
 * the program carries, one of sngl_win32_assets; one fetched, for an http or
 * https URL; or a file on disk. Its image hears that it loaded, or that it
 * failed and why (sngl_win32_image_failure), once the picture is in it. */
HWND sngl_win32_image(void);
HWND sngl_win32_picture(void);
void sngl_win32_set_fit(HWND w, int fit);

/* sngl_win32_canvas is a drawing: a window painted, each time Win32 asks,
 * by the function sngl_win32_on_paint connects, handed a drawing context
 * that is the client area mapped onto the width by height space
 * sngl_win32_set_canvas_width and sngl_win32_set_canvas_height give -- as
 * it is, centered (mode 0), scaled evenly to fit inside the area (1) or to
 * fill it (2), or stretched over it (3), as sngl_win32_set_scaling says. It
 * measures as that size, a pixel a SNGL px, and takes a share of its box's
 * spare room where it scales. sngl_win32_redraw asks it to paint again. */
typedef struct sngl_win32_gfx sngl_win32_gfx;
typedef void (*sngl_win32_paint_fn)(sngl_win32_gfx *g, void *ctx);
HWND sngl_win32_canvas(void);
void sngl_win32_set_canvas_width(HWND w, int width);
void sngl_win32_set_canvas_height(HWND w, int height);
void sngl_win32_set_scaling(HWND w, int mode);
void sngl_win32_on_paint(HWND w, sngl_win32_paint_fn fn, void *ctx, sngl_win32_release release);
void sngl_win32_redraw(HWND w);

/* sngl_win32_flow is a flow of rich text: a window that wraps and draws the
 * words of the spans it holds, made by sngl_win32_span and put in it, or in
 * a span in it, by sngl_win32_append. A span is a window never shown,
 * holding its own words (sngl_win32_set_words), the link it leads to
 * (sngl_win32_set_href) and what it sets of its appearance
 * (sngl_win32_set_span_style: a color as 0xRRGGBB or -1, a size in SNGL px or
 * 0, a weight -- 0 inherit, 1 normal, 2 bold, 3 lighter, 4 bolder -- a slant
 * -- 0 inherit, 1 normal, 2 italic, 3 oblique -- an underline, a line
 * through, and a family, "" inherit), each inherited by the spans it holds.
 * A flow's own span style is the one its spans start from. Every character
 * of a span's words is drawn as written, a newline ending the line; the
 * lines wrap between words at the flow's width, and a link is drawn as one
 * and opened as it is clicked. */
HWND sngl_win32_flow(void);
HWND sngl_win32_span(void);
void sngl_win32_set_words(HWND w, const unsigned short *words);
void sngl_win32_set_span_style(HWND w, int color, int size, int weight, int slant, int underline, int strike, const unsigned short *family);

/* A drawing context's calls are cairo's, over GDI+: a path built and then
 * filled or stroked, in the color, line and font last set, which save and
 * restore keep. Angles are radians, clockwise from the positive x axis;
 * colors are 0..255 a channel; a line's cap is 0 butt, 1 round or 2 square
 * and its join 0 miter, 1 round or 2 bevel. Text is drawn from the current
 * point, on its baseline, in a family named as CSS's generic ones are or as
 * the system knows one, sans-serif where it knows none. */
void sngl_win32_gfx_save(sngl_win32_gfx *g);
void sngl_win32_gfx_restore(sngl_win32_gfx *g);
void sngl_win32_gfx_color(sngl_win32_gfx *g, int r, int gr, int b, int a);
void sngl_win32_gfx_line_width(sngl_win32_gfx *g, double w);
void sngl_win32_gfx_line_cap(sngl_win32_gfx *g, int cap);
void sngl_win32_gfx_line_join(sngl_win32_gfx *g, int join);
void sngl_win32_gfx_rect(sngl_win32_gfx *g, double x, double y, double w, double h);
void sngl_win32_gfx_new_sub_path(sngl_win32_gfx *g);
void sngl_win32_gfx_move_to(sngl_win32_gfx *g, double x, double y);
void sngl_win32_gfx_line_to(sngl_win32_gfx *g, double x, double y);
void sngl_win32_gfx_curve_to(sngl_win32_gfx *g, double x1, double y1, double x2, double y2, double x3, double y3);
void sngl_win32_gfx_arc(sngl_win32_gfx *g, double cx, double cy, double r, double start, double end);
void sngl_win32_gfx_arc_negative(sngl_win32_gfx *g, double cx, double cy, double r, double start, double end);
void sngl_win32_gfx_close_path(sngl_win32_gfx *g);
void sngl_win32_gfx_fill(sngl_win32_gfx *g, int preserve);
void sngl_win32_gfx_stroke(sngl_win32_gfx *g);
void sngl_win32_gfx_font(sngl_win32_gfx *g, const unsigned short *family, double size);
void sngl_win32_gfx_text(sngl_win32_gfx *g, const unsigned short *text);
void sngl_win32_set_url(HWND w, const unsigned short *url);
const char *sngl_win32_image_failure(HWND w);
void sngl_win32_on_load(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release);
void sngl_win32_on_failed(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release);

/* The files a program carries, which its build defines, ending at a NULL
 * name: each by the path its URL names. */
typedef struct {
	const char *name;
	const unsigned char *data;
	size_t len;
} sngl_win32_asset;
extern const sngl_win32_asset sngl_win32_assets[];

/* sngl_win32_table is a list view of rows and columns. sngl_win32_fill_table
 * gives it its columns, one a line, and its rows, one a line of cells
 * separated by tabs, reporting nothing; sngl_win32_set_row selects a row,
 * or none where it is negative, also unreported, and sngl_win32_row is the
 * one selected. sngl_win32_set_table says whether a row can be selected,
 * which reports its change handler, and whether a column header's click
 * asks for the rows sorted by it, which reports its click handler, and
 * sngl_win32_sorted is the header last clicked. */
HWND sngl_win32_table(void);
void sngl_win32_fill_table(HWND w, const unsigned short *columns, const unsigned short *rows);
void sngl_win32_set_row(HWND w, int row);
int sngl_win32_row(HWND w);
void sngl_win32_set_table(HWND w, int selectable, int sortable);
const char *sngl_win32_sorted(HWND w);

/* sngl_win32_tree is a tree view. sngl_win32_fill_tree gives it its nodes,
 * one a line, each indented by a tab a level, expanding those whose places
 * in that order expanded lists, comma separated; sngl_win32_set_chosen
 * selects the node labeled label, and sngl_win32_chosen is the label of the
 * one selected. A selection the user makes reports the change handler, and
 * a node expanded or collapsed the activate handler. */
HWND sngl_win32_tree(void);
void sngl_win32_fill_tree(HWND w, const unsigned short *lines, const unsigned short *expanded);
void sngl_win32_set_chosen(HWND w, const unsigned short *label);
const char *sngl_win32_chosen(HWND w);

/* sngl_win32_date is a date picker, whose check box unchecked is no date.
 * Dates are "YYYY-MM-DD", or "" for none: sngl_win32_set_date shows one
 * unreported, sngl_win32_set_date_range bounds the ones it offers, and
 * sngl_win32_date_of is the one it shows. A date the user picks reports
 * the change handler. */
HWND sngl_win32_date(void);
void sngl_win32_set_date(HWND w, const unsigned short *iso);
void sngl_win32_set_date_range(HWND w, const unsigned short *min, const unsigned short *max);
const char *sngl_win32_date_of(HWND w);

/* A test's choice, as the user's, in the first table, tree or date picker
 * of the toplevel w is in: a row selected, a column header clicked, a node
 * selected by its label, a date picked. Each answers whether there was one
 * to choose. */
int sngl_win32_pick_row(HWND w, int row);
int sngl_win32_click_column(HWND w, int col);
int sngl_win32_pick_node(HWND w, const unsigned short *label);
int sngl_win32_pick_date(HWND w, const unsigned short *iso);
void sngl_win32_set_entry_disabled(HWND w, int disabled);
void sngl_win32_set_entry_checked(HWND w, int checked);
int sngl_win32_menu_items(HWND w);
int sngl_win32_choose(HWND w, const unsigned short *label);

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
/* sngl_win32_set_selected is the page a tabs shows. */
void sngl_win32_set_selected(HWND tabs, int selected);
void sngl_win32_set_scroll(HWND w, int vertical, int horizontal);
/* sngl_win32_set_tip is what a box's children show as the pointer rests
 * on them; empty, nothing. */
void sngl_win32_set_tip(HWND box, const unsigned short *tip);
/* sngl_win32_set_open shows a modal or hides it, and sngl_win32_is_open is
 * whether it is shown. A closable one's window has a close button, which
 * reports its closed handler and leaves it to the program to hide it. */
void sngl_win32_set_open(HWND m, int open);
int sngl_win32_is_open(HWND m);
void sngl_win32_set_closable(HWND m, int closable);
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
/* sngl_win32_on_reach_end calls fn when a scroll's end comes within half
 * a page of showing, as it is scrolled there or what it holds shrinks. */
void sngl_win32_on_reach_end(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release);
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
/* sngl_win32_fire_close is the window manager's close of a toplevel, or
 * of a modal's window. */
void sngl_win32_fire_close(HWND top);
/* sngl_win32_fire_tab is the tab at index chosen, and
 * sngl_win32_fire_reach_end a scroll's end arriving. */
void sngl_win32_fire_tab(HWND tabs, int index);
void sngl_win32_fire_reach_end(HWND w);

/* --- Timers --- */

/* sngl_win32_every calls fn every ms milliseconds from the loop until
 * sngl_win32_cancel is given the id it returns, which releases ctx. */
int sngl_win32_every(int ms, sngl_win32_fn fn, void *ctx, sngl_win32_release release);
void sngl_win32_cancel(int id);

#endif
