/* sngl_win32.c is sngl:platform/win32's runtime (sngl_win32.h). */
#include "sngl_win32.h"

#define COBJMACROS
#include <commctrl.h>
#include <urlmon.h>
#include <wincodec.h>
#include <gdiplus.h>
#include <math.h>
#include <shellapi.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define SNGL_BOX_CLASS L"SnglBox"
#define SNGL_TOP_CLASS L"SnglToplevel"
/* A toplevel's layout, asked for by each change in it and run once. */
#define SNGL_WM_LAYOUT (WM_APP + 1)
/* Work sngl_post queued, its handler the message's lParam. */
#define SNGL_WM_POST (WM_APP + 2)
/* A scroll's end arriving, reported once its layout is done. */
#define SNGL_WM_REACHED (WM_APP + 3)
/* A modal's open applied, once it is in the window it is shown over. */
#define SNGL_WM_OPEN (WM_APP + 4)
/* A picture's load reported to its image, once it is in one; and a fetch
 * arrived, the file it was saved to the message's lParam, or 0. */
#define SNGL_WM_LOADED (WM_APP + 5)
#define SNGL_WM_FETCHED (WM_APP + 6)

enum sngl_kind { K_TOP, K_BOX, K_TEXT, K_BUTTON, K_EDIT, K_CHECK, K_SPACER, K_PROGRESS, K_AREA, K_SELECT, K_DIVIDER, K_LINK, K_RADIO, K_TABS, K_SCROLL, K_MODAL, K_ENTRY, K_TABLE, K_TREE, K_DATE, K_IMAGE, K_PICTURE, K_CANVAS, K_FLOW, K_SPAN };

typedef struct {
	sngl_win32_fn fn;
	void *ctx;
	sngl_win32_release release;
} handler;

/* link_at is where a flow drew a link's words, and the span leading to it. */
struct link_at {
	RECT r;
	HWND span;
};

/* ctl is what the runtime keeps beside each node's window beyond its record,
 * which is sngl:platform/win32's (_Ctl): what C draws and reports it by. */
typedef struct {
	enum sngl_kind kind;
	int color, background, font_px;
	/* framed is a box drawn with an etched edge inside its bounds. */
	int framed;
	/* dpi is what its font is made for, the record's as its layout last
	 * refreshed it (sngl_win32_refont). */
	int dpi;
	HFONT font;   /* the node's own, for a font_px; else the message font */
	HBRUSH brush; /* its background's, for a background */
	handler click, change, activate, closed;
	/* visible is a toplevel's report of :visible, a sngl_win32_bool_fn. */
	handler visible;
	/* quiet is set while the runtime writes an edit's text, which Win32
	 * reports as a change the program did not make. */
	int quiet;
	/* rows is the lines a multi-line edit is measured to show, and fraction
	 * a progress bar's, negative while it is indeterminate, which show_text
	 * writes over it. */
	int rows;
	double fraction;
	int show_text;
	/* href is a link's, opened as the user clicks it. */
	WCHAR *href;
	/* tip is what a box's children show as the pointer rests on them. */
	WCHAR *tip;
	/* row is a table's row selected. */
	int row;
	handler reach;
	/* A modal is a node holding no room in its box, and dialog the window
	 * it shows what it holds in while open, over the window it is in,
	 * which modal_of names back. */
	HWND dialog, modal_of;
	int open, closable;
	/* A table's: whether a row can be selected and a column sorted by, and
	 * the column a header click last asked for. A tree's label selected. */
	int selectable, sortable, sort_col;
	WCHAR *chosen;
	/* An image's fit (sngl_win32_set_fit), its handlers, and why its
	 * picture failed; a picture's bitmap, its size, and what it shows,
	 * and the bitmap scaled to the size it was last painted at. */
	int fit;
	handler load, failed;
	char *why;
	HBITMAP bmp, scaled;
	int bw, bh, sw, sh;
	WCHAR *url;
	/* A canvas's drawing space, how it is mapped onto the canvas, and what
	 * paints it, a sngl_win32_paint_fn. */
	int cw, ch, scaling;
	handler paint;
	/* A drawing's size (sngl_win32_drawing), and each picture a URL names
	 * that a drawing painted, decoded once (sngl_win32_gfx_image). */
	double dw, dh;
	struct url_picture *pictures;
	int npictures;
	/* A span's words, and the appearance a span or a flow sets of its
	 * spans: a color or -1, a size or 0, a weight and a slant or 0 to
	 * inherit, an underline, a line through, and a family or NULL. A
	 * flow's links, where it last drew them. */
	WCHAR *words, *family;
	int sp_color, sp_px, sp_weight, sp_slant, sp_under, sp_strike;
	struct link_at *links;
	int nlinks;
	HWND self;
} ctl;

struct url_picture {
	WCHAR *url;
	HBITMAP bmp;
	int w, h;
};

static int ready;
/* firing is set while a test's event is raised. */
static int firing;
static HWND park;
/* The message font at each DPI a window has been shown at. */
static struct message_font {
	int dpi;
	HFONT font;
} *message_fonts;
static int nmessage_fonts, capmessage_fonts;
static int system_dpi = 96;
/* What Windows 10 1607 added for a per-monitor DPI, found at run time so the
 * program still starts where it is missing, as system-aware. */
static UINT(WINAPI *dpi_for_window)(HWND);
static BOOL(WINAPI *metrics_for_dpi)(UINT, UINT, PVOID, UINT, UINT);
static int(WINAPI *system_metric_for_dpi)(int, UINT);
static int shown; /* toplevels shown and not closed */
/* holding is set while an @run handler has not called run: what the program
 * attaches waits in held, hidden, until run presents it. */
static int holding;
static HWND *held;
static int nheld;
static HWND shown_last; /* the toplevel shown last */

/* Every window's record, by its handle: a window property is a trip to
 * Wine's server each time it is read, and a layout reads thousands. Open
 * addressing, a power of two slots, at most half of them full. */
static struct ctl_slot {
	HWND w;
	ctl *c;
} *ctls;
static size_t nctls, capctls;

static size_t ctl_hash(HWND w) { return (size_t)(((uintptr_t)w * 0x9E3779B97F4A7C15ull) >> 16) & (capctls - 1); }

static ctl *ctl_of(HWND w) {
	if (!w || !capctls) {
		return NULL;
	}
	for (size_t i = ctl_hash(w); ctls[i].w; i = (i + 1) & (capctls - 1)) {
		if (ctls[i].w == w) {
			return ctls[i].c;
		}
	}
	return NULL;
}

static void ctl_put(HWND w, ctl *c) {
	if ((nctls + 1) * 2 > capctls) {
		struct ctl_slot *old = ctls;
		size_t was = capctls;
		capctls = capctls ? capctls * 2 : 256;
		ctls = calloc(capctls, sizeof *ctls);
		nctls = 0;
		for (size_t i = 0; i < was; i++) {
			if (old[i].w) {
				ctl_put(old[i].w, old[i].c);
			}
		}
		free(old);
	}
	size_t i = ctl_hash(w);
	while (ctls[i].w && ctls[i].w != w) {
		i = (i + 1) & (capctls - 1);
	}
	nctls += ctls[i].w == NULL;
	ctls[i].w = w;
	ctls[i].c = c;
}

/* ctl_drop takes w's record out, moving each record after it that probed
 * past its slot back into the gap, so no lookup stops short at it. */
static void ctl_drop(HWND w) {
	if (!w || !capctls) {
		return;
	}
	size_t mask = capctls - 1, i = ctl_hash(w);
	while (ctls[i].w != w) {
		if (!ctls[i].w) {
			return;
		}
		i = (i + 1) & mask;
	}
	for (size_t j = (i + 1) & mask; ctls[j].w; j = (j + 1) & mask) {
		size_t home = ctl_hash(ctls[j].w);
		if (((j - home) & mask) >= ((j - i) & mask)) {
			ctls[i] = ctls[j];
			i = j;
		}
	}
	ctls[i].w = NULL;
	ctls[i].c = NULL;
	nctls--;
}

/* What sngl:platform/win32 hands the runtime as its package starts: each
 * window's record and the layout reading it are the SNGL's (_Ctl, _ctls),
 * and what C still reads of them it reads through these. Before they are
 * handed over, no window has a record. */
static struct {
	sngl_win32_hwnd_int2_hook made;
	sngl_win32_hwnd_hook forget, relayout, lay_out_top, layout, tab_chosen;
	sngl_win32_hwnd_int_hook command, refresh, wheel;
	sngl_win32_hwnd_int2_hook scrolled;
	sngl_win32_size_hook measure;
	sngl_win32_count_hook nkids;
	sngl_win32_kid_hook kid;
	sngl_win32_ask_hook pad;
} hooks;

void sngl_win32_hooks(sngl_win32_hwnd_int2_hook made, sngl_win32_hwnd_hook forget, sngl_win32_hwnd_hook relayout, sngl_win32_hwnd_hook lay_out_top, sngl_win32_hwnd_hook layout, sngl_win32_hwnd_hook tab_chosen, sngl_win32_hwnd_int_hook command, sngl_win32_hwnd_int_hook refresh, sngl_win32_hwnd_int_hook wheel, sngl_win32_hwnd_int2_hook scrolled, sngl_win32_size_hook measure, sngl_win32_count_hook nkids, sngl_win32_kid_hook kid, sngl_win32_ask_hook pad) {
	hooks.made = made;
	hooks.forget = forget;
	hooks.relayout = relayout;
	hooks.lay_out_top = lay_out_top;
	hooks.layout = layout;
	hooks.tab_chosen = tab_chosen;
	hooks.command = command;
	hooks.refresh = refresh;
	hooks.wheel = wheel;
	hooks.scrolled = scrolled;
	hooks.measure = measure;
	hooks.nkids = nkids;
	hooks.kid = kid;
	hooks.pad = pad;
}

/* relayout asks for the layout of the toplevel w is in (_relayout). */
static void relayout(HWND w) {
	if (hooks.relayout) {
		hooks.relayout(w);
	}
}

/* nkids is how many children w's record holds, and kid the one at i. */
static int nkids(HWND w) { return hooks.nkids ? hooks.nkids(w) : 0; }

static HWND kid(HWND w, int i) { return hooks.kid(w, i); }

/* pad is one side of w's padding in SNGL px: 0 top, 1 right, 2 bottom, 3
 * left. */
static int pad(HWND w, int side) { return hooks.pad ? hooks.pad(w, side) : 0; }

static SIZE measure(HWND w) {
	SIZE s = {0, 0};
	int cx = 0, cy = 0;
	if (hooks.measure) {
		hooks.measure(w, &cx, &cy);
		s.cx = cx;
		s.cy = cy;
	}
	return s;
}

static void lay_out_top(HWND top) {
	if (hooks.lay_out_top) {
		hooks.lay_out_top(top);
	}
}

static void refresh(HWND w, int dpi) {
	if (hooks.refresh) {
		hooks.refresh(w, dpi);
	}
}

static int dpi_of(HWND w) {
	if (dpi_for_window && w) {
		UINT d = dpi_for_window(w);
		if (d) {
			return (int)d;
		}
	}
	return system_dpi;
}

/* px is n SNGL px at dpi. */
static int px(int n, int dpi) { return MulDiv(n, dpi, 96); }

/* message_font is the font a control draws in where it is given none of its
 * own, the system's message font at dpi. */
static HFONT message_font(int dpi) {
	for (int i = 0; i < nmessage_fonts; i++) {
		if (message_fonts[i].dpi == dpi) {
			return message_fonts[i].font;
		}
	}
	NONCLIENTMETRICSW ncm;
	memset(&ncm, 0, sizeof ncm);
	ncm.cbSize = sizeof ncm;
	HFONT f = NULL;
	if (metrics_for_dpi && metrics_for_dpi(SPI_GETNONCLIENTMETRICS, sizeof ncm, &ncm, 0, (UINT)dpi)) {
		f = CreateFontIndirectW(&ncm.lfMessageFont);
	} else if (SystemParametersInfoW(SPI_GETNONCLIENTMETRICS, sizeof ncm, &ncm, 0)) {
		ncm.lfMessageFont.lfHeight = MulDiv(ncm.lfMessageFont.lfHeight, dpi, system_dpi);
		f = CreateFontIndirectW(&ncm.lfMessageFont);
	}
	if (!f) {
		f = (HFONT)GetStockObject(DEFAULT_GUI_FONT);
	}
	/* Kept for as long as the program runs, since a control may be
	 * drawing with it: a DPI is met once. */
	if (nmessage_fonts == capmessage_fonts) {
		capmessage_fonts = capmessage_fonts ? capmessage_fonts * 2 : 4;
		message_fonts = realloc(message_fonts, sizeof *message_fonts * (size_t)capmessage_fonts);
	}
	message_fonts[nmessage_fonts].dpi = dpi;
	message_fonts[nmessage_fonts].font = f;
	nmessage_fonts++;
	return f;
}

static HFONT font_of(ctl *c) {
	if (c && c->font) {
		return c->font;
	}
	return message_font(c ? c->dpi : system_dpi);
}

/* own_font makes c's own font, for its font_px at its dpi, or none. */
static void own_font(ctl *c) {
	if (c->font) {
		DeleteObject(c->font);
		c->font = NULL;
	}
	if (c->font_px > 0 || c->kind == K_LINK) {
		/* A link is underlined, in its own size or the system's. */
		LOGFONTW lf;
		GetObjectW(message_font(c->dpi), sizeof lf, &lf);
		if (c->font_px > 0) {
			lf.lfHeight = -px(c->font_px, c->dpi);
		}
		lf.lfUnderline = c->kind == K_LINK;
		c->font = CreateFontIndirectW(&lf);
	}
}

static void fire(handler *h) {
	if (h->fn) {
		h->fn(h->ctx);
	}
}

/* press fires w's click and then each of the boxes' holding it, as a
 * browser bubbles one: every override connects a node's @click whether the
 * program wrote one or not, so a text or a picture in a clickable card holds
 * an empty one. Each is found before any fires, and fired only while its
 * window stands, since a handler may close the page it is on. */
static void press(HWND w) {
	HWND chain[64];
	int n = 0;
	for (HWND p = w; p && n < 64; p = GetParent(p)) {
		ctl *c = ctl_of(p);
		if (!c || c->kind == K_TOP) {
			break;
		}
		chain[n++] = p;
	}
	for (int i = 0; i < n; i++) {
		ctl *c = IsWindow(chain[i]) ? ctl_of(chain[i]) : NULL;
		if (c) {
			fire(&c->click);
		}
	}
}

static void drop_handler(handler *h) {
	if (h->release) {
		h->release(h->ctx);
	}
	memset(h, 0, sizeof *h);
}

static void set_handler(handler *h, sngl_win32_fn fn, void *ctx, sngl_win32_release release) {
	drop_handler(h);
	h->fn = fn;
	h->ctx = ctx;
	h->release = release;
}

static int step(MSG *m);

/* forget frees what w's record holds, as w goes. */
static void forget(HWND w) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	ctl_drop(w);
	if (hooks.forget) {
		hooks.forget(w);
	}
	drop_handler(&c->click);
	drop_handler(&c->change);
	drop_handler(&c->activate);
	drop_handler(&c->closed);
	drop_handler(&c->visible);
	drop_handler(&c->reach);
	drop_handler(&c->load);
	drop_handler(&c->failed);
	drop_handler(&c->paint);
	free(c->words);
	free(c->family);
	free(c->links);
	if (c->bmp) {
		DeleteObject(c->bmp);
	}
	if (c->scaled) {
		DeleteObject(c->scaled);
	}
	free(c->why);
	free(c->url);
	for (int i = 0; i < c->npictures; i++) {
		free(c->pictures[i].url);
		if (c->pictures[i].bmp) {
			DeleteObject(c->pictures[i].bmp);
		}
	}
	free(c->pictures);
	if (c->dialog) {
		HWND owner = GetWindow(c->dialog, GW_OWNER);
		if (c->open && owner) {
			EnableWindow(owner, TRUE);
		}
		DestroyWindow(c->dialog);
	}
	if (c->font) {
		DeleteObject(c->font);
	}
	if (c->brush) {
		DeleteObject(c->brush);
	}
	free(c->href);
	free(c->tip);
	free(c->chosen);
	free(c);
}

/* --- Layout --- */

static const char *utf8(const WCHAR *wide);
static void paint_button(DRAWITEMSTRUCT *di, ctl *c);
static void touched(HWND w);

/* text_size is w's text as its font draws it. */
static SIZE text_size(HWND w) {
	SIZE s = {0, 0};
	int n = GetWindowTextLengthW(w);
	WCHAR stack[256];
	WCHAR *buf = n < 256 ? stack : malloc(sizeof(WCHAR) * (size_t)(n + 1));
	GetWindowTextW(w, buf, n + 1);
	HDC dc = GetDC(w);
	HGDIOBJ old = SelectObject(dc, font_of(ctl_of(w)));
	if (n == 0) {
		TEXTMETRICW tm;
		GetTextMetricsW(dc, &tm);
		s.cy = tm.tmHeight;
	} else {
		RECT r = {0, 0, 0, 0};
		DrawTextW(dc, buf, n, &r, DT_CALCRECT | DT_NOPREFIX);
		s.cx = r.right - r.left;
		s.cy = r.bottom - r.top;
	}
	SelectObject(dc, old);
	ReleaseDC(w, dc);
	if (buf != stack) {
		free(buf);
	}
	return s;
}

