#include "sngl_test.h"

#include <setjmp.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

struct sngl_t {
	const char *name;
	bool failed;
	jmp_buf abort;
};

/* The test running now, which a panic fails rather than ending the agent. */
static sngl_t *sngl_running;

/* sngl_json writes s to out as a JSON string. */
static void sngl_json(FILE *out, const char *s, size_t n) {
	fputc('"', out);
	for (size_t i = 0; i < n; i++) {
		unsigned char c = (unsigned char)s[i];
		switch (c) {
		case '"':
			fputs("\\\"", out);
			break;
		case '\\':
			fputs("\\\\", out);
			break;
		case '\n':
			fputs("\\n", out);
			break;
		case '\r':
			fputs("\\r", out);
			break;
		case '\t':
			fputs("\\t", out);
			break;
		default:
			if (c < 0x20) {
				fprintf(out, "\\u%04x", c);
			} else {
				fputc(c, out);
			}
		}
	}
	fputc('"', out);
}

static void sngl_json_str(FILE *out, const char *s) { sngl_json(out, s, strlen(s)); }

/* sngl_notify sends a notification naming test, with what rest writes after
 * its "test" member. */
static void sngl_notify_test(const char *method, const char *test, const char *rest) {
	fprintf(stdout, "{\"jsonrpc\":\"2.0\",\"method\":\"%s\",\"params\":{\"test\":", method);
	sngl_json_str(stdout, test);
	fprintf(stdout, "%s}}\n", rest);
	fflush(stdout);
}

void sngl_test_log(sngl_t *t, sngl_string msg) {
	fprintf(stdout, "{\"jsonrpc\":\"2.0\",\"method\":\"log\",\"params\":{\"test\":");
	sngl_json_str(stdout, t->name);
	fputs(",\"msg\":", stdout);
	sngl_json(stdout, msg.ptr, msg.len);
	fputs("}}\n", stdout);
	fflush(stdout);
}

void sngl_test_fail(sngl_t *t, sngl_string msg) {
	sngl_test_log(t, msg);
	t->failed = true;
	sngl_notify_test("markFail", t->name, "");
}

_Noreturn void sngl_test_fail_now(sngl_t *t, sngl_string msg) {
	sngl_test_fail(t, msg);
	longjmp(t->abort, 1);
}

/* A panic in a test fails that test, which the agent goes on from. */
static void sngl_test_panicked(const char *msg) {
	sngl_t *t = sngl_running;
	if (t == NULL) {
		return;
	}
	sngl_running = NULL;
	sngl_test_fail(t, sngl_concat(SNGL_STR("panic: "), (sngl_string){msg, strlen(msg)}));
	longjmp(t->abort, 1);
}

static const char *sngl_run_one(const sngl_test *test) {
	sngl_t t = {.name = test->name};
	sngl_notify_test("testStart", test->name, "");
	clock_t start = clock();
	if (setjmp(t.abort) == 0) {
		sngl_running = &t;
		test->fn(&t);
	}
	sngl_running = NULL;
	sngl_turn_end();
	const char *status = t.failed ? "fail" : "pass";
	char rest[96];
	snprintf(rest, sizeof rest, ",\"status\":\"%s\",\"durationMs\":%ld", status,
		(long)((clock() - start) * 1000 / CLOCKS_PER_SEC));
	sngl_notify_test("testEnd", test->name, rest);
	return status;
}

/* sngl_member is where the value of the JSON member named key begins in
 * line, or NULL. Enough for the driver's requests, which nest nothing a
 * key could hide in. */
static const char *sngl_member(const char *line, const char *key) {
	char pat[64];
	snprintf(pat, sizeof pat, "\"%s\"", key);
	const char *p = strstr(line, pat);
	if (p == NULL) {
		return NULL;
	}
	p += strlen(pat);
	while (*p == ' ' || *p == ':') {
		p++;
	}
	return p;
}

/* sngl_string_member copies the JSON string value of key in line into out,
 * undoing the escapes the driver's encoder writes in a filter. */
static void sngl_string_member(const char *line, const char *key, char *out, size_t n) {
	out[0] = '\0';
	const char *p = sngl_member(line, key);
	if (p == NULL || *p != '"') {
		return;
	}
	size_t i = 0;
	for (p++; *p && *p != '"' && i + 1 < n; p++) {
		if (*p == '\\' && p[1]) {
			p++;
		}
		out[i++] = *p;
	}
	out[i] = '\0';
}

static void sngl_respond(long long id, const char *result) {
	fprintf(stdout, "{\"jsonrpc\":\"2.0\",\"id\":%lld,\"result\":%s}\n", id, result);
	fflush(stdout);
}

int sngl_test_main(const sngl_test *tests, size_t n) {
	sngl_panic_hook = sngl_test_panicked;
	char line[1 << 16];
	while (fgets(line, sizeof line, stdin) != NULL) {
		const char *idp = sngl_member(line, "id");
		char method[32];
		sngl_string_member(line, "method", method, sizeof method);
		if (idp == NULL || method[0] == '\0') {
			continue;
		}
		long long id = atoll(idp);
		if (strcmp(method, "list") == 0) {
			fputs("{\"jsonrpc\":\"2.0\",\"id\":", stdout);
			fprintf(stdout, "%lld,\"result\":{\"tests\":[", id);
			for (size_t i = 0; i < n; i++) {
				if (i > 0) {
					fputc(',', stdout);
				}
				sngl_json_str(stdout, tests[i].name);
			}
			fputs("]}}\n", stdout);
			fflush(stdout);
		} else if (strcmp(method, "run") == 0) {
			char filter[256];
			sngl_string_member(line, "filter", filter, sizeof filter);
			if (filter[0] == '\0') {
				sngl_string_member(line, "Filter", filter, sizeof filter);
			}
			int passed = 0, failed = 0;
			for (size_t i = 0; i < n; i++) {
				if (filter[0] != '\0' && strstr(tests[i].name, filter) == NULL) {
					continue;
				}
				if (strcmp(sngl_run_one(&tests[i]), "pass") == 0) {
					passed++;
				} else {
					failed++;
				}
			}
			fprintf(stdout, "{\"jsonrpc\":\"2.0\",\"method\":\"runComplete\",\"params\":{\"passed\":%d,\"failed\":%d,\"skipped\":0}}\n", passed, failed);
			sngl_respond(id, "{}");
		} else if (strcmp(method, "cancel") == 0) {
			sngl_respond(id, "{}");
			return 0;
		} else {
			fprintf(stdout, "{\"jsonrpc\":\"2.0\",\"id\":%lld,\"error\":{\"code\":-32601,\"message\":\"method not found: ", id);
			fputs(method, stdout);
			fputs("\"}}\n", stdout);
			fflush(stdout);
		}
	}
	return 0;
}
