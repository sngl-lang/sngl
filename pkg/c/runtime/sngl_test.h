/* The test agent a C program SNGL writes for `sngl test` runs in: it lists
 * and runs the program's tests as the driver asks, over JSON-RPC on stdin
 * and stdout (internal/testrpc), one message a line. */
#ifndef SNGL_TEST_H
#define SNGL_TEST_H

#include "sngl.h"

typedef struct sngl_t sngl_t;

/* A test: what it is called, and its body. */
typedef struct sngl_test {
	const char *name;
	void (*fn)(sngl_t *t);
} sngl_test;

/* sngl_test_main serves the driver until stdin ends. */
int sngl_test_main(const sngl_test *tests, size_t n);

/* sngl_test_log writes msg to the test's log. */
void sngl_test_log(sngl_t *t, sngl_string msg);

/* sngl_test_fail marks the test failed, writing msg; sngl_test_fail_now
 * stops it too, as a failed `must` does. */
void sngl_test_fail(sngl_t *t, sngl_string msg);
_Noreturn void sngl_test_fail_now(sngl_t *t, sngl_string msg);

#endif
