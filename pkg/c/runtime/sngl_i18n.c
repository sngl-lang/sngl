#include "sngl_i18n.h"

#include <inttypes.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* Windows has carried ICU's C API as icu.dll since 10 1903, which MinGW
 * links (-licu) but ships no header for: what this calls is declared as
 * icu.h declares it. Elsewhere ICU's own headers rename each function for
 * its version. */
#ifdef _WIN32
typedef uint16_t UChar;
typedef int UErrorCode;
typedef double UDate;
typedef struct UPluralRules UPluralRules;
typedef void *UNumberFormat;
typedef void *UDateFormat;
enum { UPLURAL_TYPE_CARDINAL, UPLURAL_TYPE_ORDINAL };
enum { UNUM_DECIMAL = 1, UNUM_CURRENCY = 2, UNUM_PERCENT = 3, UNUM_SCIENTIFIC = 4 };
enum { UNUM_MAX_FRACTION_DIGITS = 6, UNUM_MIN_FRACTION_DIGITS = 7 };
enum { UDAT_FULL, UDAT_LONG, UDAT_MEDIUM, UDAT_SHORT, UDAT_NONE = -1 };
#define U_ZERO_ERROR 0
#define U_FAILURE(e) ((e) > U_ZERO_ERROR)
UPluralRules *uplrules_openForType(const char *locale, int type, UErrorCode *status);
int32_t uplrules_select(const UPluralRules *rules, double number, UChar *keyword, int32_t capacity, UErrorCode *status);
UNumberFormat *unum_open(int style, const UChar *pattern, int32_t patternLength, const char *locale, void *parseErr, UErrorCode *status);
void unum_setAttribute(UNumberFormat *fmt, int attr, int32_t newValue);
int32_t unum_formatInt64(const UNumberFormat *fmt, int64_t number, UChar *result, int32_t resultLength, void *pos, UErrorCode *status);
int32_t unum_formatDouble(const UNumberFormat *fmt, double number, UChar *result, int32_t resultLength, void *pos, UErrorCode *status);
UDateFormat *udat_open(int timeStyle, int dateStyle, const char *locale, const UChar *tzID, int32_t tzIDLength, const UChar *pattern, int32_t patternLength, UErrorCode *status);
int32_t udat_format(const UDateFormat *format, UDate date, UChar *result, int32_t resultLength, void *position, UErrorCode *status);
void udat_close(UDateFormat *format);
#else
#include <unicode/udat.h>
#include <unicode/unum.h>
#include <unicode/upluralrules.h>
#endif

/* --- Text --- */

static sngl_string sngl_from_utf16(const UChar *s, int32_t n) {
	sngl_buf b = {0};
	for (int32_t i = 0; i < n; i++) {
		uint32_t r = (uint16_t)s[i];
		if (r >= 0xD800 && r < 0xDC00 && i + 1 < n && (uint16_t)s[i + 1] >= 0xDC00 && (uint16_t)s[i + 1] < 0xE000) {
			r = 0x10000 + ((r - 0xD800) << 10) + ((uint16_t)s[++i] - 0xDC00);
		}
		char u[4];
		size_t len;
		if (r < 0x80) {
			u[0] = (char)r;
			len = 1;
		} else if (r < 0x800) {
			u[0] = (char)(0xC0 | (r >> 6));
			u[1] = (char)(0x80 | (r & 0x3F));
			len = 2;
		} else if (r < 0x10000) {
			u[0] = (char)(0xE0 | (r >> 12));
			u[1] = (char)(0x80 | ((r >> 6) & 0x3F));
			u[2] = (char)(0x80 | (r & 0x3F));
			len = 3;
		} else {
			u[0] = (char)(0xF0 | (r >> 18));
			u[1] = (char)(0x80 | ((r >> 12) & 0x3F));
			u[2] = (char)(0x80 | ((r >> 6) & 0x3F));
			u[3] = (char)(0x80 | (r & 0x3F));
			len = 4;
		}
		sngl_buf_put(&b, u, len);
	}
	return sngl_buf_string(&b);
}

