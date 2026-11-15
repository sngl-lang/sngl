/* sngl_http_fetch (sngl_http.h) for a program in C, over whichever client
 * answers sngl_http_start: libcurl's, or a platform's own. */
#include "sngl.h"
#include "sngl_http.h"

#include <stdlib.h>
#include <string.h>

typedef struct sngl_fetch_wait {
	bool done;
	void *work;
	sngl_http_answer answer;
} sngl_fetch_wait;

static void sngl_fetch_answered(sngl_http_answer *a, void *ctx) {
	sngl_fetch_wait *w = ctx;
	w->answer = *a;
	memset(a, 0, sizeof *a);
	w->done = true;
	if (w->work != NULL) {
		sngl_work_wake(w->work);
	}
}

static char *sngl_fetch_message(const char *s) {
	size_t n = strlen(s);
	char *out = malloc(n + 1);
	if (out != NULL) {
		memcpy(out, s, n + 1);
	}
	return out;
}

/* On a work's stack (sngl_work_start) the wait gives the loop back until the
 * answer arrives, so the requests a page makes end in the order they are
 * answered. Elsewhere it runs the loop until then, and a request a handler
 * the loop runs meanwhile makes ends before this one can. */
void sngl_http_fetch(const char *method, const char *url, size_t nheaders, const char *const *names, const char *const *values, const char *body, size_t body_len,
                     int (*ended)(void *), void *ctx, sngl_http_answer *out) {
	memset(out, 0, sizeof *out);
	sngl_fetch_wait w = {.work = sngl_work_self()};
	sngl_http_request *r = sngl_http_start(method, url, nheaders, names, values, body, body_len, sngl_fetch_answered, &w);
	if (r == NULL) {
		out->failure = SNGL_HTTP_TRANSPORT;
		out->message = sngl_fetch_message("out of memory");
		return;
	}
	while (!w.done) {
		if (ended != NULL && ended(ctx)) {
			break;
		}
		if (w.work != NULL) {
			sngl_work_wait();
		} else if (!sngl_loop_wait()) {
			break;
		}
	}
	sngl_http_cancel(r);
	if (!w.done || (ended != NULL && ended(ctx))) {
		sngl_http_answer_free(&w.answer);
		out->failure = SNGL_HTTP_CANCELLED;
		out->message = sngl_fetch_message("cancelled");
		return;
	}
	*out = w.answer;
}
