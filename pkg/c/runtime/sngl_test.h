/* The test agent a C program SNGL writes for `sngl test` runs in: it lists
 * and runs the program's tests as the driver asks, over JSON-RPC
 * (internal/testrpc), one message a line, on stdin and stdout or a channel
 * the platform gives it. */
#ifndef SNGL_TEST_H
#define SNGL_TEST_H

#include "sngl.h"

typedef struct sngl_t sngl_t;

/* A test: what it is called, and its body. */
typedef struct sngl_test {
	const char *name;
	void (*fn)(sngl_t *t);
} sngl_test;

/* sngl_test_io is the channel to the driver: read_line reads a line into
 * buf, false once the driver is gone, and write sends n bytes, each a call
 * with ctx. */
typedef struct sngl_test_io {
	bool (*read_line)(char *buf, size_t n, void *ctx);
	void (*write)(const char *s, size_t n, void *ctx);
	void *ctx;
} sngl_test_io;

/* sngl_test_serve serves the driver over io until it is gone, and
 * sngl_test_main over stdin and stdout. */
int sngl_test_serve(const sngl_test *tests, size_t n, const sngl_test_io *io);
int sngl_test_main(const sngl_test *tests, size_t n);

/* sngl_test_log writes msg to the test's log. */
void sngl_test_log(sngl_t *t, sngl_string msg);

/* sngl_test_fail marks the test failed, writing msg; sngl_test_fail_now
 * stops it too, as a failed `must` does. */
void sngl_test_fail(sngl_t *t, sngl_string msg);
_Noreturn void sngl_test_fail_now(sngl_t *t, sngl_string msg);

/* sngl_test_fault fails the test running, where one is, writing msg, and
 * lets it go on: a library's complaint that the program misused it, which
 * the library survives, so a test that stops nowhere near it still fails. */
void sngl_test_fault(const char *msg);

/* sngl_test_now_ms is the wall clock in milliseconds, which a test's wait
 * counts. */
long long sngl_test_now_ms(void);

/* sngl_test_capture, which a platform able to draw its program sets before
 * it serves, draws the program's window into *rgba, which the caller frees:
 * *width by *height pixels, four bytes each, red, green, blue and alpha, row
 * by row from the top. False, with *why set, where there is none to draw. */
extern bool (*sngl_test_capture)(unsigned char **rgba, int *width, int *height, const char **why);

/* sngl_test_snapshot compares what sngl_test_capture draws with the golden
 * named name, which the driver keeps, failing the test where they differ:
 * a test's t.snapshot. */
void sngl_test_snapshot(sngl_t *t, sngl_string name);

#endif