static sngl_string sngl_trim(sngl_string s) {
	while (s.len > 0 && (s.ptr[0] == ' ' || s.ptr[0] == '\t' || s.ptr[0] == '\n' || s.ptr[0] == '\r')) {
		s.ptr++;
		s.len--;
	}
	while (s.len > 0 && (s.ptr[s.len - 1] == ' ' || s.ptr[s.len - 1] == '\t' || s.ptr[s.len - 1] == '\n' || s.ptr[s.len - 1] == '\r')) {
		s.len--;
	}
	return s;
}

static bool sngl_is(sngl_string s, const char *lit) {
	return s.len == strlen(lit) && memcmp(s.ptr, lit, s.len) == 0;
}

static char *sngl_heap_text(sngl_string s) {
	char *out = malloc(s.len + 1);
	if (out == NULL) {
		sngl_panic("out of memory");
	}
	memcpy(out, s.ptr, s.len);
	out[s.len] = '\0';
	return out;
}

/* A BCP 47 tag as ICU names a locale. */
static const char *sngl_icu_locale(sngl_string locale) {
	char *out = sngl_alloc(locale.len + 1);
	for (size_t i = 0; i < locale.len; i++) {
		out[i] = locale.ptr[i] == '-' ? '_' : locale.ptr[i];
	}
	out[locale.len] = '\0';
	return out;
}

/* --- The locale and the manifest --- */

/* LC_ALL, LC_MESSAGES or LANG, without its codeset and modifier, as a BCP
 * 47 tag; en-US where none is set, as Go's DefaultLocale answers. */
sngl_string sngl_i18n_default_locale(void) {
	static const char *const vars[] = {"LC_ALL", "LC_MESSAGES", "LANG"};
	for (size_t k = 0; k < sizeof vars / sizeof vars[0]; k++) {
		const char *v = getenv(vars[k]);
		if (v == NULL || *v == '\0') {
			continue;
		}
		size_t n = strcspn(v, ".@");
		char *out = sngl_alloc(n + 1);
		for (size_t i = 0; i < n; i++) {
			out[i] = v[i] == '_' ? '-' : v[i];
		}
		out[n] = '\0';
		return (sngl_string){out, n};
	}
	return SNGL_STR("en-US");
}

/* The manifest read once into memory of its own, each key's translations
 * by locale. */
typedef struct sngl_entry {
	char *key;
	int64_t n;
	char **locales;
	char **templates;
} sngl_entry;

static sngl_entry *sngl_entries;
static int64_t sngl_nentries = -1;

static void sngl_load_manifest(void) {
	sngl_nentries = 0;
	const sngl_json *m = sngl_json_parse((sngl_string){sngl_i18n_manifest, sngl_i18n_manifest_len});
	if (m == NULL || m->kind != SNGL_JSON_OBJECT || m->len == 0) {
		return;
	}
	sngl_entries = calloc((size_t)m->len, sizeof *sngl_entries);
	if (sngl_entries == NULL) {
		sngl_panic("out of memory");
	}
	for (int64_t i = 0; i < m->len; i++) {
		const sngl_json *t = m->items[i].kind == SNGL_JSON_OBJECT ? sngl_json_field(&m->items[i], SNGL_STR("translations")) : NULL;
		sngl_entry *e = &sngl_entries[sngl_nentries++];
		e->key = sngl_heap_text(m->keys[i]);
		if (t == NULL || t->kind != SNGL_JSON_OBJECT) {
			continue;
		}
		e->locales = calloc((size_t)t->len + 1, sizeof *e->locales);
		e->templates = calloc((size_t)t->len + 1, sizeof *e->templates);
		if (e->locales == NULL || e->templates == NULL) {
			sngl_panic("out of memory");
		}
		for (int64_t j = 0; j < t->len; j++) {
			if (t->items[j].kind == SNGL_JSON_STRING) {
				e->locales[e->n] = sngl_heap_text(t->keys[j]);
				e->templates[e->n] = sngl_heap_text(t->items[j].text);
				e->n++;
			}
		}
	}
}

