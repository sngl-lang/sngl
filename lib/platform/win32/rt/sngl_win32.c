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
/* A picture's load reported to its image, once it is in one; and a fetch
 * arrived, the file it was saved to the message's lParam, or 0. */
#define SNGL_WM_LOADED (WM_APP + 5)
#define SNGL_WM_FETCHED (WM_APP + 6)
/* A toplevel's :visible told, its wParam the value: as an @run handler
 * holds it back and presents it. */
#define SNGL_WM_VISIBLE (WM_APP + 7)

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
 * which is sngl:platform/win32's (_Ctl): what C measures and draws it by. The
 * record holds the rest -- its handlers, its colors' brush, its tip, a
 * modal's window, a table's selection -- and the window procedures reading
 * them (procs.sngl). */
typedef struct {
	enum sngl_kind kind;
	/* color is what a flow's words and a progress bar's text are drawn
	 * in, 0xRRGGBB or -1, and font_px its font's size, as its look sets
	 * them (sngl_win32_set_paint). */
	int color, font_px;
	/* dpi is what its font is made for, the record's as its layout last
	 * refreshed it (sngl_win32_refont). */
	int dpi;
	HFONT font; /* the node's own, for a font_px; else the message font */
	/* quiet is set while the runtime writes an edit's text, or a table's,
	 * a tree's or a date picker's, which Win32 reports as a change the
	 * program did not make. */
	int quiet;
	/* fraction is a progress bar's, negative while it is indeterminate,
	 * which show_text writes over it. */
	double fraction;
	int show_text;
	/* href is a span's, which its flow draws as a link. */
	WCHAR *href;
	/* A tree's label selected. */
	WCHAR *chosen;
	/* Why an image's picture failed; a picture's bitmap, its size, and
	 * what it shows, and the bitmap scaled to the size it was last painted
	 * at. */
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

/* What sngl:platform/win32 hands the runtime as its package starts: the
 * window procedures, a box's and a toplevel's and the one each control is
 * subclassed with (procs.sngl), which its classes are registered with; and
 * what rich text reads of each window's record, its children in order.
 * Before they are handed over, the runtime makes no window. */
static struct {
	WNDPROC box;
	SUBCLASSPROC control;
	sngl_win32_count_hook nkids;
	sngl_win32_kid_hook kid;
} procs;

void sngl_win32_procs(WNDPROC box, SUBCLASSPROC control, sngl_win32_count_hook nkids, sngl_win32_kid_hook kid) {
	procs.box = box;
	procs.control = control;
	procs.nkids = nkids;
	procs.kid = kid;
	sngl_win32_init();
}

/* nkids is how many children w's record holds, and kid the one at i. */
static int nkids(HWND w) { return procs.nkids ? procs.nkids(w) : 0; }

static HWND kid(HWND w, int i) { return procs.kid(w, i); }

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

/* sngl_win32_forget frees what the runtime keeps beside w, as w goes, and
 * counts a toplevel shown out of those on screen where `counted`. */
void sngl_win32_forget(HWND w, int counted) {
	if (counted) {
		shown--;
	}
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	ctl_drop(w);
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
	if (c->font) {
		DeleteObject(c->font);
	}
	free(c->href);
	free(c->chosen);
	free(c);
}

/* --- Layout --- */

static const char *utf8(const WCHAR *wide);

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
 * draw where it is not NULL, from x0, y0, which is where the flow's links
 * are found. */
static SIZE flow_size(HWND w, ctl *c, int width, HDC draw, int x0, int y0) {
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
 * metrics, or what it asks the control for: a drop-down, a progress bar,
 * a tree, a canvas and a flow. The rest are measured by their record
 * (_measure). */
void sngl_win32_natural(HWND w, SIZE *out) {
	ctl *c = ctl_of(w);
	SIZE s = {0, 0};
	if (!c) {
		*out = s;
		return;
	}
	switch (c->kind) {
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
	case K_TREE:
		s = tree_size(w, c);
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
		 * there. */
		int most = px(240, c->dpi);
		s = flow_size(w, c, most, 0, 0, 0);
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

/* sngl_win32_text_size is w's text as its font draws it, sngl_win32_line_height
 * a line of its font, and sngl_win32_check_width a check box's box at dpi. */
void sngl_win32_text_size(HWND w, SIZE *out) { *out = text_size(w); }

int sngl_win32_line_height(HWND w) { return line_height(w); }

int sngl_win32_check_width(int dpi) { return system_metric_for_dpi ? system_metric_for_dpi(SM_CXMENUCHECK, (UINT)dpi) : GetSystemMetrics(SM_CXMENUCHECK); }

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
	return c ? flow_size(w, c, width, 0, 0, 0).cy : 0;
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

/* --- Menus --- */

/* sngl_win32_append_popup appends a submenu to m, whose handle AppendMenuW
 * takes as its id. */
void sngl_win32_append_popup(HMENU m, UINT flags, HMENU sub, const unsigned short *label) { AppendMenuW(m, flags, (UINT_PTR)sub, (LPCWSTR)label); }

/* tips is the tooltip every box with a tip shows its children's through. */
static HWND tips;

/* sngl_win32_tip_tool gives child, one of box's, the tip text, or none
 * where it is NULL or empty. */
void sngl_win32_tip_tool(HWND box, HWND child, const unsigned short *tip) {
	const WCHAR *text = (const WCHAR *)tip;
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

/* paint_picture draws a picture's bitmap into its w by h area, over
 * backdrop, as its image's fit says: whole and centered, covering it,
 * stretched over it, at its own size, or whole and no larger than its own. */
static void paint_picture(HWND w, ctl *c, HDC dc, HBRUSH backdrop, int fit) {
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
	FillRect(buf, &r, backdrop ? backdrop : (HBRUSH)GetClassLongPtrW(w, GCLP_HBRBACKGROUND));
	if (c->bmp) {
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
static void paint_canvas(HWND w, ctl *c, HDC dc, HBRUSH own) {
	RECT r;
	GetClientRect(w, &r);
	int aw = r.right, ah = r.bottom;
	if (aw <= 0 || ah <= 0) {
		return;
	}
	HDC mem = CreateCompatibleDC(dc);
	HBITMAP bmp = CreateCompatibleBitmap(dc, aw, ah);
	HGDIOBJ old = SelectObject(mem, bmp);
	HBRUSH bg = own ? own : (HBRUSH)GetClassLongPtrW(w, GCLP_HBRBACKGROUND);
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

/* --- What the window procedures ask --- */

/* The window procedures are sngl:platform/win32's (procs.sngl), which ask
 * the runtime for what it draws and keeps: a window painted, a picture's
 * load, a fetch arrived, a post or a timer's tick, a flow's link, and the
 * few conversions of a message's parameters a declaration does not make. */

/* sngl_win32_paint paints w on WM_PAINT, or into the DC a WM_PRINTCLIENT
 * hands it: a flow's words from its padding, a canvas over its own brush, a
 * picture over the backdrop of the boxes holding it as its image's fit
 * says, or a framed box's etched edge. */
LRESULT sngl_win32_paint(HWND w, UINT msg, WPARAM wp, HBRUSH own, HBRUSH backdrop, int fit, int pad_top, int pad_right, int pad_left) {
	ctl *c = ctl_of(w);
	PAINTSTRUCT ps;
	HDC dc = msg == WM_PRINTCLIENT ? (HDC)wp : BeginPaint(w, &ps);
	if (c && c->kind == K_FLOW) {
		if (msg == WM_PRINTCLIENT) {
			SendMessageW(w, WM_ERASEBKGND, wp, 0);
		}
		RECT r;
		GetClientRect(w, &r);
		flow_size(w, c, r.right - px(pad_right + pad_left, c->dpi), dc, px(pad_left, c->dpi), px(pad_top, c->dpi));
	} else if (c && c->kind == K_CANVAS) {
		paint_canvas(w, c, dc, own);
	} else if (c && c->kind == K_PICTURE) {
		paint_picture(w, c, dc, backdrop, fit);
	} else {
		if (msg == WM_PRINTCLIENT) {
			/* PrintWindow erases through this DC too. */
			SendMessageW(w, WM_ERASEBKGND, wp, 0);
		}
		RECT r;
		GetClientRect(w, &r);
		DrawEdge(dc, &r, EDGE_ETCHED, BF_RECT);
	}
	if (msg != WM_PRINTCLIENT) {
		EndPaint(w, &ps);
	}
	return 0;
}

/* sngl_win32_paint_bar is a progress bar's paint: Win32's, and its text
 * written over it where it shows any, as GTK's does; PrintWindow, a
 * snapshot's, asks for the client area with WM_PRINTCLIENT and the DC it
 * draws in. */
LRESULT sngl_win32_paint_bar(HWND w, UINT msg, WPARAM wp, LPARAM lp) {
	ctl *c = ctl_of(w);
	LRESULT r = DefSubclassProc(w, msg, wp, lp);
	if (!c || !c->show_text) {
		return r;
	}
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

/* sngl_win32_loaded hands a picture's load to the image framing it: why it
 * failed, which sngl_win32_image_failure reads, and 1 where it shows a
 * bitmap, 2 where it failed, and 0 for neither. */
int sngl_win32_loaded(HWND picture, HWND frame) {
	ctl *c = ctl_of(picture), *f = ctl_of(frame);
	if (!c || !f) {
		return 0;
	}
	free(f->why);
	f->why = c->why ? _strdup(c->why) : NULL;
	return c->bmp ? 1 : c->why ? 2 : 0;
}

/* sngl_win32_fetched takes a fetch's file, the message's lParam, or 0, for
 * the URL its wParam holds: a canvas's picture painted again, a drawing's
 * drawn again, or a picture shown. */
void sngl_win32_fetched(HWND w, WPARAM wp, LPARAM lp) {
	ctl *c = ctl_of(w);
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
	/* A fetch for a URL the picture no longer shows is dropped: a slow one
	 * may land after the one for its new URL. */
	if (c && c->kind == K_PICTURE && c->url && wcscmp(c->url, url) == 0) {
		const char *why = "the image could not be fetched";
		int bw = 0, bh = 0;
		HBITMAP bmp = path ? decode(NULL, 0, path, &bw, &bh, &why) : NULL;
		shown_as(w, c, bmp, bw, bh, bmp ? NULL : why);
	}
	free(url);
	free(path);
}

/* sngl_win32_posted runs what sngl_post queued, the message's lParam. */
void sngl_win32_posted(LPARAM lp) {
	handler *h = (handler *)lp;
	fire(h);
	drop_handler(h);
	free(h);
}

/* sngl_win32_timer ticks the timer wParam names, where w is the parked
 * window that owns the timers, and answers whether it was. */
int sngl_win32_timer(HWND w, WPARAM wp) {
	if (w != park) {
		return 0;
	}
	tick(wp);
	return 1;
}

/* sngl_win32_link_under is the span leading where the link a flow drew at
 * x, y does, or NULL, and sngl_win32_over_link whether the pointer is over
 * one. */
HWND sngl_win32_link_under(HWND flow, int x, int y) {
	ctl *c = ctl_of(flow);
	return c ? link_under(c, x, y) : NULL;
}

int sngl_win32_over_link(HWND flow) {
	POINT pt;
	GetCursorPos(&pt);
	ScreenToClient(flow, &pt);
	return sngl_win32_link_under(flow, pt.x, pt.y) != NULL;
}

/* sngl_win32_hand shows the hand, where what is under the pointer is
 * clicked. */
void sngl_win32_hand(void) { SetCursor(LoadCursorW(NULL, (LPCWSTR)IDC_HAND)); }

/* What a message's parameter holds where it holds a handle: a window, a
 * WM_COMMAND's lParam; a DC, a WM_CTLCOLOR*'s wParam; and the brush a
 * WM_CTLCOLOR* answers. */
HWND sngl_win32_window_of(LPARAM lp) { return (HWND)lp; }

HDC sngl_win32_dc_of(WPARAM wp) { return (HDC)wp; }

LRESULT sngl_win32_brush_answer(HBRUSH b) { return (LRESULT)b; }

/* sngl_win32_brush is a brush of rgb, 0xRRGGBB, which sngl_win32_drop_brush
 * deletes. */
HBRUSH sngl_win32_brush(int rgb) { return CreateSolidBrush(RGB((rgb >> 16) & 0xff, (rgb >> 8) & 0xff, rgb & 0xff)); }

void sngl_win32_drop_brush(HBRUSH b) { DeleteObject(b); }

/* sngl_win32_fill fills r of dc with b, or with w's class's background
 * where b is NULL; sngl_win32_focus_rect draws the focus inside r. */
void sngl_win32_fill(HWND w, HDC dc, RECT r, HBRUSH b) { FillRect(dc, &r, b ? b : (HBRUSH)GetClassLongPtrW(w, GCLP_HBRBACKGROUND)); }

void sngl_win32_focus_rect(HDC dc, RECT r) { DrawFocusRect(dc, &r); }

/* sngl_win32_draw_label draws text in r of dc in w's font, as format
 * says. */
void sngl_win32_draw_label(HWND w, HDC dc, const unsigned short *text, RECT r, UINT format) {
	HGDIOBJ old = SelectObject(dc, font_of(ctl_of(w)));
	DrawTextW(dc, (const WCHAR *)text, -1, &r, format);
	SelectObject(dc, old);
}

/* sngl_win32_quiet is whether the runtime is writing w, which reports a
 * change the program did not make. */
int sngl_win32_quiet(HWND w) {
	ctl *c = ctl_of(w);
	return c && c->quiet;
}

/* sngl_win32_unsubclass takes w's subclass off it, as it goes. */
void sngl_win32_unsubclass(HWND w) { RemoveWindowSubclass(w, procs.control, 1); }

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
	if (ready || !procs.box) {
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
	wc.lpfnWndProc = procs.box;
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

/* new_ctl gives w what the runtime keeps beside it, or NULL where there is
 * no w: Win32 made no window, and each call on one is then a call on NULL,
 * which Win32 refuses. Its record is the platform's, which it makes as the
 * function making w returns (_made). */
static ctl *new_ctl(HWND w, enum sngl_kind kind) {
	if (!w) {
		return NULL;
	}
	ctl *c = calloc(1, sizeof *c);
	c->kind = kind;
	c->dpi = dpi_of(w);
	c->color = -1;
	c->self = w;
	ctl_put(w, c);
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
		SetWindowSubclass(w, procs.control, 1, 0);
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

/* sngl_win32_closable gives a modal's window a close button, or none. */
void sngl_win32_closable(HWND dialog, int closable) {
	LONG_PTR style = GetWindowLongPtrW(dialog, GWL_STYLE) & ~(LONG_PTR)WS_SYSMENU;
	SetWindowLongPtrW(dialog, GWL_STYLE, style | (closable ? WS_SYSMENU : 0));
	SetWindowPos(dialog, NULL, 0, 0, 0, 0, SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE | SWP_FRAMECHANGED);
}

/* sngl_win32_set_owner makes owner, or none, the window a modal's is shown
 * over. */
void sngl_win32_set_owner(HWND dialog, HWND owner) { SetWindowLongPtrW(dialog, GWLP_HWNDPARENT, (LONG_PTR)owner); }

/* sngl_win32_frame_size is the size w's frame takes for a client area of cw
 * by ch. */
void sngl_win32_frame_size(HWND w, int cw, int ch, SIZE *out) {
	RECT r = {0, 0, cw, ch};
	AdjustWindowRectEx(&r, (DWORD)GetWindowLongPtrW(w, GWL_STYLE), GetMenu(w) != NULL, (DWORD)GetWindowLongPtrW(w, GWL_EXSTYLE));
	out->cx = r.right - r.left;
	out->cy = r.bottom - r.top;
}

/* sngl_win32_set_href is what a span leads to, which its flow draws as a
 * link. */
void sngl_win32_set_href(HWND w, const unsigned short *href) {
	ctl *c = ctl_of(w);
	if (c) {
		free(c->href);
		c->href = _wcsdup((const WCHAR *)href);
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
	ShowWindow(w, SW_HIDE);
	return w;
}

/* sngl_win32_dialog is the window a modal shows what it holds in, a
 * toplevel owned by the window the modal is in while it is open. */
HWND sngl_win32_dialog(void) {
	HWND d = CreateWindowExW(WS_EX_DLGMODALFRAME | WS_EX_CONTROLPARENT, SNGL_TOP_CLASS, L"", WS_POPUP | WS_CAPTION | WS_CLIPCHILDREN, 0, 0, 0, 0, park, NULL, GetModuleHandleW(NULL), NULL);
	new_ctl(d, K_TOP);
	return d;
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

void sngl_win32_fill_table(HWND w, const unsigned short *columns, const unsigned short *rows, int row) {
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
	select_row(w, c, row);
	c->quiet--;
}

void sngl_win32_select_row(HWND w, int row) {
	ctl *c = ctl_of(w);
	if (c) {
		select_row(w, c, row);
	}
}

/* sngl_win32_sort_header lets a table's column headers be clicked, or
 * not. */
void sngl_win32_sort_header(HWND w, int sortable) {
	LONG_PTR style = GetWindowLongPtrW(w, GWL_STYLE) & ~(LONG_PTR)LVS_NOSORTHEADER;
	SetWindowLongPtrW(w, GWL_STYLE, style | (sortable ? 0 : LVS_NOSORTHEADER));
}

const char *sngl_win32_column(HWND w, int col) {
	WCHAR buf[256] = {0};
	LVCOLUMNW lc;
	memset(&lc, 0, sizeof lc);
	lc.mask = LVCF_TEXT;
	lc.pszText = buf;
	lc.cchTextMax = 256;
	SendMessageW(w, LVM_GETCOLUMNW, (WPARAM)col, (LPARAM)&lc);
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

static WCHAR *dup16(const unsigned short *s) { return s && s[0] ? _wcsdup((const WCHAR *)s) : NULL; }

void sngl_win32_set_words(HWND w, const unsigned short *words) {
	ctl *c = ctl_of(w);
	if (c) {
		free(c->words);
		c->words = dup16(words);
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
	}
}

HWND sngl_win32_canvas(void) {
	gdiplus_start();
	return control(SNGL_BOX_CLASS, 0, 0, K_CANVAS);
}

/* canvas_size answers whether the size changed, and its record lays it
 * out again (_rtSetCanvasWidth). */
static int canvas_size(HWND w, int *at, int v) {
	if (*at != v) {
		*at = v;
		InvalidateRect(w, NULL, FALSE);
		return 1;
	}
	return 0;
}

int sngl_win32_set_canvas_width(HWND w, int width) {
	ctl *c = ctl_of(w);
	return c ? canvas_size(w, &c->cw, width) : 0;
}

int sngl_win32_set_canvas_height(HWND w, int height) {
	ctl *c = ctl_of(w);
	return c ? canvas_size(w, &c->ch, height) : 0;
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

HWND sngl_win32_radio(void) { return control(L"BUTTON", BS_AUTORADIOBUTTON | WS_TABSTOP, 0, K_RADIO); }

HWND sngl_win32_select(void) { return control(WC_COMBOBOXW, CBS_DROPDOWNLIST | WS_VSCROLL | WS_TABSTOP, 0, K_SELECT); }

/* --- The tree --- */

/* sngl_win32_nothing is no window, which a slot's anchor is in a container
 * keeping none. */
HWND sngl_win32_nothing(void) { return NULL; }

/* sngl_win32_show_attached shows a toplevel attach sized, or holds it back
 * while an @run handler has not called run, which node's :visible hears. */
void sngl_win32_show_attached(HWND top, HWND node) {
	if (holding) {
		/* Not shown yet, which the window's :visible hears. */
		held = realloc(held, sizeof(HWND) * (size_t)(nheld + 1));
		held[nheld++] = top;
		SendMessageW(node, SNGL_WM_VISIBLE, 0, 0);
	} else {
		sngl_win32_set_visible(top, 1);
	}
}

void sngl_win32_hold(void) { holding = 1; }

void sngl_win32_present(void) {
	holding = 0;
	for (int i = 0; i < nheld; i++) {
		if (IsWindow(held[i])) {
			sngl_win32_set_visible(held[i], 1);
			SendMessageW(held[i], SNGL_WM_VISIBLE, 1, 0);
		}
	}
	free(held);
	held = NULL;
	nheld = 0;
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

/* sngl_win32_set_fraction answers whether the bar writes its fraction over
 * itself, and its record lays it out again (_rtSetFraction). */
int sngl_win32_set_fraction(HWND w, double fraction) {
	ctl *c = ctl_of(w);
	if (!c) {
		return 0;
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
	return c->show_text;
}

int sngl_win32_set_show_text(HWND w, int show) {
	ctl *c = ctl_of(w);
	if (c && c->show_text != (show != 0)) {
		c->show_text = show != 0;
		InvalidateRect(w, NULL, TRUE);
		return 1;
	}
	return 0;
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

/* sngl_win32_owner_draw has a push button drawn by its box, as its theme
 * paints one whatever WM_CTLCOLORBTN answers, or by its theme again. */
void sngl_win32_owner_draw(HWND w, int on) {
	LONG_PTR style = GetWindowLongPtrW(w, GWL_STYLE) & ~(LONG_PTR)BS_TYPEMASK;
	SetWindowLongPtrW(w, GWL_STYLE, style | (on ? BS_OWNERDRAW : BS_PUSHBUTTON));
}

/* sngl_win32_set_paint is what of a look C draws a node by, its text's
 * color and its font's size, which it is drawn again in, with what it
 * holds, since what has no background of its own shows the one its look
 * sets (_paintLook). */
void sngl_win32_set_paint(HWND w, int color, int font_px) {
	ctl *c = ctl_of(w);
	if (!c) {
		return;
	}
	c->color = color;
	if (font_px != c->font_px) {
		c->font_px = font_px;
		own_font(c);
		SendMessageW(w, WM_SETFONT, (WPARAM)font_of(c), TRUE);
	}
	RedrawWindow(w, NULL, NULL, RDW_INVALIDATE | RDW_ERASE | RDW_ALLCHILDREN);
}

/* --- A test's events --- */

/* notify sends the control's notification to the window it is in. */
static void notify(HWND w, WORD code) {
	SendMessageW(GetParent(w), WM_COMMAND, MAKEWPARAM(GetDlgCtrlID(w), code), (LPARAM)w);
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
