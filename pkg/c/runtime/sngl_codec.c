#if !defined(_WIN32) && !defined(_POSIX_C_SOURCE)
#define _POSIX_C_SOURCE 200809L
#endif

#include "sngl.h"

#include <errno.h>
#include <inttypes.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <windows.h>
#else
#include <sys/stat.h>
#endif

sngl_string sngl_json_failure;

/* The parser keeps to what Go's encoding/json reads: one value, whitespace
 * around it, and nothing after. */
typedef struct sngl_json_reader {
	const char *p, *end;
	int depth;
} sngl_json_reader;

static void sngl_json_space(sngl_json_reader *r) {
	while (r->p < r->end && (*r->p == ' ' || *r->p == '\t' || *r->p == '\n' || *r->p == '\r')) {
		r->p++;
	}
}

static bool sngl_json_literal(sngl_json_reader *r, const char *word) {
	size_t n = strlen(word);
	if ((size_t)(r->end - r->p) < n || memcmp(r->p, word, n) != 0) {
		return false;
	}
	r->p += n;
	return true;
}

static int sngl_json_hex(const char *p) {
	int v = 0;
	for (int i = 0; i < 4; i++) {
		char c = p[i];
		v <<= 4;
		if (c >= '0' && c <= '9') {
			v |= c - '0';
		} else if (c >= 'a' && c <= 'f') {
			v |= c - 'a' + 10;
		} else if (c >= 'A' && c <= 'F') {
			v |= c - 'A' + 10;
		} else {
			return -1;
		}
	}
	return v;
}

static size_t sngl_utf8_put(char *out, uint32_t r) {
	if (r < 0x80) {
		out[0] = (char)r;
		return 1;
	}
	if (r < 0x800) {
		out[0] = (char)(0xC0 | (r >> 6));
		out[1] = (char)(0x80 | (r & 0x3F));
		return 2;
	}
	if (r < 0x10000) {
		out[0] = (char)(0xE0 | (r >> 12));
		out[1] = (char)(0x80 | ((r >> 6) & 0x3F));
		out[2] = (char)(0x80 | (r & 0x3F));
		return 3;
	}
	out[0] = (char)(0xF0 | (r >> 18));
	out[1] = (char)(0x80 | ((r >> 12) & 0x3F));
	out[2] = (char)(0x80 | ((r >> 6) & 0x3F));
	out[3] = (char)(0x80 | (r & 0x3F));
	return 4;
}

/* A string's text unescaped; an escape is never longer than what it writes,
 * so the source's length bounds the result. A lone surrogate is U+FFFD, as
 * Go reads one. */
static bool sngl_json_text(sngl_json_reader *r, sngl_string *out) {
	r->p++;
	const char *start = r->p;
	bool escaped = false;
	while (r->p < r->end && *r->p != '"') {
		if ((unsigned char)*r->p < 0x20) {
			return false;
		}
		if (*r->p == '\\') {
			escaped = true;
			r->p++;
			if (r->p >= r->end) {
				return false;
			}
		}
		r->p++;
	}
	if (r->p >= r->end) {
		return false;
	}
	const char *stop = r->p++;
	if (!escaped) {
		*out = sngl_string_bytes(start, (size_t)(stop - start));
		return true;
	}
	char *buf = sngl_alloc((size_t)(stop - start) + 1);
	size_t n = 0;
	for (const char *p = start; p < stop; p++) {
		if (*p != '\\') {
			buf[n++] = *p;
			continue;
		}
		p++;
		switch (*p) {
		case '"': buf[n++] = '"'; break;
		case '\\': buf[n++] = '\\'; break;
		case '/': buf[n++] = '/'; break;
		case 'b': buf[n++] = '\b'; break;
		case 'f': buf[n++] = '\f'; break;
		case 'n': buf[n++] = '\n'; break;
		case 'r': buf[n++] = '\r'; break;
		case 't': buf[n++] = '\t'; break;
		case 'u': {
			if (stop - p < 5) {
				return false;
			}
			int u = sngl_json_hex(p + 1);
			if (u < 0) {
				return false;
			}
			p += 4;
			uint32_t rune = (uint32_t)u;
			if (u >= 0xD800 && u < 0xDC00) {
				int lo = stop - p >= 7 && p[1] == '\\' && p[2] == 'u' ? sngl_json_hex(p + 3) : -1;
				if (lo >= 0xDC00 && lo < 0xE000) {
					rune = 0x10000 + (((uint32_t)u - 0xD800) << 10) + ((uint32_t)lo - 0xDC00);
					p += 6;
				} else {
					rune = 0xFFFD;
				}
			} else if (u >= 0xDC00 && u < 0xE000) {
				rune = 0xFFFD;
			}
			n += sngl_utf8_put(buf + n, rune);
			break;
		}
		default:
			return false;
		}
	}
	buf[n] = '\0';
	*out = (sngl_string){buf, n};
	return true;
}

