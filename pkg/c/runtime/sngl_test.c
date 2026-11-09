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
 * undoing the escapes the driver's encoder writes: a filter's, and a
 * snapshot's diff, which runs over lines. A \u escape past ASCII, which
 * Go's encoder writes only for a character it must, is kept as '?'. */
static void sngl_string_member(const char *line, const char *key, char *out, size_t n) {
	out[0] = '\0';
	const char *p = sngl_member(line, key);
	if (p == NULL || *p != '"') {
		return;
	}
	size_t i = 0;
	for (p++; *p && *p != '"' && i + 1 < n; p++) {
		char c = *p;
		if (c == '\\' && p[1]) {
			switch (*++p) {
			case 'n':
				c = '\n';
				break;
			case 't':
				c = '\t';
				break;
			case 'r':
				c = '\r';
				break;
			case 'u': {
				char hex[5] = {0};
				for (int k = 0; k < 4 && p[1]; k++) {
					hex[k] = *++p;
				}
				long u = strtol(hex, NULL, 16);
				c = u > 0 && u < 0x80 ? (char)u : '?';
				break;
			}
			default:
				c = *p;
			}
		}
		out[i++] = c;
	}
	out[i] = '\0';
}

/* --- Snapshots --- */

bool (*sngl_test_capture)(unsigned char **png, size_t *n, const char **why);

/* The id of the request the agent sent last: its own, counted apart from
 * the driver's. */
static long long sngl_request_id;