/* The template for key at locale: its translation for the locale, or for
 * each shorter form of it ("es-MX", then "es"), else the template written
 * at the call, else the key, as Go's Manifest.Lookup answers. A key the
 * manifest holds twice is its last. */
static sngl_string sngl_lookup(sngl_string key, sngl_string tmpl, sngl_string locale) {
	if (sngl_nentries < 0) {
		sngl_load_manifest();
	}
	const sngl_entry *e = NULL;
	for (int64_t i = sngl_nentries - 1; i >= 0 && e == NULL; i--) {
		if (strlen(sngl_entries[i].key) == key.len && memcmp(sngl_entries[i].key, key.ptr, key.len) == 0) {
			e = &sngl_entries[i];
		}
	}
	if (e != NULL) {
		sngl_string loc = locale;
		while (loc.len > 0) {
			for (int64_t j = e->n - 1; j >= 0; j--) {
				if (strlen(e->locales[j]) == loc.len && memcmp(e->locales[j], loc.ptr, loc.len) == 0) {
					return sngl_string_copy(e->templates[j]);
				}
			}
			size_t cut = loc.len;
			while (cut > 0 && loc.ptr[cut - 1] != '-') {
				cut--;
			}
			if (cut == 0) {
				break;
			}
			loc.len = cut - 1;
		}
	}
	return tmpl.len > 0 ? tmpl : key;
}

/* --- ICU --- */

/* Each plural rule set, number formatter and date formatter opened, kept
 * for the run: a program asks the same few again and again. */
typedef struct sngl_opened {
	char *locale;
	int kind, a, b;
	void *icu;
} sngl_opened;

static sngl_opened *sngl_cache;
static size_t sngl_ncache, sngl_capcache;

static void *sngl_cached(const char *locale, int kind, int a, int b) {
	for (size_t i = 0; i < sngl_ncache; i++) {
		sngl_opened *o = &sngl_cache[i];
		if (o->kind == kind && o->a == a && o->b == b && strcmp(o->locale, locale) == 0) {
			return o->icu;
		}
	}
	return NULL;
}

static void *sngl_keep_opened(const char *locale, int kind, int a, int b, void *icu) {
	if (icu == NULL) {
		return NULL;
	}
	if (sngl_ncache == sngl_capcache) {
		sngl_capcache = sngl_capcache ? sngl_capcache * 2 : 16;
		sngl_cache = realloc(sngl_cache, sngl_capcache * sizeof *sngl_cache);
		if (sngl_cache == NULL) {
			sngl_panic("out of memory");
		}
	}
	sngl_cache[sngl_ncache++] = (sngl_opened){sngl_heap_text(sngl_string_copy(locale)), kind, a, b, icu};
	return icu;
}

enum { SNGL_PLURALS, SNGL_NUMBERS, SNGL_DATES };

/* The CLDR category of n at locale, by its absolute value, as Go reads
 * one: "other" where ICU has no rules. */
static sngl_string sngl_category(sngl_string locale, int64_t n, bool ordinal) {
	const char *loc = sngl_icu_locale(locale);
	int type = ordinal ? UPLURAL_TYPE_ORDINAL : UPLURAL_TYPE_CARDINAL;
	UPluralRules *rules = sngl_cached(loc, SNGL_PLURALS, type, 0);
	if (rules == NULL) {
		UErrorCode err = U_ZERO_ERROR;
		UPluralRules *opened = uplrules_openForType(loc, type, &err);
		rules = U_FAILURE(err) ? NULL : sngl_keep_opened(loc, SNGL_PLURALS, type, 0, opened);
	}
	if (rules == NULL) {
		return SNGL_STR("other");
	}
	UChar kw[32];
	UErrorCode err = U_ZERO_ERROR;
	int32_t len = uplrules_select(rules, fabs((double)n), kw, 32, &err);
	if (U_FAILURE(err) || len <= 0) {
		return SNGL_STR("other");
	}
	return sngl_from_utf16(kw, len);
}