/* bar_text is what a progress bar shows over itself where it shows any: its
 * text, or how far along it is. */
static void bar_text(HWND w, ctl *c, WCHAR *buf, int n) {
	buf[0] = 0;
	GetWindowTextW(w, buf, n);
	if (buf[0] == 0 && c->fraction >= 0) {
		swprintf(buf, (size_t)n, L"%d%%", (int)(c->fraction * 100 + 0.5));
	}
}

/* line_height is a line of w's font. */
static int line_height(HWND w) {
	HDC dc = GetDC(w);
	HGDIOBJ old = SelectObject(dc, font_of(ctl_of(w)));
	TEXTMETRICW tm;
	GetTextMetricsW(dc, &tm);
	SelectObject(dc, old);
	ReleaseDC(w, dc);
	return tm.tmHeight;
}

/* select_size is a drop-down's: its longest choice beside its arrow. */
static SIZE select_size(HWND w, ctl *c) {
	SIZE s = {0, line_height(w)};
	int n = (int)SendMessageW(w, CB_GETCOUNT, 0, 0);
	HDC dc = GetDC(w);
	HGDIOBJ old = SelectObject(dc, font_of(c));
	for (int i = 0; i < n; i++) {
		int len = (int)SendMessageW(w, CB_GETLBTEXTLEN, (WPARAM)i, 0);
		if (len <= 0) {
			continue;
		}
		WCHAR *buf = malloc(sizeof(WCHAR) * (size_t)(len + 1));
		SendMessageW(w, CB_GETLBTEXT, (WPARAM)i, (LPARAM)buf);
		RECT r = {0, 0, 0, 0};
		DrawTextW(dc, buf, len, &r, DT_CALCRECT | DT_NOPREFIX | DT_SINGLELINE);
		free(buf);
		if (r.right - r.left > s.cx) {
			s.cx = r.right - r.left;
		}
	}
	SelectObject(dc, old);
	ReleaseDC(w, dc);
	s.cx += GetSystemMetrics(SM_CXVSCROLL) + px(16, c->dpi);
	if (s.cx < px(100, c->dpi)) {
		s.cx = px(100, c->dpi);
	}
	s.cy += px(10, c->dpi);
	return s;
}

/* inherited is the nearest background among w and the boxes holding it,
 * as a node with none of its own shows its box's in a browser, or NULL
 * where none of them has one. */
static ctl *inherited(HWND w) {
	for (HWND p = w; p; p = GetParent(p)) {
		ctl *c = ctl_of(p);
		if (!c) {
			break;
		}
		if (c->brush) {
			return c;
		}
		if (c->kind == K_TOP) {
			break;
		}
	}
	return NULL;
}

/* backdrop is the brush w shows where it draws nothing: the inherited
 * background, or the class's. */
static HBRUSH backdrop(HWND w) {
	ctl *c = inherited(w);
	return c ? c->brush : (HBRUSH)GetClassLongPtrW(w, GCLP_HBRBACKGROUND);
}

/* tree_size is a tree's: its widest row shown, as deep as it is, and every
 * row shown, unless its style gives it a height. */
static SIZE tree_size(HWND w, ctl *c) {
	SIZE s = {0, 0};
	int rows = 0, indent = (int)SendMessageW(w, TVM_GETINDENT, 0, 0);
	HDC dc = GetDC(w);
	HGDIOBJ old = SelectObject(dc, font_of(c));
	for (HTREEITEM it = (HTREEITEM)SendMessageW(w, TVM_GETNEXTITEM, TVGN_ROOT, 0); it; it = (HTREEITEM)SendMessageW(w, TVM_GETNEXTITEM, TVGN_NEXTVISIBLE, (LPARAM)it)) {
		WCHAR buf[256];
		TVITEMW ti;
		memset(&ti, 0, sizeof ti);
		ti.mask = TVIF_TEXT;
		ti.hItem = it;
		ti.pszText = buf;
		ti.cchTextMax = 256;
		SendMessageW(w, TVM_GETITEMW, 0, (LPARAM)&ti);
		int depth = 0;
		for (HTREEITEM p = (HTREEITEM)SendMessageW(w, TVM_GETNEXTITEM, TVGN_PARENT, (LPARAM)it); p; p = (HTREEITEM)SendMessageW(w, TVM_GETNEXTITEM, TVGN_PARENT, (LPARAM)p)) {
			depth++;
		}
		RECT r = {0, 0, 0, 0};
		DrawTextW(dc, buf, -1, &r, DT_CALCRECT | DT_NOPREFIX | DT_SINGLELINE);
		int wide = r.right + indent * (depth + 1) + px(24, c->dpi);
		s.cx = wide > s.cx ? wide : s.cx;
		rows++;
	}
	SelectObject(dc, old);
	ReleaseDC(w, dc);
	s.cy = rows * (int)SendMessageW(w, TVM_GETITEMHEIGHT, 0, 0) + px(4, c->dpi);
	if (s.cx < px(120, c->dpi)) {
		s.cx = px(120, c->dpi);
	}
	return s;
}

/* sngl_win32_tabs_size is a tabs' whose largest page is cx by cy: that,
 * and the strip of tabs over it. */
void sngl_win32_tabs_size(HWND w, int cx, int cy, SIZE *out) {
	ctl *c = ctl_of(w);
	RECT r = {0, 0, cx, cy};
	SendMessageW(w, TCM_ADJUSTRECT, TRUE, (LPARAM)&r);
	SIZE s = {r.right - r.left, r.bottom - r.top};
	int n = (int)SendMessageW(w, TCM_GETITEMCOUNT, 0, 0);
	RECT last;
	if (c && n > 0 && SendMessageW(w, TCM_GETITEMRECT, (WPARAM)(n - 1), (LPARAM)&last) && last.right + px(4, c->dpi) > s.cx) {
		s.cx = last.right + px(4, c->dpi);
	}
	*out = s;
}

/* --- Rich text --- */

/* span_look is what a run of a flow is drawn in, as the spans around it
 * set it: each field as sngl_win32_set_span_style takes it, and the span
 * leading where its link does, if one does. */
typedef struct {
	int color, px, weight, slant, under, strike;
	const WCHAR *family;
	HWND link;
} span_look;

/* piece is a word, a run of spaces or a newline of a flow, where it is
 * drawn: the font it is drawn in, its color, its width, and its place on
 * its line. A run of spaces a wrap left at the start of a line is skipped. */
typedef struct {
	const WCHAR *s;
	int len, kind, font, color, w, x, line, skip;
	HWND link;
} piece;

enum { P_WORD, P_SPACE, P_NEWLINE };

typedef struct {
	piece *ps;
	int n, cap;
	HFONT *fonts;
	int *asc, *desc;
	int nfonts, capfonts;
} flow_text;

static void inherit(span_look *l, ctl *c) {
	if (c->sp_color >= 0) {
		l->color = c->sp_color;
	}
	if (c->sp_px > 0) {
		l->px = c->sp_px;
	}
	if (c->sp_weight) {
		l->weight = c->sp_weight;
	}
	if (c->sp_slant) {
		l->slant = c->sp_slant;
	}
	l->under |= c->sp_under;
	l->strike |= c->sp_strike;
	if (c->family && c->family[0]) {
		l->family = c->family;
	}
	if (c->kind == K_SPAN && c->href && c->href[0]) {
		l->link = c->self;
	}
}

/* look_font is the font l draws in at dpi: the message font as l changes
 * it, a link underlined, and CSS's generic families as Windows' faces. */
static HFONT look_font(const span_look *l, int dpi) {
	LOGFONTW lf;
	GetObjectW(message_font(dpi), sizeof lf, &lf);
	if (l->px > 0) {
		lf.lfHeight = -px(l->px, dpi);
	}
	switch (l->weight) {
	case 1:
		lf.lfWeight = FW_NORMAL;
		break;
	case 2:
	case 4:
		lf.lfWeight = FW_BOLD;
		break;
	case 3:
		lf.lfWeight = FW_LIGHT;
		break;
	}
	lf.lfItalic = l->slant >= 2;
	lf.lfUnderline = l->under || l->link;
	lf.lfStrikeOut = (BYTE)l->strike;
	if (l->family) {
		if (wcscmp(l->family, L"monospace") == 0) {
			wcscpy(lf.lfFaceName, L"Courier New");
			lf.lfPitchAndFamily = FIXED_PITCH | FF_MODERN;
		} else if (wcscmp(l->family, L"serif") == 0) {
			wcscpy(lf.lfFaceName, L"Times New Roman");
			lf.lfPitchAndFamily = VARIABLE_PITCH | FF_ROMAN;
		} else if (wcscmp(l->family, L"sans-serif") != 0) {
			wcsncpy(lf.lfFaceName, l->family, LF_FACESIZE - 1);
			lf.lfFaceName[LF_FACESIZE - 1] = 0;
		}
	}
	return CreateFontIndirectW(&lf);
}

static int add_font(flow_text *t, HDC dc, const span_look *l, int dpi) {
	if (t->nfonts == t->capfonts) {
		t->capfonts = t->capfonts ? 2 * t->capfonts : 8;
		t->fonts = realloc(t->fonts, sizeof(HFONT) * (size_t)t->capfonts);
		t->asc = realloc(t->asc, sizeof(int) * (size_t)t->capfonts);
		t->desc = realloc(t->desc, sizeof(int) * (size_t)t->capfonts);
	}
	HFONT f = look_font(l, dpi);
	TEXTMETRICW tm;
	HGDIOBJ old = SelectObject(dc, f);
	GetTextMetricsW(dc, &tm);
	SelectObject(dc, old);
	t->fonts[t->nfonts] = f;
	t->asc[t->nfonts] = tm.tmAscent;
	t->desc[t->nfonts] = tm.tmDescent;
	return t->nfonts++;
}

static void add_piece(flow_text *t, HDC dc, const WCHAR *s, int len, int kind, int font, const span_look *l) {
	if (t->n == t->cap) {
		t->cap = t->cap ? 2 * t->cap : 32;
		t->ps = realloc(t->ps, sizeof(piece) * (size_t)t->cap);
	}
	piece *p = &t->ps[t->n++];
	memset(p, 0, sizeof *p);
	p->s = s;
	p->len = len;
	p->kind = kind;
	p->font = font;
	p->color = l->color;
	p->link = l->link;
	if (kind != P_NEWLINE) {
		SIZE e;
		HGDIOBJ old = SelectObject(dc, t->fonts[font]);
		GetTextExtentPoint32W(dc, s, len, &e);
		SelectObject(dc, old);
		p->w = e.cx;
	}
}

/* collect is the pieces of w's words and of every span under it, in the
 * order they are read, each in the look the spans around it make. */
static void collect(HWND w, span_look l, flow_text *t, HDC dc, int dpi) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	inherit(&l, c);
	if (c->words && c->words[0]) {
		int font = add_font(t, dc, &l, dpi);
		const WCHAR *s = c->words;
		while (*s) {
			const WCHAR *e = s;
			int kind = *s == L'\n' ? P_NEWLINE : *s == L' ' ? P_SPACE : P_WORD;
			if (kind == P_NEWLINE) {
				e++;
			} else {
				while (*e && *e != L'\n' && (*e == L' ') == (kind == P_SPACE)) {
					e++;
				}
			}
			add_piece(t, dc, s, (int)(e - s), kind, font, &l);
			s = e;
		}
	}
	for (int i = 0, n = nkids(w); i < n; i++) {
		collect(kid(w, i), l, t, dc, dpi);
	}
}

/* flow_size is the size a flow's words take wrapped at width, drawn into
 * draw where it is not NULL, which is where the flow's links are found. */
static SIZE flow_size(HWND w, ctl *c, int width, HDC draw) {
	HDC dc = draw ? draw : GetDC(w);
	flow_text t;
	memset(&t, 0, sizeof t);
	span_look base = {c->color, c->font_px, 0, 0, 0, 0, NULL, NULL};
	/* The flow's own font is the first, which an empty line is as tall
	 * as. */
	span_look own = base;
	inherit(&own, c);
	add_font(&t, dc, &own, c->dpi);
	collect(w, base, &t, dc, c->dpi);
	int x = 0, line = 0, wrapped = 0, most = 0;
	for (int i = 0; i < t.n; i++) {
		piece *p = &t.ps[i];
		p->line = line;
		if (p->kind == P_NEWLINE) {
			line++;
			x = 0;
			wrapped = 0;
			continue;
		}
		if (p->kind == P_SPACE && x == 0 && wrapped) {
			p->skip = 1;
			continue;
		}
		if (p->kind == P_WORD && x > 0 && x + p->w > width) {
			p->line = ++line;
			x = 0;
			wrapped = 1;
		}
		p->x = x;
		x += p->w;
		most = x > most ? x : most;
	}
	int lines = line + 1;
	int *asc = calloc((size_t)lines, sizeof(int)), *desc = calloc((size_t)lines, sizeof(int)), *top = calloc((size_t)lines, sizeof(int));
	for (int i = 0; i < t.n; i++) {
		piece *p = &t.ps[i];
		if (!p->skip) {
			asc[p->line] = t.asc[p->font] > asc[p->line] ? t.asc[p->font] : asc[p->line];
			desc[p->line] = t.desc[p->font] > desc[p->line] ? t.desc[p->font] : desc[p->line];
		}
	}
	SIZE size = {most, 0};
	for (int i = 0; i < lines; i++) {
		if (asc[i] == 0 && desc[i] == 0) {
			asc[i] = t.asc[0];
			desc[i] = t.desc[0];
		}
		top[i] = size.cy;
		size.cy += asc[i] + desc[i];
	}
	if (draw) {
		int x0 = px(pad(w, 3), c->dpi), y0 = px(pad(w, 0), c->dpi);
		free(c->links);
		c->links = NULL;
		c->nlinks = 0;
		SetBkMode(dc, TRANSPARENT);
		HGDIOBJ old = SelectObject(dc, t.fonts[0]);
		for (int i = 0; i < t.n; i++) {
			piece *p = &t.ps[i];
			if (p->skip || p->kind == P_NEWLINE) {
				continue;
			}
			SelectObject(dc, t.fonts[p->font]);
			COLORREF fg = p->color >= 0 ? RGB((p->color >> 16) & 0xff, (p->color >> 8) & 0xff, p->color & 0xff)
										: GetSysColor(p->link ? COLOR_HOTLIGHT : COLOR_WINDOWTEXT);
			SetTextColor(dc, fg);
			int y = y0 + top[p->line] + asc[p->line] - t.asc[p->font];
			/* Each character at the advance the piece was measured with:
			 * Wine draws its fonts tighter than GetTextExtentPoint32 says,
			 * which left a gap after every piece placed by its width, and
			 * a link's underline past its last letter. */
			INT stack[64], *dx = p->len <= 64 ? stack : malloc(sizeof(INT) * (size_t)p->len);
			SIZE e;
			if (GetTextExtentExPointW(dc, p->s, p->len, 0, NULL, dx, &e)) {
				for (int k = p->len - 1; k > 0; k--) {
					dx[k] -= dx[k - 1];
				}
				ExtTextOutW(dc, x0 + p->x, y, 0, NULL, p->s, (UINT)p->len, dx);
			} else {
				TextOutW(dc, x0 + p->x, y, p->s, p->len);
			}
			if (dx != stack) {
				free(dx);
			}
			if (p->link) {
				c->links = realloc(c->links, sizeof(struct link_at) * (size_t)(c->nlinks + 1));
				struct link_at *a = &c->links[c->nlinks++];
				SetRect(&a->r, x0 + p->x, y0 + top[p->line], x0 + p->x + p->w, y0 + top[p->line] + asc[p->line] + desc[p->line]);
				a->span = p->link;
			}
		}
		SelectObject(dc, old);
	} else {
		ReleaseDC(w, dc);
	}
	for (int i = 0; i < t.nfonts; i++) {
		DeleteObject(t.fonts[i]);
	}
	free(asc);
	free(desc);
	free(top);
	free(t.ps);
	free(t.fonts);
	free(t.asc);
	free(t.desc);
	return size;
}

/* link_under is the span leading where the link a flow drew at x, y does,
 * or NULL. */
static HWND link_under(ctl *c, int x, int y) {
	POINT pt = {x, y};
	for (int i = 0; i < c->nlinks; i++) {
		if (PtInRect(&c->links[i].r, pt)) {
			return c->links[i].span;
		}
	}
	return NULL;
}

/* sngl_win32_natural is what a control's content measures, in device
 * pixels, before its record's padding and sizes: its text in its font, its
 * metrics, or what it asks the control for. A box, a scroll, tabs, a
 * divider and an image are measured by their record (_measure). */