static bool sngl_json_digits(sngl_json_reader *r) {
	const char *start = r->p;
	while (r->p < r->end && *r->p >= '0' && *r->p <= '9') {
		r->p++;
	}
	return r->p > start;
}

static bool sngl_json_value(sngl_json_reader *r, sngl_json *v);

/* A growing array of the turn's: what an array or an object holds, copied
 * into a buffer of its exact size once it is read. */
typedef struct sngl_json_items {
	sngl_json *items;
	sngl_string *keys;
	int64_t len, cap;
} sngl_json_items;

static void sngl_json_push(sngl_json_items *xs, bool keyed) {
	if (xs->len < xs->cap) {
		return;
	}
	int64_t cap = xs->cap < 8 ? 8 : xs->cap * 2;
	sngl_json *items = sngl_alloc(sizeof(sngl_json) * (size_t)cap);
	if (xs->len > 0) {
		memcpy(items, xs->items, sizeof(sngl_json) * (size_t)xs->len);
	}
	xs->items = items;
	if (keyed) {
		sngl_string *keys = sngl_alloc(sizeof(sngl_string) * (size_t)cap);
		if (xs->len > 0) {
			memcpy(keys, xs->keys, sizeof(sngl_string) * (size_t)xs->len);
		}
		xs->keys = keys;
	}
	xs->cap = cap;
}

static bool sngl_json_value(sngl_json_reader *r, sngl_json *v) {
	sngl_json_space(r);
	if (r->p >= r->end) {
		return false;
	}
	memset(v, 0, sizeof *v);
	const char *start = r->p;
	switch (*r->p) {
	case 'n':
		if (!sngl_json_literal(r, "null")) {
			return false;
		}
		v->kind = SNGL_JSON_NULL;
		break;
	case 't':
	case 'f':
		v->kind = SNGL_JSON_BOOL;
		v->b = *r->p == 't';
		if (!sngl_json_literal(r, v->b ? "true" : "false")) {
			return false;
		}
		break;
	case '"':
		v->kind = SNGL_JSON_STRING;
		if (!sngl_json_text(r, &v->text)) {
			return false;
		}
		break;
	case '[':
	case '{': {
		bool object = *r->p == '{';
		char close = object ? '}' : ']';
		if (++r->depth > 10000) {
			return false;
		}
		r->p++;
		sngl_json_items xs = {0};
		sngl_json_space(r);
		if (r->p < r->end && *r->p == close) {
			r->p++;
		} else {
			for (;;) {
				sngl_json_push(&xs, object);
				if (object) {
					sngl_json_space(r);
					if (r->p >= r->end || *r->p != '"' || !sngl_json_text(r, &xs.keys[xs.len])) {
						return false;
					}
					sngl_json_space(r);
					if (r->p >= r->end || *r->p != ':') {
						return false;
					}
					r->p++;
				}
				if (!sngl_json_value(r, &xs.items[xs.len])) {
					return false;
				}
				xs.len++;
				sngl_json_space(r);
				if (r->p < r->end && *r->p == ',') {
					r->p++;
					continue;
				}
				if (r->p < r->end && *r->p == close) {
					r->p++;
					break;
				}
				return false;
			}
		}
		r->depth--;
		v->kind = object ? SNGL_JSON_OBJECT : SNGL_JSON_ARRAY;
		v->items = xs.items;
		v->keys = xs.keys;
		v->len = xs.len;
		break;
	}
	default:
		v->kind = SNGL_JSON_NUMBER;
		if (*r->p == '-') {
			r->p++;
		}
		if (r->p < r->end && *r->p == '0') {
			r->p++;
		} else if (!sngl_json_digits(r)) {
			return false;
		}
		if (r->p < r->end && *r->p == '.') {
			r->p++;
			if (!sngl_json_digits(r)) {
				return false;
			}
		}
		if (r->p < r->end && (*r->p == 'e' || *r->p == 'E')) {
			r->p++;
			if (r->p < r->end && (*r->p == '+' || *r->p == '-')) {
				r->p++;
			}
			if (!sngl_json_digits(r)) {
				return false;
			}
		}
		v->text = sngl_string_bytes(start, (size_t)(r->p - start));
	}
	v->raw = (sngl_string){start, (size_t)(r->p - start)};
	return true;
}