static int sngl_number_style(const char *style) {
	if (strcmp(style, "percent") == 0) {
		return UNUM_PERCENT;
	}
	if (strcmp(style, "currency") == 0) {
		return UNUM_CURRENCY;
	}
	if (strcmp(style, "scientific") == 0) {
		return UNUM_SCIENTIFIC;
	}
	return UNUM_DECIMAL;
}

/* fraction is how many digits a decimal float shows after its point, -1
 * for the formatter's own: a decimal float is written as Go's %g writes
 * it, in the fewest digits that read back as the same value. */
static UNumberFormat *sngl_number_format(sngl_string locale, int style, int fraction) {
	const char *loc = sngl_icu_locale(locale);
	UNumberFormat *f = sngl_cached(loc, SNGL_NUMBERS, style, fraction);
	if (f != NULL) {
		return f;
	}
	UErrorCode err = U_ZERO_ERROR;
	f = unum_open(style, NULL, 0, loc, NULL, &err);
	if (U_FAILURE(err)) {
		return NULL;
	}
	if (fraction >= 0) {
		unum_setAttribute(f, UNUM_MIN_FRACTION_DIGITS, fraction);
		unum_setAttribute(f, UNUM_MAX_FRACTION_DIGITS, fraction);
	}
	return sngl_keep_opened(loc, SNGL_NUMBERS, style, fraction, f);
}

static int sngl_fraction_digits(double v) {
	char e[64];
	int prec = 0;
	for (; prec < 17; prec++) {
		snprintf(e, sizeof e, "%.*e", prec, v);
		if (strtod(e, NULL) == v) {
			break;
		}
	}
	const char *x = strpbrk(e, "eE");
	int exp = x != NULL ? atoi(x + 1) : 0;
	int digits = prec - exp;
	return digits > 0 ? digits : 0;
}

static sngl_string sngl_format_number(sngl_string locale, bool integral, int64_t i, double f, const char *style) {
	int st = sngl_number_style(style);
	if (integral && st == UNUM_PERCENT) {
		integral = false;
		f = (double)i / 100.0;
	}
	int fraction = !integral && st == UNUM_DECIMAL && isfinite(f) ? sngl_fraction_digits(f) : -1;
	UNumberFormat *fmt = sngl_number_format(locale, st, fraction);
	if (fmt == NULL) {
		return integral ? sngl_int_format(i) : sngl_float_format(f);
	}
	UChar buf[128];
	UErrorCode err = U_ZERO_ERROR;
	int32_t len = integral ? unum_formatInt64(fmt, i, buf, 128, NULL, &err) : unum_formatDouble(fmt, f, buf, 128, NULL, &err);
	if (U_FAILURE(err)) {
		return integral ? sngl_int_format(i) : sngl_float_format(f);
	}
	return sngl_from_utf16(buf, len);
}

static int sngl_date_style(const char *style) {
	if (strcmp(style, "short") == 0) {
		return UDAT_SHORT;
	}
	if (strcmp(style, "long") == 0) {
		return UDAT_LONG;
	}
	if (strcmp(style, "full") == 0) {
		return UDAT_FULL;
	}
	return UDAT_MEDIUM;
}

static bool sngl_digits(const char *s, size_t at, size_t n, size_t len, int *out) {
	if (at + n > len) {
		return false;
	}
	int v = 0;
	for (size_t i = at; i < at + n; i++) {
		if (s[i] < '0' || s[i] > '9') {
			return false;
		}
		v = v * 10 + (s[i] - '0');
	}
	*out = v;
	return true;
}

static int64_t sngl_days_from_civil(int64_t y, int m, int d) {
	y -= m <= 2;
	int64_t era = (y >= 0 ? y : y - 399) / 400;
	int64_t yoe = y - era * 400;
	int64_t doy = (153 * (m + (m > 2 ? -3 : 9)) + 2) / 5 + d - 1;
	int64_t doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
	return era * 146097 + doe - 719468;
}