void sngl_win32_natural(HWND w, SIZE *out) {
	ctl *c = ctl_of(w);
	SIZE s = {0, 0};
	if (!c) {
		*out = s;
		return;
	}
	switch (c->kind) {
	case K_TEXT:
		s = text_size(w);
		break;
	case K_BUTTON:
		s = text_size(w);
		s.cx += px(20, c->dpi);
		s.cy += px(10, c->dpi);
		if (s.cx < px(75, c->dpi)) {
			s.cx = px(75, c->dpi);
		}
		break;
	case K_CHECK:
	case K_RADIO:
		s = text_size(w);
		s.cx += (system_metric_for_dpi ? system_metric_for_dpi(SM_CXMENUCHECK, (UINT)c->dpi) : GetSystemMetrics(SM_CXMENUCHECK)) + px(8, c->dpi);
		s.cy += px(4, c->dpi);
		break;
	case K_EDIT:
		s = text_size(w);
		s.cx = px(150, c->dpi);
		s.cy += px(8, c->dpi);
		break;
	case K_AREA:
		s.cx = px(200, c->dpi);
		s.cy = line_height(w) * (c->rows > 0 ? c->rows : 3) + px(8, c->dpi);
		break;
	case K_SELECT:
		s = select_size(w, c);
		break;
	case K_PROGRESS:
		s.cx = px(100, c->dpi);
		s.cy = px(16, c->dpi);
		if (c->show_text) {
			WCHAR buf[256];
			bar_text(w, c, buf, 256);
			HDC dc = GetDC(w);
			HGDIOBJ old = SelectObject(dc, font_of(c));
			RECT r = {0, 0, 0, 0};
			DrawTextW(dc, buf, -1, &r, DT_CALCRECT | DT_NOPREFIX | DT_SINGLELINE);
			SelectObject(dc, old);
			ReleaseDC(w, dc);
			if (r.right - r.left + px(16, c->dpi) > s.cx) {
				s.cx = r.right - r.left + px(16, c->dpi);
			}
			if (r.bottom - r.top + px(4, c->dpi) > s.cy) {
				s.cy = r.bottom - r.top + px(4, c->dpi);
			}
		}
		break;
	case K_LINK:
		s = text_size(w);
		break;
	case K_TABLE: {
		/* Every column, and every row under the header. */
		HWND header = (HWND)SendMessageW(w, LVM_GETHEADER, 0, 0);
		int cols = (int)SendMessageW(header, HDM_GETITEMCOUNT, 0, 0);
		for (int i = 0; i < cols; i++) {
			s.cx += (int)SendMessageW(w, LVM_GETCOLUMNWIDTH, (WPARAM)i, 0);
		}
		RECT hr = {0, 0, 0, 0};
		if (cols > 0) {
			SendMessageW(header, HDM_GETITEMRECT, 0, (LPARAM)&hr);
		}
		int head = hr.bottom - hr.top > 0 ? hr.bottom - hr.top : line_height(w) + px(8, c->dpi);
		int rows = (int)SendMessageW(w, LVM_GETITEMCOUNT, 0, 0), row = line_height(w) + px(4, c->dpi);
		RECT ir;
		ir.left = LVIR_BOUNDS;
		if (rows > 0 && SendMessageW(w, LVM_GETITEMRECT, 0, (LPARAM)&ir) && ir.bottom > ir.top) {
			row = ir.bottom - ir.top;
		}
		s.cx += 2 * GetSystemMetrics(SM_CXEDGE) + GetSystemMetrics(SM_CXVSCROLL);
		s.cy = head + rows * row + 2 * GetSystemMetrics(SM_CYEDGE) + px(2, c->dpi);
		break;
	}
	case K_TREE:
		s = tree_size(w, c);
		break;
	case K_DATE: {
		SIZE ideal = {0, 0};
		if (SendMessageW(w, DTM_GETIDEALSIZE, 0, (LPARAM)&ideal) && ideal.cx > 0) {
			s = ideal;
		} else {
			s = text_size(w);
			s.cx = px(130, c->dpi);
		}
		if (s.cy < line_height(w) + px(8, c->dpi)) {
			s.cy = line_height(w) + px(8, c->dpi);
		}
		break;
	}
	case K_CANVAS:
		s.cx = px(c->cw, c->dpi);
		s.cy = px(c->ch, c->dpi);
		break;
	case K_FLOW: {
		/* Its words on as few lines as their newlines allow, but asking
		 * for no more than 240 px across, since what it asks for is the
		 * least a window holding it is sized to: a box down the page
		 * gives it the whole of its width whatever it asks, and it wraps
		 * there. */
		int most = px(240, c->dpi);
		s = flow_size(w, c, most, 0);
		if (s.cx > most) {
			s.cx = most;
		}
		break;
	}
	default:
		break;
	}
	*out = s;
}

/* sngl_win32_picture_size is a picture's bitmap's size, where it has one. */
int sngl_win32_picture_size(HWND w, SIZE *out) {
	ctl *c = ctl_of(w);
	if (!c || !c->bmp) {
		return 0;
	}
	out->cx = c->bw;
	out->cy = c->bh;
	return 1;
}

/* sngl_win32_flow_height is the height a flow's words wrap to at width. */
int sngl_win32_flow_height(HWND w, int width) {
	ctl *c = ctl_of(w);
	return c ? flow_size(w, c, width, 0).cy : 0;
}

/* sngl_win32_refont gives w the font for dpi, the DPI of the toplevel it is
 * in, as its record's refresh takes it there. */
void sngl_win32_refont(HWND w, int dpi) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	c->dpi = dpi;
	own_font(c);
	if (c->kind != K_TOP && c->kind != K_BOX) {
		SendMessageW(w, WM_SETFONT, (WPARAM)font_of(c), FALSE);
	}
}

int sngl_win32_dpi_of(HWND w) { return dpi_of(w); }

/* sngl_win32_resize_client sizes a toplevel's frame so its client area is w
 * by h. */
void sngl_win32_resize_client(HWND top, int w, int h) {
	RECT r = {0, 0, w, h};
	AdjustWindowRectEx(&r, (DWORD)GetWindowLongPtrW(top, GWL_STYLE), GetMenu(top) != NULL, (DWORD)GetWindowLongPtrW(top, GWL_EXSTYLE));
	SetWindowPos(top, NULL, 0, 0, r.right - r.left, r.bottom - r.top, SWP_NOMOVE | SWP_NOZORDER | SWP_NOACTIVATE);
}

/* sngl_win32_scroll_children moves what a scroll shows by dx, dy, its
 * children with it, and paints what it uncovers. */
void sngl_win32_scroll_children(HWND w, int dx, int dy) {
	ScrollWindowEx(w, dx, dy, NULL, NULL, NULL, NULL, SW_SCROLLCHILDREN | SW_INVALIDATE | SW_ERASE);
}

/* --- Timers --- */

/* A timer is the parked window's, which is never shown and so never closed:
 * its id is the one SetTimer is given, and its handler what each tick calls. */
static struct timer {
	UINT_PTR id;
	handler h;
} *timers;
static int ntimers, captimers;
static UINT_PTR next_timer = 1;

static void tick(UINT_PTR id) {
	for (int i = 0; i < ntimers; i++) {
		if (timers[i].id == id) {
			/* By value: the handler may cancel its own timer. */
			handler h = timers[i].h;
			fire(&h);
			return;
		}
	}
}

/* --- Tabs, scrolls, modals and tips --- */

/* sngl_win32_tab_insert gives a tabs a tab at `at` titled with page's
 * text. */
void sngl_win32_tab_insert(HWND tabs, int at, HWND page) {
	int n = GetWindowTextLengthW(page);
	WCHAR *title = malloc(sizeof(WCHAR) * (size_t)(n + 1));
	GetWindowTextW(page, title, n + 1);
	TCITEMW item;
	memset(&item, 0, sizeof item);
	item.mask = TCIF_TEXT;
	item.pszText = title;
	SendMessageW(tabs, TCM_INSERTITEMW, (WPARAM)at, (LPARAM)&item);
	free(title);
}

/* sngl_win32_clicked fires w's click handler, as a tab's page hears its tab
 * chosen. */
void sngl_win32_clicked(HWND w) {
	ctl *c = ctl_of(w);
	if (c) {
		fire(&c->click);
	}
}

/* show_modal shows a modal's window over the window the modal is in,
 * disabling that one while it is open, or hides it and enables it again. */
static void show_modal(HWND m, ctl *c) {
	HWND owner = GetAncestor(m, GA_ROOT);
	if (owner == park) {
		owner = NULL;
	}
	HWND was = GetWindow(c->dialog, GW_OWNER);
	if (c->open) {
		if (IsWindowVisible(c->dialog)) {
			return;
		}
		SetWindowLongPtrW(c->dialog, GWLP_HWNDPARENT, (LONG_PTR)owner);
		ctl *d = ctl_of(c->dialog);
		refresh(c->dialog, owner ? dpi_of(owner) : system_dpi);
		SIZE s = measure(c->dialog);
		RECT r = {0, 0, s.cx < px(240, d->dpi) ? px(240, d->dpi) : s.cx, s.cy < px(80, d->dpi) ? px(80, d->dpi) : s.cy};
		AdjustWindowRectEx(&r, (DWORD)GetWindowLongPtrW(c->dialog, GWL_STYLE), GetMenu(c->dialog) != NULL, (DWORD)GetWindowLongPtrW(c->dialog, GWL_EXSTYLE));
		int ww = r.right - r.left, wh = r.bottom - r.top, x = 0, y = 0;
		RECT o;
		if (owner && GetWindowRect(owner, &o)) {
			x = o.left + (o.right - o.left - ww) / 2;
			y = o.top + (o.bottom - o.top - wh) / 2;
		}
		SetWindowPos(c->dialog, HWND_TOP, x, y, ww, wh, SWP_NOACTIVATE);
		ShowWindow(c->dialog, SW_SHOW);
		lay_out_top(c->dialog);
		if (owner) {
			EnableWindow(owner, FALSE);
		}
	} else if (IsWindowVisible(c->dialog)) {
		if (was) {
			EnableWindow(was, TRUE);
		}
		ShowWindow(c->dialog, SW_HIDE);
	}
}

/* --- Menus --- */

/* sngl_win32_append_popup appends a submenu to m, whose handle AppendMenuW
 * takes as its id. */
void sngl_win32_append_popup(HMENU m, UINT flags, HMENU sub, const unsigned short *label) { AppendMenuW(m, flags, (UINT_PTR)sub, (LPCWSTR)label); }

/* tips is the tooltip every box with a tip shows its children's through. */
static HWND tips;

/* tip_tool gives child, one of box's, the tip text, or none where it is NULL
 * or empty. */
static void tip_tool(HWND box, HWND child, const WCHAR *text) {
	if (!tips) {
		tips = CreateWindowExW(WS_EX_TOPMOST, TOOLTIPS_CLASSW, NULL, WS_POPUP | TTS_ALWAYSTIP | TTS_NOPREFIX, CW_USEDEFAULT, CW_USEDEFAULT, CW_USEDEFAULT, CW_USEDEFAULT, park, NULL, GetModuleHandleW(NULL), NULL);
	}
	TOOLINFOW ti;
	memset(&ti, 0, sizeof ti);
	ti.cbSize = sizeof ti;
	ti.uFlags = TTF_IDISHWND | TTF_SUBCLASS;
	ti.hwnd = box;
	ti.uId = (UINT_PTR)child;
	SendMessageW(tips, TTM_DELTOOLW, 0, (LPARAM)&ti);
	if (text && text[0]) {
		ti.lpszText = (WCHAR *)text;
		SendMessageW(tips, TTM_ADDTOOLW, 0, (LPARAM)&ti);
	}
}

/* --- Pictures --- */

static IWICImagingFactory *wic;

/* decode is the picture data, or the file path, holds as a 32-bit DIB of
 * premultiplied BGRA, *w by *h, or NULL with *why set. */
static HBITMAP decode(const unsigned char *data, size_t len, const WCHAR *path, int *w, int *h, const char **why) {
	if (!wic && FAILED(CoCreateInstance(&CLSID_WICImagingFactory, NULL, CLSCTX_INPROC_SERVER, &IID_IWICImagingFactory, (void **)&wic))) {
		*why = "no image decoder (WIC)";
		return NULL;
	}
	IWICBitmapDecoder *dec = NULL;
	IWICStream *stream = NULL;
	HRESULT hr;
	if (data) {
		hr = IWICImagingFactory_CreateStream(wic, &stream);
		if (SUCCEEDED(hr)) {
			hr = IWICStream_InitializeFromMemory(stream, (BYTE *)data, (DWORD)len);
		}
		if (SUCCEEDED(hr)) {
			hr = IWICImagingFactory_CreateDecoderFromStream(wic, (IStream *)stream, NULL, WICDecodeMetadataCacheOnDemand, &dec);
		}
	} else {
		hr = IWICImagingFactory_CreateDecoderFromFilename(wic, path, NULL, GENERIC_READ, WICDecodeMetadataCacheOnDemand, &dec);
	}
	IWICBitmapFrameDecode *frame = NULL;
	IWICBitmapSource *pbgra = NULL;
	HBITMAP bmp = NULL;
	UINT fw = 0, fh = 0;
	if (SUCCEEDED(hr)) {
		hr = IWICBitmapDecoder_GetFrame(dec, 0, &frame);
	}
	if (SUCCEEDED(hr)) {
		hr = WICConvertBitmapSource(&GUID_WICPixelFormat32bppPBGRA, (IWICBitmapSource *)frame, &pbgra);
	}
	if (SUCCEEDED(hr)) {
		hr = IWICBitmapSource_GetSize(pbgra, &fw, &fh);
	}
	if (SUCCEEDED(hr) && fw > 0 && fh > 0) {
		BITMAPINFO bi;
		memset(&bi, 0, sizeof bi);
		bi.bmiHeader.biSize = sizeof bi.bmiHeader;
		bi.bmiHeader.biWidth = (LONG)fw;
		bi.bmiHeader.biHeight = -(LONG)fh;
		bi.bmiHeader.biPlanes = 1;
		bi.bmiHeader.biBitCount = 32;
		bi.bmiHeader.biCompression = BI_RGB;
		void *bits = NULL;
		bmp = CreateDIBSection(NULL, &bi, DIB_RGB_COLORS, &bits, NULL, 0);
		if (bmp && FAILED(IWICBitmapSource_CopyPixels(pbgra, NULL, fw * 4, fw * fh * 4, bits))) {
			DeleteObject(bmp);
			bmp = NULL;
		}
	}
	if (pbgra) {
		IWICBitmapSource_Release(pbgra);
	}
	if (frame) {
		IWICBitmapFrameDecode_Release(frame);
	}
	if (dec) {
		IWICBitmapDecoder_Release(dec);
	}
	if (stream) {
		IWICStream_Release(stream);
	}
	if (!bmp) {
		*why = data ? "the image could not be decoded" : "the image could not be read";
		return NULL;
	}
	*w = (int)fw;
	*h = (int)fh;
	return bmp;
}

/* shown_as is the picture's result: its bitmap, or why it has none, which
 * its image hears once it is in one. */
static void shown_as(HWND w, ctl *c, HBITMAP bmp, int bw, int bh, const char *why) {
	if (c->bmp) {
		DeleteObject(c->bmp);
	}
	if (c->scaled) {
		DeleteObject(c->scaled);
		c->scaled = NULL;
	}
	c->bmp = bmp;
	c->bw = bw;
	c->bh = bh;
	free(c->why);
	c->why = why ? _strdup(why) : NULL;
	InvalidateRect(w, NULL, FALSE);
	PostMessageW(w, SNGL_WM_LOADED, 0, 0);
}

typedef struct {
	HWND w;
	WCHAR *url;
} fetch;

/* fetched saves the URL a fetch names to the cache, off the loop, and
 * hands the loop the file and the URL it was fetched for. */
static DWORD WINAPI fetched(void *arg) {
	fetch *f = arg;
	WCHAR path[MAX_PATH];
	WCHAR *saved = NULL;
	if (SUCCEEDED(URLDownloadToCacheFileW(NULL, f->url, path, MAX_PATH, 0, NULL))) {
		saved = _wcsdup(path);
	}
	if (!PostMessageW(f->w, SNGL_WM_FETCHED, (WPARAM)f->url, (LPARAM)saved)) {
		free(saved);
		free(f->url);
	}
	free(f);
	return 0;
}

/* The files the program carries: its build's table, and each handed over on
 * its own (sngl_win32.h). */
static const sngl_win32_asset *carried;
static sngl_win32_asset *carried_files;
static size_t ncarried_files;

void sngl_win32_carry(const sngl_win32_asset *assets) {
	carried = assets;
}

void sngl_win32_carry_file(const char *name, const char *data, int len) {
	size_t n = len > 0 ? (size_t)len : 0;
	unsigned char *copy = malloc(n ? n : 1);
	memcpy(copy, data, n);
	carried_files = realloc(carried_files, (ncarried_files + 1) * sizeof *carried_files);
	carried_files[ncarried_files++] = (sngl_win32_asset){_strdup(name), copy, n};
}