sngl_json *sngl_json_parse(sngl_string text) {
	sngl_json_reader r = {text.ptr, text.ptr + text.len, 0};
	sngl_json *v = sngl_alloc(sizeof *v);
	if (!sngl_json_value(&r, v)) {
		return NULL;
	}
	sngl_json_space(&r);
	return r.p == r.end ? v : NULL;
}

const sngl_json *sngl_json_field(const sngl_json *obj, sngl_string key) {
	for (int64_t i = obj->len - 1; i >= 0; i--) {
		if (sngl_string_eq(obj->keys[i], key)) {
			return &obj->items[i];
		}
	}
	return NULL;
}

int64_t *sngl_string_order(const sngl_string *keys, int64_t n) {
	int64_t *a = sngl_alloc(sizeof(int64_t) * (size_t)(n + 1));
	int64_t *b = sngl_alloc(sizeof(int64_t) * (size_t)(n + 1));
	for (int64_t i = 0; i < n; i++) {
		a[i] = i;
	}
	for (int64_t w = 1; w < n; w *= 2) {
		for (int64_t lo = 0; lo < n; lo += 2 * w) {
			int64_t mid = lo + w < n ? lo + w : n, hi = lo + 2 * w < n ? lo + 2 * w : n;
			int64_t i = lo, j = mid, k = lo;
			while (i < mid && j < hi) {
				b[k++] = sngl_string_cmp(keys[a[j]], keys[a[i]]) < 0 ? a[j++] : a[i++];
			}
			while (i < mid) {
				b[k++] = a[i++];
			}
			while (j < hi) {
				b[k++] = a[j++];
			}
		}
		int64_t *t = a;
		a = b;
		b = t;
	}
	return a;
}

/* The order is stable, so the last of a key written twice is the last of
 * its run, which Go's map keeps. */
int64_t sngl_json_sorted(const sngl_json *obj, int64_t **order) {
	int64_t *a = sngl_string_order(obj->keys, obj->len);
	int64_t m = 0;
	for (int64_t i = 0; i < obj->len; i++) {
		if (m > 0 && sngl_string_eq(obj->keys[a[m - 1]], obj->keys[a[i]])) {
			a[m - 1] = a[i];
		} else {
			a[m++] = a[i];
		}
	}
	*order = a;
	return m;
}

static void sngl_json_path_put(sngl_buf *b, const sngl_json_path *p) {
	if (p == NULL) {
		sngl_buf_put(b, "$", 1);
		return;
	}
	sngl_json_path_put(b, p->up);
	if (p->index >= 0) {
		char n[32];
		int len = snprintf(n, sizeof n, "[%" PRId64 "]", p->index);
		sngl_buf_put(b, n, (size_t)len);
	} else {
		sngl_buf_put(b, ".", 1);
		sngl_buf_str(b, p->key);
	}
}

bool sngl_json_want(const sngl_json_path *p, const char *what) {
	sngl_buf b = {0};
	sngl_json_path_put(&b, p);
	sngl_buf_put(&b, ": want ", 7);
	sngl_buf_put(&b, what, strlen(what));
	sngl_json_failure = sngl_buf_string(&b);
	return false;
}

