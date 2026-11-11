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
#define SNGL_PROP L"sngl.ctl"
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

/* How a box lays its children out (sngl_win32_set_mode). */
enum { M_LINE, M_GRID, M_FLOW, M_STACK, M_EQUAL };

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

/* ctl is the record the runtime keeps beside each node's window. */
typedef struct {
	enum sngl_kind kind;
	int vertical;
	double flex;
	int gap;
	int pad[4]; /* top, right, bottom, left */
	int min_w, min_h;
	int color, background, font_px;
	/* dpi is what its font is made for. Every length above is a SNGL px,
	 * a 96th of an inch, scaled to the window's DPI where it is used. */
	int dpi;
	HFONT font;   /* the node's own, for a font_px; else the message font */
	HBRUSH brush; /* its background's, for a background */
	handler click, change, activate, closed;
	/* visible is a toplevel's report of :visible, a sngl_win32_bool_fn. */
	handler visible;
	/* quiet is set while the runtime writes an edit's text, which Win32
	 * reports as a change the program did not make. */
	int quiet;
	/* laying is set while a toplevel's layout is posted and not yet run. */
	int laying;
	/* rows is the lines a multi-line edit is measured to show, and fraction
	 * a progress bar's, negative while it is indeterminate, which show_text
	 * writes over it. */
	int rows;
	double fraction;
	int show_text;
	/* A box's mode, its columns as a grid, and the height a flow wrapped
	 * to at the width it was last laid out at, which it measures as next. */
	int mode, columns, flow_h;
	/* framed is a box drawn with an etched edge inside its bounds. */
	int framed;
	/* off_prop and off_look hide a node, by its visible prop and by its
	 * style's display, and either takes it out of its box's layout. */
	int off_prop, off_look;
	/* href is a link's, opened as the user clicks it. */
	WCHAR *href;
	/* tip is what a box's children show as the pointer rests on them. */
	WCHAR *tip;
	/* selected is a tabs' page shown. */
	int selected;
	/* A scroll's axes, where it is along each, and whether its end was
	 * reported and has not been left since; reach is its handler. */
	int scroll_v, scroll_h, pos_x, pos_y, at_end;
	handler reach;
	/* A modal is a node holding no room in its box, and dialog the window
	 * it shows what it holds in while open, over the window it is in,
	 * which modal_of names back. */
	HWND dialog, modal_of;
	int open, closable;
	/* A menu entry's command, its state, and the menu bar a toplevel shows
	 * its entries in. */
	UINT id;
	int disabled, checked;
	HMENU menu;
	/* A table's: whether a row can be selected and a column sorted by, and
	 * the column a header click last asked for. A tree's label selected. */
	int selectable, sortable, sort_col;
	WCHAR *chosen;
	/* An image's fit (sngl_win32_set_fit), its handlers, and why its
	 * picture failed; a picture's bitmap, its size, and what it shows. */
	int fit;
	handler load, failed;
	char *why;
	HBITMAP bmp;
	int bw, bh;
	WCHAR *url;
	/* A canvas's drawing space, how it is mapped onto the canvas, and what
	 * paints it, a sngl_win32_paint_fn. */
	int cw, ch, scaling;
	handler paint;
	/* A span's words, and the appearance a span or a flow sets of its
	 * spans: a color or -1, a size or 0, a weight and a slant or 0 to
	 * inherit, an underline, a line through, and a family or NULL. A
	 * flow's links, where it last drew them. */
	WCHAR *words, *family;
	int sp_color, sp_px, sp_weight, sp_slant, sp_under, sp_strike;
	struct link_at *links;
	int nlinks;
	HWND self;
	HWND *kids;
	int nkids, capkids;
} ctl;

static int ready;
/* firing is set while a test's event is raised. */
static int firing;
static HWND park;
/* The message font at each DPI a window has been shown at. */
static struct {
	int dpi;
	HFONT font;
} message_fonts[8];
static int nmessage_fonts;
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

static ctl *ctl_of(HWND w) { return w ? (ctl *)GetPropW(w, SNGL_PROP) : NULL; }

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
	if (nmessage_fonts < (int)(sizeof message_fonts / sizeof message_fonts[0])) {
		message_fonts[nmessage_fonts].dpi = dpi;
		message_fonts[nmessage_fonts].font = f;
		nmessage_fonts++;
	}
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

/* forget frees what w's record holds, as w goes. */
static void forget(HWND w) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	RemovePropW(w, SNGL_PROP);
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
	free(c->why);
	free(c->url);
	if (c->dialog) {
		HWND owner = GetWindow(c->dialog, GW_OWNER);
		if (c->open && owner) {
			EnableWindow(owner, TRUE);
		}
		DestroyWindow(c->dialog);
	}
	if (c->menu) {
		SetMenu(w, NULL);
		DestroyMenu(c->menu);
	}
	if (c->font) {
		DeleteObject(c->font);
	}
	if (c->brush) {
		DeleteObject(c->brush);
	}
	free(c->kids);
	free(c->href);
	free(c->tip);
	free(c->chosen);
	free(c);
}

/* --- Layout --- */

static void measure(HWND w, SIZE *out);
static void relayout(HWND w);
static const char *utf8(const WCHAR *wide);
static void refresh(HWND w, int dpi);
static void lay_out_top(HWND top);
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

/* laid is whether a box lays w out: a node hidden takes no room. */
static int laid(HWND w) {
	ctl *k = ctl_of(w);
	return k && !k->off_prop && !k->off_look;
}

/* edge is the room a framed box's edge takes inside each of its sides. */
static int edge(ctl *c) { return c->framed ? px(2, c->dpi) : 0; }

/* kid is a child a box lays out, and the size it measures. */
typedef struct {
	HWND w;
	SIZE s;
	double flex;
} kid;

/* laid_kids is c's children it lays out, which the caller frees, and how
 * many there are. */
static kid *laid_kids(ctl *c, int *n) {
	kid *ks = malloc(sizeof(kid) * (size_t)(c->nkids ? c->nkids : 1));
	*n = 0;
	for (int i = 0; i < c->nkids; i++) {
		if (!laid(c->kids[i])) {
			continue;
		}
		kid *k = &ks[(*n)++];
		k->w = c->kids[i];
		measure(k->w, &k->s);
		k->flex = ctl_of(k->w)->flex;
	}
	return ks;
}

/* columns_of is how many columns a grid lays its children in. */
static int columns_of(ctl *c) { return c->columns > 0 ? c->columns : 1; }

/* box_measure is what a box's or a toplevel's children need as its mode
 * lays them out: end to end, in a grid's rows, wrapped, one over another,
 * or each the room of the largest. */
static SIZE box_measure(ctl *c) {
	SIZE s = {0, 0}, most = {0, 0};
	int gap = px(c->gap, c->dpi), n;
	kid *ks = laid_kids(c, &n);
	for (int i = 0; i < n; i++) {
		most.cx = ks[i].s.cx > most.cx ? ks[i].s.cx : most.cx;
		most.cy = ks[i].s.cy > most.cy ? ks[i].s.cy : most.cy;
	}
	switch (c->mode) {
	case M_GRID: {
		int cols = columns_of(c), rows = (n + cols - 1) / cols;
		s.cx = cols * most.cx + (cols - 1) * gap;
		for (int r = 0; r < rows; r++) {
			int h = 0;
			for (int i = r * cols; i < n && i < (r + 1) * cols; i++) {
				h = ks[i].s.cy > h ? ks[i].s.cy : h;
			}
			s.cy += h + (r > 0 ? gap : 0);
		}
		break;
	}
	case M_STACK:
		s = most;
		break;
	case M_FLOW:
		/* As wide as its widest child, which it wraps the rest around at
		 * the width it is given, and as tall as that last wrapped. */
		s.cx = most.cx;
		s.cy = c->flow_h > most.cy ? c->flow_h : most.cy;
		break;
	case M_EQUAL:
		if (c->vertical) {
			s.cx = most.cx;
			s.cy = n * most.cy + (n > 0 ? (n - 1) * gap : 0);
		} else {
			s.cx = n * most.cx + (n > 0 ? (n - 1) * gap : 0);
			s.cy = most.cy;
		}
		break;
	default: {
		int vertical = c->vertical;
		for (int i = 0; i < n; i++) {
			if (vertical) {
				s.cy += ks[i].s.cy + (i > 0 ? gap : 0);
			} else {
				s.cx += ks[i].s.cx + (i > 0 ? gap : 0);
			}
		}
		if (vertical) {
			s.cx = most.cx;
		} else {
			s.cy = most.cy;
		}
	}
	}
	free(ks);
	s.cx += 2 * edge(c);
	s.cy += 2 * edge(c);
	return s;
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
	s.cy = c->min_h > 0 ? 0 : rows * (int)SendMessageW(w, TVM_GETITEMHEIGHT, 0, 0) + px(4, c->dpi);
	if (s.cx < px(120, c->dpi)) {
		s.cx = px(120, c->dpi);
	}
	return s;
}

