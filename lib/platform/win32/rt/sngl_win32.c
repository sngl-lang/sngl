/* sngl_win32.c is sngl:platform/win32's runtime (sngl_win32.h). */
#include "sngl_win32.h"

#include <commctrl.h>
#include <stdlib.h>
#include <string.h>

#define SNGL_BOX_CLASS L"SnglBox"
#define SNGL_TOP_CLASS L"SnglToplevel"
#define SNGL_PROP L"sngl.ctl"
/* A toplevel's layout, asked for by each change in it and run once. */
#define SNGL_WM_LAYOUT (WM_APP + 1)

enum sngl_kind { K_TOP, K_BOX, K_TEXT, K_BUTTON, K_EDIT, K_CHECK, K_SPACER };

typedef struct {
	sngl_win32_fn fn;
	void *ctx;
	sngl_win32_release release;
} handler;

/* ctl is the record the runtime keeps beside each node's window. */
typedef struct {
	enum sngl_kind kind;
	int vertical;
	double flex;
	int gap;
	int pad[4]; /* top, right, bottom, left */
	int min_w, min_h;
	int color, background, font_px;
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
	HWND *kids;
	int nkids, capkids;
} ctl;

static int ready;
static HWND park;
static HFONT message_font;
static int shown; /* toplevels shown and not closed */

static ctl *ctl_of(HWND w) { return w ? (ctl *)GetPropW(w, SNGL_PROP) : NULL; }

static HFONT font_of(ctl *c) { return c && c->font ? c->font : message_font; }

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
	if (c->font) {
		DeleteObject(c->font);
	}
	if (c->brush) {
		DeleteObject(c->brush);
	}
	free(c->kids);
	free(c);
}

/* --- Layout --- */

static void measure(HWND w, SIZE *out);

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

/* box_measure is a box's or a toplevel's children laid end to end. */
static SIZE box_measure(ctl *c) {
	SIZE s = {0, 0};
	for (int i = 0; i < c->nkids; i++) {
		SIZE k;
		measure(c->kids[i], &k);
		if (c->vertical) {
			s.cy += k.cy + (i > 0 ? c->gap : 0);
			s.cx = k.cx > s.cx ? k.cx : s.cx;
		} else {
			s.cx += k.cx + (i > 0 ? c->gap : 0);
			s.cy = k.cy > s.cy ? k.cy : s.cy;
		}
	}
	return s;
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
		s.cx += 20;
		s.cy += 10;
		if (s.cx < 75) {
			s.cx = 75;
		}
		break;
	case K_CHECK:
		s = text_size(w);
		s.cx += GetSystemMetrics(SM_CXMENUCHECK) + 8;
		s.cy += 4;
		break;
	case K_EDIT:
		s = text_size(w);
		s.cx = 150;
		s.cy += 8;
		break;
	case K_SPACER:
		break;
	}
	s.cx += c->pad[1] + c->pad[3];
	s.cy += c->pad[0] + c->pad[2];
	if (s.cx < c->min_w) {
		s.cx = c->min_w;
	}
	if (s.cy < c->min_h) {
		s.cy = c->min_h;
	}
	*out = s;
}

/* lay_out places a box's or a toplevel's children in its w by h client
 * area: each its own size along the box, the spare room shared by flex, and
 * the whole of the box across it. */
static void lay_out(HWND box, int w, int h) {
	ctl *c = ctl_of(box);
	if (!c || c->nkids == 0) {
		return;
	}
	int x0 = c->pad[3], y0 = c->pad[0];
	int iw = w - c->pad[1] - c->pad[3], ih = h - c->pad[0] - c->pad[2];
	int avail = (c->vertical ? ih : iw) - c->gap * (c->nkids - 1);
	int *size = malloc(sizeof(int) * (size_t)c->nkids);
	int total = 0;
	double flex = 0;
	for (int i = 0; i < c->nkids; i++) {
		SIZE k;
		measure(c->kids[i], &k);
		size[i] = c->vertical ? k.cy : k.cx;
		total += size[i];
		ctl *kc = ctl_of(c->kids[i]);
		flex += kc ? kc->flex : 0;
	}
	int spare = avail - total;
	if (spare > 0 && flex > 0) {
		int given = 0, last = -1;
		for (int i = 0; i < c->nkids; i++) {
			ctl *kc = ctl_of(c->kids[i]);
			if (kc && kc->flex > 0) {
				int more = (int)(spare * (kc->flex / flex));
				size[i] += more;
				given += more;
				last = i;
			}
		}
		/* What rounding left goes to the last to flex. */
		size[last] += spare - given;
	}
	HDWP dwp = BeginDeferWindowPos(c->nkids);
	int at = c->vertical ? y0 : x0;
	for (int i = 0; i < c->nkids; i++) {
		int x = c->vertical ? x0 : at, y = c->vertical ? at : y0;
		int cw = c->vertical ? iw : size[i], ch = c->vertical ? size[i] : ih;
		if (dwp) {
			dwp = DeferWindowPos(dwp, c->kids[i], NULL, x, y, cw, ch, SWP_NOZORDER | SWP_NOACTIVATE);
		}
		at += size[i] + c->gap;
	}
	if (dwp) {
		EndDeferWindowPos(dwp);
	}
	for (int i = 0; i < c->nkids; i++) {
		ctl *kc = ctl_of(c->kids[i]);
		if (kc && kc->kind == K_BOX) {
			lay_out(c->kids[i], c->vertical ? iw : size[i], c->vertical ? size[i] : ih);
		}
	}
	free(size);
	InvalidateRect(box, NULL, TRUE);
}