/* The milliseconds since the epoch the text of a date, a time (on the
 * epoch's day) or a datetime names, and the zone it was written in, as
 * ICU names one: what the formatter shows it in, as Go shows a time in its
 * own location. */
static bool sngl_instant(sngl_string t, int kind, UDate *at, char zone[16]) {
	const char *s = t.ptr;
	size_t n = t.len;
	int y = 1970, mo = 1, d = 1, h = 0, mi = 0, se = 0;
	size_t i = 0;
	strcpy(zone, "UTC");
	if (kind != SNGL_I18N_TIME) {
		if (!sngl_digits(s, 0, 4, n, &y) || n < 10 || s[4] != '-' || !sngl_digits(s, 5, 2, n, &mo) || s[7] != '-' || !sngl_digits(s, 8, 2, n, &d)) {
			return false;
		}
		i = 10;
		if (kind == SNGL_I18N_DATETIME) {
			if (i >= n || (s[i] != 'T' && s[i] != 't' && s[i] != ' ')) {
				return false;
			}
			i++;
		}
	}
	if (kind != SNGL_I18N_DATE) {
		if (!sngl_digits(s, i, 2, n, &h) || i + 2 >= n || s[i + 2] != ':' || !sngl_digits(s, i + 3, 2, n, &mi)) {
			return false;
		}
		i += 5;
		if (i < n && s[i] == ':' && sngl_digits(s, i + 1, 2, n, &se)) {
			i += 3;
		}
		while (i < n && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9'))) {
			i++;
		}
	}
	int64_t offset = 0;
	if (kind == SNGL_I18N_DATETIME && i < n && (s[i] == '+' || s[i] == '-')) {
		int oh = 0, om = 0;
		if (!sngl_digits(s, i + 1, 2, n, &oh) || !sngl_digits(s, i + 4, 2, n, &om)) {
			return false;
		}
		offset = (s[i] == '-' ? -1 : 1) * (oh * 60 + om);
		snprintf(zone, 16, "GMT%c%02d:%02d", s[i], oh, om);
	}
	int64_t secs = sngl_days_from_civil(y, mo, d) * 86400 + h * 3600 + mi * 60 + se - offset * 60;
	*at = (UDate)secs * 1000.0;
	return true;
}

static sngl_string sngl_format_instant(sngl_string locale, sngl_string text, int kind, int date_style, int time_style) {
	UDate at;
	char zone[16];
	if (!sngl_instant(text, kind, &at, zone)) {
		return text;
	}
	UChar tz[16];
	int32_t tzn = 0;
	for (; zone[tzn] != '\0'; tzn++) {
		tz[tzn] = (UChar)zone[tzn];
	}
	UErrorCode err = U_ZERO_ERROR;
	UDateFormat *fmt = udat_open(time_style, date_style, sngl_icu_locale(locale), tz, tzn, NULL, 0, &err);
	if (U_FAILURE(err)) {
		return text;
	}
	UChar buf[256];
	int32_t len = udat_format(fmt, at, buf, 256, NULL, &err);
	udat_close(fmt);
	if (U_FAILURE(err)) {
		return text;
	}
	return sngl_from_utf16(buf, len);
}

/* --- The template --- */

typedef struct sngl_template {
	sngl_string locale;
	const sngl_i18n_arg *args;
	int64_t nargs;
} sngl_template;

static void sngl_walk(sngl_buf *out, const sngl_template *t, sngl_string tmpl, sngl_string current);

static bool sngl_meta(char c) {
	return c == '{' || c == '}' || c == '#' || c == '|';
}

/* The end, past its closing brace, of the placeholder whose opening brace
 * is at start, a quoted run's braces not counted; 0 where it never
 * closes. */