bool sngl_json_missing(const sngl_json_path *p, sngl_string wire) {
	sngl_buf b = {0};
	sngl_json_path q = {p, wire, -1};
	sngl_json_path_put(&b, &q);
	sngl_buf_put(&b, ": missing", 9);
	sngl_json_failure = sngl_buf_string(&b);
	return false;
}

bool sngl_json_bool(const sngl_json *v, const sngl_json_path *p, bool *out) {
	if (v->kind != SNGL_JSON_BOOL) {
		return sngl_json_want(p, "a boolean");
	}
	*out = v->b;
	return true;
}

/* An integer is a number with no fraction, as Go's sngljson.Integer reads
 * one: 1e3 is one, 1.5 is not. */
bool sngl_json_int(const sngl_json *v, const sngl_json_path *p, int64_t lo, int64_t hi, int64_t *out) {
	if (v->kind != SNGL_JSON_NUMBER) {
		return sngl_json_want(p, "an integer");
	}
	int64_t i = 0;
	bool ok = false;
	if (strpbrk(v->text.ptr, ".eE") == NULL) {
		errno = 0;
		long long n = strtoll(v->text.ptr, NULL, 10);
		if (errno == 0) {
			i = (int64_t)n;
			ok = true;
		}
	}
	if (!ok) {
		double f = sngl_float_of(v->text);
		if (f == trunc(f) && fabs(f) < 9223372036854775808.0) {
			i = (int64_t)f;
			ok = true;
		}
	}
	if (!ok || i < lo || i > hi) {
		return sngl_json_want(p, "an integer");
	}
	*out = i;
	return true;
}

bool sngl_json_float(const sngl_json *v, const sngl_json_path *p, double *out) {
	if (v->kind != SNGL_JSON_NUMBER) {
		return sngl_json_want(p, "a number");
	}
	double f = sngl_float_of(v->text);
	if (isinf(f)) {
		return sngl_json_want(p, "a number");
	}
	*out = f;
	return true;
}

bool sngl_json_string(const sngl_json *v, const sngl_json_path *p, sngl_string *out) {
	if (v->kind != SNGL_JSON_STRING) {
		return sngl_json_want(p, "a string");
	}
	*out = v->text;
	return true;
}

static bool sngl_digits_at(const char *s, int n, int *out) {
	int v = 0;
	for (int i = 0; i < n; i++) {
		if (s[i] < '0' || s[i] > '9') {
			return false;
		}
		v = v * 10 + (s[i] - '0');
	}
	*out = v;
	return true;
}

static bool sngl_full_date(const char *s) {
	int y, m, d;
	if (!sngl_digits_at(s, 4, &y) || s[4] != '-' || !sngl_digits_at(s + 5, 2, &m) || s[7] != '-' || !sngl_digits_at(s + 8, 2, &d)) {
		return false;
	}
	static const int days[] = {31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31};
	if (m < 1 || m > 12 || d < 1) {
		return false;
	}
	bool leap = (y % 4 == 0 && y % 100 != 0) || y % 400 == 0;
	return d <= days[m - 1] + (m == 2 && leap ? 1 : 0);
}

/* A date is RFC 3339's full-date, a datetime its date-time: what Go's
 * time.Parse reads with "2006-01-02" and RFC3339Nano. C holds either as its
 * text. */
bool sngl_json_date(const sngl_json *v, const sngl_json_path *p, sngl_string *out) {
	if (v->kind != SNGL_JSON_STRING || v->text.len != 10 || !sngl_full_date(v->text.ptr)) {
		return sngl_json_want(p, "a date");
	}
	*out = v->text;
	return true;
}

static bool sngl_full_datetime(sngl_string t) {
	const char *s = t.ptr;
	size_t n = t.len;
	int h, mi, se;
	if (n < 20 || !sngl_full_date(s) || s[10] != 'T' || !sngl_digits_at(s + 11, 2, &h) || s[13] != ':' ||
	    !sngl_digits_at(s + 14, 2, &mi) || s[16] != ':' || !sngl_digits_at(s + 17, 2, &se) || h > 23 || mi > 59 || se > 59) {
		return false;
	}
	size_t i = 19;
	if (s[i] == '.') {
		size_t from = ++i;
		while (i < n && s[i] >= '0' && s[i] <= '9') {
			i++;
		}
		if (i == from) {
			return false;
		}
	}
	if (i < n && s[i] == 'Z') {
		return i + 1 == n;
	}
	int oh, om;
	return i + 6 == n && (s[i] == '+' || s[i] == '-') && sngl_digits_at(s + i + 1, 2, &oh) && s[i + 3] == ':' &&
	       sngl_digits_at(s + i + 4, 2, &om) && oh < 24 && om < 60;
}