/* sngl_base64 writes the n bytes at p into the message as base64. */
static void sngl_base64(const unsigned char *p, size_t n) {
	static const char digits[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
	for (size_t i = 0; i < n; i += 3) {
		uint32_t v = (uint32_t)p[i] << 16;
		if (i + 1 < n) {
			v |= (uint32_t)p[i + 1] << 8;
		}
		if (i + 2 < n) {
			v |= p[i + 2];
		}
		char quad[4] = {digits[v >> 18 & 63], digits[v >> 12 & 63], i + 1 < n ? digits[v >> 6 & 63] : '=', i + 2 < n ? digits[v & 63] : '='};
		sngl_put(quad, 4);
	}
}

/* sngl_snapshot_failed fails t over the snapshot named name, saying what. */
static void sngl_snapshot_failed(sngl_t *t, sngl_string name, const char *what, const char *detail) {
	sngl_string msg = sngl_concat(SNGL_STR("snapshot \""), name);
	msg = sngl_concat(msg, (sngl_string){what, strlen(what)});
	sngl_test_fail(t, sngl_concat(msg, (sngl_string){detail, strlen(detail)}));
}

void sngl_test_snapshot(sngl_t *t, sngl_string name) {
	if (sngl_test_capture == NULL) {
		sngl_snapshot_failed(t, name, "\": ", "this platform draws no snapshot in C");
		return;
	}
	unsigned char *png = NULL;
	size_t n = 0;
	const char *why = "";
	if (!sngl_test_capture(&png, &n, &why)) {
		sngl_snapshot_failed(t, name, "\": capture: ", why);
		return;
	}
	long long id = ++sngl_request_id;
	sngl_printf("{\"jsonrpc\":\"2.0\",\"id\":%lld,\"method\":\"snapshotAssert\",\"params\":{\"test\":", id);
	sngl_json_str(t->name);
	sngl_puts(",\"name\":");
	sngl_json(name.ptr, name.len);
	sngl_puts(",\"mime\":\"image/png\",\"bytes\":\"");
	sngl_base64(png, n);
	sngl_puts("\"}}\n");
	free(png);
	sngl_send();
	/* The driver sends nothing else while a test runs, so the next line
	 * with this id is its answer. */
	static char line[1 << 16];
	static char detail[1 << 15];
	while (sngl_io->read_line(line, sizeof line, sngl_io->ctx)) {
		const char *idp = sngl_member(line, "id");
		if (idp == NULL || atoll(idp) != id || sngl_member(line, "method") != NULL) {
			continue;
		}
		if (sngl_member(line, "error") != NULL) {
			sngl_string_member(line, "message", detail, sizeof detail);
			sngl_snapshot_failed(t, name, "\": driver: ", detail);
			return;
		}
		const char *pass = sngl_member(line, "pass");
		if (pass == NULL || strncmp(pass, "true", 4) != 0) {
			sngl_string_member(line, "diff", detail, sizeof detail);
			sngl_snapshot_failed(t, name, "\" mismatch:\n", detail);
		}
		return;
	}
	sngl_snapshot_failed(t, name, "\": ", "the driver went away before it answered");
}

/* sngl_crc32 continues crc, PNG's, over the n bytes at p. */
static uint32_t sngl_crc32(uint32_t crc, const unsigned char *p, size_t n) {
	static uint32_t table[256];
	if (table[1] == 0) {
		for (uint32_t i = 0; i < 256; i++) {
			uint32_t c = i;
			for (int k = 0; k < 8; k++) {
				c = c & 1 ? 0xedb88320u ^ (c >> 1) : c >> 1;
			}
			table[i] = c;
		}
	}
	crc = ~crc;
	for (size_t i = 0; i < n; i++) {
		crc = table[(crc ^ p[i]) & 0xff] ^ (crc >> 8);
	}
	return ~crc;
}

static unsigned char *sngl_be32(unsigned char *p, uint32_t v) {
	p[0] = (unsigned char)(v >> 24);
	p[1] = (unsigned char)(v >> 16);
	p[2] = (unsigned char)(v >> 8);
	p[3] = (unsigned char)v;
	return p + 4;
}

/* sngl_png_chunk writes the chunk of type typ holding the n bytes at data
 * at p, returning where it ends. */
static unsigned char *sngl_png_chunk(unsigned char *p, const char *typ, const unsigned char *data, size_t n) {
	p = sngl_be32(p, (uint32_t)n);
	unsigned char *start = p;
	memcpy(p, typ, 4);
	if (n > 0) {
		memcpy(p + 4, data, n);
	}
	p += 4 + n;
	return sngl_be32(p, sngl_crc32(0, start, 4 + n));
}

/* sngl_bits is a deflate stream being written, its bits from the lowest. */
typedef struct {
	unsigned char *p;
	size_t n, cap;
	uint32_t acc;
	int nacc;
} sngl_bits;

static void sngl_bits_put(sngl_bits *b, uint32_t v, int n) {
	b->acc |= v << b->nacc;
	b->nacc += n;
	while (b->nacc >= 8) {
		if (b->n == b->cap) {
			b->cap = b->cap * 2 + 64;
			b->p = realloc(b->p, b->cap);
		}
		b->p[b->n++] = (unsigned char)b->acc;
		b->acc >>= 8;
		b->nacc -= 8;
	}
}

/* sngl_bits_code writes a Huffman code, whose bits deflate packs from the
 * highest. */
static void sngl_bits_code(sngl_bits *b, uint32_t code, int n) {
	uint32_t r = 0;
	for (int i = 0; i < n; i++) {
		r = r << 1 | (code >> i & 1);
	}
	sngl_bits_put(b, r, n);
}

/* sngl_bits_symbol writes a literal, a length or the end in deflate's fixed
 * code. */
static void sngl_bits_symbol(sngl_bits *b, int s) {
	if (s < 144) {
		sngl_bits_code(b, 0x30 + (uint32_t)s, 8);
	} else if (s < 256) {
		sngl_bits_code(b, 0x190 + (uint32_t)(s - 144), 9);
	} else if (s < 280) {
		sngl_bits_code(b, (uint32_t)(s - 256), 7);
	} else {
		sngl_bits_code(b, 0xc0 + (uint32_t)(s - 280), 8);
	}
}

static const uint16_t sngl_len_base[29] = {3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258};
static const uint8_t sngl_len_extra[29] = {0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0};
static const uint16_t sngl_dist_base[30] = {1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577};
static const uint8_t sngl_dist_extra[30] = {0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13};

/* sngl_bits_match writes a copy of len bytes from dist back. */
static void sngl_bits_match(sngl_bits *b, int len, int dist) {
	int k = 28;
	while (sngl_len_base[k] > len) {
		k--;
	}
	sngl_bits_symbol(b, 257 + k);
	sngl_bits_put(b, (uint32_t)(len - sngl_len_base[k]), sngl_len_extra[k]);
	int j = 29;
	while (sngl_dist_base[j] > dist) {
		j--;
	}
	sngl_bits_code(b, (uint32_t)j, 5);
	sngl_bits_put(b, (uint32_t)(dist - sngl_dist_base[j]), sngl_dist_extra[j]);
}

/* The PNG holds its pixels deflated in one block of the fixed code, whose
 * only copies are of the pixel before and of the row above: what a window
 * repeats, its flat fills, costs a few bits a run, and no C runtime ships
 * a deflate. */
unsigned char *sngl_test_png(const unsigned char *rgba, int w, int h, size_t *n) {
	size_t row = (size_t)w * 4 + 1, raw = row * (size_t)h;
	unsigned char *r = malloc(raw);
	for (int y = 0; y < h; y++) {
		/* Each row starts with its filter, none. */
		r[(size_t)y * row] = 0;
		memcpy(r + (size_t)y * row + 1, rgba + (size_t)y * (size_t)w * 4, (size_t)w * 4);
	}
	sngl_bits b = {0};
	sngl_bits_put(&b, 0x78, 8);
	sngl_bits_put(&b, 0x01, 8);
	/* The last block, of the fixed code. */
	sngl_bits_put(&b, 1, 1);
	sngl_bits_put(&b, 1, 2);
	size_t dists[2] = {4, row};
	for (size_t i = 0; i < raw;) {
		size_t best = 0, at = 0;
		for (int k = 0; k < 2; k++) {
			size_t d = dists[k];
			if (d > i || d > 32768) {
				continue;
			}
			size_t l = 0;
			while (l < 258 && i + l < raw && r[i + l] == r[i + l - d]) {
				l++;
			}
			if (l > best) {
				best = l;
				at = d;
			}
		}
		if (best >= 3) {
			sngl_bits_match(&b, (int)best, (int)at);
			i += best;
		} else {
			sngl_bits_symbol(&b, r[i++]);
		}
	}
	sngl_bits_symbol(&b, 256);
	if (b.nacc > 0) {
		sngl_bits_put(&b, 0, 8 - b.nacc);
	}
	uint32_t a = 1, s2 = 0;
	for (size_t i = 0; i < raw; i++) {
		a = (a + r[i]) % 65521;
		s2 = (s2 + a) % 65521;
	}
	free(r);
	unsigned char adler[4];
	sngl_be32(adler, s2 << 16 | a);
	for (int i = 0; i < 4; i++) {
		sngl_bits_put(&b, adler[i], 8);
	}
	unsigned char ihdr[13];
	sngl_be32(ihdr, (uint32_t)w);
	sngl_be32(ihdr + 4, (uint32_t)h);
	/* Eight bits a sample, RGBA, deflate, PNG's one filter method, no
	 * interlace. */
	memcpy(ihdr + 8, "\x08\x06\x00\x00\x00", 5);
	unsigned char *png = malloc(8 + 25 + 12 + b.n + 12);
	unsigned char *p = png;
	memcpy(p, "\x89PNG\r\n\x1a\n", 8);
	p = sngl_png_chunk(p + 8, "IHDR", ihdr, sizeof ihdr);
	p = sngl_png_chunk(p, "IDAT", b.p, b.n);
	p = sngl_png_chunk(p, "IEND", NULL, 0);
	free(b.p);
	*n = (size_t)(p - png);
	return png;
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