static size_t sngl_placeholder_end(sngl_string s, size_t start) {
	int depth = 0;
	for (size_t i = start; i < s.len; i++) {
		switch (s.ptr[i]) {
		case '{':
			depth++;
			break;
		case '}':
			if (--depth == 0) {
				return i + 1;
			}
			break;
		case '\'':
			if (i + 1 < s.len && s.ptr[i + 1] == '\'') {
				i++;
			} else if (i + 1 < s.len && sngl_meta(s.ptr[i + 1])) {
				i++;
				while (i < s.len) {
					if (s.ptr[i] == '\'') {
						if (i + 1 < s.len && s.ptr[i + 1] == '\'') {
							i += 2;
							continue;
						}
						break;
					}
					i++;
				}
			}
			break;
		}
	}
	return 0;
}

static const sngl_i18n_arg *sngl_arg(const sngl_template *t, sngl_string name) {
	for (int64_t i = 0; i < t->nargs; i++) {
		if (sngl_string_eq(t->args[i].name, name)) {
			return &t->args[i];
		}
	}
	return NULL;
}

/* The next selector and body of a plural's or a select's cases, from *at. */
static bool sngl_next_case(sngl_string raw, size_t *at, sngl_string *sel, sngl_string *body) {
	size_t i = *at;
	while (i < raw.len && (raw.ptr[i] == ' ' || raw.ptr[i] == '\t' || raw.ptr[i] == '\n' || raw.ptr[i] == '\r')) {
		i++;
	}
	size_t from = i;
	while (i < raw.len && raw.ptr[i] != '{') {
		i++;
	}
	if (i >= raw.len) {
		return false;
	}
	*sel = sngl_trim((sngl_string){raw.ptr + from, i - from});
	int depth = 0;
	size_t open = i + 1;
	for (; i < raw.len; i++) {
		if (raw.ptr[i] == '{') {
			depth++;
		} else if (raw.ptr[i] == '}' && --depth == 0) {
			*body = (sngl_string){raw.ptr + open, i - open};
			*at = i + 1;
			return true;
		}
	}
	return false;
}

static void sngl_plural_cases(sngl_buf *out, const sngl_template *t, int64_t n, sngl_string raw, bool ordinal) {
	sngl_string num = sngl_int_format(n);
	sngl_string sel, body;
	size_t at = 0;
	while (sngl_next_case(raw, &at, &sel, &body)) {
		if (sel.len > 0 && sel.ptr[0] == '=' && sngl_string_eq((sngl_string){sel.ptr + 1, sel.len - 1}, num)) {
			sngl_walk(out, t, body, num);
			return;
		}
	}
	sngl_string keyword = sngl_category(t->locale, n, ordinal);
	for (int pass = 0; pass < 2; pass++) {
		at = 0;
		while (sngl_next_case(raw, &at, &sel, &body)) {
			if (sngl_string_eq(sel, pass == 0 ? keyword : SNGL_STR("other"))) {
				sngl_walk(out, t, body, num);
				return;
			}
		}
	}
}

static void sngl_select_cases(sngl_buf *out, const sngl_template *t, sngl_string value, sngl_string raw) {
	sngl_string sel, body;
	for (int pass = 0; pass < 2; pass++) {
		size_t at = 0;
		while (sngl_next_case(raw, &at, &sel, &body)) {
			if (sngl_string_eq(sel, pass == 0 ? value : SNGL_STR("other"))) {
				sngl_walk(out, t, body, SNGL_STR(""));
				return;
			}
		}
	}
}

/* One placeholder's body, split at its top-level commas into its name, its
 * type, and the rest, which is a style or a plural's or a select's cases. */