/* carried_file is the file the program carries by name, or NULL. */
static const sngl_win32_asset *carried_file(const char *name) {
	for (const sngl_win32_asset *a = carried; a && a->name; a++) {
		if (strcmp(a->name, name) == 0) {
			return a;
		}
	}
	for (size_t i = 0; i < ncarried_files; i++) {
		if (strcmp(carried_files[i].name, name) == 0) {
			return &carried_files[i];
		}
	}
	return NULL;
}

static int fetch_for(HWND w, const WCHAR *url);

/* load shows what url names: a file the program carries (carried_file), one
 * fetched where it is an http or https URL, or one on disk. */
static void load(HWND w, ctl *c) {
	const char *why = NULL;
	int bw = 0, bh = 0;
	HBITMAP bmp = NULL;
	if (!c->url || !c->url[0]) {
		shown_as(w, c, NULL, 0, 0, NULL);
		return;
	}
	/* A carried file's URL is its path from the root it is served under. */
	char *name = _strdup(utf8(c->url[0] == L'/' ? c->url + 1 : c->url));
	const sngl_win32_asset *a = carried_file(name);
	free(name);
	if (a) {
		bmp = decode(a->data, a->len, NULL, &bw, &bh, &why);
		shown_as(w, c, bmp, bw, bh, why);
		return;
	}
	if (wcsncmp(c->url, L"http://", 7) == 0 || wcsncmp(c->url, L"https://", 8) == 0) {
		if (!fetch_for(w, c->url)) {
			shown_as(w, c, NULL, 0, 0, "the image could not be fetched");
		}
		return;
	}
	bmp = decode(NULL, 0, c->url, &bw, &bh, &why);
	shown_as(w, c, bmp, bw, bh, why);
}

/* paint_picture draws a picture's bitmap into its w by h area as its
 * image's fit says: whole and centered, covering it, stretched over it, at
 * its own size, or whole and no larger than its own. */
static void paint_picture(HWND w, ctl *c, HDC dc) {
	RECT r;
	GetClientRect(w, &r);
	int aw = r.right, ah = r.bottom;
	if (aw <= 0 || ah <= 0) {
		return;
	}
	/* Painted whole off screen over what shows through it, since erasing
	 * first showed the window behind for a frame each time it was laid
	 * out. */
	HDC buf = CreateCompatibleDC(dc);
	HBITMAP frame_bmp = CreateCompatibleBitmap(dc, aw, ah);
	HGDIOBJ old_frame = SelectObject(buf, frame_bmp);
	FillRect(buf, &r, backdrop(w));
	if (c->bmp) {
		ctl *frame = ctl_of(GetParent(w));
		int fit = frame ? frame->fit : 0;
		double sx = (double)aw / c->bw, sy = (double)ah / c->bh, sc;
		switch (fit) {
		case 1: /* cover */
			sc = sx > sy ? sx : sy;
			break;
		case 3: /* none */
			sc = 1;
			break;
		case 4: /* scaleDown */
			sc = sx < sy ? sx : sy;
			sc = sc < 1 ? sc : 1;
			break;
		default: /* contain, and fill below */
			sc = sx < sy ? sx : sy;
		}
		int dw = (int)(c->bw * sc + 0.5), dh = (int)(c->bh * sc + 0.5);
		if (fit == 2) {
			dw = aw;
			dh = ah;
		}
		if (dw > 0 && dh > 0) {
			HDC mem = CreateCompatibleDC(dc);
			BLENDFUNCTION bf = {AC_SRC_OVER, 0, 255, AC_SRC_ALPHA};
			if (!c->scaled || c->sw != dw || c->sh != dh) {
				/* Scaling a large picture is the slow part of painting
				 * it, so it is done once a size. */
				if (c->scaled) {
					DeleteObject(c->scaled);
				}
				BITMAPINFO bi = {{sizeof(BITMAPINFOHEADER), dw, -dh, 1, 32, BI_RGB, 0, 0, 0, 0, 0}, {{0, 0, 0, 0}}};
				void *bits;
				c->scaled = CreateDIBSection(dc, &bi, DIB_RGB_COLORS, &bits, NULL, 0);
				c->sw = dw;
				c->sh = dh;
				if (c->scaled) {
					memset(bits, 0, (size_t)dw * (size_t)dh * 4);
					HDC to = CreateCompatibleDC(dc);
					HGDIOBJ old_to = SelectObject(to, c->scaled);
					HGDIOBJ old_mem = SelectObject(mem, c->bmp);
					AlphaBlend(to, 0, 0, dw, dh, mem, 0, 0, c->bw, c->bh, bf);
					SelectObject(mem, old_mem);
					SelectObject(to, old_to);
					DeleteDC(to);
				}
			}
			if (c->scaled) {
				HGDIOBJ old = SelectObject(mem, c->scaled);
				AlphaBlend(buf, (aw - dw) / 2, (ah - dh) / 2, dw, dh, mem, 0, 0, dw, dh, bf);
				SelectObject(mem, old);
			}
			DeleteDC(mem);
		}
	}
	BitBlt(dc, 0, 0, aw, ah, buf, 0, 0, SRCCOPY);
	SelectObject(buf, old_frame);
	DeleteObject(frame_bmp);
	DeleteDC(buf);
}

/* --- Drawing --- */

static ULONG_PTR gdiplus;

/* fetch_for starts a fetch of url, whose file is posted to w. */
static int fetch_for(HWND w, const WCHAR *url) {
	fetch *f = malloc(sizeof *f);
	f->w = w;
	f->url = _wcsdup(url);
	HANDLE t = CreateThread(NULL, 0, fetched, f, 0, NULL);
	if (!t) {
		free(f->url);
		free(f);
		return 0;
	}
	CloseHandle(t);
	return 1;
}

/* url_picture_of is the picture url names that w's drawing painted,
 * decoded the first time it is asked for: a carried file or one on disk at
 * once, a fetched one as it arrives (SNGL_WM_FETCHED), with no bitmap until
 * then or where it failed. */
static struct url_picture *url_picture_of(HWND w, ctl *c, const WCHAR *url) {
	for (int i = 0; i < c->npictures; i++) {
		if (wcscmp(c->pictures[i].url, url) == 0) {
			return &c->pictures[i];
		}
	}
	c->pictures = realloc(c->pictures, (size_t)(c->npictures + 1) * sizeof *c->pictures);
	struct url_picture *p = &c->pictures[c->npictures++];
	memset(p, 0, sizeof *p);
	p->url = _wcsdup(url);
	const char *why = NULL;
	char *name = _strdup(utf8(url[0] == L'/' ? url + 1 : url));
	const sngl_win32_asset *a = carried_file(name);
	free(name);
	if (a) {
		p->bmp = decode(a->data, a->len, NULL, &p->w, &p->h, &why);
	} else if (wcsncmp(url, L"http://", 7) == 0 || wcsncmp(url, L"https://", 8) == 0) {
		fetch_for(w, url);
	} else {
		p->bmp = decode(NULL, 0, url, &p->w, &p->h, &why);
	}
	return p;
}

void sngl_win32_gfx_image(sngl_win32_gfx *g, const unsigned short *url, double x, double y, double w, double h) {
	ctl *c = ctl_of(g->owner);
	if (!c || !url || !url[0]) {
		return;
	}
	struct url_picture *p = url_picture_of(g->owner, c, (const WCHAR *)url);
	DIBSECTION ds;
	if (!p->bmp || GetObjectW(p->bmp, sizeof ds, &ds) != sizeof ds) {
		return;
	}
	GpBitmap *img = NULL;
	if (GdipCreateBitmapFromScan0(p->w, p->h, p->w * 4, PixelFormat32bppPARGB, ds.dsBm.bmBits, &img) == Ok) {
		GdipDrawImageRect(g->g, (GpImage *)img, (REAL)x, (REAL)y, (REAL)w, (REAL)h);
		GdipDisposeImage((GpImage *)img);
	}
}

/* gfx_begin readies a context over gr, drawing for owner: a path of its
 * own, and the paint's serial, by which SNGL's drawing state, the rest of
 * what the context is (sngl:platform/win32's _drawing), knows a paint has
 * begun and starts afresh, as cairo's: black, a line one wide, and the
 * default font. */
static void gfx_begin(sngl_win32_gfx *g, GpGraphics *gr, HWND owner) {
	static int serial;
	memset(g, 0, sizeof *g);
	g->g = gr;
	g->owner = owner;
	g->serial = ++serial;
	GdipCreatePath(FillModeWinding, &g->path);
	GdipSetSmoothingMode(gr, SmoothingModeAntiAlias);
	GdipSetPixelOffsetMode(gr, PixelOffsetModeHalf);
	GdipSetTextRenderingHint(gr, TextRenderingHintAntiAlias);
}

/* paint_canvas paints a canvas into dc: its background, and its drawing
 * mapped onto its client area as its scaling says, off screen first. */
static void paint_canvas(HWND w, ctl *c, HDC dc) {
	RECT r;
	GetClientRect(w, &r);
	int aw = r.right, ah = r.bottom;
	if (aw <= 0 || ah <= 0) {
		return;
	}
	HDC mem = CreateCompatibleDC(dc);
	HBITMAP bmp = CreateCompatibleBitmap(dc, aw, ah);
	HGDIOBJ old = SelectObject(mem, bmp);
	HBRUSH bg = c->brush ? c->brush : (HBRUSH)GetClassLongPtrW(w, GCLP_HBRBACKGROUND);
	FillRect(mem, &r, bg);
	sngl_win32_gfx g;
	GpGraphics *gr = NULL;
	if (c->paint.fn && GdipCreateFromHDC(mem, &gr) == Ok) {
		gfx_begin(&g, gr, w);
		/* The drawing's space onto the area: at its own size, a SNGL px a
		 * 96th of an inch, centered, or scaled. */
		double k = (double)c->dpi / 96, sx = k, sy = k;
		if (c->cw > 0 && c->ch > 0 && c->scaling != 0) {
			sx = (double)aw / c->cw;
			sy = (double)ah / c->ch;
			if (c->scaling == 1) {
				sx = sy = sx < sy ? sx : sy;
			} else if (c->scaling == 2) {
				sx = sy = sx > sy ? sx : sy;
			}
		}
		GdipSetClipRectI(g.g, 0, 0, aw, ah, CombineModeReplace);
		GdipTranslateWorldTransform(g.g, (REAL)((aw - c->cw * sx) / 2), (REAL)((ah - c->ch * sy) / 2), MatrixOrderPrepend);
		GdipScaleWorldTransform(g.g, (REAL)sx, (REAL)sy, MatrixOrderPrepend);
		((sngl_win32_paint_fn)(void (*)(void))c->paint.fn)(&g, c->paint.ctx);
		GdipDeletePath(g.path);
		GdipDeleteGraphics(g.g);
	}
	BitBlt(dc, 0, 0, aw, ah, mem, 0, 0, SRCCOPY);
	SelectObject(mem, old);
	DeleteObject(bmp);
	DeleteDC(mem);
}

static void gdiplus_start(void) {
	if (!gdiplus) {
		GdiplusStartupInput in = {1, NULL, FALSE, FALSE};
		GdiplusStartup(&gdiplus, &in, NULL);
	}
}

/* render_drawing paints a drawing into its picture's bitmap, its size in
 * pixels, its drawing's space mapped onto it one to one. */
static void render_drawing(HWND w, ctl *c) {
	int iw = (int)ceil(c->dw), ih = (int)ceil(c->dh);
	if (!c->paint.fn || iw <= 0 || ih <= 0) {
		return;
	}
	BITMAPINFO bi = {{sizeof(BITMAPINFOHEADER), iw, -ih, 1, 32, BI_RGB, 0, 0, 0, 0, 0}, {{0, 0, 0, 0}}};
	void *bits = NULL;
	HBITMAP bmp = CreateDIBSection(NULL, &bi, DIB_RGB_COLORS, &bits, NULL, 0);
	if (!bmp) {
		return;
	}
	memset(bits, 0, (size_t)iw * (size_t)ih * 4);
	/* Drawn through a GDI+ bitmap over the section's own pixels, since GDI+
	 * drawing on a DC leaves its alpha unwritten. */
	GpBitmap *img = NULL;
	GpGraphics *gr = NULL;
	if (GdipCreateBitmapFromScan0(iw, ih, iw * 4, PixelFormat32bppPARGB, bits, &img) == Ok) {
		if (GdipGetImageGraphicsContext((GpImage *)img, &gr) == Ok) {
			sngl_win32_gfx g;
			gfx_begin(&g, gr, w);
			((sngl_win32_paint_fn)(void (*)(void))c->paint.fn)(&g, c->paint.ctx);
			GdipDeletePath(g.path);
			GdipDeleteGraphics(gr);
		}
		GdipDisposeImage((GpImage *)img);
	}
	shown_as(w, c, bmp, iw, ih, NULL);
}

/* --- Window procedures --- */

static LRESULT color(ctl *c, HDC dc, LRESULT otherwise) {
	if (!c) {
		return otherwise;
	}
	if (c->color >= 0) {
		SetTextColor(dc, RGB((c->color >> 16) & 0xff, (c->color >> 8) & 0xff, c->color & 0xff));
	} else if (c->kind == K_LINK) {
		SetTextColor(dc, IsWindowEnabled(c->self) ? GetSysColor(COLOR_HOTLIGHT) : GetSysColor(COLOR_GRAYTEXT));
	}
	if (c->brush) {
		SetBkColor(dc, RGB((c->background >> 16) & 0xff, (c->background >> 8) & 0xff, c->background & 0xff));
		return (LRESULT)c->brush;
	}
	return otherwise;
}

/* box_proc is a box's and a toplevel's: each control's notifications reach
 * the window it is in, which hands them to the control's record. */