static void lay_out_top(HWND top) {
	RECT r;
	GetClientRect(top, &r);
	lay_out(top, r.right - r.left, r.bottom - r.top);
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

/* --- Window procedures --- */

static LRESULT color(ctl *c, HDC dc, LRESULT otherwise) {
	if (!c) {
		return otherwise;
	}
	if (c->color >= 0) {
		SetTextColor(dc, RGB((c->color >> 16) & 0xff, (c->color >> 8) & 0xff, c->color & 0xff));
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
		ctl *src = ctl_of((HWND)lp);
		if (!src) {
			break;
		}
		switch (HIWORD(wp)) {
		case BN_CLICKED:
			fire(&src->click);
			return 0;
		case EN_CHANGE:
			if (!src->quiet) {
				fire(&src->change);
			}
			return 0;
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
		if (c && c->brush) {
			RECT r;
			GetClientRect(w, &r);
			FillRect((HDC)wp, &r, c->brush);
			return 1;
		}
		break;
	case WM_SIZE:
		if (c && c->kind == K_TOP) {
			lay_out_top(w);
		}
		return 0;
	case SNGL_WM_LAYOUT:
		if (c) {
			c->laying = 0;
			lay_out_top(w);
		}
		return 0;
	case WM_CLOSE:
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
		if (c && c->kind == K_TOP && IsWindowVisible(w)) {
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
	case WM_NCDESTROY:
		RemoveWindowSubclass(w, control_proc, 1);
		forget(w);
		break;
	}
	return DefSubclassProc(w, msg, wp, lp);
}

/* --- Making nodes --- */

void sngl_win32_init(void) {
	if (ready) {
		return;
	}
	ready = 1;
	SetProcessDPIAware();
	INITCOMMONCONTROLSEX icc = {sizeof icc, ICC_STANDARD_CLASSES};
	InitCommonControlsEx(&icc);
	NONCLIENTMETRICSW ncm;
	memset(&ncm, 0, sizeof ncm);
	ncm.cbSize = sizeof ncm;
	if (SystemParametersInfoW(SPI_GETNONCLIENTMETRICS, sizeof ncm, &ncm, 0)) {
		message_font = CreateFontIndirectW(&ncm.lfMessageFont);
	}
	if (!message_font) {
		message_font = (HFONT)GetStockObject(DEFAULT_GUI_FONT);
	}
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
	c->color = c->background = -1;
	SetPropW(w, SNGL_PROP, c);
	return c;
}

static HWND control(LPCWSTR class, DWORD style, DWORD ex, enum sngl_kind kind) {
	sngl_win32_init();
	HWND w = CreateWindowExW(ex, class, L"", WS_CHILD | WS_VISIBLE | style, 0, 0, 0, 0, park, NULL, GetModuleHandleW(NULL), NULL);
	new_ctl(w, kind);
	SendMessageW(w, WM_SETFONT, (WPARAM)message_font, FALSE);
	if (kind != K_BOX && kind != K_SPACER) {
		SetWindowSubclass(w, control_proc, 1, 0);
	}
	return w;
}

HWND sngl_win32_toplevel(void) {
	sngl_win32_init();
	HWND w = CreateWindowExW(WS_EX_CONTROLPARENT, SNGL_TOP_CLASS, L"", WS_OVERLAPPEDWINDOW | WS_CLIPCHILDREN, CW_USEDEFAULT, CW_USEDEFAULT, 400, 300, NULL, NULL, GetModuleHandleW(NULL), NULL);
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
		relayout(box);
	}
}

HWND sngl_win32_text(void) { return control(L"STATIC", SS_LEFT | SS_NOPREFIX, 0, K_TEXT); }

HWND sngl_win32_button(void) { return control(L"BUTTON", BS_PUSHBUTTON | WS_TABSTOP, 0, K_BUTTON); }

HWND sngl_win32_edit(void) { return control(L"EDIT", ES_AUTOHSCROLL | WS_TABSTOP, WS_EX_CLIENTEDGE, K_EDIT); }

HWND sngl_win32_checkbox(void) { return control(L"BUTTON", BS_AUTOCHECKBOX | WS_TABSTOP, 0, K_CHECK); }

HWND sngl_win32_spacer(void) { return control(SNGL_BOX_CLASS, 0, 0, K_SPACER); }

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

static void adopt(HWND parent, HWND child, int at) {
	ctl *c = ctl_of(parent);
	if (!c) {
		return;
	}
	SetParent(child, parent);
	kids_insert(c, child, at);
	relayout(parent);
}

void sngl_win32_append(HWND parent, HWND child) {
	ctl *c = ctl_of(parent);
	if (c) {
		adopt(parent, child, c->nkids);
	}
}

void sngl_win32_insert(HWND parent, HWND child, HWND before) {
	ctl *c = ctl_of(parent);
	if (!c) {
		return;
	}
	int at = kids_index(c, before);
	adopt(parent, child, at < 0 ? c->nkids : at);
}

void sngl_win32_remove(HWND parent, HWND child) {
	ctl *c = ctl_of(parent);
	if (c) {
		int at = kids_index(c, child);
		if (at >= 0) {
			memmove(c->kids + at, c->kids + at + 1, sizeof(HWND) * (size_t)(c->nkids - at - 1));
			c->nkids--;
		}
		relayout(parent);
	}
	DestroyWindow(child);
}

void sngl_win32_attach(HWND top) {
	ctl *c = ctl_of(top);
	if (!c) {
		return;
	}
	/* Sized to what it holds, as a GTK window is to its content. */
	SIZE s;
	measure(top, &s);
	RECT r = {0, 0, s.cx < 200 ? 200 : s.cx, s.cy < 100 ? 100 : s.cy};
	AdjustWindowRectEx(&r, (DWORD)GetWindowLongPtrW(top, GWL_STYLE), FALSE, (DWORD)GetWindowLongPtrW(top, GWL_EXSTYLE));
	SetWindowPos(top, NULL, 0, 0, r.right - r.left, r.bottom - r.top, SWP_NOMOVE | SWP_NOZORDER | SWP_NOACTIVATE);
	sngl_win32_set_visible(top, 1);
	lay_out_top(top);
}

void sngl_win32_detach(HWND top) { DestroyWindow(top); }

/* --- Props --- */

void sngl_win32_set_text(HWND w, const unsigned short *text) {
	SetWindowTextW(w, (LPCWSTR)text);
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
	return buf;
}

void sngl_win32_set_edit_text(HWND w, const unsigned short *text) {
	ctl *c = ctl_of(w);
	int n = GetWindowTextLengthW(w);
	WCHAR *now = malloc(sizeof(WCHAR) * (size_t)(n + 1));
	GetWindowTextW(w, now, n + 1);
	int same = wcscmp(now, (LPCWSTR)text) == 0;
	free(now);
	if (same) {
		return;
	}
	if (c) {
		c->quiet = 1;
	}
	SetWindowTextW(w, (LPCWSTR)text);
	if (c) {
		c->quiet = 0;
	}
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
		if (c->font) {
			DeleteObject(c->font);
			c->font = NULL;
		}
		c->font_px = font_px;
		if (font_px > 0) {
			LOGFONTW lf;
			GetObjectW(message_font, sizeof lf, &lf);
			lf.lfHeight = -font_px;
			c->font = CreateFontIndirectW(&lf);
		}
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

void sngl_win32_fire_click(HWND w) { notify(w, BN_CLICKED); }

void sngl_win32_fire_check(HWND w, int checked) {
	sngl_win32_set_checked(w, checked);
	notify(w, BN_CLICKED);
}

void sngl_win32_fire_change(HWND w, const unsigned short *text) {
	ctl *c = ctl_of(w);
	if (c) {
		c->quiet = 1;
	}
	SetWindowTextW(w, (LPCWSTR)text);
	if (c) {
		c->quiet = 0;
	}
	notify(w, EN_CHANGE);
}

void sngl_win32_fire_activate(HWND w, const unsigned short *text) {
	sngl_win32_set_edit_text(w, text);
	SendMessageW(w, WM_KEYDOWN, VK_RETURN, 0);
}

void sngl_win32_fire_close(HWND top) { SendMessageW(top, WM_CLOSE, 0, 0); }

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

void sngl_win32_run(void) {
	sngl_win32_init();
	MSG m;
	while (!quitting && GetMessageW(&m, NULL, 0, 0) > 0) {
		step(&m);
		if (shown <= 0) {
			/* The last window the program showed is closed. */
			break;
		}
	}
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
