#include "sngl_test.h"

#include <setjmp.h>
#include <stdarg.h>
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

/* The driver's channel, and the message being written to it. */
static const sngl_test_io *sngl_io;
static char *sngl_out;
static size_t sngl_out_len, sngl_out_cap;

static void sngl_put(const char *s, size_t n) {
	if (sngl_out_len + n + 1 > sngl_out_cap) {
		sngl_out_cap = (sngl_out_len + n + 1) * 2;
		sngl_out = realloc(sngl_out, sngl_out_cap);
	}
	memcpy(sngl_out + sngl_out_len, s, n);
	sngl_out_len += n;
}

static void sngl_puts(const char *s) { sngl_put(s, strlen(s)); }

static void sngl_putc(char c) { sngl_put(&c, 1); }

static void sngl_printf(const char *format, ...) {
	char buf[256];
	va_list ap;
	va_start(ap, format);
	int n = vsnprintf(buf, sizeof buf, format, ap);
	va_end(ap);
	sngl_put(buf, n < 0 ? 0 : (size_t)n < sizeof buf ? (size_t)n : sizeof buf - 1);
}

/* sngl_send sends the message written. */
static void sngl_send(void) {
	sngl_io->write(sngl_out, sngl_out_len, sngl_io->ctx);
	sngl_out_len = 0;
}

/* sngl_json writes s into the message as a JSON string. */
static void sngl_json(const char *s, size_t n) {
	sngl_putc('"');
	for (size_t i = 0; i < n; i++) {
		unsigned char c = (unsigned char)s[i];
		switch (c) {
		case '"':
			sngl_puts("\\\"");
			break;
		case '\\':
			sngl_puts("\\\\");
			break;
		case '\n':
			sngl_puts("\\n");
			break;
		case '\r':
			sngl_puts("\\r");
			break;
		case '\t':
			sngl_puts("\\t");
			break;
		default:
			if (c < 0x20) {
				sngl_printf("\\u%04x", c);
			} else {
				sngl_putc((char)c);
			}
		}
	}
	sngl_putc('"');
}

static void sngl_json_str(const char *s) { sngl_json(s, strlen(s)); }

/* sngl_notify sends a notification naming test, with what rest writes after
 * its "test" member. */
static void sngl_notify_test(const char *method, const char *test, const char *rest) {
	sngl_printf("{\"jsonrpc\":\"2.0\",\"method\":\"%s\",\"params\":{\"test\":", method);
	sngl_json_str(test);
	sngl_printf("%s}}\n", rest);
	sngl_send();
}

void sngl_test_log(sngl_t *t, sngl_string msg) {
	sngl_printf("{\"jsonrpc\":\"2.0\",\"method\":\"log\",\"params\":{\"test\":");
	sngl_json_str(t->name);
	sngl_puts(",\"msg\":");
	sngl_json(msg.ptr, msg.len);
	sngl_puts("}}\n");
	sngl_send();
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

void sngl_test_fault(const char *msg) {
	if (sngl_running != NULL) {
		sngl_test_fail(sngl_running, (sngl_string){msg, strlen(msg)});
	}
}

/* The wall clock in milliseconds: a test that waits on the program's loop
 * takes the time it waited, which clock(), the processor's time, does not
 * count. */
long long sngl_test_now_ms(void) {
	struct timespec ts;
	timespec_get(&ts, TIME_UTC);
	return (long long)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

static const char *sngl_run_one(const sngl_test *test) {
	/* Static, since a failure writes it after the setjmp, and C leaves a
	 * local so written indeterminate once the longjmp lands. */
	static sngl_t t;
	t = (sngl_t){.name = test->name};
	sngl_notify_test("testStart", test->name, "");
	long long start = sngl_test_now_ms();
	if (setjmp(t.abort) == 0) {
		sngl_running = &t;
		test->fn(&t);
	}
	sngl_running = NULL;
	/* A failure or a panic leaves the handler it stopped without taking its
	 * catch points off. */
	sngl_catching = NULL;
	sngl_turn_end();
	const char *status = t.failed ? "fail" : "pass";
	char rest[96];
	snprintf(rest, sizeof rest, ",\"status\":\"%s\",\"durationMs\":%lld", status,
		sngl_test_now_ms() - start);
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
	sngl_printf("{\"jsonrpc\":\"2.0\",\"id\":%lld,\"result\":%s}\n", id, result);
	sngl_send();
}

static bool sngl_stdin_line(char *buf, size_t n, void *ctx) {
	(void)ctx;
	return fgets(buf, (int)n, stdin) != NULL;
}

static void sngl_stdout_write(const char *s, size_t n, void *ctx) {
	(void)ctx;
	fwrite(s, 1, n, stdout);
	fflush(stdout);
}

int sngl_test_main(const sngl_test *tests, size_t n) {
	static const sngl_test_io stdio = {sngl_stdin_line, sngl_stdout_write, NULL};
	return sngl_test_serve(tests, n, &stdio);
}

int sngl_test_serve(const sngl_test *tests, size_t n, const sngl_test_io *io) {
	sngl_io = io;
	sngl_panic_hook = sngl_test_panicked;
	static char line[1 << 16];
	while (io->read_line(line, sizeof line, io->ctx)) {
		const char *idp = sngl_member(line, "id");
		char method[32];
		sngl_string_member(line, "method", method, sizeof method);
		if (idp == NULL || method[0] == '\0') {
			continue;
		}
		long long id = atoll(idp);
		if (strcmp(method, "list") == 0) {
			sngl_puts("{\"jsonrpc\":\"2.0\",\"id\":");
			sngl_printf("%lld,\"result\":{\"tests\":[", id);
			for (size_t i = 0; i < n; i++) {
				if (i > 0) {
					sngl_putc(',');
				}
				sngl_json_str(tests[i].name);
			}
			sngl_puts("]}}\n");
			sngl_send();
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
			sngl_printf("{\"jsonrpc\":\"2.0\",\"method\":\"runComplete\",\"params\":{\"passed\":%d,\"failed\":%d,\"skipped\":0}}\n", passed, failed);
			sngl_respond(id, "{}");
		} else if (strcmp(method, "cancel") == 0) {
			sngl_respond(id, "{}");
			return 0;
		} else {
			sngl_printf("{\"jsonrpc\":\"2.0\",\"id\":%lld,\"error\":{\"code\":-32601,\"message\":\"method not found: ", id);
			sngl_puts(method);
			sngl_puts("\"}}\n");
			sngl_send();
		}
	}
	return 0;
}