static LRESULT CALLBACK box_proc(HWND w, UINT msg, WPARAM wp, LPARAM lp) {
	ctl *c = ctl_of(w);
	switch (msg) {
	case WM_COMMAND: {
		if (lp == 0 && HIWORD(wp) == 0 && c && c->kind == K_TOP) {
			/* A menu's command, its entry's id. */
			if (hooks.command) {
				hooks.command(w, LOWORD(wp));
			}
			return 0;
		}
		ctl *src = ctl_of((HWND)lp);
		if (!src) {
			break;
		}
		switch (HIWORD(wp)) {
		case BN_CLICKED:
			/* STN_CLICKED too, a text's or a link's. A test's click
			 * reports it without opening anything. */
			if (src->kind == K_LINK && src->href && src->href[0] && !firing) {
				ShellExecuteW(NULL, L"open", src->href, NULL, NULL, SW_SHOWNORMAL);
			}
			if (src->kind == K_TEXT && !firing) {
				press((HWND)lp);
			} else {
				fire(&src->click);
			}
			return 0;
		case EN_CHANGE:
			if (!src->quiet) {
				fire(&src->change);
			}
			return 0;
		case EN_KILLFOCUS:
			/* A multi-line edit's text is committed as it is left, Enter
			 * being a line of it. */
			if (src->kind == K_AREA) {
				fire(&src->activate);
				return 0;
			}
			break;
		case CBN_SELCHANGE:
			/* BN_PAINT's code too, which only a BS_NOTIFY button sends. */
			if (src->kind == K_SELECT) {
				fire(&src->change);
				return 0;
			}
			break;
		}
		break;
	}
	case WM_CTLCOLORSTATIC:
	case WM_CTLCOLORBTN:
	case WM_CTLCOLOREDIT: {
		LRESULT otherwise = DefWindowProcW(w, msg, wp, lp);
		ctl *src = ctl_of((HWND)lp);
		ctl *under = src && !src->brush && msg != WM_CTLCOLOREDIT ? inherited((HWND)lp) : NULL;
		if (under) {
			/* A control with no background of its own shows its box's. */
			SetBkMode((HDC)wp, TRANSPARENT);
			color(src, (HDC)wp, 0);
			return (LRESULT)under->brush;
		}
		return color(src, (HDC)wp, otherwise);
	}
	case WM_ERASEBKGND:
		if (c && (c->kind == K_CANVAS || c->kind == K_PICTURE)) {
			/* Its paint fills what it draws over, off screen, so it does
			 * not flicker. */
			return 1;
		}
		if (c && inherited(w)) {
			RECT r;
			GetClientRect(w, &r);
			FillRect((HDC)wp, &r, backdrop(w));
			return 1;
		}
		break;
	case WM_DRAWITEM: {
		DRAWITEMSTRUCT *di = (DRAWITEMSTRUCT *)lp;
		ctl *src = ctl_of(di->hwndItem);
		if (src && src->kind == K_BUTTON) {
			paint_button(di, src);
			return TRUE;
		}
		break;
	}
	case WM_NCHITTEST:
		if (c && c->kind == K_PICTURE) {
			/* Its image takes the click. */
			return HTTRANSPARENT;
		}
		break;
	case SNGL_WM_LOADED:
		if (c && c->kind == K_PICTURE) {
			ctl *frame = ctl_of(GetParent(w));
			if (frame && frame->kind == K_IMAGE) {
				free(frame->why);
				frame->why = c->why ? _strdup(c->why) : NULL;
				if (c->bmp) {
					fire(&frame->load);
				} else if (c->why) {
					fire(&frame->failed);
				}
				relayout(w);
			}
		}
		return 0;
	case SNGL_WM_FETCHED: {
		WCHAR *url = (WCHAR *)wp, *path = (WCHAR *)lp;
		for (int i = 0; c && path && i < c->npictures; i++) {
			struct url_picture *p = &c->pictures[i];
			if (!p->bmp && wcscmp(p->url, url) == 0) {
				const char *why = NULL;
				p->bmp = decode(NULL, 0, path, &p->w, &p->h, &why);
				if (c->kind == K_CANVAS) {
					InvalidateRect(w, NULL, FALSE);
				} else {
					render_drawing(w, c);
				}
			}
		}
		/* A fetch for a URL the picture no longer shows is dropped: a
		 * slow one may land after the one for its new URL. */
		if (c && c->kind == K_PICTURE && c->url && wcscmp(c->url, url) == 0) {
			const char *why = "the image could not be fetched";
			int bw = 0, bh = 0;
			HBITMAP bmp = path ? decode(NULL, 0, path, &bw, &bh, &why) : NULL;
			shown_as(w, c, bmp, bw, bh, bmp ? NULL : why);
		}
		free(url);
		free(path);
		return 0;
	}
	case WM_PAINT:
	case WM_PRINTCLIENT:
		if (c && c->kind == K_FLOW) {
			PAINTSTRUCT ps;
			HDC dc = msg == WM_PRINTCLIENT ? (HDC)wp : BeginPaint(w, &ps);
			if (msg == WM_PRINTCLIENT) {
				SendMessageW(w, WM_ERASEBKGND, wp, 0);
			}
			RECT r;
			GetClientRect(w, &r);
			flow_size(w, c, r.right - px(pad(w, 1) + pad(w, 3), c->dpi), dc);
			if (msg != WM_PRINTCLIENT) {
				EndPaint(w, &ps);
			}
			return 0;
		}
		if (c && c->kind == K_CANVAS) {
			PAINTSTRUCT ps;
			HDC dc = msg == WM_PRINTCLIENT ? (HDC)wp : BeginPaint(w, &ps);
			paint_canvas(w, c, dc);
			if (msg != WM_PRINTCLIENT) {
				EndPaint(w, &ps);
			}
			return 0;
		}
		if (c && c->kind == K_PICTURE) {
			PAINTSTRUCT ps;
			HDC dc = msg == WM_PRINTCLIENT ? (HDC)wp : BeginPaint(w, &ps);
			paint_picture(w, c, dc);
			if (msg != WM_PRINTCLIENT) {
				EndPaint(w, &ps);
			}
			return 0;
		}
		if (c && c->framed) {
			PAINTSTRUCT ps;
			HDC dc = msg == WM_PRINTCLIENT ? (HDC)wp : BeginPaint(w, &ps);
			if (msg == WM_PRINTCLIENT) {
				/* PrintWindow erases through this DC too. */
				SendMessageW(w, WM_ERASEBKGND, wp, 0);
			}
			RECT r;
			GetClientRect(w, &r);
			DrawEdge(dc, &r, EDGE_ETCHED, BF_RECT);
			if (msg != WM_PRINTCLIENT) {
				EndPaint(w, &ps);
			}
			return 0;
		}
		break;
	case WM_LBUTTONUP:
		if (c && c->kind == K_FLOW) {
			/* A link's words open where it leads. */
			ctl *link = ctl_of(link_under(c, (short)LOWORD(lp), (short)HIWORD(lp)));
			if (link && link->href && link->href[0]) {
				ShellExecuteW(NULL, L"open", link->href, NULL, NULL, SW_SHOWNORMAL);
			}
			return 0;
		}
		/* A box with a click handler, a clickable card, is pressed as a
		 * button is, wherever in it the click lands: on a box it holds,
		 * or a picture, which lets the click through to its image. */
		if (c && c->kind != K_TOP) {
			press(w);
			return 0;
		}
		break;
	case WM_SETCURSOR:
		if (c && c->click.fn && LOWORD(lp) == HTCLIENT) {
			SetCursor(LoadCursorW(NULL, (LPCWSTR)IDC_HAND));
			return TRUE;
		}
		break;
	case WM_SIZE:
		if (c && c->kind == K_TOP) {
			lay_out_top(w);
		}
		return 0;
	case WM_NOTIFY: {
		NMHDR *nm = (NMHDR *)lp;
		ctl *src = ctl_of(nm->hwndFrom);
		if (!src) {
			break;
		}
		switch (src->kind) {
		case K_TABS:
			if (nm->code == TCN_SELCHANGE) {
				if (hooks.tab_chosen) {
					hooks.tab_chosen(nm->hwndFrom);
				}
				return 0;
			}
			break;
		case K_TABLE: {
			NMLISTVIEW *lv = (NMLISTVIEW *)lp;
			if (nm->code == LVN_ITEMCHANGING && (lv->uChanged & LVIF_STATE) && (lv->uNewState & LVIS_SELECTED) && !src->selectable && !src->quiet) {
				/* A table no row of which can be selected refuses it. */
				return TRUE;
			}
			if (nm->code == LVN_ITEMCHANGED && (lv->uChanged & LVIF_STATE) && (lv->uNewState & LVIS_SELECTED) && !(lv->uOldState & LVIS_SELECTED) && !src->quiet) {
				src->row = lv->iItem;
				fire(&src->change);
				return 0;
			}
			if (nm->code == LVN_COLUMNCLICK && src->sortable) {
				src->sort_col = lv->iSubItem;
				fire(&src->click);
				return 0;
			}
			break;
		}
		case K_TREE:
			if (nm->code == TVN_SELCHANGEDW && !src->quiet) {
				fire(&src->change);
				return 0;
			}
			if (nm->code == TVN_ITEMEXPANDEDW && !src->quiet) {
				fire(&src->activate);
				relayout(nm->hwndFrom);
				return 0;
			}
			break;
		case K_DATE:
			if (nm->code == DTN_DATETIMECHANGE && !src->quiet) {
				fire(&src->change);
				return 0;
			}
			break;
		default:
			break;
		}
		break;
	}
	case WM_VSCROLL:
	case WM_HSCROLL:
		if (c && c->kind == K_SCROLL) {
			if (hooks.scrolled) {
				hooks.scrolled(w, msg == WM_VSCROLL ? SB_VERT : SB_HORZ, LOWORD(wp));
			}
			return 0;
		}
		break;
	case WM_MOUSEWHEEL:
		if (c && c->kind == K_SCROLL) {
			/* What a precision touchpad sends a notch in pieces is
			 * added up until it makes a line. */
			if (hooks.wheel) {
				hooks.wheel(w, GET_WHEEL_DELTA_WPARAM(wp));
			}
			return 0;
		}
		break;
	case SNGL_WM_REACHED:
		if (c) {
			fire(&c->reach);
		}
		return 0;
	case SNGL_WM_OPEN:
		if (c && c->kind == K_MODAL) {
			show_modal(w, c);
		}
		return 0;
	case SNGL_WM_POST: {
		handler *h = (handler *)lp;
		fire(h);
		drop_handler(h);
		free(h);
		return 0;
	}
	case WM_TIMER:
		if (w == park) {
			tick(wp);
			return 0;
		}
		break;
	case SNGL_WM_LAYOUT:
		if (c && hooks.layout) {
			hooks.layout(w);
		}
		return 0;
	case WM_DPICHANGED:
		/* Moved to a monitor of another DPI: the size Windows suggests,
		 * and everything in it measured again at the new one. */
		if (c && c->kind == K_TOP) {
			RECT *r = (RECT *)lp;
			refresh(w, HIWORD(wp));
			SetWindowPos(w, NULL, r->left, r->top, r->right - r->left, r->bottom - r->top, SWP_NOZORDER | SWP_NOACTIVATE);
			lay_out_top(w);
			return 0;
		}
		break;
	case WM_CLOSE:
		if (c && c->modal_of) {
			/* A modal's close is the program's to answer, by its open. */
			ctl *m = ctl_of(c->modal_of);
			if (m && m->closable) {
				fire(&m->closed);
			}
			return 0;
		}
		if (c && c->kind == K_TOP) {
			sngl_win32_set_visible(w, 0);
			/* The program's write-back of :visible may take the window
			 * away, and its record with it: @closed is held out of the
			 * record while the two run, and handed back to it if it is
			 * still there. */
			handler closed = c->closed;
			memset(&c->closed, 0, sizeof c->closed);
			if (c->visible.fn) {
				((sngl_win32_bool_fn)(void (*)(void))c->visible.fn)(0, c->visible.ctx);
			}
			fire(&closed);
			ctl *still = IsWindow(w) ? ctl_of(w) : NULL;
			if (still && !still->closed.fn) {
				still->closed = closed;
			} else {
				drop_handler(&closed);
			}
			return 0;
		}
		break;
	case WM_NCDESTROY:
		if (c && c->kind == K_TOP && !c->modal_of && IsWindowVisible(w)) {
			shown--;
		}
		forget(w);
		break;
	}
	return DefWindowProcW(w, msg, wp, lp);
}

/* shade is rgb, 0xRRGGBB, darkened by a share of 256. */
static COLORREF shade(int rgb, int by) {
	int r = (rgb >> 16) & 0xff, g = (rgb >> 8) & 0xff, b = rgb & 0xff;
	return RGB(r * (256 - by) / 256, g * (256 - by) / 256, b * (256 - by) / 256);
}

/* paint_button draws a button given a background, which a push button's
 * theme paints over: flat in its background, darker while pressed, as
 * gtk4's shades one under :active, and its text centered in its color. */
static void paint_button(DRAWITEMSTRUCT *di, ctl *c) {
	HDC dc = di->hDC;
	RECT r = di->rcItem;
	int pressed = (di->itemState & ODS_SELECTED) != 0;
	HBRUSH fill = pressed ? CreateSolidBrush(shade(c->background, 48)) : NULL;
	FillRect(dc, &r, fill ? fill : c->brush);
	if (fill) {
		DeleteObject(fill);
	}
	WCHAR stack[256];
	int n = GetWindowTextLengthW(di->hwndItem);
	WCHAR *buf = n < 256 ? stack : malloc(sizeof(WCHAR) * (size_t)(n + 1));
	GetWindowTextW(di->hwndItem, buf, n + 1);
	HGDIOBJ old = SelectObject(dc, font_of(c));
	SetBkMode(dc, TRANSPARENT);
	if (di->itemState & ODS_DISABLED) {
		SetTextColor(dc, GetSysColor(COLOR_GRAYTEXT));
	} else if (c->color >= 0) {
		SetTextColor(dc, RGB((c->color >> 16) & 0xff, (c->color >> 8) & 0xff, c->color & 0xff));
	} else {
		SetTextColor(dc, GetSysColor(COLOR_BTNTEXT));
	}
	DrawTextW(dc, buf, n, &r, DT_CENTER | DT_VCENTER | DT_SINGLELINE | DT_NOPREFIX);
	SelectObject(dc, old);
	if (buf != stack) {
		free(buf);
	}
	if ((di->itemState & ODS_FOCUS) && !(di->itemState & ODS_NOFOCUSRECT)) {
		RECT f = r;
		InflateRect(&f, -px(3, c->dpi), -px(3, c->dpi));
		DrawFocusRect(dc, &f);
	}
}

/* control_proc subclasses each control the runtime makes: Enter in an edit
 * is its activation, and a control going takes its record with it. */
static LRESULT CALLBACK control_proc(HWND w, UINT msg, WPARAM wp, LPARAM lp, UINT_PTR id, DWORD_PTR data) {
	(void)id;
	(void)data;
	switch (msg) {
	case WM_GETDLGCODE: {
		/* An edit taking Enter as its activation claims the key, which
		 * IsDialogMessage would take as the dialog's default button and
		 * send the window, where it does nothing. */
		ctl *c = ctl_of(w);
		MSG *m = (MSG *)lp;
		if (c && c->kind == K_EDIT && c->activate.fn && m && (m->message == WM_KEYDOWN || m->message == WM_CHAR) && (m->wParam == VK_RETURN || m->wParam == '\r')) {
			return DefSubclassProc(w, msg, wp, lp) | DLGC_WANTALLKEYS;
		}
		break;
	}
	case WM_KEYDOWN:
		if (wp == VK_RETURN) {
			ctl *c = ctl_of(w);
			if (c && c->kind == K_EDIT && c->activate.fn) {
				fire(&c->activate);
				return 0;
			}
		}
		break;
	case WM_CHAR:
		if (wp == '\r') {
			ctl *c = ctl_of(w);
			if (c && c->kind == K_EDIT) {
				/* No beep for the Enter the activation took. */
				return 0;
			}
		}
		break;
	case WM_SETCURSOR: {
		ctl *c = ctl_of(w);
		if (c && c->kind == K_LINK) {
			SetCursor(LoadCursorW(NULL, (LPCWSTR)IDC_HAND));
			return TRUE;
		}
		if (c && c->kind == K_FLOW) {
			POINT pt;
			GetCursorPos(&pt);
			ScreenToClient(w, &pt);
			if (link_under(c, pt.x, pt.y)) {
				SetCursor(LoadCursorW(NULL, (LPCWSTR)IDC_HAND));
				return TRUE;
			}
		}
		break;
	}
	case WM_PAINT:
	case WM_PRINTCLIENT: {
		/* A progress bar writes its text over what Win32 draws, as GTK's
		 * does; PrintWindow, a snapshot's, asks for the client area with
		 * WM_PRINTCLIENT and the DC it draws in. */
		ctl *c = ctl_of(w);
		if (!c || c->kind != K_PROGRESS || !c->show_text) {
			break;
		}
		LRESULT r = DefSubclassProc(w, msg, wp, lp);
		WCHAR buf[256];
		bar_text(w, c, buf, 256);
		HDC dc = msg == WM_PRINTCLIENT ? (HDC)wp : GetDC(w);
		RECT rc;
		GetClientRect(w, &rc);
		HGDIOBJ old = SelectObject(dc, font_of(c));
		SetBkMode(dc, TRANSPARENT);
		if (c->color >= 0) {
			SetTextColor(dc, RGB((c->color >> 16) & 0xff, (c->color >> 8) & 0xff, c->color & 0xff));
		}
		DrawTextW(dc, buf, -1, &rc, DT_CENTER | DT_VCENTER | DT_SINGLELINE | DT_NOPREFIX);
		SelectObject(dc, old);
		if (msg != WM_PRINTCLIENT) {
			ReleaseDC(w, dc);
		}
		return r;
	}
	case WM_NCDESTROY:
		RemoveWindowSubclass(w, control_proc, 1);
		forget(w);
		break;
	}
	return DefSubclassProc(w, msg, wp, lp);
}

/* --- Making nodes --- */

/* aware has the process draw at each monitor's own DPI where Windows can,
 * and at the system's where it cannot, rather than be scaled up blurred. */
static void aware(void) {
	HMODULE user32 = GetModuleHandleW(L"user32.dll");
	BOOL(WINAPI * per_monitor)(HANDLE) = NULL;
	if (user32) {
		per_monitor = (BOOL(WINAPI *)(HANDLE))(void (*)(void))GetProcAddress(user32, "SetProcessDpiAwarenessContext");
		dpi_for_window = (UINT(WINAPI *)(HWND))(void (*)(void))GetProcAddress(user32, "GetDpiForWindow");
		metrics_for_dpi = (BOOL(WINAPI *)(UINT, UINT, PVOID, UINT, UINT))(void (*)(void))GetProcAddress(user32, "SystemParametersInfoForDpi");
		system_metric_for_dpi = (int(WINAPI *)(int, UINT))(void (*)(void))GetProcAddress(user32, "GetSystemMetricsForDpi");
	}
	/* DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2, which older headers lack. */
	if (!per_monitor || !per_monitor((HANDLE)(INT_PTR)-4)) {
		SetProcessDPIAware();
		dpi_for_window = NULL;
	}
	HDC dc = GetDC(NULL);
	if (dc) {
		system_dpi = GetDeviceCaps(dc, LOGPIXELSY);
		ReleaseDC(NULL, dc);
	}
}

/* The manifest asking for Common Controls 6, whose controls draw in the
 * system's theme where 5's draw as Windows 95 did. */
static const char common_controls[] =
	"<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?>"
	"<assembly xmlns=\"urn:schemas-microsoft-com:asm.v1\" manifestVersion=\"1.0\">"
	"<dependency><dependentAssembly><assemblyIdentity type=\"win32\""
	" name=\"Microsoft.Windows.Common-Controls\" version=\"6.0.0.0\""
	" processorArchitecture=\"*\" publicKeyToken=\"6595b64144ccf1df\" language=\"*\"/>"
	"</dependentAssembly></dependency></assembly>";

/* common_controls_v6 activates the manifest for the rest of the process, so
 * every control made after is 6's. An activation context is made from a
 * file or a module's resource only, and a resource needs a step in every
 * language's build, so the manifest is written to a file of its own and
 * the file removed once read. */