bool sngl_json_datetime(const sngl_json *v, const sngl_json_path *p, sngl_string *out) {
	if (v->kind != SNGL_JSON_STRING || !sngl_full_datetime(v->text)) {
		return sngl_json_want(p, "a datetime");
	}
	*out = v->text;
	return true;
}

void sngl_buf_put(sngl_buf *b, const char *s, size_t n) {
	if (b->len + n + 1 > b->cap) {
		size_t cap = b->cap < 64 ? 64 : b->cap * 2;
		while (cap < b->len + n + 1) {
			cap *= 2;
		}
		char *data = sngl_alloc(cap);
		if (b->len > 0) {
			memcpy(data, b->data, b->len);
		}
		b->data = data;
		b->cap = cap;
	}
	if (n > 0) {
		memcpy(b->data + b->len, s, n);
	}
	b->len += n;
	b->data[b->len] = '\0';
}

void sngl_buf_str(sngl_buf *b, sngl_string s) {
	sngl_buf_put(b, s.ptr, s.len);
}

sngl_string sngl_buf_string(sngl_buf *b) {
	if (b->len == 0) {
		return SNGL_STR("");
	}
	return (sngl_string){b->data, b->len};
}

void sngl_json_quote(sngl_buf *b, sngl_string s) {
	sngl_buf_put(b, "\"", 1);
	size_t from = 0;
	for (size_t i = 0; i < s.len; i++) {
		unsigned char c = (unsigned char)s.ptr[i];
		const char *esc = NULL;
		char u[8];
		switch (c) {
		case '"': esc = "\\\""; break;
		case '\\': esc = "\\\\"; break;
		case '\b': esc = "\\b"; break;
		case '\f': esc = "\\f"; break;
		case '\n': esc = "\\n"; break;
		case '\r': esc = "\\r"; break;
		case '\t': esc = "\\t"; break;
		default:
			if (c < 0x20) {
				snprintf(u, sizeof u, "\\u%04x", c);
				esc = u;
			}
		}
		if (esc != NULL) {
			sngl_buf_put(b, s.ptr + from, i - from);
			sngl_buf_put(b, esc, strlen(esc));
			from = i + 1;
		}
	}
	sngl_buf_put(b, s.ptr + from, s.len - from);
	sngl_buf_put(b, "\"", 1);
}

void sngl_json_int64(sngl_buf *b, int64_t v) {
	char n[32];
	int len = snprintf(n, sizeof n, "%" PRId64, v);
	sngl_buf_put(b, n, (size_t)len);
}

void sngl_json_uint64(sngl_buf *b, uint64_t v) {
	char n[32];
	int len = snprintf(n, sizeof n, "%" PRIu64, v);
	sngl_buf_put(b, n, (size_t)len);
}

/* A float as JSON and JavaScript write one: the shortest digits that read
 * back as the same value, no fraction for a whole number, and an exponent
 * from 1e21 up and below 1e-6. Not a number and the infinities are null, as
 * sngljson.Number writes them. The digits are read off %e by position, so
 * the locale's point never reaches the text. */