static void sngl_placeholder(sngl_buf *out, const sngl_template *t, sngl_string body) {
	sngl_string parts[3] = {body, {"", 0}, {"", 0}};
	int nparts = 1;
	int depth = 0;
	for (size_t i = 0; i < body.len && nparts < 3; i++) {
		char c = body.ptr[i];
		if (c == '{') {
			depth++;
		} else if (c == '}') {
			depth--;
		} else if (c == ',' && depth == 0) {
			sngl_string *last = &parts[nparts - 1];
			last->len = (size_t)(body.ptr + i - last->ptr);
			parts[nparts++] = (sngl_string){body.ptr + i + 1, body.len - i - 1};
		}
	}
	const sngl_i18n_arg *a = sngl_arg(t, sngl_trim(parts[0]));
	sngl_string text = a != NULL ? a->text : SNGL_STR("<nil>");
	if (nparts == 1) {
		sngl_buf_str(out, text);
		return;
	}
	sngl_string type = sngl_trim(parts[1]);
	sngl_string rest = nparts > 2 ? parts[2] : SNGL_STR("");
	int64_t n = a != NULL ? a->i : 0;
	if (sngl_is(type, "plural") || sngl_is(type, "selectordinal")) {
		sngl_plural_cases(out, t, n, sngl_trim(rest), sngl_is(type, "selectordinal"));
		return;
	}
	if (sngl_is(type, "select")) {
		sngl_select_cases(out, t, text, sngl_trim(rest));
		return;
	}
	bool number = sngl_is(type, "number");
	if (!number && !sngl_is(type, "date") && !sngl_is(type, "time") && !sngl_is(type, "dateTime")) {
		sngl_buf_str(out, text);
		return;
	}
	sngl_string style = nparts > 2 ? sngl_trim(rest) : (number ? SNGL_STR("decimal") : SNGL_STR("medium"));
	const char *st = sngl_string_bytes(style.ptr, style.len).ptr;
	if (number && a != NULL && (a->kind == SNGL_I18N_INT || a->kind == SNGL_I18N_FLOAT)) {
		sngl_buf_str(out, sngl_format_number(t->locale, a->kind == SNGL_I18N_INT, a->i, a->f, st));
		return;
	}
	int kind = sngl_is(type, "date") ? SNGL_I18N_DATE : sngl_is(type, "time") ? SNGL_I18N_TIME : SNGL_I18N_DATETIME;
	if (!number && a != NULL && a->kind == kind) {
		int s = sngl_date_style(st);
		sngl_buf_str(out, sngl_format_instant(t->locale, a->text, kind, kind == SNGL_I18N_TIME ? UDAT_NONE : s, kind == SNGL_I18N_DATE ? UDAT_NONE : s));
		return;
	}
	sngl_buf_str(out, text);
}

/* The walk of pkg/go/i18n's formatICUInto: ICU's apostrophe quoting, `#`
 * as current inside a plural's case, and each placeholder; one that never
 * closes is written as it stands, with the rest of the template. */
static void sngl_walk(sngl_buf *out, const sngl_template *t, sngl_string tmpl, sngl_string current) {
	size_t i = 0;
	while (i < tmpl.len) {
		char c = tmpl.ptr[i];
		if (c == '\'') {
			if (i + 1 < tmpl.len && tmpl.ptr[i + 1] == '\'') {
				sngl_buf_put(out, "'", 1);
				i += 2;
				continue;
			}
			if (i + 1 < tmpl.len && sngl_meta(tmpl.ptr[i + 1])) {
				i++;
				while (i < tmpl.len) {
					if (tmpl.ptr[i] == '\'') {
						if (i + 1 < tmpl.len && tmpl.ptr[i + 1] == '\'') {
							sngl_buf_put(out, "'", 1);
							i += 2;
							continue;
						}
						i++;
						break;
					}
					sngl_buf_put(out, tmpl.ptr + i, 1);
					i++;
				}
				continue;
			}
			sngl_buf_put(out, "'", 1);
			i++;
			continue;
		}
		if (c == '#' && current.len > 0) {
			sngl_buf_str(out, current);
			i++;
			continue;
		}
		if (c == '{') {
			size_t end = sngl_placeholder_end(tmpl, i);
			if (end == 0) {
				sngl_buf_put(out, tmpl.ptr + i, tmpl.len - i);
				return;
			}
			sngl_placeholder(out, t, (sngl_string){tmpl.ptr + i + 1, end - i - 2});
			i = end;
			continue;
		}
		sngl_buf_put(out, &c, 1);
		i++;
	}
}

