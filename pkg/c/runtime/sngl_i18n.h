/* sngl:i18n for a C program, written beside it where it translates
 * (sngl_i18n.c): the template walk is pkg/go/i18n's, so a placeholder, a
 * quote, a plural's `#` and a fallback read as Go reads them, and what
 * depends on the locale -- a plural's category, a number, a date -- is
 * ICU's. Every string it answers is the turn's. */
#ifndef SNGL_I18N_H
#define SNGL_I18N_H

#include "sngl.h"

/* The manifest the program carries, i18n.manifest.json's text: the
 * program defines it, empty where it has none. */
extern const char sngl_i18n_manifest[];
extern const size_t sngl_i18n_manifest_len;

/* An argument of a template: its field's name, its value as Go's %v writes
 * it, and as an integer where a plural reads one. kind says which of
 * ICU's formatters a {name, number}, {name, date}, {name, time} or
 * {name, dateTime} placeholder hands it to. */
enum { SNGL_I18N_TEXT, SNGL_I18N_INT, SNGL_I18N_FLOAT, SNGL_I18N_DATE, SNGL_I18N_TIME, SNGL_I18N_DATETIME };
typedef struct sngl_i18n_arg {
	sngl_string name;
	sngl_string text;
	int kind;
	int64_t i;
	double f;
} sngl_i18n_arg;

sngl_string sngl_i18n_default_locale(void);
sngl_string sngl_i18n_translate(sngl_string locale, sngl_string key, sngl_string tmpl, const sngl_i18n_arg *args, int64_t n);
sngl_string sngl_i18n_format(sngl_string locale, sngl_string tmpl, const sngl_i18n_arg *args, int64_t n);
sngl_string sngl_i18n_number_int(sngl_string locale, int64_t n, const char *style);
sngl_string sngl_i18n_number_float(sngl_string locale, double n, const char *style);
sngl_string sngl_i18n_date(sngl_string locale, sngl_string d, const char *style);
sngl_string sngl_i18n_time(sngl_string locale, sngl_string t, const char *style);
sngl_string sngl_i18n_datetime(sngl_string locale, sngl_string dt, const char *date_style, const char *time_style);
sngl_string sngl_i18n_select(sngl_string value, const sngl_string *keys, const sngl_string *values, int64_t n);
/* A plural's forms by key: =N where exact, else the CLDR category whose
 * index ns holds (zero, one, two, few, many, other). */
sngl_string sngl_i18n_plural(sngl_string locale, int64_t count, const int64_t *ns, const bool *exact, const sngl_string *forms, int64_t n, bool ordinal);

#endif