void sngl_json_number(sngl_buf *b, double v) {
	if (isnan(v) || isinf(v)) {
		sngl_buf_put(b, "null", 4);
		return;
	}
	char e[64];
	int prec = 0;
	for (; prec < 17; prec++) {
		snprintf(e, sizeof e, "%.*e", prec, v);
		if (strtod(e, NULL) == v) {
			break;
		}
	}
	char digits[24];
	int nd = 0;
	const char *p = e;
	bool neg = *p == '-';
	if (neg) {
		p++;
	}
	for (; *p != 'e' && *p != 'E'; p++) {
		if (*p >= '0' && *p <= '9') {
			digits[nd++] = *p;
		}
	}
	int exp = atoi(p + 1);
	while (nd > 1 && digits[nd - 1] == '0') {
		nd--;
	}
	if (neg) {
		sngl_buf_put(b, "-", 1);
	}
	if (exp < -6 || exp >= 21) {
		sngl_buf_put(b, digits, 1);
		if (nd > 1) {
			sngl_buf_put(b, ".", 1);
			sngl_buf_put(b, digits + 1, (size_t)(nd - 1));
		}
		char x[16];
		int len = snprintf(x, sizeof x, "e%c%d", exp < 0 ? '-' : '+', exp < 0 ? -exp : exp);
		sngl_buf_put(b, x, (size_t)len);
		return;
	}
	if (exp < 0) {
		sngl_buf_put(b, "0.", 2);
		for (int i = 0; i < -exp - 1; i++) {
			sngl_buf_put(b, "0", 1);
		}
		sngl_buf_put(b, digits, (size_t)nd);
		return;
	}
	if (nd <= exp + 1) {
		sngl_buf_put(b, digits, (size_t)nd);
		for (int i = nd; i <= exp; i++) {
			sngl_buf_put(b, "0", 1);
		}
		return;
	}
	sngl_buf_put(b, digits, (size_t)exp + 1);
	sngl_buf_put(b, ".", 1);
	sngl_buf_put(b, digits + exp + 1, (size_t)(nd - exp - 1));
}

/* A datetime as Go's RFC3339 writes it, which drops a fraction of a second
 * the text read held. */
void sngl_json_datetime_put(sngl_buf *b, sngl_string t) {
	if (t.len > 19 && t.ptr[19] == '.') {
		size_t i = 20;
		while (i < t.len && t.ptr[i] >= '0' && t.ptr[i] <= '9') {
			i++;
		}
		sngl_buf_put(b, t.ptr, 19);
		sngl_buf_put(b, t.ptr + i, t.len - i);
		return;
	}
	sngl_buf_str(b, t);
}

static bool sngl_unreserved(unsigned char c) {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~';
}

static void sngl_escape_put(sngl_buf *b, sngl_string s, const char *also) {
	static const char hex[] = "0123456789ABCDEF";
	for (size_t i = 0; i < s.len; i++) {
		unsigned char c = (unsigned char)s.ptr[i];
		if (sngl_unreserved(c) || (also != NULL && c != 0 && strchr(also, c) != NULL)) {
			sngl_buf_put(b, (const char *)&c, 1);
			continue;
		}
		char e[3] = {'%', hex[c >> 4], hex[c & 15]};
		sngl_buf_put(b, e, 3);
	}
}

sngl_string sngl_path_escape(sngl_string s) {
	sngl_buf b = {0};
	sngl_escape_put(&b, s, NULL);
	return sngl_buf_string(&b);
}

void sngl_query_put(sngl_buf *b, sngl_string wire, sngl_string value) {
	if (b->len > 0) {
		sngl_buf_put(b, "&", 1);
	}
	sngl_escape_put(b, wire, NULL);
	sngl_buf_put(b, "=", 1);
	sngl_escape_put(b, value, NULL);
}

sngl_string sngl_with_query(sngl_string url, sngl_string query) {
	if (query.len == 0) {
		return url;
	}
	bool has = memchr(url.ptr, '?', url.len) != NULL;
	return sngl_concat(sngl_concat(url, has ? SNGL_STR("&") : SNGL_STR("?")), query);
}

/* Settings are a JSON object per scope in a file under the user's
 * configuration directory, as pkg/go/snglsettings keeps them, so a program
 * built in either language reads what the other kept: settings.json for the
 * app's own, <scope>.json for another. */
static char *sngl_settings_app;

void sngl_settings_open(const char *app) {
	free(sngl_settings_app);
	size_t n = strlen(app);
	sngl_settings_app = malloc(n + 1);
	if (sngl_settings_app != NULL) {
		memcpy(sngl_settings_app, app, n + 1);
	}
}

bool sngl_settings_persistent(void) {
	return sngl_settings_app != NULL;
}