static sngl_string sngl_locale_or_default(sngl_string locale) {
	return locale.len > 0 ? locale : sngl_i18n_default_locale();
}

sngl_string sngl_i18n_format(sngl_string locale, sngl_string tmpl, const sngl_i18n_arg *args, int64_t n) {
	sngl_template t = {sngl_locale_or_default(locale), args, n};
	sngl_buf out = {0};
	sngl_walk(&out, &t, tmpl, SNGL_STR(""));
	return sngl_buf_string(&out);
}

sngl_string sngl_i18n_translate(sngl_string locale, sngl_string key, sngl_string tmpl, const sngl_i18n_arg *args, int64_t n) {
	locale = sngl_locale_or_default(locale);
	return sngl_i18n_format(locale, sngl_lookup(key, tmpl, locale), args, n);
}

sngl_string sngl_i18n_number_int(sngl_string locale, int64_t n, const char *style) {
	return sngl_format_number(sngl_locale_or_default(locale), true, n, 0, style);
}

sngl_string sngl_i18n_number_float(sngl_string locale, double n, const char *style) {
	return sngl_format_number(sngl_locale_or_default(locale), false, 0, n, style);
}

sngl_string sngl_i18n_date(sngl_string locale, sngl_string d, const char *style) {
	return sngl_format_instant(sngl_locale_or_default(locale), d, SNGL_I18N_DATE, sngl_date_style(style), UDAT_NONE);
}

sngl_string sngl_i18n_time(sngl_string locale, sngl_string t, const char *style) {
	return sngl_format_instant(sngl_locale_or_default(locale), t, SNGL_I18N_TIME, UDAT_NONE, sngl_date_style(style));
}

sngl_string sngl_i18n_datetime(sngl_string locale, sngl_string dt, const char *date_style, const char *time_style) {
	return sngl_format_instant(sngl_locale_or_default(locale), dt, SNGL_I18N_DATETIME, sngl_date_style(date_style), sngl_date_style(time_style));
}

/* A select's message is its case as written, as Go's Select answers it. */
sngl_string sngl_i18n_select(sngl_string value, const sngl_string *keys, const sngl_string *values, int64_t n) {
	for (int pass = 0; pass < 2; pass++) {
		for (int64_t i = 0; i < n; i++) {
			if (sngl_string_eq(keys[i], pass == 0 ? value : SNGL_STR("other"))) {
				return values[i];
			}
		}
	}
	return SNGL_STR("");
}

/* A plural's form: the exact one, else its category's, else other's,
 * formatted with `#` and `n` the count, as Go's Plural formats it. */
sngl_string sngl_i18n_plural(sngl_string locale, int64_t count, const int64_t *ns, const bool *exact, const sngl_string *forms, int64_t n, bool ordinal) {
	static const char *const categories[] = {"zero", "one", "two", "few", "many", "other"};
	locale = sngl_locale_or_default(locale);
	sngl_string kw = sngl_category(locale, count, ordinal);
	int64_t want = 5;
	for (int64_t c = 0; c < 6; c++) {
		if (sngl_is(kw, categories[c])) {
			want = c;
		}
	}
	int64_t found = -1;
	for (int pass = 0; pass < 3 && found < 0; pass++) {
		for (int64_t i = 0; i < n && found < 0; i++) {
			if ((pass == 0 && exact[i] && ns[i] == count) || (pass == 1 && !exact[i] && ns[i] == want) || (pass == 2 && !exact[i] && ns[i] == 5)) {
				found = i;
			}
		}
	}
	if (found < 0) {
		return SNGL_STR("");
	}
	sngl_string num = sngl_int_format(count);
	sngl_i18n_arg args[2] = {{SNGL_STR("#"), num, SNGL_I18N_INT, count, 0}, {SNGL_STR("n"), num, SNGL_I18N_INT, count, 0}};
	return sngl_i18n_format(locale, forms[found], args, 2);
}