static void common_controls_v6(void) {
	WCHAR dir[MAX_PATH], path[MAX_PATH];
	if (!GetTempPathW(MAX_PATH, dir) || !GetTempFileNameW(dir, L"sngl", 0, path)) {
		return;
	}
	HANDLE f = CreateFileW(path, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS, FILE_ATTRIBUTE_TEMPORARY, NULL);
	if (f == INVALID_HANDLE_VALUE) {
		return;
	}
	DWORD wrote = 0;
	BOOL ok = WriteFile(f, common_controls, sizeof common_controls - 1, &wrote, NULL);
	CloseHandle(f);
	if (ok) {
		ACTCTXW ac;
		memset(&ac, 0, sizeof ac);
		ac.cbSize = sizeof ac;
		ac.lpSource = path;
		HANDLE ctx = CreateActCtxW(&ac);
		ULONG_PTR cookie;
		if (ctx != INVALID_HANDLE_VALUE) {
			ActivateActCtx(ctx, &cookie);
		}
	}
	DeleteFileW(path);
}

void sngl_win32_init(void) {
	if (ready) {
		return;
	}
	ready = 1;
	CoInitializeEx(NULL, COINIT_APARTMENTTHREADED);
	aware();
	common_controls_v6();
	INITCOMMONCONTROLSEX icc = {sizeof icc, ICC_STANDARD_CLASSES | ICC_PROGRESS_CLASS | ICC_TAB_CLASSES | ICC_BAR_CLASSES | ICC_LISTVIEW_CLASSES | ICC_TREEVIEW_CLASSES | ICC_DATE_CLASSES};
	InitCommonControlsEx(&icc);
	HINSTANCE inst = GetModuleHandleW(NULL);
	WNDCLASSEXW wc;
	memset(&wc, 0, sizeof wc);
	wc.cbSize = sizeof wc;
	wc.lpfnWndProc = box_proc;
	wc.hInstance = inst;
	wc.hCursor = LoadCursorW(NULL, (LPCWSTR)IDC_ARROW);
	wc.hbrBackground = (HBRUSH)(COLOR_BTNFACE + 1);
	wc.lpszClassName = SNGL_BOX_CLASS;
	RegisterClassExW(&wc);
	wc.lpszClassName = SNGL_TOP_CLASS;
	wc.hIcon = LoadIconW(NULL, (LPCWSTR)IDI_APPLICATION);
	RegisterClassExW(&wc);
	park = CreateWindowExW(0, SNGL_BOX_CLASS, L"", WS_POPUP, 0, 0, 0, 0, NULL, NULL, inst, NULL);
}

/* new_ctl gives w its record, or NULL where there is no w: Win32 made no
 * window, and each call on one is then a call on NULL, which Win32
 * refuses. */
static ctl *new_ctl(HWND w, enum sngl_kind kind) {
	if (!w) {
		return NULL;
	}
	ctl *c = calloc(1, sizeof *c);
	c->kind = kind;
	c->dpi = dpi_of(w);
	c->color = c->background = -1;
	c->self = w;
	ctl_put(w, c);
	if (hooks.made) {
		hooks.made(w, (int)kind, c->dpi);
	}
	return c;
}

static HWND control(LPCWSTR class, DWORD style, DWORD ex, enum sngl_kind kind) {
	sngl_win32_init();
	/* Each clips its siblings, so a stack's layers paint only where they
	 * show. */
	HWND w = CreateWindowExW(ex, class, L"", WS_CHILD | WS_VISIBLE | WS_CLIPSIBLINGS | style, 0, 0, 0, 0, park, NULL, GetModuleHandleW(NULL), NULL);
	new_ctl(w, kind);
	SendMessageW(w, WM_SETFONT, (WPARAM)font_of(ctl_of(w)), FALSE);
	if (kind != K_BOX && kind != K_SPACER) {
		SetWindowSubclass(w, control_proc, 1, 0);
	}
	return w;
}

HWND sngl_win32_toplevel(void) {
	sngl_win32_init();
	HWND w = CreateWindowExW(WS_EX_CONTROLPARENT, SNGL_TOP_CLASS, L"", WS_OVERLAPPEDWINDOW | WS_CLIPCHILDREN, CW_USEDEFAULT, CW_USEDEFAULT, px(400, system_dpi), px(300, system_dpi), NULL, NULL, GetModuleHandleW(NULL), NULL);
	new_ctl(w, K_TOP);
	return w;
}

HWND sngl_win32_box(void) { return control(SNGL_BOX_CLASS, WS_CLIPCHILDREN, WS_EX_CONTROLPARENT, K_BOX); }

/* sngl_win32_divider_style draws a divider down or across. */
void sngl_win32_divider_style(HWND w, int vertical) {
	LONG_PTR style = GetWindowLongPtrW(w, GWL_STYLE) & ~(LONG_PTR)(SS_ETCHEDHORZ | SS_ETCHEDVERT);
	SetWindowLongPtrW(w, GWL_STYLE, style | (vertical ? SS_ETCHEDVERT : SS_ETCHEDHORZ));
	InvalidateRect(w, NULL, TRUE);
}

void sngl_win32_set_framed(HWND box, int framed) {
	ctl *c = ctl_of(box);
	if (c) {
		c->framed = framed != 0;
		InvalidateRect(box, NULL, TRUE);
	}
}

/* sngl_win32_show shows or hides w, and answers whether it was shown or
 * hidden otherwise, by Win32 or its own style. */
int sngl_win32_show(HWND w, int on) {
	if ((IsWindowVisible(w) != 0) != (on != 0) || (GetWindowLongPtrW(w, GWL_STYLE) & WS_VISIBLE) != (on ? WS_VISIBLE : 0)) {
		ShowWindow(w, on ? SW_SHOWNA : SW_HIDE);
		return 1;
	}
	return 0;
}

/* sngl_win32_scroll_style gives a scroll the bars of the axes it scrolls
 * along. */
void sngl_win32_scroll_style(HWND w, int vertical, int horizontal) {
	LONG_PTR style = GetWindowLongPtrW(w, GWL_STYLE) & ~(LONG_PTR)(WS_VSCROLL | WS_HSCROLL);
	SetWindowLongPtrW(w, GWL_STYLE, style | (vertical ? WS_VSCROLL : 0) | (horizontal ? WS_HSCROLL : 0));
	SetWindowPos(w, NULL, 0, 0, 0, 0, SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE | SWP_FRAMECHANGED);
}

void sngl_win32_set_tip(HWND box, const unsigned short *tip) {
	ctl *c = ctl_of(box);
	if (!c) {
		return;
	}
	free(c->tip);
	c->tip = _wcsdup((const WCHAR *)tip);
	for (int i = 0, n = nkids(box); i < n; i++) {
		tip_tool(box, kid(box, i), c->tip);
	}
}

void sngl_win32_set_open(HWND m, int open) {
	ctl *c = ctl_of(m);
	if (c && c->kind == K_MODAL) {
		c->open = open != 0;
		/* Applied once the modal is in the window it shows over. */
		PostMessageW(m, SNGL_WM_OPEN, 0, 0);
	}
}

int sngl_win32_is_open(HWND m) {
	ctl *c = ctl_of(m);
	return c && c->dialog && IsWindowVisible(c->dialog);
}

void sngl_win32_set_closable(HWND m, int closable) {
	ctl *c = ctl_of(m);
	if (!c || !c->dialog) {
		return;
	}
	c->closable = closable != 0;
	LONG_PTR style = GetWindowLongPtrW(c->dialog, GWL_STYLE) & ~(LONG_PTR)WS_SYSMENU;
	SetWindowLongPtrW(c->dialog, GWL_STYLE, style | (c->closable ? WS_SYSMENU : 0));
	SetWindowPos(c->dialog, NULL, 0, 0, 0, 0, SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE | SWP_FRAMECHANGED);
}

void sngl_win32_set_href(HWND w, const unsigned short *href) {
	ctl *c = ctl_of(w);
	if (c) {
		free(c->href);
		c->href = _wcsdup((const WCHAR *)href);
		/* A span's, which its flow draws as a link. */
		touched(w);
	}
}

HWND sngl_win32_text(void) { return control(L"STATIC", SS_LEFT | SS_NOPREFIX | SS_NOTIFY, 0, K_TEXT); }

HWND sngl_win32_button(void) { return control(L"BUTTON", BS_PUSHBUTTON | WS_TABSTOP, 0, K_BUTTON); }

HWND sngl_win32_edit(void) { return control(L"EDIT", ES_AUTOHSCROLL | WS_TABSTOP, WS_EX_CLIENTEDGE, K_EDIT); }

HWND sngl_win32_checkbox(void) { return control(L"BUTTON", BS_AUTOCHECKBOX | WS_TABSTOP, 0, K_CHECK); }

HWND sngl_win32_spacer(void) { return control(SNGL_BOX_CLASS, 0, 0, K_SPACER); }

HWND sngl_win32_progress(void) {
	HWND w = control(PROGRESS_CLASSW, 0, 0, K_PROGRESS);
	SendMessageW(w, PBM_SETRANGE32, 0, 10000);
	return w;
}

HWND sngl_win32_textarea(void) { return control(L"EDIT", ES_MULTILINE | ES_AUTOVSCROLL | ES_WANTRETURN | WS_VSCROLL | WS_TABSTOP, WS_EX_CLIENTEDGE, K_AREA); }

HWND sngl_win32_divider(void) { return control(L"STATIC", SS_ETCHEDHORZ, 0, K_DIVIDER); }

HWND sngl_win32_link(void) {
	HWND w = control(L"STATIC", SS_LEFT | SS_NOPREFIX | SS_NOTIFY, 0, K_LINK);
	ctl *c = ctl_of(w);
	if (c) {
		own_font(c);
		SendMessageW(w, WM_SETFONT, (WPARAM)font_of(c), FALSE);
	}
	return w;
}

HWND sngl_win32_tabs(void) { return control(WC_TABCONTROLW, WS_CLIPCHILDREN | WS_TABSTOP, WS_EX_CONTROLPARENT, K_TABS); }

HWND sngl_win32_scroll(void) { return control(SNGL_BOX_CLASS, WS_CLIPCHILDREN, WS_EX_CONTROLPARENT, K_SCROLL); }

HWND sngl_win32_modal(void) {
	HWND w = control(SNGL_BOX_CLASS, 0, 0, K_MODAL);
	ctl *c = ctl_of(w);
	if (!c) {
		return w;
	}
	ShowWindow(w, SW_HIDE);
	c->dialog = CreateWindowExW(WS_EX_DLGMODALFRAME | WS_EX_CONTROLPARENT, SNGL_TOP_CLASS, L"", WS_POPUP | WS_CAPTION | WS_CLIPCHILDREN, 0, 0, 0, 0, park, NULL, GetModuleHandleW(NULL), NULL);
	ctl *d = new_ctl(c->dialog, K_TOP);
	if (d) {
		d->modal_of = w;
	}
	return w;
}

/* sngl_win32_dialog is the window a modal shows what it holds in, and
 * sngl_win32_host the window what is put in parent goes in: a modal's
 * dialog, and otherwise parent. */
HWND sngl_win32_dialog(HWND m) {
	ctl *c = ctl_of(m);
	return c ? c->dialog : NULL;
}

HWND sngl_win32_host(HWND parent) {
	ctl *c = ctl_of(parent);
	return c && c->kind == K_MODAL ? c->dialog : parent;
}

HWND sngl_win32_entry(void) {
	HWND w = control(SNGL_BOX_CLASS, 0, 0, K_ENTRY);
	ctl *c = ctl_of(w);
	if (c) {
		ShowWindow(w, SW_HIDE);
	}
	return w;
}

HWND sngl_win32_table(void) {
	HWND w = control(WC_LISTVIEWW, LVS_REPORT | LVS_SINGLESEL | LVS_SHOWSELALWAYS | LVS_NOSORTHEADER | WS_TABSTOP, WS_EX_CLIENTEDGE, K_TABLE);
	SendMessageW(w, LVM_SETEXTENDEDLISTVIEWSTYLE, 0, LVS_EX_FULLROWSELECT | LVS_EX_GRIDLINES);
	ctl *c = ctl_of(w);
	if (c) {
		c->row = -1;
	}
	return w;
}

HWND sngl_win32_tree(void) { return control(WC_TREEVIEWW, TVS_HASLINES | TVS_LINESATROOT | TVS_HASBUTTONS | TVS_SHOWSELALWAYS | WS_TABSTOP, WS_EX_CLIENTEDGE, K_TREE); }

HWND sngl_win32_date(void) { return control(DATETIMEPICK_CLASSW, DTS_SHORTDATEFORMAT | DTS_SHOWNONE | WS_TABSTOP, 0, K_DATE); }

/* split calls each with each piece of text between seps, and its length. */
static void split(const WCHAR *text, WCHAR sep, void (*each)(const WCHAR *piece, size_t n, void *arg), void *arg) {
	if (!*text) {
		return;
	}
	for (;;) {
		const WCHAR *end = wcschr(text, sep);
		size_t n = end ? (size_t)(end - text) : wcslen(text);
		each(text, n, arg);
		if (!end) {
			return;
		}
		text = end + 1;
	}
}

static WCHAR *dup_n(const WCHAR *s, size_t n) {
	WCHAR *out = malloc(sizeof(WCHAR) * (n + 1));
	memcpy(out, s, sizeof(WCHAR) * n);
	out[n] = 0;
	return out;
}

static void add_column(const WCHAR *label, size_t n, void *arg) {
	HWND w = arg;
	int at = (int)SendMessageW((HWND)SendMessageW(w, LVM_GETHEADER, 0, 0), HDM_GETITEMCOUNT, 0, 0);
	LVCOLUMNW col;
	memset(&col, 0, sizeof col);
	col.mask = LVCF_TEXT | LVCF_WIDTH | LVCF_SUBITEM;
	col.pszText = dup_n(label, n);
	col.cx = 60;
	col.iSubItem = at;
	SendMessageW(w, LVM_INSERTCOLUMNW, (WPARAM)at, (LPARAM)&col);
	free(col.pszText);
}

typedef struct {
	HWND w;
	int row, col;
} cell_at;

static void add_cell(const WCHAR *text, size_t n, void *arg) {
	cell_at *at = arg;
	LVITEMW it;
	memset(&it, 0, sizeof it);
	it.mask = LVIF_TEXT;
	it.iItem = at->row;
	it.iSubItem = at->col;
	it.pszText = dup_n(text, n);
	if (at->col == 0) {
		SendMessageW(at->w, LVM_INSERTITEMW, 0, (LPARAM)&it);
	} else {
		SendMessageW(at->w, LVM_SETITEMTEXTW, (WPARAM)at->row, (LPARAM)&it);
	}
	free(it.pszText);
	at->col++;
}

static void add_row(const WCHAR *line, size_t n, void *arg) {
	cell_at *at = arg;
	WCHAR *row = dup_n(line, n);
	at->col = 0;
	if (n == 0) {
		add_cell(row, 0, at);
	}
	split(row, L'\t', add_cell, at);
	free(row);
	at->row++;
}

/* fit_columns makes each of a table's columns as wide as its header or its
 * widest cell, whichever is wider. */
static void fit_columns(HWND w, ctl *c) {
	int cols = (int)SendMessageW((HWND)SendMessageW(w, LVM_GETHEADER, 0, 0), HDM_GETITEMCOUNT, 0, 0);
	int rows = (int)SendMessageW(w, LVM_GETITEMCOUNT, 0, 0);
	HDC dc = GetDC(w);
	HGDIOBJ old = SelectObject(dc, font_of(c));
	for (int i = 0; i < cols; i++) {
		WCHAR buf[256];
		int wide = 0;
		for (int r = -1; r < rows; r++) {
			buf[0] = 0;
			if (r < 0) {
				LVCOLUMNW col;
				memset(&col, 0, sizeof col);
				col.mask = LVCF_TEXT;
				col.pszText = buf;
				col.cchTextMax = 256;
				SendMessageW(w, LVM_GETCOLUMNW, (WPARAM)i, (LPARAM)&col);
			} else {
				LVITEMW it;
				memset(&it, 0, sizeof it);
				it.iSubItem = i;
				it.pszText = buf;
				it.cchTextMax = 256;
				SendMessageW(w, LVM_GETITEMTEXTW, (WPARAM)r, (LPARAM)&it);
			}
			RECT tr = {0, 0, 0, 0};
			DrawTextW(dc, buf, -1, &tr, DT_CALCRECT | DT_NOPREFIX | DT_SINGLELINE);
			wide = tr.right > wide ? tr.right : wide;
		}
		SendMessageW(w, LVM_SETCOLUMNWIDTH, (WPARAM)i, wide + px(16, c->dpi));
	}
	SelectObject(dc, old);
	ReleaseDC(w, dc);
}

/* select_row selects a table's row at, or none where it is negative,
 * reporting nothing. */
static void select_row(HWND w, ctl *c, int at) {
	c->quiet++;
	LVITEMW it;
	memset(&it, 0, sizeof it);
	it.stateMask = LVIS_SELECTED | LVIS_FOCUSED;
	SendMessageW(w, LVM_SETITEMSTATE, (WPARAM)-1, (LPARAM)&it);
	if (at >= 0) {
		it.state = LVIS_SELECTED | LVIS_FOCUSED;
		SendMessageW(w, LVM_SETITEMSTATE, (WPARAM)at, (LPARAM)&it);
		SendMessageW(w, LVM_ENSUREVISIBLE, (WPARAM)at, FALSE);
	}
	c->quiet--;
}