/* The directory Go's os.UserConfigDir answers, "" where it answers none. */
static sngl_string sngl_config_dir(void) {
#ifdef _WIN32
	wchar_t w[32768];
	DWORD wn = GetEnvironmentVariableW(L"APPDATA", w, sizeof w / sizeof w[0]);
	if (wn == 0 || wn >= sizeof w / sizeof w[0]) {
		return SNGL_STR("");
	}
	int n = WideCharToMultiByte(CP_UTF8, 0, w, (int)wn, NULL, 0, NULL, NULL);
	char *s = sngl_alloc((size_t)n + 1);
	WideCharToMultiByte(CP_UTF8, 0, w, (int)wn, s, n, NULL, NULL);
	return (sngl_string){s, (size_t)n};
#else
	const char *home = getenv("HOME");
#ifdef __APPLE__
	if (home == NULL || *home == 0) {
		return SNGL_STR("");
	}
	return sngl_concat(sngl_string_copy(home), SNGL_STR("/Library/Application Support"));
#else
	const char *xdg = getenv("XDG_CONFIG_HOME");
	if (xdg != NULL && xdg[0] == '/') {
		return sngl_string_copy(xdg);
	}
	if (xdg != NULL && *xdg != 0) {
		return SNGL_STR("");
	}
	if (home == NULL || *home == 0) {
		return SNGL_STR("");
	}
	return sngl_concat(sngl_string_copy(home), SNGL_STR("/.config"));
#endif
#endif
}

#ifdef _WIN32
#define SNGL_SEP "\\"
#else
#define SNGL_SEP "/"
#endif

static sngl_string sngl_settings_dir(void) {
	sngl_string dir = sngl_config_dir();
	if (dir.len == 0) {
		return dir;
	}
	return sngl_concat(sngl_concat(dir, SNGL_STR(SNGL_SEP)), sngl_string_copy(sngl_settings_app));
}

/* A scope as Go's url.PathEscape writes it into the file's name. */
static sngl_string sngl_settings_path(sngl_string scope) {
	sngl_string dir = sngl_settings_dir();
	if (dir.len == 0) {
		return dir;
	}
	sngl_buf b = {0};
	sngl_buf_str(&b, dir);
	sngl_buf_put(&b, SNGL_SEP, 1);
	if (scope.len == 0) {
		sngl_buf_put(&b, "settings", 8);
	} else {
		sngl_escape_put(&b, scope, "$&+:=@");
	}
	sngl_buf_put(&b, ".json", 5);
	return sngl_buf_string(&b);
}