/* tabs_size is a tabs': its largest page, and the strip of tabs over it. */
static SIZE tabs_size(HWND w, ctl *c) {
	SIZE most = {0, 0};
	for (int i = 0; i < c->nkids; i++) {
		SIZE k;
		measure(c->kids[i], &k);
		most.cx = k.cx > most.cx ? k.cx : most.cx;
		most.cy = k.cy > most.cy ? k.cy : most.cy;
	}
	RECT r = {0, 0, most.cx, most.cy};
	SendMessageW(w, TCM_ADJUSTRECT, TRUE, (LPARAM)&r);
	SIZE s = {r.right - r.left, r.bottom - r.top};
	int n = (int)SendMessageW(w, TCM_GETITEMCOUNT, 0, 0);
	RECT last;
	if (n > 0 && SendMessageW(w, TCM_GETITEMRECT, (WPARAM)(n - 1), (LPARAM)&last) && last.right + px(4, c->dpi) > s.cx) {
		s.cx = last.right + px(4, c->dpi);
	}
	return s;
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
	for (int i = 0; i < c->nkids; i++) {
		collect(c->kids[i], l, t, dc, dpi);
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
		int x0 = px(c->pad[3], c->dpi), y0 = px(c->pad[0], c->dpi);
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
			TextOutW(dc, x0 + p->x, y, p->s, p->len);
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

static void measure(HWND w, SIZE *out) {
	ctl *c = ctl_of(w);
	SIZE s = {0, 0};
	if (!c) {
		*out = s;
		return;
	}
	switch (c->kind) {
	case K_TOP:
	case K_BOX:
		s = box_measure(c);
		break;
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
	case K_DIVIDER:
		/* An etched line, two pixels across. */
		if (c->vertical) {
			s.cx = 2;
		} else {
			s.cy = 2;
		}
		break;
	case K_LINK:
		s = text_size(w);
		break;
	case K_TABS:
		s = tabs_size(w, c);
		break;
	case K_SCROLL: {
		/* What it holds across the axes it does not scroll along, and
		 * along one it does, its least size where its style gives one
		 * and otherwise no more than a few lines of it. */
		s = box_measure(c);
		int most = px(200, c->dpi);
		if (c->scroll_v) {
			int cap = c->min_h > 0 ? 0 : most;
			s.cy = s.cy < cap ? s.cy : cap;
			s.cx += GetSystemMetrics(SM_CXVSCROLL);
		}
		if (c->scroll_h) {
			int cap = c->min_w > 0 ? 0 : most;
			s.cx = s.cx < cap ? s.cx : cap;
			s.cy += GetSystemMetrics(SM_CYHSCROLL);
		}
		break;
	}
	case K_TABLE: {
		/* Every column, and every row under the header, unless its style
		 * gives it a height. */
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
		s.cy = c->min_h > 0 ? 0 : head + rows * row + 2 * GetSystemMetrics(SM_CYEDGE) + px(2, c->dpi);
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
	case K_IMAGE:
		/* Its picture's size, a pixel a SNGL px, or nothing while it has
		 * none. */
		for (int i = 0; i < c->nkids; i++) {
			ctl *k = ctl_of(c->kids[i]);
			if (k && k->bmp) {
				s.cx = px(k->bw, c->dpi);
				s.cy = px(k->bh, c->dpi);
			}
		}
		break;
	case K_CANVAS:
		s.cx = px(c->cw, c->dpi);
		s.cy = px(c->ch, c->dpi);
		break;
	case K_FLOW: {
		/* Its words on as few lines as their newlines allow, but asking
		 * for no more than 240 px across, since what it asks for is the
		 * least a window holding it is sized to: a box down the page
		 * gives it the whole of its width whatever it asks, and it wraps
		 * there. As tall as they wrap to at the width it was last laid
		 * out at. */
		int most = px(240, c->dpi);
		s = flow_size(w, c, most, 0);
		if (s.cx > most) {
			s.cx = most;
		}
		if (c->flow_h > 0) {
			s.cy = c->flow_h;
		}
		break;
	}
	case K_SPAN:
	case K_PICTURE:
	case K_MODAL:
	case K_ENTRY:
	case K_SPACER:
		break;
	}
	s.cx += px(c->pad[1] + c->pad[3], c->dpi);
	s.cy += px(c->pad[0] + c->pad[2], c->dpi);
	if (s.cx < px(c->min_w, c->dpi)) {
		s.cx = px(c->min_w, c->dpi);
	}
	if (s.cy < px(c->min_h, c->dpi)) {
		s.cy = px(c->min_h, c->dpi);
	}
	*out = s;
}

/* line places a line's children along it: each its own size, the spare
 * room shared by flex, and the whole of the box across it. */
static void line(ctl *c, kid *ks, int n, RECT *at, int x0, int y0, int iw, int ih, int gap) {
	int avail = (c->vertical ? ih : iw) - gap * (n - 1);
	int *size = malloc(sizeof(int) * (size_t)n);
	int total = 0;
	double flex = 0;
	for (int i = 0; i < n; i++) {
		size[i] = c->vertical ? ks[i].s.cy : ks[i].s.cx;
		total += size[i];
		flex += ks[i].flex;
	}
	int spare = avail - total;
	if (c->mode == M_EQUAL) {
		/* Every child the same share, what rounding left the last's. */
		for (int i = 0; i < n; i++) {
			size[i] = avail / n;
		}
		size[n - 1] += avail - (avail / n) * n;
	} else if (spare > 0 && flex > 0) {
		int given = 0, last = -1;
		for (int i = 0; i < n; i++) {
			if (ks[i].flex > 0) {
				int more = (int)(spare * (ks[i].flex / flex));
				size[i] += more;
				given += more;
				last = i;
			}
		}
		/* What rounding left goes to the last to flex. */
		size[last] += spare - given;
	}
	int pos = c->vertical ? y0 : x0;
	for (int i = 0; i < n; i++) {
		int x = c->vertical ? x0 : pos, y = c->vertical ? pos : y0;
		SetRect(&at[i], x, y, x + (c->vertical ? iw : size[i]), y + (c->vertical ? size[i] : ih));
		pos += size[i] + gap;
	}
	free(size);
}

/* grid places a grid's children cols to a row, in columns of equal width
 * sharing the grid's, each row as tall as its tallest. */
static void grid(ctl *c, kid *ks, int n, RECT *at, int x0, int y0, int iw, int gap) {
	int cols = columns_of(c);
	int cw = (iw - gap * (cols - 1)) / cols, y = y0;
	for (int r = 0; r * cols < n; r++) {
		int h = 0;
		for (int i = r * cols; i < n && i < (r + 1) * cols; i++) {
			h = ks[i].s.cy > h ? ks[i].s.cy : h;
		}
		for (int i = r * cols; i < n && i < (r + 1) * cols; i++) {
			int x = x0 + (i - r * cols) * (cw + gap);
			SetRect(&at[i], x, y, x + cw, y + h);
		}
		y += h + gap;
	}
}

/* flow places a flow's children along a row each its own size, starting
 * another where the next would not fit, and answers how tall that is. */
static int flow(kid *ks, int n, RECT *at, int x0, int y0, int iw, int gap) {
	int x = x0, y = y0, h = 0;
	for (int i = 0; i < n; i++) {
		if (x > x0 && x + ks[i].s.cx > x0 + iw) {
			x = x0;
			y += h + gap;
			h = 0;
		}
		SetRect(&at[i], x, y, x + ks[i].s.cx, y + ks[i].s.cy);
		x += ks[i].s.cx + gap;
		h = ks[i].s.cy > h ? ks[i].s.cy : h;
	}
	return n > 0 ? y + h - y0 : 0;
}

static void place(HWND w, int cw, int ch);

/* scroll_area is the room a scroll lays what it holds out in, inside its
 * vw by vh client area: as large as what it holds along each axis it
 * scrolls, and moved by how far it has. It sets the scroll bars by it, and
 * reports its end coming within half a page of showing, once each time it
 * arrives there. */
static void scroll_area(HWND box, ctl *c, int vw, int vh, int *x0, int *y0, int *iw, int *ih) {
	SIZE content = box_measure(c);
	int pw = *iw, ph = *ih;
	if (c->scroll_h && content.cx > *iw) {
		*iw = content.cx;
	}
	if (c->scroll_v && content.cy > *ih) {
		*ih = content.cy;
	}
	int max_x = *iw - pw > 0 ? *iw - pw : 0, max_y = *ih - ph > 0 ? *ih - ph : 0;
	c->pos_x = c->pos_x > max_x ? max_x : c->pos_x < 0 ? 0 : c->pos_x;
	c->pos_y = c->pos_y > max_y ? max_y : c->pos_y < 0 ? 0 : c->pos_y;
	SCROLLINFO si = {sizeof si, SIF_ALL | SIF_DISABLENOSCROLL, 0, 0, 0, 0, 0};
	if (c->scroll_v) {
		si.nMax = *ih + (vh - ph) - 1;
		si.nPage = (UINT)vh;
		si.nPos = c->pos_y;
		SetScrollInfo(box, SB_VERT, &si, TRUE);
	}
	if (c->scroll_h) {
		si.nMax = *iw + (vw - pw) - 1;
		si.nPage = (UINT)vw;
		si.nPos = c->pos_x;
		SetScrollInfo(box, SB_HORZ, &si, TRUE);
	}
	*x0 -= c->pos_x;
	*y0 -= c->pos_y;
	int ending = c->scroll_v ? c->pos_y + ph >= *ih - ph / 2 : c->pos_x + pw >= *iw - pw / 2;
	if (ending && !c->at_end) {
		c->at_end = 1;
		PostMessageW(box, SNGL_WM_REACHED, 0, 0);
	} else if (!ending) {
		c->at_end = 0;
	}
}

/* lay_out_tabs shows a tabs' page selected in the room under its strip,
 * and hides the rest. */
static void lay_out_tabs(HWND tabs, int w, int h) {
	ctl *c = ctl_of(tabs);
	RECT r = {0, 0, w, h};
	SendMessageW(tabs, TCM_ADJUSTRECT, FALSE, (LPARAM)&r);
	for (int i = 0; i < c->nkids; i++) {
		if (i == c->selected) {
			SetWindowPos(c->kids[i], HWND_TOP, r.left, r.top, r.right - r.left, r.bottom - r.top, SWP_NOACTIVATE | SWP_SHOWWINDOW);
			place(c->kids[i], r.right - r.left, r.bottom - r.top);
		} else {
			ShowWindow(c->kids[i], SW_HIDE);
		}
	}
	InvalidateRect(tabs, NULL, TRUE);
}

/* lay_out places a box's or a toplevel's children in its w by h client
 * area, as its mode does, and each box among them its own. */
static void lay_out(HWND box, int w, int h) {
	ctl *c = ctl_of(box);
	if (!c || c->nkids == 0) {
		InvalidateRect(box, NULL, TRUE);
		return;
	}
	int pad[4], gap = px(c->gap, c->dpi), e = edge(c);
	for (int i = 0; i < 4; i++) {
		pad[i] = px(c->pad[i], c->dpi) + e;
	}
	if (c->kind == K_SCROLL) {
		/* The bars always shown, so the room inside is the same
		 * whatever it holds. */
		w -= c->scroll_v ? GetSystemMetrics(SM_CXVSCROLL) : 0;
		h -= c->scroll_h ? GetSystemMetrics(SM_CYHSCROLL) : 0;
	}
	int x0 = pad[3], y0 = pad[0];
	int iw = w - pad[1] - pad[3], ih = h - pad[0] - pad[2];
	if (c->kind == K_SCROLL) {
		scroll_area(box, c, w, h, &x0, &y0, &iw, &ih);
	}
	int n;
	kid *ks = laid_kids(c, &n);
	RECT *at = calloc((size_t)(n ? n : 1), sizeof(RECT));
	if (n > 0) {
		switch (c->mode) {
		case M_GRID:
			grid(c, ks, n, at, x0, y0, iw, gap);
			break;
		case M_FLOW: {
			int fh = flow(ks, n, at, x0, y0, iw, gap);
			if (fh != c->flow_h) {
				/* It wraps to another height: what holds it measures
				 * it again. */
				c->flow_h = fh;
				relayout(box);
			}
			break;
		}
		case M_STACK:
			for (int i = 0; i < n; i++) {
				SetRect(&at[i], x0, y0, x0 + iw, y0 + ih);
			}
			break;
		default:
			line(c, ks, n, at, x0, y0, iw, ih, gap);
		}
	}
	HDWP dwp = BeginDeferWindowPos(n ? n : 1);
	for (int i = 0; i < n; i++) {
		int ch = at[i].bottom - at[i].top;
		ctl *kc = ctl_of(ks[i].w);
		if (kc->kind == K_SELECT) {
			/* A drop-down's height is its list's too, dropped. */
			ch += px(200, kc->dpi);
		}
		if (dwp) {
			dwp = DeferWindowPos(dwp, ks[i].w, NULL, at[i].left, at[i].top, at[i].right - at[i].left, ch, SWP_NOZORDER | SWP_NOACTIVATE);
		}
	}
	if (dwp) {
		EndDeferWindowPos(dwp);
	}
	if (c->mode == M_STACK) {
		/* The last layer on top, drawn over the rest. */
		for (int i = n - 1; i >= 0; i--) {
			SetWindowPos(ks[i].w, i == n - 1 ? HWND_TOP : ks[i + 1].w, 0, 0, 0, 0, SWP_NOMOVE | SWP_NOSIZE | SWP_NOACTIVATE);
		}
	}
	for (int i = 0; i < n; i++) {
		place(ks[i].w, at[i].right - at[i].left, at[i].bottom - at[i].top);
	}
	free(at);
	free(ks);
	InvalidateRect(box, NULL, TRUE);
}

/* place lays out what w holds in its cw by ch bounds, where it holds any. */
static void place(HWND w, int cw, int ch) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	switch (c->kind) {
	case K_BOX:
	case K_SCROLL:
		lay_out(w, cw, ch);
		break;
	case K_TABS:
		lay_out_tabs(w, cw, ch);
		break;
	case K_FLOW: {
		/* Its words wrap at its width, and what holds it is laid out
		 * again where they wrap to another height. */
		int e = px(c->pad[1] + c->pad[3], c->dpi);
		int fh = flow_size(w, c, cw - e, 0).cy;
		if (fh != c->flow_h) {
			c->flow_h = fh;
			relayout(w);
		}
		InvalidateRect(w, NULL, TRUE);
		break;
	}
	case K_IMAGE:
		/* Its picture fills it, and paints itself fitted. */
		for (int i = 0; i < c->nkids; i++) {
			SetWindowPos(c->kids[i], NULL, 0, 0, cw, ch, SWP_NOZORDER | SWP_NOACTIVATE);
			InvalidateRect(c->kids[i], NULL, TRUE);
		}
		break;
	default:
		break;
	}
}

/* lay_out_now lays out what w holds in the bounds it has. */
static void lay_out_now(HWND w) {
	RECT r;
	GetWindowRect(w, &r);
	place(w, r.right - r.left, r.bottom - r.top);
}

static void lay_out_top(HWND top) {
	RECT r;
	GetClientRect(top, &r);
	lay_out(top, r.right - r.left, r.bottom - r.top);
}

/* refresh gives w and what is under it the fonts for dpi, the DPI of the
 * toplevel they are in: what a control made, parked at the system's, takes
 * as it is put in one, and all of them as the toplevel moves to a monitor of
 * another. */
static void refresh(HWND w, int dpi) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	if (c->dpi != dpi) {
		c->dpi = dpi;
		own_font(c);
		if (c->kind != K_TOP && c->kind != K_BOX) {
			SendMessageW(w, WM_SETFONT, (WPARAM)font_of(c), FALSE);
		}
	}
	for (int i = 0; i < c->nkids; i++) {
		refresh(c->kids[i], dpi);
	}
}

/* fit sizes a toplevel to hold what it holds, at least min_w by min_h, as a
 * GTK window grows to its content: grown, and never shrunk, since a window
 * the user sized keeps that size. */
static void fit(HWND top, int min_w, int min_h) {
	SIZE s;
	measure(top, &s);
	RECT r;
	GetClientRect(top, &r);
	int w = r.right - r.left, h = r.bottom - r.top;
	int want_w = s.cx > min_w ? s.cx : min_w, want_h = s.cy > min_h ? s.cy : min_h;
	if (want_w <= w && want_h <= h) {
		return;
	}
	RECT wr = {0, 0, want_w > w ? want_w : w, want_h > h ? want_h : h};
	AdjustWindowRectEx(&wr, (DWORD)GetWindowLongPtrW(top, GWL_STYLE), FALSE, (DWORD)GetWindowLongPtrW(top, GWL_EXSTYLE));
	SetWindowPos(top, NULL, 0, 0, wr.right - wr.left, wr.bottom - wr.top, SWP_NOMOVE | SWP_NOZORDER | SWP_NOACTIVATE);
}

/* relayout asks for the layout of the toplevel w is in, once whatever is
 * changing it has. A node not yet under one waits for its append. */
static void relayout(HWND w) {
	HWND top = GetAncestor(w, GA_ROOT);
	ctl *c = ctl_of(top);
	if (!c || c->kind != K_TOP || c->laying) {
		return;
	}
	c->laying = 1;
	PostMessageW(top, SNGL_WM_LAYOUT, 0, 0);
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

/* tabs_sync gives a tabs a tab for each page it holds, titled with the
 * page's text, and selects the one it shows. */
static void tabs_sync(HWND tabs) {
	ctl *c = ctl_of(tabs);
	if (!c) {
		return;
	}
	SendMessageW(tabs, TCM_DELETEALLITEMS, 0, 0);
	for (int i = 0; i < c->nkids; i++) {
		int n = GetWindowTextLengthW(c->kids[i]);
		WCHAR *title = malloc(sizeof(WCHAR) * (size_t)(n + 1));
		GetWindowTextW(c->kids[i], title, n + 1);
		TCITEMW item;
		memset(&item, 0, sizeof item);
		item.mask = TCIF_TEXT;
		item.pszText = title;
		SendMessageW(tabs, TCM_INSERTITEMW, (WPARAM)i, (LPARAM)&item);
		free(title);
	}
	SendMessageW(tabs, TCM_SETCURSEL, (WPARAM)c->selected, 0);
	relayout(tabs);
}

/* tab_chosen is the user's choice of a tab: its page is shown and reports
 * it, or, where the page is disabled, the tab selected before is again. */
static void tab_chosen(HWND tabs, ctl *c) {
	int at = (int)SendMessageW(tabs, TCM_GETCURSEL, 0, 0);
	if (at < 0 || at >= c->nkids || !IsWindowEnabled(c->kids[at])) {
		SendMessageW(tabs, TCM_SETCURSEL, (WPARAM)c->selected, 0);
		return;
	}
	c->selected = at;
	lay_out_now(tabs);
	ctl *page = ctl_of(c->kids[at]);
	if (page) {
		fire(&page->click);
	}
}

/* scrolled moves a scroll along bar as the bar's request asks. */
static void scrolled(HWND w, ctl *c, int bar, int request) {
	SCROLLINFO si = {sizeof si, SIF_ALL, 0, 0, 0, 0, 0};
	GetScrollInfo(w, bar, &si);
	int pos = si.nPos, line = px(20, c->dpi);
	switch (request) {
	case SB_LINEUP:
		pos -= line;
		break;
	case SB_LINEDOWN:
		pos += line;
		break;
	case SB_PAGEUP:
		pos -= (int)si.nPage;
		break;
	case SB_PAGEDOWN:
		pos += (int)si.nPage;
		break;
	case SB_THUMBTRACK:
	case SB_THUMBPOSITION:
		pos = si.nTrackPos;
		break;
	case SB_TOP:
		pos = 0;
		break;
	case SB_BOTTOM:
		pos = si.nMax;
		break;
	default:
		return;
	}
	if (bar == SB_VERT) {
		c->pos_y = pos;
	} else {
		c->pos_x = pos;
	}
	lay_out_now(w);
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
		SIZE s;
		measure(c->dialog, &s);
		RECT r = {0, 0, s.cx < px(240, d->dpi) ? px(240, d->dpi) : s.cx, s.cy < px(80, d->dpi) ? px(80, d->dpi) : s.cy};
		AdjustWindowRectEx(&r, (DWORD)GetWindowLongPtrW(c->dialog, GWL_STYLE), FALSE, (DWORD)GetWindowLongPtrW(c->dialog, GWL_EXSTYLE));
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

/* entry_flags is how an entry's state shows in a menu. */
static UINT entry_flags(ctl *e) { return (e->disabled ? MF_GRAYED : MF_ENABLED) | (e->checked ? MF_CHECKED : MF_UNCHECKED); }

/* fill_menu puts in m an item for each entry entry holds, or the top's
 * walk finds: a submenu where it holds entries itself, and otherwise its
 * command. */
static void fill_menu(HMENU m, HWND w, int top);

static void add_entry(HMENU m, HWND e) {
	ctl *ec = ctl_of(e);
	int n = GetWindowTextLengthW(e);
	WCHAR *label = malloc(sizeof(WCHAR) * (size_t)(n + 1));
	GetWindowTextW(e, label, n + 1);
	int subs = 0;
	for (int i = 0; i < ec->nkids; i++) {
		ctl *k = ctl_of(ec->kids[i]);
		subs += k && k->kind == K_ENTRY;
	}
	if (subs) {
		HMENU sub = CreatePopupMenu();
		fill_menu(sub, e, 0);
		AppendMenuW(m, MF_POPUP | entry_flags(ec), (UINT_PTR)sub, label);
	} else {
		AppendMenuW(m, MF_STRING | entry_flags(ec), ec->id, label);
	}
	free(label);
}

static void fill_menu(HMENU m, HWND w, int top) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	for (int i = 0; i < c->nkids; i++) {
		ctl *k = ctl_of(c->kids[i]);
		if (!k) {
			continue;
		}
		if (k->kind == K_ENTRY) {
			add_entry(m, c->kids[i]);
		} else if (top) {
			/* A menu bar's entries are found wherever the window holds
			 * them, ui.menubar's included. */
			fill_menu(m, c->kids[i], 1);
		}
	}
}

/* menu_sync gives the toplevel w is in a menu bar of every entry it holds,
 * or none where it holds none. */
static void menu_sync(HWND w) {
	HWND top = GetAncestor(w, GA_ROOT);
	ctl *c = ctl_of(top);
	if (!c || c->kind != K_TOP) {
		return;
	}
	HMENU m = CreateMenu();
	fill_menu(m, top, 1);
	HMENU old = c->menu;
	c->menu = GetMenuItemCount(m) > 0 ? m : NULL;
	if (!c->menu) {
		DestroyMenu(m);
	}
	if (c->menu || old) {
		SetMenu(top, c->menu);
		DrawMenuBar(top);
	}
	if (old) {
		DestroyMenu(old);
	}
}

/* find_entry is the entry under w whose command is id, or NULL. */
static ctl *find_entry(HWND w, UINT id) {
	ctl *c = ctl_of(w);
	if (!c) {
		return NULL;
	}
	if (c->kind == K_ENTRY && c->id == id) {
		return c;
	}
	for (int i = 0; i < c->nkids; i++) {
		ctl *e = find_entry(c->kids[i], id);
		if (e) {
			return e;
		}
	}
	return NULL;
}

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
	c->bmp = bmp;
	c->bw = bw;
	c->bh = bh;
	free(c->why);
	c->why = why ? _strdup(why) : NULL;
	InvalidateRect(w, NULL, TRUE);
	PostMessageW(w, SNGL_WM_LOADED, 0, 0);
}

typedef struct {
	HWND w;
	WCHAR *url;
} fetch;

/* fetched saves the URL a fetch names to the cache, off the loop, and
 * hands the loop the file. */
static DWORD WINAPI fetched(void *arg) {
	fetch *f = arg;
	WCHAR path[MAX_PATH];
	WCHAR *saved = NULL;
	if (SUCCEEDED(URLDownloadToCacheFileW(NULL, f->url, path, MAX_PATH, 0, NULL))) {
		saved = _wcsdup(path);
	}
	if (!PostMessageW(f->w, SNGL_WM_FETCHED, 0, (LPARAM)saved)) {
		free(saved);
	}
	free(f->url);
	free(f);
	return 0;
}

/* load shows what url names: a file the program carries (sngl_win32_assets),
 * one fetched where it is an http or https URL, or one on disk. */
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
	for (const sngl_win32_asset *a = sngl_win32_assets; a->name; a++) {
		if (strcmp(a->name, name) == 0) {
			bmp = decode(a->data, a->len, NULL, &bw, &bh, &why);
			free(name);
			shown_as(w, c, bmp, bw, bh, why);
			return;
		}
	}
	free(name);
	if (wcsncmp(c->url, L"http://", 7) == 0 || wcsncmp(c->url, L"https://", 8) == 0) {
		fetch *f = malloc(sizeof *f);
		f->w = w;
		f->url = _wcsdup(c->url);
		HANDLE t = CreateThread(NULL, 0, fetched, f, 0, NULL);
		if (t) {
			CloseHandle(t);
		} else {
			free(f->url);
			free(f);
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
	if (!c->bmp) {
		return;
	}
	RECT r;
	GetClientRect(w, &r);
	int aw = r.right, ah = r.bottom;
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
	HDC mem = CreateCompatibleDC(dc);
	HGDIOBJ old = SelectObject(mem, c->bmp);
	int saved = SaveDC(dc);
	IntersectClipRect(dc, 0, 0, aw, ah);
	BLENDFUNCTION bf = {AC_SRC_OVER, 0, 255, AC_SRC_ALPHA};
	AlphaBlend(dc, (aw - dw) / 2, (ah - dh) / 2, dw, dh, mem, 0, 0, c->bw, c->bh, bf);
	RestoreDC(dc, saved);
	SelectObject(mem, old);
	DeleteDC(mem);
}

/* --- Drawing --- */

static ULONG_PTR gdiplus;

/* What save keeps of a context beside GDI+'s own state. */
typedef struct {
	GraphicsState gs;
	ARGB color;
	REAL width;
	int cap, join;
	REAL font_px;
	WCHAR family[64];
} gfx_state;

struct sngl_win32_gfx {
	GpGraphics *g;
	GpPath *path;
	/* The current point, where the figure being built started, and whether
	 * there is a current point, and whether the figure has a point of its
	 * own yet: a move only says where the next segment starts. */
	REAL x, y, fx, fy;
	int at, begun;
	gfx_state now;
	gfx_state saved[16];
	int depth;
};

void sngl_win32_gfx_save(sngl_win32_gfx *g) {
	if (g->depth < 16) {
		g->saved[g->depth] = g->now;
		GdipSaveGraphics(g->g, &g->saved[g->depth].gs);
	}
	g->depth++;
}

void sngl_win32_gfx_restore(sngl_win32_gfx *g) {
	if (g->depth == 0) {
		return;
	}
	g->depth--;
	if (g->depth < 16) {
		GdipRestoreGraphics(g->g, g->saved[g->depth].gs);
		g->now = g->saved[g->depth];
	}
}

static int channel(int v) { return v < 0 ? 0 : v > 255 ? 255 : v; }

void sngl_win32_gfx_color(sngl_win32_gfx *g, int r, int gr, int b, int a) {
	g->now.color = ((ARGB)channel(a) << 24) | ((ARGB)channel(r) << 16) | ((ARGB)channel(gr) << 8) | (ARGB)channel(b);
}

void sngl_win32_gfx_line_width(sngl_win32_gfx *g, double w) { g->now.width = (REAL)w; }
void sngl_win32_gfx_line_cap(sngl_win32_gfx *g, int cap) { g->now.cap = cap; }
void sngl_win32_gfx_line_join(sngl_win32_gfx *g, int join) { g->now.join = join; }

void sngl_win32_gfx_rect(sngl_win32_gfx *g, double x, double y, double w, double h) {
	GdipAddPathRectangle(g->path, (REAL)x, (REAL)y, (REAL)w, (REAL)h);
	g->x = g->fx = (REAL)x;
	g->y = g->fy = (REAL)y;
	g->at = 1;
	g->begun = 0;
}

void sngl_win32_gfx_new_sub_path(sngl_win32_gfx *g) {
	GdipStartPathFigure(g->path);
	g->at = 0;
	g->begun = 0;
}

void sngl_win32_gfx_move_to(sngl_win32_gfx *g, double x, double y) {
	GdipStartPathFigure(g->path);
	g->x = g->fx = (REAL)x;
	g->y = g->fy = (REAL)y;
	g->at = 1;
	g->begun = 0;
}

void sngl_win32_gfx_line_to(sngl_win32_gfx *g, double x, double y) {
	if (!g->at) {
		sngl_win32_gfx_move_to(g, x, y);
		return;
	}
	GdipAddPathLine(g->path, g->x, g->y, (REAL)x, (REAL)y);
	g->x = (REAL)x;
	g->y = (REAL)y;
	g->begun = 1;
}

void sngl_win32_gfx_curve_to(sngl_win32_gfx *g, double x1, double y1, double x2, double y2, double x3, double y3) {
	if (!g->at) {
		sngl_win32_gfx_move_to(g, x1, y1);
	}
	GdipAddPathBezier(g->path, g->x, g->y, (REAL)x1, (REAL)y1, (REAL)x2, (REAL)y2, (REAL)x3, (REAL)y3);
	g->x = (REAL)x3;
	g->y = (REAL)y3;
	g->begun = 1;
}

/* arc adds the arc of sweep radians from start, joined by a line to the
 * current point where there is one, as cairo's is. */
static void arc(sngl_win32_gfx *g, double cx, double cy, double r, double start, double sweep) {
	const double tau = 6.283185307179586;
	if (sweep > tau) {
		sweep = tau;
	} else if (sweep < -tau) {
		sweep = -tau;
	}
	REAL sx = (REAL)(cx + r * cos(start)), sy = (REAL)(cy + r * sin(start));
	if (g->at && !g->begun) {
		/* GDI+ joins an arc to the figure's last point, and a move made
		 * none. */
		GdipAddPathLine(g->path, g->x, g->y, sx, sy);
	} else if (!g->at) {
		g->fx = sx;
		g->fy = sy;
	}
	/* As cubic segments of a quarter turn or less, as cairo's is: Wine's
	 * GdipAddPathArc ends one with a segment of no length, after which its
	 * stroke drops the rest of the figure. */
	int n = (int)ceil(fabs(sweep) / (tau / 4) - 1e-9);
	double step = n > 0 ? sweep / n : 0, k = 4.0 / 3 * tan(step / 4), a = start;
	for (int i = 0; r > 0 && i < n; i++, a += step) {
		double b = a + step;
		double x0 = cx + r * cos(a), y0 = cy + r * sin(a), x3 = cx + r * cos(b), y3 = cy + r * sin(b);
		GdipAddPathBezier(g->path, (REAL)x0, (REAL)y0, (REAL)(x0 - k * r * sin(a)), (REAL)(y0 + k * r * cos(a)), (REAL)(x3 + k * r * sin(b)), (REAL)(y3 - k * r * cos(b)), (REAL)x3, (REAL)y3);
	}
	g->x = (REAL)(cx + r * cos(start + sweep));
	g->y = (REAL)(cy + r * sin(start + sweep));
	g->at = 1;
	g->begun = 1;
}

void sngl_win32_gfx_arc(sngl_win32_gfx *g, double cx, double cy, double r, double start, double end) {
	const double tau = 6.283185307179586;
	while (end < start) {
		end += tau;
	}
	arc(g, cx, cy, r, start, end - start);
}

void sngl_win32_gfx_arc_negative(sngl_win32_gfx *g, double cx, double cy, double r, double start, double end) {
	const double tau = 6.283185307179586;
	while (end > start) {
		end -= tau;
	}
	arc(g, cx, cy, r, start, end - start);
}

void sngl_win32_gfx_close_path(sngl_win32_gfx *g) {
	GdipClosePathFigure(g->path);
	g->x = g->fx;
	g->y = g->fy;
	g->begun = 0;
}

static void new_path(sngl_win32_gfx *g) {
	GdipResetPath(g->path);
	GdipSetPathFillMode(g->path, FillModeWinding);
	g->at = 0;
	g->begun = 0;
}

void sngl_win32_gfx_fill(sngl_win32_gfx *g, int preserve) {
	GpSolidFill *b = NULL;
	if (GdipCreateSolidFill(g->now.color, &b) == Ok) {
		GdipFillPath(g->g, (GpBrush *)b, g->path);
		GdipDeleteBrush((GpBrush *)b);
	}
	if (!preserve) {
		new_path(g);
	}
}

void sngl_win32_gfx_stroke(sngl_win32_gfx *g) {
	static const LineCap caps[] = {LineCapFlat, LineCapRound, LineCapSquare};
	static const DashCap dashes[] = {DashCapFlat, DashCapRound, DashCapFlat};
	static const LineJoin joins[] = {LineJoinMiter, LineJoinRound, LineJoinBevel};
	int cap = g->now.cap >= 0 && g->now.cap < 3 ? g->now.cap : 0;
	int join = g->now.join >= 0 && g->now.join < 3 ? g->now.join : 0;
	GpPen *p = NULL;
	if (GdipCreatePen1(g->now.color, g->now.width, UnitPixel, &p) == Ok) {
		GdipSetPenLineCap197819(p, caps[cap], caps[cap], dashes[cap]);
		GdipSetPenLineJoin(p, joins[join]);
		GdipDrawPath(g->g, p, g->path);
		GdipDeletePen(p);
	}
	new_path(g);
}

void sngl_win32_gfx_font(sngl_win32_gfx *g, const unsigned short *family, double size) {
	wcsncpy(g->now.family, family ? (const WCHAR *)family : L"", 63);
	g->now.family[63] = 0;
	g->now.font_px = (REAL)size;
}

/* family is the font family name names: CSS's generic families as GDI+'s,
 * or the one the system knows by that name, or its sans-serif. */
static GpFontFamily *family(const WCHAR *name) {
	GpFontFamily *f = NULL;
	if (wcscmp(name, L"serif") == 0) {
		GdipGetGenericFontFamilySerif(&f);
	} else if (wcscmp(name, L"monospace") == 0) {
		GdipGetGenericFontFamilyMonospace(&f);
	} else if (wcscmp(name, L"sans-serif") != 0 && name[0]) {
		GdipCreateFontFamilyFromName(name, NULL, &f);
	}
	if (!f) {
		GdipGetGenericFontFamilySansSerif(&f);
	}
	return f;
}

void sngl_win32_gfx_text(sngl_win32_gfx *g, const unsigned short *text) {
	GpFontFamily *ff = family(g->now.family);
	if (!ff) {
		return;
	}
	GpFont *font = NULL;
	GpSolidFill *b = NULL;
	GpStringFormat *fmt = NULL;
	UINT16 ascent = 0, em = 0;
	GdipGetCellAscent(ff, FontStyleRegular, &ascent);
	GdipGetEmHeight(ff, FontStyleRegular, &em);
	if (GdipCreateFont(ff, g->now.font_px, FontStyleRegular, UnitPixel, &font) == Ok && GdipCreateSolidFill(g->now.color, &b) == Ok &&
		GdipStringFormatGetGenericTypographic(&fmt) == Ok) {
		/* Drawn from the top of its line, which is the ascent above the
		 * baseline the point is on. */
		REAL top = g->y - (em ? g->now.font_px * ascent / em : g->now.font_px);
		RectF at = {g->x, top, 0, 0};
		GdipDrawString(g->g, (const WCHAR *)text, -1, font, &at, fmt, (GpBrush *)b);
	}
	if (fmt) {
		GdipDeleteStringFormat(fmt);
	}
	if (b) {
		GdipDeleteBrush((GpBrush *)b);
	}
	if (font) {
		GdipDeleteFont(font);
	}
	GdipDeleteFontFamily(ff);
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
	memset(&g, 0, sizeof g);
	if (c->paint.fn && GdipCreateFromHDC(mem, &g.g) == Ok) {
		GdipCreatePath(FillModeWinding, &g.path);
		GdipSetSmoothingMode(g.g, SmoothingModeAntiAlias);
		GdipSetPixelOffsetMode(g.g, PixelOffsetModeHalf);
		GdipSetTextRenderingHint(g.g, TextRenderingHintAntiAlias);
		g.now.color = 0xff000000;
		g.now.width = 1;
		g.now.font_px = 16;
		wcscpy(g.now.family, L"sans-serif");
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
			ctl *e = find_entry(w, LOWORD(wp));
			if (e) {
				fire(&e->click);
			}
			return 0;
		}
		ctl *src = ctl_of((HWND)lp);
		if (!src) {
			break;
		}
		switch (HIWORD(wp)) {
		case BN_CLICKED:
			/* STN_CLICKED too, a link's. A test's click reports it
			 * without opening anything. */
			if (src->kind == K_LINK && src->href && src->href[0] && !firing) {
				ShellExecuteW(NULL, L"open", src->href, NULL, NULL, SW_SHOWNORMAL);
			}
			fire(&src->click);
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
		if (src && !src->brush && c && c->brush && msg != WM_CTLCOLOREDIT) {
			/* A control with no background of its own shows its box's. */
			SetBkMode((HDC)wp, TRANSPARENT);
			color(src, (HDC)wp, 0);
			return (LRESULT)c->brush;
		}
		return color(src, (HDC)wp, otherwise);
	}
	case WM_ERASEBKGND:
		if (c && c->kind == K_CANVAS) {
			/* Its paint fills what it draws over, off screen, so it does
			 * not flicker. */
			return 1;
		}
		if (c && c->brush) {
			RECT r;
			GetClientRect(w, &r);
			FillRect((HDC)wp, &r, c->brush);
			return 1;
		}
		break;
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
	case SNGL_WM_FETCHED:
		if (c && c->kind == K_PICTURE) {
			WCHAR *path = (WCHAR *)lp;
			const char *why = "the image could not be fetched";
			int bw = 0, bh = 0;
			HBITMAP bmp = path ? decode(NULL, 0, path, &bw, &bh, &why) : NULL;
			free(path);
			shown_as(w, c, bmp, bw, bh, bmp ? NULL : why);
		}
		return 0;
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
			flow_size(w, c, r.right - px(c->pad[1] + c->pad[3], c->dpi), dc);
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
			if (msg == WM_PRINTCLIENT) {
				SendMessageW(w, WM_ERASEBKGND, wp, 0);
			}
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
		 * button is. */
		if (c && c->click.fn) {
			fire(&c->click);
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
				tab_chosen(nm->hwndFrom, src);
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
				src->selected = lv->iItem;
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
			scrolled(w, c, msg == WM_VSCROLL ? SB_VERT : SB_HORZ, LOWORD(wp));
			return 0;
		}
		break;
	case WM_MOUSEWHEEL:
		if (c && c->kind == K_SCROLL) {
			int lines = GET_WHEEL_DELTA_WPARAM(wp) / WHEEL_DELTA;
			for (int i = 0; i < (lines < 0 ? -lines : lines); i++) {
				scrolled(w, c, c->scroll_v ? SB_VERT : SB_HORZ, lines < 0 ? SB_LINEDOWN : SB_LINEUP);
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
		if (c) {
			c->laying = 0;
			refresh(w, dpi_of(w));
			fit(w, 0, 0);
			lay_out_top(w);
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
			if (c->visible.fn) {
				((sngl_win32_bool_fn)(void (*)(void))c->visible.fn)(0, c->visible.ctx);
			}
			fire(&c->closed);
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

/* control_proc subclasses each control the runtime makes: Enter in an edit
 * is its activation, and a control going takes its record with it. */
static LRESULT CALLBACK control_proc(HWND w, UINT msg, WPARAM wp, LPARAM lp, UINT_PTR id, DWORD_PTR data) {
	(void)id;
	(void)data;
	switch (msg) {
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
	SetPropW(w, SNGL_PROP, c);
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
	ctl *c = new_ctl(w, K_TOP);
	if (c) {
		c->vertical = 1;
	}
	return w;
}

HWND sngl_win32_box(int vertical) {
	HWND w = control(SNGL_BOX_CLASS, WS_CLIPCHILDREN, WS_EX_CONTROLPARENT, K_BOX);
	ctl *c = ctl_of(w);
	if (c) {
		c->vertical = vertical;
	}
	return w;
}

void sngl_win32_set_vertical(HWND box, int vertical) {
	ctl *c = ctl_of(box);
	if (c && c->vertical != vertical) {
		c->vertical = vertical;
		if (c->kind == K_DIVIDER) {
			LONG_PTR style = GetWindowLongPtrW(box, GWL_STYLE) & ~(LONG_PTR)(SS_ETCHEDHORZ | SS_ETCHEDVERT);
			SetWindowLongPtrW(box, GWL_STYLE, style | (vertical ? SS_ETCHEDVERT : SS_ETCHEDHORZ));
			InvalidateRect(box, NULL, TRUE);
		}
		relayout(box);
	}
}

void sngl_win32_set_mode(HWND box, int mode) {
	ctl *c = ctl_of(box);
	if (c && c->mode != mode) {
		c->mode = mode;
		c->flow_h = 0;
		relayout(box);
	}
}

void sngl_win32_set_columns(HWND box, int columns) {
	ctl *c = ctl_of(box);
	if (c && c->columns != columns) {
		c->columns = columns;
		relayout(box);
	}
}

void sngl_win32_set_framed(HWND box, int framed) {
	ctl *c = ctl_of(box);
	if (c && c->framed != (framed != 0)) {
		c->framed = framed != 0;
		InvalidateRect(box, NULL, TRUE);
		relayout(box);
	}
}

/* show shows or hides w as neither its prop nor its look hides it, and lays
 * out what it is in again, where it takes room or none. */
static void show(HWND w, ctl *c) {
	int on = !c->off_prop && !c->off_look;
	if ((IsWindowVisible(w) != 0) != on || (GetWindowLongPtrW(w, GWL_STYLE) & WS_VISIBLE) != (on ? WS_VISIBLE : 0)) {
		ShowWindow(w, on ? SW_SHOWNA : SW_HIDE);
		HWND parent = GetParent(w);
		if (parent) {
			relayout(parent);
		}
	}
}

void sngl_win32_set_shown(HWND w, int shown) {
	ctl *c = ctl_of(w);
	if (c) {
		c->off_prop = !shown;
		show(w, c);
	}
}

int sngl_win32_is_shown(HWND w) {
	ctl *c = ctl_of(w);
	return c && !c->off_prop && !c->off_look;
}

void sngl_win32_set_enabled(HWND w, int enabled) {
	EnableWindow(w, enabled != 0);
	InvalidateRect(w, NULL, TRUE);
}

void sngl_win32_set_selected(HWND tabs, int selected) {
	ctl *c = ctl_of(tabs);
	if (c && c->selected != selected) {
		c->selected = selected;
		SendMessageW(tabs, TCM_SETCURSEL, (WPARAM)selected, 0);
		relayout(tabs);
	}
}

void sngl_win32_set_scroll(HWND w, int vertical, int horizontal) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	c->scroll_v = vertical != 0;
	c->scroll_h = horizontal != 0;
	LONG_PTR style = GetWindowLongPtrW(w, GWL_STYLE) & ~(LONG_PTR)(WS_VSCROLL | WS_HSCROLL);
	SetWindowLongPtrW(w, GWL_STYLE, style | (c->scroll_v ? WS_VSCROLL : 0) | (c->scroll_h ? WS_HSCROLL : 0));
	SetWindowPos(w, NULL, 0, 0, 0, 0, SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE | SWP_FRAMECHANGED);
	/* What it holds is laid down its axis, or across where it scrolls only
	 * that way. */
	c->vertical = !(c->scroll_h && !c->scroll_v);
	relayout(w);
}

void sngl_win32_set_tip(HWND box, const unsigned short *tip) {
	ctl *c = ctl_of(box);
	if (!c) {
		return;
	}
	free(c->tip);
	c->tip = _wcsdup((const WCHAR *)tip);
	for (int i = 0; i < c->nkids; i++) {
		tip_tool(box, c->kids[i], c->tip);
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

HWND sngl_win32_text(void) { return control(L"STATIC", SS_LEFT | SS_NOPREFIX, 0, K_TEXT); }

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

/* A page is a box, its title its text. */
HWND sngl_win32_tab_page(void) { return sngl_win32_box(1); }

HWND sngl_win32_scroll(void) {
	HWND w = control(SNGL_BOX_CLASS, WS_CLIPCHILDREN, WS_EX_CONTROLPARENT, K_SCROLL);
	ctl *c = ctl_of(w);
	if (c) {
		c->vertical = 1;
		sngl_win32_set_scroll(w, 1, 0);
	}
	return w;
}

HWND sngl_win32_modal(void) {
	HWND w = control(SNGL_BOX_CLASS, 0, 0, K_MODAL);
	ctl *c = ctl_of(w);
	if (!c) {
		return w;
	}
	c->off_prop = 1;
	ShowWindow(w, SW_HIDE);
	c->dialog = CreateWindowExW(WS_EX_DLGMODALFRAME | WS_EX_CONTROLPARENT, SNGL_TOP_CLASS, L"", WS_POPUP | WS_CAPTION | WS_CLIPCHILDREN, 0, 0, 0, 0, park, NULL, GetModuleHandleW(NULL), NULL);
	ctl *d = new_ctl(c->dialog, K_TOP);
	if (d) {
		d->vertical = 1;
		d->gap = 6;
		d->pad[0] = d->pad[1] = d->pad[2] = d->pad[3] = 12;
		d->modal_of = w;
	}
	return w;
}

HWND sngl_win32_entry(void) {
	static UINT next_entry = 1000;
	HWND w = control(SNGL_BOX_CLASS, 0, 0, K_ENTRY);
	ctl *c = ctl_of(w);
	if (c) {
		c->off_prop = 1;
		c->id = next_entry++;
		ShowWindow(w, SW_HIDE);
	}
	return w;
}

void sngl_win32_set_entry_disabled(HWND w, int disabled) {
	ctl *c = ctl_of(w);
	if (c && c->disabled != (disabled != 0)) {
		c->disabled = disabled != 0;
		menu_sync(w);
	}
}

void sngl_win32_set_entry_checked(HWND w, int checked) {
	ctl *c = ctl_of(w);
	if (c && c->checked != (checked != 0)) {
		c->checked = checked != 0;
		menu_sync(w);
	}
}

/* entry_labeled is the entry under w labeled label, or NULL. */
static HWND entry_labeled(HWND w, const WCHAR *label) {
	ctl *c = ctl_of(w);
	if (!c) {
		return NULL;
	}
	if (c->kind == K_ENTRY) {
		WCHAR buf[256];
		GetWindowTextW(w, buf, 256);
		if (wcscmp(buf, label) == 0) {
			return w;
		}
	}
	for (int i = 0; i < c->nkids; i++) {
		HWND e = entry_labeled(c->kids[i], label);
		if (e) {
			return e;
		}
	}
	return NULL;
}

int sngl_win32_choose(HWND w, const unsigned short *label) {
	HWND top = GetAncestor(w, GA_ROOT);
	ctl *c = ctl_of(top);
	HWND e = entry_labeled(top, (const WCHAR *)label);
	if (!c || !c->menu || !e) {
		return 0;
	}
	/* As the user's choice: through the menu, where a grayed item is none. */
	ctl *ec = ctl_of(e);
	MENUITEMINFOW mi = {sizeof mi, MIIM_STATE, 0, 0, 0, NULL, NULL, NULL, 0, NULL, 0, NULL};
	if (!GetMenuItemInfoW(c->menu, ec->id, FALSE, &mi) || (mi.fState & MFS_GRAYED)) {
		return 0;
	}
	SendMessageW(top, WM_COMMAND, MAKEWPARAM(ec->id, 0), 0);
	return 1;
}

int sngl_win32_menu_items(HWND w) {
	ctl *c = ctl_of(GetAncestor(w, GA_ROOT));
	return c && c->menu ? GetMenuItemCount(c->menu) : 0;
}

HWND sngl_win32_table(void) {
	HWND w = control(WC_LISTVIEWW, LVS_REPORT | LVS_SINGLESEL | LVS_SHOWSELALWAYS | LVS_NOSORTHEADER | WS_TABSTOP, WS_EX_CLIENTEDGE, K_TABLE);
	SendMessageW(w, LVM_SETEXTENDEDLISTVIEWSTYLE, 0, LVS_EX_FULLROWSELECT | LVS_EX_GRIDLINES);
	ctl *c = ctl_of(w);
	if (c) {
		c->selected = -1;
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
	select_row(w, c, c->selected);
	c->quiet--;
	relayout(w);
}

void sngl_win32_set_row(HWND w, int row) {
	ctl *c = ctl_of(w);
	if (c) {
		c->selected = row;
		select_row(w, c, row);
	}
}

int sngl_win32_row(HWND w) {
	ctl *c = ctl_of(w);
	return c ? c->selected : -1;
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

static void tree_select(HWND w, ctl *c) {
	HTREEITEM root = (HTREEITEM)SendMessageW(w, TVM_GETNEXTITEM, TVGN_ROOT, 0);
	HTREEITEM it = c->chosen && c->chosen[0] ? tree_find(w, root, c->chosen) : NULL;
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

const char *sngl_win32_date_of(HWND w) {
	SYSTEMTIME st;
	if (SendMessageW(w, DTM_GETSYSTEMTIME, 0, (LPARAM)&st) != GDT_VALID) {
		return "";
	}
	WCHAR buf[16];
	swprintf(buf, 16, L"%04d-%02d-%02d", st.wYear, st.wMonth, st.wDay);
	return utf8(buf);
}

/* find_kind is the first node of kind under w, w itself included, or NULL. */
static HWND find_kind(HWND w, enum sngl_kind kind) {
	ctl *c = ctl_of(w);
	if (!c) {
		return NULL;
	}
	if (c->kind == kind) {
		return w;
	}
	for (int i = 0; i < c->nkids; i++) {
		HWND k = find_kind(c->kids[i], kind);
		if (k) {
			return k;
		}
	}
	return NULL;
}

int sngl_win32_pick_row(HWND w, int row) {
	HWND t = find_kind(GetAncestor(w, GA_ROOT), K_TABLE);
	if (!t) {
		return 0;
	}
	LVITEMW it;
	memset(&it, 0, sizeof it);
	it.stateMask = LVIS_SELECTED | LVIS_FOCUSED;
	it.state = LVIS_SELECTED | LVIS_FOCUSED;
	return (int)SendMessageW(t, LVM_SETITEMSTATE, (WPARAM)row, (LPARAM)&it);
}

int sngl_win32_click_column(HWND w, int col) {
	HWND t = find_kind(GetAncestor(w, GA_ROOT), K_TABLE);
	if (!t) {
		return 0;
	}
	NMLISTVIEW lv;
	memset(&lv, 0, sizeof lv);
	lv.hdr.hwndFrom = t;
	lv.hdr.idFrom = (UINT_PTR)GetDlgCtrlID(t);
	lv.hdr.code = LVN_COLUMNCLICK;
	lv.iItem = -1;
	lv.iSubItem = col;
	SendMessageW(GetParent(t), WM_NOTIFY, lv.hdr.idFrom, (LPARAM)&lv);
	return 1;
}

int sngl_win32_pick_node(HWND w, const unsigned short *label) {
	HWND t = find_kind(GetAncestor(w, GA_ROOT), K_TREE);
	if (!t) {
		return 0;
	}
	HTREEITEM it = tree_find(t, (HTREEITEM)SendMessageW(t, TVM_GETNEXTITEM, TVGN_ROOT, 0), (const WCHAR *)label);
	return it && SendMessageW(t, TVM_SELECTITEM, TVGN_CARET, (LPARAM)it);
}

int sngl_win32_pick_date(HWND w, const unsigned short *iso) {
	HWND t = find_kind(GetAncestor(w, GA_ROOT), K_DATE);
	if (!t) {
		return 0;
	}
	sngl_win32_set_date(t, iso);
	NMDATETIMECHANGE ch;
	memset(&ch, 0, sizeof ch);
	ch.nmhdr.hwndFrom = t;
	ch.nmhdr.idFrom = (UINT_PTR)GetDlgCtrlID(t);
	ch.nmhdr.code = DTN_DATETIMECHANGE;
	ch.dwFlags = (DWORD)SendMessageW(t, DTM_GETSYSTEMTIME, 0, (LPARAM)&ch.st);
	SendMessageW(GetParent(t), WM_NOTIFY, ch.nmhdr.idFrom, (LPARAM)&ch);
	return 1;
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
	if (!gdiplus) {
		GdiplusStartupInput in = {1, NULL, FALSE, FALSE};
		GdiplusStartup(&gdiplus, &in, NULL);
	}
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

void sngl_win32_set_scaling(HWND w, int mode) {
	ctl *c = ctl_of(w);
	if (c && c->scaling != mode) {
		c->scaling = mode;
		/* A drawing that scales grows into the room its box has spare. */
		c->flex = mode != 0 ? 1 : 0;
		InvalidateRect(w, NULL, FALSE);
		relayout(w);
	}
}

void sngl_win32_on_paint(HWND w, sngl_win32_paint_fn fn, void *ctx, sngl_win32_release release) {
	ctl *c = ctl_of(w);
	if (c) {
		set_handler(&c->paint, (sngl_win32_fn)(void (*)(void))fn, ctx, release);
		InvalidateRect(w, NULL, FALSE);
	}
}

void sngl_win32_redraw(HWND w) {
	if (w) {
		InvalidateRect(w, NULL, FALSE);
	}
}

void sngl_win32_set_fit(HWND w, int fit) {
	ctl *c = ctl_of(w);
	if (c) {
		c->fit = fit;
		for (int i = 0; i < c->nkids; i++) {
			InvalidateRect(c->kids[i], NULL, TRUE);
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

static void kids_insert(ctl *c, HWND child, int at) {
	if (c->nkids == c->capkids) {
		c->capkids = c->capkids ? c->capkids * 2 : 4;
		c->kids = realloc(c->kids, sizeof(HWND) * (size_t)c->capkids);
	}
	memmove(c->kids + at + 1, c->kids + at, sizeof(HWND) * (size_t)(c->nkids - at));
	c->kids[at] = child;
	c->nkids++;
}

static int kids_index(ctl *c, HWND child) {
	for (int i = 0; i < c->nkids; i++) {
		if (c->kids[i] == child) {
			return i;
		}
	}
	return -1;
}

/* holds_entries is whether w is a menu entry or holds one. */
static int holds_entries(HWND w) {
	ctl *c = ctl_of(w);
	if (!c) {
		return 0;
	}
	if (c->kind == K_ENTRY) {
		return 1;
	}
	for (int i = 0; i < c->nkids; i++) {
		if (holds_entries(c->kids[i])) {
			return 1;
		}
	}
	return 0;
}

/* host is the window that holds what is put in parent: a modal's own,
 * and otherwise parent. */
static HWND host(HWND parent) {
	ctl *c = ctl_of(parent);
	return c && c->kind == K_MODAL ? c->dialog : parent;
}

static void adopt(HWND parent, HWND child, int at) {
	ctl *c = ctl_of(parent);
	if (!c) {
		return;
	}
	SetParent(child, parent);
	kids_insert(c, child, at);
	if (c->kind == K_TABS) {
		tabs_sync(parent);
	}
	if (c->tip) {
		tip_tool(parent, child, c->tip);
	}
	if (holds_entries(child)) {
		menu_sync(parent);
	}
	relayout(parent);
}

void sngl_win32_append(HWND parent, HWND child) {
	parent = host(parent);
	ctl *c = ctl_of(parent);
	if (c) {
		adopt(parent, child, c->nkids);
	}
}

void sngl_win32_insert(HWND parent, HWND child, HWND before) {
	parent = host(parent);
	ctl *c = ctl_of(parent);
	if (!c) {
		return;
	}
	int at = kids_index(c, before);
	adopt(parent, child, at < 0 ? c->nkids : at);
}

void sngl_win32_remove(HWND parent, HWND child) {
	parent = host(parent);
	ctl *c = ctl_of(parent);
	if (c) {
		int at = kids_index(c, child);
		if (at >= 0) {
			memmove(c->kids + at, c->kids + at + 1, sizeof(HWND) * (size_t)(c->nkids - at - 1));
			c->nkids--;
		}
		if (c->tip) {
			tip_tool(parent, child, NULL);
		}
	}
	int entries = holds_entries(child);
	DestroyWindow(child);
	if (entries) {
		menu_sync(parent);
	}
	if (c && c->kind == K_TABS) {
		if (c->selected >= c->nkids) {
			c->selected = c->nkids > 0 ? c->nkids - 1 : 0;
		}
		tabs_sync(parent);
	}
	if (c) {
		relayout(parent);
	}
}

void sngl_win32_attach(HWND top) {
	ctl *c = ctl_of(top);
	if (!c) {
		return;
	}
	if (c->kind != K_TOP) {
		/* A node the program attaches bare -- a test's component, a
		 * package body with no window -- is shown in a window of its
		 * own. */
		HWND node = top;
		top = sngl_win32_toplevel();
		sngl_win32_append(top, node);
	}
	/* Sized to what it holds, as a GTK window is to its content. */
	SIZE s;
	measure(top, &s);
	RECT r = {0, 0, s.cx < 200 ? 200 : s.cx, s.cy < 100 ? 100 : s.cy};
	AdjustWindowRectEx(&r, (DWORD)GetWindowLongPtrW(top, GWL_STYLE), FALSE, (DWORD)GetWindowLongPtrW(top, GWL_EXSTYLE));
	SetWindowPos(top, NULL, 0, 0, r.right - r.left, r.bottom - r.top, SWP_NOMOVE | SWP_NOZORDER | SWP_NOACTIVATE);
	if (holding) {
		/* Not shown yet, which the window's :visible hears. */
		held = realloc(held, sizeof(HWND) * (size_t)(nheld + 1));
		held[nheld++] = top;
		if (c->visible.fn) {
			((sngl_win32_bool_fn)(void (*)(void))c->visible.fn)(0, c->visible.ctx);
		}
	} else {
		sngl_win32_set_visible(top, 1);
	}
	lay_out_top(top);
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
	DestroyWindow(top);
}

/* --- Props --- */

void sngl_win32_set_text(HWND w, const unsigned short *text) {
	ctl *c = ctl_of(w);
	if (c && c->kind == K_MODAL) {
		/* A modal's is its window's title. */
		SetWindowTextW(c->dialog, (LPCWSTR)text);
		return;
	}
	SetWindowTextW(w, (LPCWSTR)text);
	if (c && c->kind == K_ENTRY) {
		menu_sync(w);
		return;
	}
	HWND parent = GetParent(w);
	ctl *pc = ctl_of(parent);
	if (pc && pc->kind == K_TABS) {
		/* A page's is its tab's. */
		tabs_sync(parent);
	}
	relayout(w);
}

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

/* find is the choice of a drop-down labeled label, or -1. */
static int find(HWND w, const WCHAR *label) {
	int n = (int)SendMessageW(w, CB_GETCOUNT, 0, 0);
	for (int i = 0; i < n; i++) {
		int len = (int)SendMessageW(w, CB_GETLBTEXTLEN, (WPARAM)i, 0);
		WCHAR *buf = malloc(sizeof(WCHAR) * (size_t)(len + 1));
		SendMessageW(w, CB_GETLBTEXT, (WPARAM)i, (LPARAM)buf);
		int same = wcscmp(buf, label) == 0;
		free(buf);
		if (same) {
			return i;
		}
	}
	return -1;
}

void sngl_win32_offer(HWND w, const unsigned short *labels, const unsigned short *chosen) {
	SendMessageW(w, CB_RESETCONTENT, 0, 0);
	const WCHAR *at = (const WCHAR *)labels;
	if (*at) {
		for (;;) {
			const WCHAR *end = wcschr(at, L'\n');
			size_t len = end ? (size_t)(end - at) : wcslen(at);
			WCHAR *one = malloc(sizeof(WCHAR) * (len + 1));
			memcpy(one, at, sizeof(WCHAR) * len);
			one[len] = 0;
			SendMessageW(w, CB_ADDSTRING, 0, (LPARAM)one);
			free(one);
			if (!end) {
				break;
			}
			at = end + 1;
		}
	}
	/* CB_SETCURSEL reports nothing, which is what an offer wants. */
	SendMessageW(w, CB_SETCURSEL, (WPARAM)find(w, (const WCHAR *)chosen), 0);
	relayout(w);
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

void sngl_win32_set_checked(HWND w, int checked) { SendMessageW(w, BM_SETCHECK, checked ? BST_CHECKED : BST_UNCHECKED, 0); }

int sngl_win32_checked(HWND w) { return SendMessageW(w, BM_GETCHECK, 0, 0) == BST_CHECKED; }

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
	int color = look.color, background = look.background, font_px = look.fontPx;
	c->flex = look.flex;
	c->gap = look.gap;
	c->pad[0] = look.padTop;
	c->pad[1] = look.padRight;
	c->pad[2] = look.padBottom;
	c->pad[3] = look.padLeft;
	c->min_w = look.minWidth;
	c->min_h = look.minHeight;
	if (c->off_look != (look.hidden != 0)) {
		c->off_look = look.hidden != 0;
		show(w, c);
	}
	c->color = color;
	if (c->brush) {
		DeleteObject(c->brush);
		c->brush = NULL;
	}
	c->background = background;
	if (background >= 0) {
		c->brush = CreateSolidBrush(RGB((background >> 16) & 0xff, (background >> 8) & 0xff, background & 0xff));
	}
	if (font_px != c->font_px) {
		c->font_px = font_px;
		own_font(c);
		SendMessageW(w, WM_SETFONT, (WPARAM)font_of(c), TRUE);
	}
	InvalidateRect(w, NULL, TRUE);
	relayout(w);
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

void sngl_win32_fire_check(HWND w, int checked) {
	sngl_win32_set_checked(w, checked);
	notify(w, BN_CLICKED);
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
	SendMessageW(w, WM_KEYDOWN, VK_RETURN, 0);
}

void sngl_win32_fire_select(HWND w, const unsigned short *label) {
	SendMessageW(w, CB_SETCURSEL, (WPARAM)find(w, (const WCHAR *)label), 0);
	notify(w, CBN_SELCHANGE);
}

void sngl_win32_fire_close(HWND top) {
	ctl *c = ctl_of(top);
	if (c && c->kind == K_MODAL) {
		/* A modal's close is its window's. */
		top = c->dialog;
	}
	SendMessageW(top, WM_CLOSE, 0, 0);
}

void sngl_win32_fire_tab(HWND tabs, int index) {
	SendMessageW(tabs, TCM_SETCURSEL, (WPARAM)index, 0);
	NMHDR nm = {tabs, (UINT_PTR)GetDlgCtrlID(tabs), TCN_SELCHANGE};
	SendMessageW(GetParent(tabs), WM_NOTIFY, nm.idFrom, (LPARAM)&nm);
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
		AdjustWindowRectEx(&r, (DWORD)GetWindowLongPtrW(top, GWL_STYLE), FALSE, (DWORD)GetWindowLongPtrW(top, GWL_EXSTYLE));
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
