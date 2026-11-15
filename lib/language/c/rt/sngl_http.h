/* sngl:http's requests for a runtime in C, in either language: what the
 * generated code and a platform's own fetches -- a picture's -- call. C's
 * own client is libcurl (sngl_http.c); a platform with a client of its own
 * defines sngl_http_start, sngl_http_cancel and sngl_http_answer_free
 * instead (win32's WinHTTP), and ships this header alone. sngl_http_fetch
 * is a program in C's alone (sngl_fetch.c), over whichever client it has.
 * It names no type of sngl.h's, since a Go program's runtime has none. */
#ifndef SNGL_HTTP_H
#define SNGL_HTTP_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

/* An answer: a failure (TRANSPORT, TIMEOUT or CANCELLED) with its message,
 * or the status, the headers named in lower case, a name given twice joined
 * with ", ", and the body. The runtime's until sngl_http_answer_free. */
enum { SNGL_HTTP_ANSWERED, SNGL_HTTP_TRANSPORT, SNGL_HTTP_TIMEOUT, SNGL_HTTP_CANCELLED };
typedef struct sngl_http_answer {
	int failure;
	char *message;
	int status;
	size_t nheaders;
	char **names, **values;
	char *body;
	size_t body_len;
} sngl_http_answer;
void sngl_http_answer_free(sngl_http_answer *a);

/* sngl_http_fetch makes a request and waits for its answer, the window
 * drawing meanwhile, until it arrives or ended, where it is not NULL, says
 * with ctx to stop waiting. body is NULL for none. */
void sngl_http_fetch(const char *method, const char *url, size_t nheaders, const char *const *names, const char *const *values, const char *body, size_t body_len,
                     int (*ended)(void *), void *ctx, sngl_http_answer *out);

/* sngl_http_start makes a request off the loop's thread and returns at
 * once: done runs on the loop with the answer, which it may take (zeroing
 * what it took), unless sngl_http_cancel was called first, after which
 * done never runs. The caller lets go of r with sngl_http_cancel, once done
 * ran too. */
typedef struct sngl_http_request sngl_http_request;
sngl_http_request *sngl_http_start(const char *method, const char *url, size_t nheaders, const char *const *names, const char *const *values, const char *body,
                                   size_t body_len, void (*done)(sngl_http_answer *a, void *ctx), void *ctx);
void sngl_http_cancel(sngl_http_request *r);

/* What the platform's runtime defines for sngl_http.c and sngl_fetch.c:
 * sngl_post runs fn on the loop and may be called from any thread, and
 * sngl_loop_wait handles what the loop has, waiting for something where it
 * has nothing, and is false once the loop is ending. */
void sngl_post(void (*fn)(void *ctx), void *ctx, void (*release)(void *ctx));
bool sngl_loop_wait(void);

#endif