void sngl_win32_fill_table(HWND w, const unsigned short *columns, const unsigned short *rows) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	c->quiet++;
	SendMessageW(w, LVM_DELETEALLITEMS, 0, 0);
	while (SendMessageW(w, LVM_DELETECOLUMN, 0, 0)) {
	}
	split((const WCHAR *)columns, L'\n', add_column, w);
	cell_at at = {w, 0, 0};
	split((const WCHAR *)rows, L'\n', add_row, &at);
	fit_columns(w, c);
	select_row(w, c, c->row);
	c->quiet--;
	relayout(w);
}

void sngl_win32_set_row(HWND w, int row) {
	ctl *c = ctl_of(w);
	if (c) {
		c->row = row;
		select_row(w, c, row);
	}
}

int sngl_win32_row(HWND w) {
	ctl *c = ctl_of(w);
	return c ? c->row : -1;
}

void sngl_win32_set_table(HWND w, int selectable, int sortable) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	c->selectable = selectable != 0;
	c->sortable = sortable != 0;
	LONG_PTR style = GetWindowLongPtrW(w, GWL_STYLE) & ~(LONG_PTR)LVS_NOSORTHEADER;
	SetWindowLongPtrW(w, GWL_STYLE, style | (c->sortable ? 0 : LVS_NOSORTHEADER));
}

const char *sngl_win32_sorted(HWND w) {
	ctl *c = ctl_of(w);
	WCHAR buf[256] = {0};
	LVCOLUMNW col;
	memset(&col, 0, sizeof col);
	col.mask = LVCF_TEXT;
	col.pszText = buf;
	col.cchTextMax = 256;
	if (c) {
		SendMessageW(w, LVM_GETCOLUMNW, (WPARAM)c->sort_col, (LPARAM)&col);
	}
	return utf8(buf);
}

typedef struct {
	HWND w;
	HTREEITEM parents[64];
	int index;
	const WCHAR *expanded;
	HTREEITEM *open;
	int nopen;
} tree_fill;

/* listed is whether n is among the numbers list holds, comma separated. */
static int listed(const WCHAR *list, int n) {
	for (const WCHAR *at = list; *at;) {
		if (_wtoi(at) == n) {
			return 1;
		}
		const WCHAR *comma = wcschr(at, L',');
		if (!comma) {
			break;
		}
		at = comma + 1;
	}
	return 0;
}

static void add_node(const WCHAR *line, size_t n, void *arg) {
	tree_fill *f = arg;
	size_t depth = 0;
	while (depth < n && line[depth] == L'\t') {
		depth++;
	}
	if (depth > 62) {
		depth = 62;
	}
	TVINSERTSTRUCTW ins;
	memset(&ins, 0, sizeof ins);
	ins.hParent = depth == 0 ? TVI_ROOT : f->parents[depth - 1];
	ins.hInsertAfter = TVI_LAST;
	ins.item.mask = TVIF_TEXT;
	ins.item.pszText = dup_n(line + depth, n - depth);
	HTREEITEM it = (HTREEITEM)SendMessageW(f->w, TVM_INSERTITEMW, 0, (LPARAM)&ins);
	free(ins.item.pszText);
	f->parents[depth] = it;
	if (listed(f->expanded, f->index)) {
		f->open = realloc(f->open, sizeof(HTREEITEM) * (size_t)(f->nopen + 1));
		f->open[f->nopen++] = it;
	}
	f->index++;
}

/* tree_find is the item of w under from, or from itself, labeled label. */
static HTREEITEM tree_find(HWND w, HTREEITEM from, const WCHAR *label) {
	for (HTREEITEM it = from; it; it = (HTREEITEM)SendMessageW(w, TVM_GETNEXTITEM, TVGN_NEXT, (LPARAM)it)) {
		WCHAR buf[256];
		TVITEMW ti;
		memset(&ti, 0, sizeof ti);
		ti.mask = TVIF_TEXT;
		ti.hItem = it;
		ti.pszText = buf;
		ti.cchTextMax = 256;
		SendMessageW(w, TVM_GETITEMW, 0, (LPARAM)&ti);
		if (wcscmp(buf, label) == 0) {
			return it;
		}
		HTREEITEM in = tree_find(w, (HTREEITEM)SendMessageW(w, TVM_GETNEXTITEM, TVGN_CHILD, (LPARAM)it), label);
		if (in) {
			return in;
		}
	}
	return NULL;
}

HTREEITEM sngl_win32_tree_find(HWND w, const unsigned short *label) {
	return tree_find(w, (HTREEITEM)SendMessageW(w, TVM_GETNEXTITEM, TVGN_ROOT, 0), (const WCHAR *)label);
}

static void tree_select(HWND w, ctl *c) {
	HTREEITEM it = c->chosen && c->chosen[0] ? sngl_win32_tree_find(w, (const unsigned short *)c->chosen) : NULL;
	c->quiet++;
	SendMessageW(w, TVM_SELECTITEM, TVGN_CARET, (LPARAM)it);
	c->quiet--;
}

void sngl_win32_fill_tree(HWND w, const unsigned short *lines, const unsigned short *expanded) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	c->quiet++;
	SendMessageW(w, TVM_DELETEITEM, 0, (LPARAM)TVI_ROOT);
	tree_fill f;
	memset(&f, 0, sizeof f);
	f.w = w;
	f.expanded = (const WCHAR *)expanded;
	split((const WCHAR *)lines, L'\n', add_node, &f);
	for (int i = 0; i < f.nopen; i++) {
		SendMessageW(w, TVM_EXPAND, TVE_EXPAND, (LPARAM)f.open[i]);
	}
	free(f.open);
	tree_select(w, c);
	c->quiet--;
	relayout(w);
}

void sngl_win32_set_chosen(HWND w, const unsigned short *label) {
	ctl *c = ctl_of(w);
	if (c) {
		free(c->chosen);
		c->chosen = _wcsdup((const WCHAR *)label);
		tree_select(w, c);
	}
}

const char *sngl_win32_chosen(HWND w) {
	WCHAR buf[256] = {0};
	HTREEITEM it = (HTREEITEM)SendMessageW(w, TVM_GETNEXTITEM, TVGN_CARET, 0);
	if (it) {
		TVITEMW ti;
		memset(&ti, 0, sizeof ti);
		ti.mask = TVIF_TEXT;
		ti.hItem = it;
		ti.pszText = buf;
		ti.cchTextMax = 256;
		SendMessageW(w, TVM_GETITEMW, 0, (LPARAM)&ti);
	}
	return utf8(buf);
}

/* iso_date reads "YYYY-MM-DD" into *st, or answers zero. */
static int iso_date(const WCHAR *iso, SYSTEMTIME *st) {
	int y, m, d;
	if (!iso || swscanf(iso, L"%d-%d-%d", &y, &m, &d) != 3) {
		return 0;
	}
	memset(st, 0, sizeof *st);
	st->wYear = (WORD)y;
	st->wMonth = (WORD)m;
	st->wDay = (WORD)d;
	return 1;
}

void sngl_win32_set_date(HWND w, const unsigned short *iso) {
	ctl *c = ctl_of(w);
	SYSTEMTIME st;
	if (c) {
		c->quiet++;
	}
	if (iso_date((const WCHAR *)iso, &st)) {
		SendMessageW(w, DTM_SETSYSTEMTIME, GDT_VALID, (LPARAM)&st);
	} else {
		/* Windows ignores the time with GDT_NONE; Wine refuses a NULL one. */
		memset(&st, 0, sizeof st);
		SendMessageW(w, DTM_SETSYSTEMTIME, GDT_NONE, (LPARAM)&st);
	}
	if (c) {
		c->quiet--;
	}
}

void sngl_win32_set_date_range(HWND w, const unsigned short *min, const unsigned short *max) {
	SYSTEMTIME st[2];
	WPARAM has = 0;
	if (iso_date((const WCHAR *)min, &st[0])) {
		has |= GDTR_MIN;
	}
	if (iso_date((const WCHAR *)max, &st[1])) {
		has |= GDTR_MAX;
	}
	SendMessageW(w, DTM_SETRANGE, has, (LPARAM)st);
}

HWND sngl_win32_image(void) { return control(SNGL_BOX_CLASS, WS_CLIPCHILDREN, 0, K_IMAGE); }

HWND sngl_win32_picture(void) { return control(SNGL_BOX_CLASS, 0, 0, K_PICTURE); }

HWND sngl_win32_flow(void) {
	HWND w = control(SNGL_BOX_CLASS, 0, 0, K_FLOW);
	ctl *c = ctl_of(w);
	if (c) {
		c->sp_color = -1;
	}
	return w;
}

/* A span is a window never shown: what a flow draws is read off it. */
HWND sngl_win32_span(void) {
	sngl_win32_init();
	HWND w = CreateWindowExW(0, SNGL_BOX_CLASS, L"", WS_CHILD, 0, 0, 0, 0, park, NULL, GetModuleHandleW(NULL), NULL);
	ctl *c = new_ctl(w, K_SPAN);
	if (c) {
		c->sp_color = -1;
	}
	return w;
}

/* touched draws the flow w is in again, and lays it out where its words now
 * wrap to another height. */
static void touched(HWND w) {
	for (HWND at = w; at; at = GetParent(at)) {
		ctl *c = ctl_of(at);
		if (!c || (c->kind != K_SPAN && c->kind != K_FLOW)) {
			return;
		}
		if (c->kind == K_FLOW) {
			InvalidateRect(at, NULL, TRUE);
			relayout(at);
			return;
		}
	}
}

static WCHAR *dup16(const unsigned short *s) { return s && s[0] ? _wcsdup((const WCHAR *)s) : NULL; }

void sngl_win32_set_words(HWND w, const unsigned short *words) {
	ctl *c = ctl_of(w);
	if (c) {
		free(c->words);
		c->words = dup16(words);
		touched(w);
	}
}

void sngl_win32_set_span_style(HWND w, int color, int size, int weight, int slant, int underline, int strike, const unsigned short *family) {
	ctl *c = ctl_of(w);
	if (c) {
		c->sp_color = color;
		c->sp_px = size;
		c->sp_weight = weight;
		c->sp_slant = slant;
		c->sp_under = underline;
		c->sp_strike = strike;
		free(c->family);
		c->family = dup16(family);
		touched(w);
	}
}

HWND sngl_win32_canvas(void) {
	gdiplus_start();
	return control(SNGL_BOX_CLASS, 0, 0, K_CANVAS);
}

static void canvas_size(HWND w, int *at, int v) {
	if (*at != v) {
		*at = v;
		InvalidateRect(w, NULL, FALSE);
		relayout(w);
	}
}

void sngl_win32_set_canvas_width(HWND w, int width) {
	ctl *c = ctl_of(w);
	if (c) {
		canvas_size(w, &c->cw, width);
	}
}

void sngl_win32_set_canvas_height(HWND w, int height) {
	ctl *c = ctl_of(w);
	if (c) {
		canvas_size(w, &c->ch, height);
	}
}

/* sngl_win32_set_scaling answers whether the scaling changed, and its
 * record's flex with it (_setScaling). */
int sngl_win32_set_scaling(HWND w, int mode) {
	ctl *c = ctl_of(w);
	if (c && c->scaling != mode) {
		c->scaling = mode;
		InvalidateRect(w, NULL, FALSE);
		return 1;
	}
	return 0;
}

void sngl_win32_on_paint(HWND w, sngl_win32_paint_fn fn, void *ctx, sngl_win32_release release) {
	ctl *c = ctl_of(w);
	if (c) {
		set_handler(&c->paint, (sngl_win32_fn)(void (*)(void))fn, ctx, release);
		InvalidateRect(w, NULL, FALSE);
	}
}

HWND sngl_win32_drawing(void) {
	gdiplus_start();
	return control(SNGL_BOX_CLASS, 0, 0, K_PICTURE);
}

static void drawing_size(HWND w, double *at, double v) {
	ctl *c = ctl_of(w);
	if (c && *at != v) {
		*at = v;
		render_drawing(w, c);
	}
}

void sngl_win32_set_drawing_width(HWND w, double width) {
	ctl *c = ctl_of(w);
	if (c) {
		drawing_size(w, &c->dw, width);
	}
}

void sngl_win32_set_drawing_height(HWND w, double height) {
	ctl *c = ctl_of(w);
	if (c) {
		drawing_size(w, &c->dh, height);
	}
}

void sngl_win32_drawing_on_paint(HWND w, sngl_win32_paint_fn fn, void *ctx, sngl_win32_release release) {
	ctl *c = ctl_of(w);
	if (c) {
		set_handler(&c->paint, (sngl_win32_fn)(void (*)(void))fn, ctx, release);
		render_drawing(w, c);
	}
}

void sngl_win32_drawing_redraw(HWND w) {
	ctl *c = ctl_of(w);
	if (c) {
		render_drawing(w, c);
	}
}

void sngl_win32_set_fit(HWND w, int fit) {
	ctl *c = ctl_of(w);
	if (c) {
		c->fit = fit;
		for (int i = 0, n = nkids(w); i < n; i++) {
			InvalidateRect(kid(w, i), NULL, TRUE);
		}
	}
}

void sngl_win32_set_url(HWND w, const unsigned short *url) {
	ctl *c = ctl_of(w);
	if (!c || (c->url && wcscmp(c->url, (const WCHAR *)url) == 0)) {
		return;
	}
	free(c->url);
	c->url = _wcsdup((const WCHAR *)url);
	load(w, c);
}

const char *sngl_win32_image_failure(HWND w) {
	ctl *c = ctl_of(w);
	return c && c->why ? c->why : "";
}

void sngl_win32_on_load(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release) {
	ctl *c = ctl_of(w);
	if (c) {
		set_handler(&c->load, fn, ctx, release);
	}
}

void sngl_win32_on_failed(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release) {
	ctl *c = ctl_of(w);
	if (c) {
		set_handler(&c->failed, fn, ctx, release);
	}
}

HWND sngl_win32_radio(void) { return control(L"BUTTON", BS_AUTORADIOBUTTON | WS_TABSTOP, 0, K_RADIO); }

HWND sngl_win32_select(void) { return control(WC_COMBOBOXW, CBS_DROPDOWNLIST | WS_VSCROLL | WS_TABSTOP, 0, K_SELECT); }

/* --- The tree --- */

/* sngl_win32_tip_child gives child, one of box's, box's tip, or takes it
 * away. */
void sngl_win32_tip_child(HWND box, HWND child, int on) {
	ctl *c = ctl_of(box);
	if (c && c->tip) {
		tip_tool(box, child, on ? c->tip : NULL);
	}
}

/* sngl_win32_nothing is no window, which a slot's anchor is in a container
 * keeping none. */
HWND sngl_win32_nothing(void) { return NULL; }

/* sngl_win32_show_attached shows a toplevel attach sized, or holds it back
 * while an @run handler has not called run, which node's :visible hears. */
void sngl_win32_show_attached(HWND top, HWND node) {
	ctl *c = ctl_of(node);
	if (holding) {
		/* Not shown yet, which the window's :visible hears. */
		held = realloc(held, sizeof(HWND) * (size_t)(nheld + 1));
		held[nheld++] = top;
		if (c && c->visible.fn) {
			((sngl_win32_bool_fn)(void (*)(void))c->visible.fn)(0, c->visible.ctx);
		}
	} else {
		sngl_win32_set_visible(top, 1);
	}
}

void sngl_win32_hold(void) { holding = 1; }

void sngl_win32_present(void) {
	holding = 0;
	for (int i = 0; i < nheld; i++) {
		if (IsWindow(held[i])) {
			ctl *c = ctl_of(held[i]);
			sngl_win32_set_visible(held[i], 1);
			if (c && c->visible.fn) {
				((sngl_win32_bool_fn)(void (*)(void))c->visible.fn)(1, c->visible.ctx);
			}
		}
	}
	free(held);
	held = NULL;
	nheld = 0;
}

void sngl_win32_detach(HWND top) {
	ctl *c = ctl_of(top);
	if (c && c->kind != K_TOP) {
		/* The window attach made it. */
		HWND made = GetAncestor(top, GA_ROOT);
		if (made != park) {
			top = made;
		}
	}
	/* Hidden first, so it is counted out of what is on screen: Windows
	 * hides a window it destroys before telling it, which no count sees. */
	ctl *t = ctl_of(top);
	if (t && t->kind == K_TOP && !t->modal_of) {
		sngl_win32_set_visible(top, 0);
	}
	DestroyWindow(top);
}

/* --- Props --- */

const char *sngl_win32_get_text(HWND w) {
	static char *buf;
	static int cap;
	int n = GetWindowTextLengthW(w);
	WCHAR *wide = malloc(sizeof(WCHAR) * (size_t)(n + 1));
	GetWindowTextW(w, wide, n + 1);
	int need = WideCharToMultiByte(CP_UTF8, 0, wide, -1, NULL, 0, NULL, NULL);
	if (need > cap) {
		cap = need;
		buf = realloc(buf, (size_t)cap);
	}
	WideCharToMultiByte(CP_UTF8, 0, wide, -1, buf, cap, NULL, NULL);
	free(wide);
	/* A multi-line edit ends each line with "\r\n", which SNGL writes "\n". */
	char *to = buf;
	for (char *from = buf; *from; from++) {
		if (*from != '\r') {
			*to++ = *from;
		}
	}
	*to = 0;
	return buf;
}