/* The whole of the file at path, or false where it cannot be read. */
static bool sngl_read_file(sngl_string path, sngl_string *out) {
	sngl_buf b = {0};
	char chunk[4096];
#ifdef _WIN32
	HANDLE f = CreateFileW((const wchar_t *)sngl_utf16(path), GENERIC_READ, FILE_SHARE_READ, NULL, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
	if (f == INVALID_HANDLE_VALUE) {
		return false;
	}
	DWORD n;
	while (ReadFile(f, chunk, sizeof chunk, &n, NULL) && n > 0) {
		sngl_buf_put(&b, chunk, n);
	}
	CloseHandle(f);
#else
	FILE *f = fopen(path.ptr, "rb");
	if (f == NULL) {
		return false;
	}
	size_t n;
	while ((n = fread(chunk, 1, sizeof chunk, f)) > 0) {
		sngl_buf_put(&b, chunk, n);
	}
	fclose(f);
#endif
	*out = sngl_buf_string(&b);
	return true;
}

/* Writes data to tmp and renames it over path. */
static bool sngl_replace_file(sngl_string path, sngl_string tmp, sngl_string data) {
#ifdef _WIN32
	const wchar_t *wtmp = (const wchar_t *)sngl_utf16(tmp);
	HANDLE f = CreateFileW(wtmp, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL);
	if (f == INVALID_HANDLE_VALUE) {
		return false;
	}
	DWORD n = 0;
	bool ok = WriteFile(f, data.ptr, (DWORD)data.len, &n, NULL) && n == data.len;
	ok = CloseHandle(f) && ok;
	ok = ok && MoveFileExW(wtmp, (const wchar_t *)sngl_utf16(path), MOVEFILE_REPLACE_EXISTING);
	if (!ok) {
		DeleteFileW(wtmp);
	}
	return ok;
#else
	FILE *f = fopen(tmp.ptr, "wb");
	if (f == NULL) {
		return false;
	}
	bool ok = fwrite(data.ptr, 1, data.len, f) == data.len;
	ok = fclose(f) == 0 && ok;
	ok = ok && rename(tmp.ptr, path.ptr) == 0;
	if (!ok) {
		remove(tmp.ptr);
	}
	return ok;
#endif
}

static sngl_json *sngl_settings_load(sngl_string path) {
	sngl_string text;
	if (!sngl_read_file(path, &text)) {
		return NULL;
	}
	sngl_json *v = sngl_json_parse(text);
	return v != NULL && v->kind == SNGL_JSON_OBJECT ? v : NULL;
}

sngl_string sngl_settings_read(sngl_string scope, sngl_string name) {
	if (sngl_settings_app == NULL) {
		return SNGL_STR("");
	}
	sngl_string path = sngl_settings_path(scope);
	sngl_json *values = path.len > 0 ? sngl_settings_load(path) : NULL;
	const sngl_json *v = values != NULL ? sngl_json_field(values, name) : NULL;
	return v != NULL ? sngl_string_bytes(v->raw.ptr, v->raw.len) : SNGL_STR("");
}

static bool sngl_make_dirs(sngl_string dir) {
	for (size_t i = 1; i <= dir.len; i++) {
		if (i < dir.len && dir.ptr[i] != '/' && dir.ptr[i] != '\\') {
			continue;
		}
		sngl_string part = sngl_string_bytes(dir.ptr, i);
#ifdef _WIN32
		if (i == 2 && dir.ptr[1] == ':') {
			continue;
		}
		if (!CreateDirectoryW((const wchar_t *)sngl_utf16(part), NULL) && GetLastError() != ERROR_ALREADY_EXISTS) {
			return false;
		}
#else
		if (mkdir(part.ptr, 0755) != 0 && errno != EEXIST) {
			return false;
		}
#endif
	}
	return true;
}

/* A write that fails is lost, as Go's is: a setting is never worth stopping
 * the program. The file is written beside its place and renamed over it. */
void sngl_settings_write(sngl_string scope, sngl_string name, sngl_string text) {
	if (sngl_settings_app == NULL || sngl_json_parse(text) == NULL) {
		return;
	}
	sngl_string path = sngl_settings_path(scope);
	if (path.len == 0 || !sngl_make_dirs(sngl_settings_dir())) {
		return;
	}
	sngl_json *values = sngl_settings_load(path);
	sngl_json kept = {0};
	if (values == NULL) {
		values = &kept;
		values->kind = SNGL_JSON_OBJECT;
	}
	sngl_json named = {0};
	named.kind = SNGL_JSON_OBJECT;
	named.len = values->len + 1;
	named.keys = sngl_alloc(sizeof(sngl_string) * (size_t)named.len);
	named.items = sngl_alloc(sizeof(sngl_json) * (size_t)named.len);
	if (values->len > 0) {
		memcpy(named.keys, values->keys, sizeof(sngl_string) * (size_t)values->len);
		memcpy(named.items, values->items, sizeof(sngl_json) * (size_t)values->len);
	}
	named.keys[values->len] = name;
	memset(&named.items[values->len], 0, sizeof(sngl_json));
	named.items[values->len].raw = text;
	int64_t *order;
	int64_t n = sngl_json_sorted(&named, &order);
	sngl_buf b = {0};
	sngl_buf_put(&b, "{", 1);
	for (int64_t i = 0; i < n; i++) {
		sngl_buf_put(&b, i > 0 ? ",\n  " : "\n  ", i > 0 ? 4 : 3);
		sngl_json_quote(&b, named.keys[order[i]]);
		sngl_buf_put(&b, ": ", 2);
		sngl_buf_str(&b, named.items[order[i]].raw);
	}
	sngl_buf_put(&b, "\n}", 2);
	sngl_replace_file(path, sngl_concat(path, SNGL_STR(".tmp")), sngl_buf_string(&b));
}