/* utf8 is wide as UTF-8, in a buffer the next call overwrites. */
static const char *utf8(const WCHAR *wide) {
	static char *buf;
	static int cap;
	int need = WideCharToMultiByte(CP_UTF8, 0, wide, -1, NULL, 0, NULL, NULL);
	if (need > cap) {
		cap = need;
		buf = realloc(buf, (size_t)cap);
	}
	WideCharToMultiByte(CP_UTF8, 0, wide, -1, buf, cap, NULL, NULL);
	return buf;
}

/* crlf is text with each "\n" a multi-line edit's "\r\n", which the caller
 * frees. */
static WCHAR *crlf(const WCHAR *text) {
	size_t n = 0, lf = 0;
	for (; text[n]; n++) {
		lf += text[n] == '\n';
	}
	WCHAR *out = malloc(sizeof(WCHAR) * (n + lf + 1)), *to = out;
	for (size_t i = 0; i < n; i++) {
		if (text[i] == '\n' && (i == 0 || text[i - 1] != '\r')) {
			*to++ = '\r';
		}
		*to++ = text[i];
	}
	*to = 0;
	return out;
}

/* set_quietly writes w's text without reporting a change, as a multi-line
 * edit's lines where it is one. */
static void set_quietly(HWND w, const WCHAR *text) {
	ctl *c = ctl_of(w);
	WCHAR *lines = c && c->kind == K_AREA ? crlf(text) : NULL;
	if (c) {
		c->quiet = 1;
	}
	SetWindowTextW(w, lines ? lines : text);
	if (c) {
		c->quiet = 0;
	}
	free(lines);
}

void sngl_win32_set_edit_text(HWND w, const unsigned short *text) {
	ctl *c = ctl_of(w);
	int n = GetWindowTextLengthW(w);
	WCHAR *now = malloc(sizeof(WCHAR) * (size_t)(n + 1));
	GetWindowTextW(w, now, n + 1);
	WCHAR *want = c && c->kind == K_AREA ? crlf((LPCWSTR)text) : NULL;
	int same = wcscmp(now, want ? want : (LPCWSTR)text) == 0;
	free(now);
	free(want);
	if (!same) {
		set_quietly(w, (LPCWSTR)text);
	}
}

void sngl_win32_set_rows(HWND w, int rows) {
	ctl *c = ctl_of(w);
	if (c && c->rows != rows) {
		c->rows = rows;
		relayout(w);
	}
}

void sngl_win32_set_fraction(HWND w, double fraction) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	c->fraction = fraction;
	LONG_PTR style = GetWindowLongPtrW(w, GWL_STYLE);
	if (fraction < 0) {
		SetWindowLongPtrW(w, GWL_STYLE, style | PBS_MARQUEE);
		SendMessageW(w, PBM_SETMARQUEE, TRUE, 30);
	} else {
		if (style & PBS_MARQUEE) {
			SendMessageW(w, PBM_SETMARQUEE, FALSE, 0);
			SetWindowLongPtrW(w, GWL_STYLE, style & ~(LONG_PTR)PBS_MARQUEE);
		}
		SendMessageW(w, PBM_SETPOS, (WPARAM)(fraction > 1 ? 10000 : (int)(fraction * 10000 + 0.5)), 0);
	}
	InvalidateRect(w, NULL, TRUE);
	if (c->show_text) {
		relayout(w);
	}
}

void sngl_win32_set_show_text(HWND w, int show) {
	ctl *c = ctl_of(w);
	if (c && c->show_text != (show != 0)) {
		c->show_text = show != 0;
		InvalidateRect(w, NULL, TRUE);
		relayout(w);
	}
}

int sngl_win32_find_choice(HWND w, const unsigned short *label) {
	int n = (int)SendMessageW(w, CB_GETCOUNT, 0, 0);
	for (int i = 0; i < n; i++) {
		int len = (int)SendMessageW(w, CB_GETLBTEXTLEN, (WPARAM)i, 0);
		WCHAR *buf = malloc(sizeof(WCHAR) * (size_t)(len + 1));
		SendMessageW(w, CB_GETLBTEXT, (WPARAM)i, (LPARAM)buf);
		int same = wcscmp(buf, (const WCHAR *)label) == 0;
		free(buf);
		if (same) {
			return i;
		}
	}
	return -1;
}

const char *sngl_win32_selected(HWND w) {
	int at = (int)SendMessageW(w, CB_GETCURSEL, 0, 0);
	if (at < 0) {
		return "";
	}
	int len = (int)SendMessageW(w, CB_GETLBTEXTLEN, (WPARAM)at, 0);
	WCHAR *buf = malloc(sizeof(WCHAR) * (size_t)(len + 1));
	SendMessageW(w, CB_GETLBTEXT, (WPARAM)at, (LPARAM)buf);
	const char *out = utf8(buf);
	free(buf);
	return out;
}

void sngl_win32_set_visible(HWND top, int visible) {
	int was = IsWindowVisible(top) != 0;
	if (was == (visible != 0)) {
		return;
	}
	ShowWindow(top, visible ? SW_SHOW : SW_HIDE);
	shown += visible ? 1 : -1;
	if (visible) {
		shown_last = top;
	}
}

HWND sngl_win32_shown(void) {
	if (shown_last && (!IsWindow(shown_last) || !IsWindowVisible(shown_last))) {
		shown_last = NULL;
	}
	return shown_last;
}

void sngl_win32_set_look(HWND w, sngl_win32_look look) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	/* What it asks of a layout is its record's (_look), which lays it out
	 * again after this. */
	int color = look.color, background = look.background, font_px = look.fontPx;
	c->color = color;
	if (c->brush) {
		DeleteObject(c->brush);
		c->brush = NULL;
	}
	c->background = background;
	if (background >= 0) {
		c->brush = CreateSolidBrush(RGB((background >> 16) & 0xff, (background >> 8) & 0xff, background & 0xff));
	}
	if (c->kind == K_BUTTON) {
		/* Its theme paints a push button whatever WM_CTLCOLORBTN
		 * answers, so one given a background is drawn by the runtime. */
		LONG_PTR style = GetWindowLongPtrW(w, GWL_STYLE) & ~(LONG_PTR)BS_TYPEMASK;
		SetWindowLongPtrW(w, GWL_STYLE, style | (c->brush ? BS_OWNERDRAW : BS_PUSHBUTTON));
	}
	if (font_px != c->font_px) {
		c->font_px = font_px;
		own_font(c);
		SendMessageW(w, WM_SETFONT, (WPARAM)font_of(c), TRUE);
	}
	/* What has no background of its own shows this one's. */
	RedrawWindow(w, NULL, NULL, RDW_INVALIDATE | RDW_ERASE | RDW_ALLCHILDREN);
}

/* --- Events --- */

void sngl_win32_on_click(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release) {
	ctl *c = ctl_of(w);
	if (c) {
		set_handler(&c->click, fn, ctx, release);
	}
}

void sngl_win32_on_change(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release) {
	ctl *c = ctl_of(w);
	if (c) {
		set_handler(&c->change, fn, ctx, release);
	}
}

void sngl_win32_on_activate(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release) {
	ctl *c = ctl_of(w);
	if (c) {
		set_handler(&c->activate, fn, ctx, release);
	}
}

void sngl_win32_on_closed(HWND top, sngl_win32_fn fn, void *ctx, sngl_win32_release release) {
	ctl *c = ctl_of(top);
	if (c) {
		set_handler(&c->closed, fn, ctx, release);
	}
}

void sngl_win32_on_reach_end(HWND w, sngl_win32_fn fn, void *ctx, sngl_win32_release release) {
	ctl *c = ctl_of(w);
	if (c) {
		set_handler(&c->reach, fn, ctx, release);
	}
}

void sngl_win32_on_visible(HWND top, sngl_win32_bool_fn fn, void *ctx, sngl_win32_release release) {
	ctl *c = ctl_of(top);
	if (c) {
		set_handler(&c->visible, (sngl_win32_fn)(void (*)(void))fn, ctx, release);
	}
}

/* --- A test's events --- */

/* notify sends the control's notification to the window it is in. */
static void notify(HWND w, WORD code) {
	SendMessageW(GetParent(w), WM_COMMAND, MAKEWPARAM(GetDlgCtrlID(w), code), (LPARAM)w);
}

void sngl_win32_fire_click(HWND w) {
	firing = 1;
	notify(w, BN_CLICKED);
	firing = 0;
}

void sngl_win32_fire_change(HWND w, const unsigned short *text) {
	set_quietly(w, (LPCWSTR)text);
	notify(w, EN_CHANGE);
}

void sngl_win32_fire_activate(HWND w, const unsigned short *text) {
	sngl_win32_set_edit_text(w, text);
	ctl *c = ctl_of(w);
	if (c && c->kind == K_AREA) {
		notify(w, EN_KILLFOCUS);
		return;
	}
	/* Through the loop's own step, as a key the user presses reaches it:
	 * IsDialogMessage first. */
	MSG m = {.hwnd = w, .message = WM_KEYDOWN, .wParam = VK_RETURN};
	step(&m);
}

void sngl_win32_fire_close(HWND top) {
	ctl *c = ctl_of(top);
	if (c && c->kind == K_MODAL) {
		/* A modal's close is its window's. */
		top = c->dialog;
	}
	SendMessageW(top, WM_CLOSE, 0, 0);
}

void sngl_win32_fire_reach_end(HWND w) {
	ctl *c = ctl_of(w);
	if (c) {
		fire(&c->reach);
	}
}

/* --- The loop --- */

static int quitting;

void sngl_win32_quit(void) {
	quitting = 1;
	PostQuitMessage(0);
}

static int step(MSG *m) {
	HWND top = GetAncestor(m->hwnd, GA_ROOT);
	if (top && IsDialogMessageW(top, m)) {
		return 1;
	}
	TranslateMessage(m);
	DispatchMessageW(m);
	return 1;
}

/* sngl_post (sngl.h) queues fn behind what the loop already holds, on the
 * parked window every control is made in, which the program never shows
 * and so never closes. */
void sngl_post(void (*fn)(void *ctx), void *ctx, void (*release)(void *ctx)) {
	sngl_win32_init();
	handler *h = calloc(1, sizeof *h);
	set_handler(h, fn, ctx, release);
	if (!PostMessageW(park, SNGL_WM_POST, 0, (LPARAM)h)) {
		drop_handler(h);
		free(h);
	}
}

int sngl_win32_every(int ms, sngl_win32_fn fn, void *ctx, sngl_win32_release release) {
	sngl_win32_init();
	if (ntimers == captimers) {
		captimers = captimers ? captimers * 2 : 4;
		timers = realloc(timers, sizeof *timers * (size_t)captimers);
	}
	struct timer *t = &timers[ntimers++];
	memset(t, 0, sizeof *t);
	t->id = next_timer++;
	set_handler(&t->h, fn, ctx, release);
	SetTimer(park, t->id, (UINT)(ms > 0 ? ms : 1), NULL);
	return (int)t->id;
}

void sngl_win32_cancel(int id) {
	for (int i = 0; i < ntimers; i++) {
		if (timers[i].id == (UINT_PTR)id) {
			KillTimer(park, timers[i].id);
			handler h = timers[i].h;
			timers[i] = timers[--ntimers];
			drop_handler(&h);
			return;
		}
	}
}

void sngl_win32_run(void) {
	sngl_win32_init();
	/* A start with no window on screen ends only when the program quits. */
	int hidden = shown <= 0;
	MSG m;
	while (!quitting && GetMessageW(&m, NULL, 0, 0) > 0) {
		step(&m);
		if (!hidden && shown <= 0) {
			/* The last window the program showed is closed. */
			break;
		}
	}
}

void sngl_win32_refuse_run_again(const char *pos) {
	fprintf(stderr, "%s: run called again; an application runs once per process\n", pos);
	exit(2);
}

void sngl_win32_step(void) {
	if (!GetQueueStatus(QS_ALLINPUT)) {
		MsgWaitForMultipleObjects(0, NULL, FALSE, 1, QS_ALLINPUT);
	}
	sngl_win32_pump();
}

/* sngl_loop_wait (sngl_http.h) handles what the loop has, waiting for a
 * message where it has none: a request's answer is posted as one. */
bool sngl_loop_wait(void) {
	sngl_win32_init();
	if (!quitting) {
		MsgWaitForMultipleObjectsEx(0, NULL, INFINITE, QS_ALLINPUT, MWMO_INPUTAVAILABLE);
		sngl_win32_pump();
	}
	return !quitting;
}

void sngl_win32_pump(void) {
	MSG m;
	while (PeekMessageW(&m, NULL, 0, 0, PM_REMOVE)) {
		if (m.message == WM_QUIT) {
			quitting = 1;
			break;
		}
		step(&m);
	}
}

/* sngl_win32_capture draws the toplevel shown last, its client area, into a DIB
 * section: PrintWindow has the window and each control in it paint there, as
 * they would on a screen, which under Wine's null driver there is none of. */
int sngl_win32_capture(unsigned char **rgba, int *width, int *height, const char **why) {
	/* A layout a change posted runs first. */
	sngl_win32_pump();
	HWND top = sngl_win32_shown();
	if (top == NULL) {
		*why = "no window is shown";
		return 0;
	}
	RECT r;
	GetClientRect(top, &r);
	int w = r.right - r.left, h = r.bottom - r.top;
	if (w <= 0 || h <= 0) {
		*why = "the window shown has no area";
		return 0;
	}
	BITMAPINFO bi;
	memset(&bi, 0, sizeof bi);
	bi.bmiHeader.biSize = sizeof bi.bmiHeader;
	bi.bmiHeader.biWidth = w;
	/* Negative: the rows from the top, as PNG has them. */
	bi.bmiHeader.biHeight = -h;
	bi.bmiHeader.biPlanes = 1;
	bi.bmiHeader.biBitCount = 32;
	bi.bmiHeader.biCompression = BI_RGB;
	HDC dc = CreateCompatibleDC(NULL);
	void *bits = NULL;
	HBITMAP bmp = CreateDIBSection(dc, &bi, DIB_RGB_COLORS, &bits, NULL, 0);
	if (bmp == NULL) {
		DeleteDC(dc);
		*why = "CreateDIBSection failed";
		return 0;
	}
	HGDIOBJ was = SelectObject(dc, bmp);
	BOOL drawn = PrintWindow(top, dc, PW_CLIENTONLY);
	GdiFlush();
	if (drawn) {
		/* Blue, green, red and an unused byte to red, green, blue and an
		 * opaque alpha. */
		unsigned char *p = bits;
		for (size_t i = 0; i < (size_t)w * (size_t)h; i++, p += 4) {
			unsigned char b = p[0];
			p[0] = p[2];
			p[2] = b;
			p[3] = 0xff;
		}
		*rgba = malloc((size_t)w * (size_t)h * 4);
		memcpy(*rgba, bits, (size_t)w * (size_t)h * 4);
		*width = w;
		*height = h;
	}
	SelectObject(dc, was);
	DeleteObject(bmp);
	DeleteDC(dc);
	if (!drawn) {
		*why = "PrintWindow failed";
	}
	return drawn != 0;
}

int sngl_win32_snapshot(const char *path, int width, int height) {
	HWND top = sngl_win32_shown();
	if (top != NULL && width > 0 && height > 0) {
		RECT r = {0, 0, width, height};
		AdjustWindowRectEx(&r, (DWORD)GetWindowLongPtrW(top, GWL_STYLE), GetMenu(top) != NULL, (DWORD)GetWindowLongPtrW(top, GWL_EXSTYLE));
		SetWindowPos(top, NULL, 0, 0, r.right - r.left, r.bottom - r.top, SWP_NOMOVE | SWP_NOZORDER | SWP_NOACTIVATE);
	}
	unsigned char *rgba = NULL;
	int w = 0, h = 0;
	const char *why = NULL;
	if (!sngl_win32_capture(&rgba, &w, &h, &why)) {
		fprintf(stderr, "snapshot: %s\n", why);
		return 1;
	}
	FILE *f = fopen(path, "wb");
	if (f == NULL) {
		free(rgba);
		fprintf(stderr, "snapshot: cannot write %s\n", path);
		return 1;
	}
	unsigned char head[8];
	for (int i = 0; i < 4; i++) {
		head[i] = (unsigned char)((unsigned)w >> (8 * i));
		head[4 + i] = (unsigned char)((unsigned)h >> (8 * i));
	}
	size_t n = (size_t)w * (size_t)h * 4;
	int ok = fwrite(head, 1, sizeof head, f) == sizeof head && fwrite(rgba, 1, n, f) == n;
	ok = fclose(f) == 0 && ok;
	free(rgba);
	return ok ? 0 : 1;
}
