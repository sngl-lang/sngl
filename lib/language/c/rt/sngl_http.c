/* sngl:http's requests in C: libcurl's multi interface on one thread of its
 * own, which every request is handed to, its answer handed back on the loop
 * (sngl_http.h). One multi handle keeps its connections, and multiplexes
 * HTTP/2 over one, as Go's client does: a request each with a handshake of
 * its own made the Pokédex's Moves tab, a request a move, wait seconds. */
#if !defined(_WIN32) && !defined(_POSIX_C_SOURCE)
#define _POSIX_C_SOURCE 200809L
#endif

#include "sngl_http.h"

#include <curl/curl.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

struct sngl_http_request {
	pthread_mutex_t lock;
	int refs;
	bool cancelled;
	char *method, *url, *body;
	size_t body_len;
	struct curl_slist *headers;
	void (*done)(sngl_http_answer *a, void *ctx);
	void *ctx;
	sngl_http_answer answer;
	size_t body_cap;
	CURL *easy;
	char why[CURL_ERROR_SIZE];
	struct sngl_http_request *next;
};

static char *sngl_http_copy(const char *s, size_t n) {
	char *out = malloc(n + 1);
	if (out != NULL) {
		memcpy(out, s, n);
		out[n] = '\0';
	}
	return out;
}

void sngl_http_answer_free(sngl_http_answer *a) {
	for (size_t i = 0; i < a->nheaders; i++) {
		free(a->names[i]);
		free(a->values[i]);
	}
	free(a->names);
	free(a->values);
	free(a->message);
	free(a->body);
	memset(a, 0, sizeof *a);
}

static void sngl_http_release(sngl_http_request *r) {
	pthread_mutex_lock(&r->lock);
	int left = --r->refs;
	pthread_mutex_unlock(&r->lock);
	if (left > 0) {
		return;
	}
	pthread_mutex_destroy(&r->lock);
	curl_slist_free_all(r->headers);
	free(r->method);
	free(r->url);
	free(r->body);
	sngl_http_answer_free(&r->answer);
	free(r);
}

static size_t sngl_http_body(char *data, size_t size, size_t n, void *p) {
	sngl_http_request *r = p;
	sngl_http_answer *a = &r->answer;
	size_t len = size * n;
	if (a->body_len + len + 1 > r->body_cap) {
		size_t cap = (a->body_len + len + 1) * 2;
		char *grown = realloc(a->body, cap);
		if (grown == NULL) {
			return 0;
		}
		a->body = grown;
		r->body_cap = cap;
	}
	memcpy(a->body + a->body_len, data, len);
	a->body_len += len;
	a->body[a->body_len] = '\0';
	return len;
}

/* Each header line of the last response, a redirect's dropped as the next
 * one's status line arrives. */
static size_t sngl_http_header(char *data, size_t size, size_t n, void *p) {
	sngl_http_request *r = p;
	sngl_http_answer *a = &r->answer;
	size_t len = size * n;
	if (len >= 5 && memcmp(data, "HTTP/", 5) == 0) {
		for (size_t i = 0; i < a->nheaders; i++) {
			free(a->names[i]);
			free(a->values[i]);
		}
		a->nheaders = 0;
		return len;
	}
	const char *colon = memchr(data, ':', len);
	if (colon == NULL) {
		return len;
	}
	size_t nn = (size_t)(colon - data);
	const char *v = colon + 1, *end = data + len;
	while (v < end && (*v == ' ' || *v == '\t')) {
		v++;
	}
	while (end > v && (end[-1] == '\r' || end[-1] == '\n' || end[-1] == ' ' || end[-1] == '\t')) {
		end--;
	}
	char *name = sngl_http_copy(data, nn);
	if (name == NULL) {
		return 0;
	}
	for (char *c = name; *c; c++) {
		if (*c >= 'A' && *c <= 'Z') {
			*c = (char)(*c - 'A' + 'a');
		}
	}
	size_t vn = (size_t)(end - v);
	for (size_t i = 0; i < a->nheaders; i++) {
		if (strcmp(a->names[i], name) != 0) {
			continue;
		}
		size_t old = strlen(a->values[i]);
		char *joined = realloc(a->values[i], old + 2 + vn + 1);
		free(name);
		if (joined == NULL) {
			return 0;
		}
		memcpy(joined + old, ", ", 2);
		memcpy(joined + old + 2, v, vn);
		joined[old + 2 + vn] = '\0';
		a->values[i] = joined;
		return len;
	}
	char **names = realloc(a->names, sizeof *names * (a->nheaders + 1));
	if (names != NULL) {
		a->names = names;
	}
	char **values = realloc(a->values, sizeof *values * (a->nheaders + 1));
	if (values != NULL) {
		a->values = values;
	}
	char *value = sngl_http_copy(v, vn);
	if (names == NULL || values == NULL || value == NULL) {
		free(name);
		free(value);
		return 0;
	}
	a->names[a->nheaders] = name;
	a->values[a->nheaders] = value;
	a->nheaders++;
	return len;
}

static int sngl_http_progress(void *p, curl_off_t dt, curl_off_t dn, curl_off_t ut, curl_off_t un) {
	(void)dt;
	(void)dn;
	(void)ut;
	(void)un;
	sngl_http_request *r = p;
	pthread_mutex_lock(&r->lock);
	bool stop = r->cancelled;
	pthread_mutex_unlock(&r->lock);
	return stop ? 1 : 0;
}

/* A redirect is followed ten deep, as Go's client follows one, and a 301,
 * 302 or 303 of a POST asks again with a GET, as it does. */
static CURL *sngl_http_easy(sngl_http_request *r) {
	CURL *curl = curl_easy_init();
	if (curl == NULL) {
		return NULL;
	}
	curl_easy_setopt(curl, CURLOPT_URL, r->url);
	curl_easy_setopt(curl, CURLOPT_USERAGENT, "sngl");
	curl_easy_setopt(curl, CURLOPT_FOLLOWLOCATION, 1L);
	curl_easy_setopt(curl, CURLOPT_MAXREDIRS, 10L);
	curl_easy_setopt(curl, CURLOPT_NOSIGNAL, 1L);
	curl_easy_setopt(curl, CURLOPT_ERRORBUFFER, r->why);
	curl_easy_setopt(curl, CURLOPT_HTTPHEADER, r->headers);
	curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, sngl_http_body);
	curl_easy_setopt(curl, CURLOPT_WRITEDATA, r);
	curl_easy_setopt(curl, CURLOPT_HEADERFUNCTION, sngl_http_header);
	curl_easy_setopt(curl, CURLOPT_HEADERDATA, r);
	curl_easy_setopt(curl, CURLOPT_NOPROGRESS, 0L);
	curl_easy_setopt(curl, CURLOPT_XFERINFOFUNCTION, sngl_http_progress);
	curl_easy_setopt(curl, CURLOPT_XFERINFODATA, r);
	curl_easy_setopt(curl, CURLOPT_PRIVATE, r);
	if (strcmp(r->method, "POST") == 0) {
		curl_easy_setopt(curl, CURLOPT_POST, 1L);
		curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE_LARGE, (curl_off_t)r->body_len);
		curl_easy_setopt(curl, CURLOPT_POSTFIELDS, r->body != NULL ? r->body : "");
	} else if (strcmp(r->method, "HEAD") == 0) {
		curl_easy_setopt(curl, CURLOPT_NOBODY, 1L);
	} else if (strcmp(r->method, "GET") != 0) {
		curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, r->method);
		if (r->body != NULL) {
			curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE_LARGE, (curl_off_t)r->body_len);
			curl_easy_setopt(curl, CURLOPT_POSTFIELDS, r->body);
		}
	}
	return curl;
}

static void sngl_http_finish(sngl_http_request *r, CURLcode res) {
	if (res == CURLE_OK) {
		long status = 0;
		curl_easy_getinfo(r->easy, CURLINFO_RESPONSE_CODE, &status);
		r->answer.status = (int)status;
	} else {
		const char *msg = r->why[0] != '\0' ? r->why : curl_easy_strerror(res);
		r->answer.failure = res == CURLE_OPERATION_TIMEDOUT ? SNGL_HTTP_TIMEOUT : res == CURLE_ABORTED_BY_CALLBACK ? SNGL_HTTP_CANCELLED : SNGL_HTTP_TRANSPORT;
		r->answer.message = sngl_http_copy(msg, strlen(msg));
	}
}

static void sngl_http_deliver(void *p) {
	sngl_http_request *r = p;
	pthread_mutex_lock(&r->lock);
	bool cancelled = r->cancelled;
	pthread_mutex_unlock(&r->lock);
	if (!cancelled) {
		r->done(&r->answer, r->ctx);
	}
	sngl_http_release(r);
}

/* The worker: what start queued is added to the multi handle, which is
 * driven until it waits on nothing, then the worker sleeps until more is
 * queued (curl_multi_wakeup). */
static pthread_mutex_t sngl_http_queue_lock = PTHREAD_MUTEX_INITIALIZER;
static sngl_http_request *sngl_http_queue;
static CURLM *sngl_http_multi;

static void *sngl_http_run(void *p) {
	(void)p;
	for (;;) {
		pthread_mutex_lock(&sngl_http_queue_lock);
		sngl_http_request *queued = sngl_http_queue;
		sngl_http_queue = NULL;
		pthread_mutex_unlock(&sngl_http_queue_lock);
		while (queued != NULL) {
			sngl_http_request *r = queued;
			queued = r->next;
			r->easy = sngl_http_easy(r);
			if (r->easy == NULL || curl_multi_add_handle(sngl_http_multi, r->easy) != CURLM_OK) {
				r->answer.failure = SNGL_HTTP_TRANSPORT;
				r->answer.message = sngl_http_copy("no curl handle", 14);
				sngl_post(sngl_http_deliver, r, NULL);
			}
		}
		int running = 0;
		curl_multi_perform(sngl_http_multi, &running);
		CURLMsg *m;
		int left;
		while ((m = curl_multi_info_read(sngl_http_multi, &left)) != NULL) {
			if (m->msg != CURLMSG_DONE) {
				continue;
			}
			sngl_http_request *r = NULL;
			curl_easy_getinfo(m->easy_handle, CURLINFO_PRIVATE, (char **)&r);
			CURLcode res = m->data.result;
			curl_multi_remove_handle(sngl_http_multi, m->easy_handle);
			sngl_http_finish(r, res);
			curl_easy_cleanup(r->easy);
			r->easy = NULL;
			sngl_post(sngl_http_deliver, r, NULL);
		}
		curl_multi_poll(sngl_http_multi, NULL, 0, 1000, NULL);
	}
	return NULL;
}

static pthread_once_t sngl_http_once = PTHREAD_ONCE_INIT;
static bool sngl_http_ready;

static void sngl_http_init(void) {
	curl_global_init(CURL_GLOBAL_DEFAULT);
	sngl_http_multi = curl_multi_init();
	if (sngl_http_multi == NULL) {
		return;
	}
	curl_multi_setopt(sngl_http_multi, CURLMOPT_PIPELINING, CURLPIPE_MULTIPLEX);
	pthread_t t;
	if (pthread_create(&t, NULL, sngl_http_run, NULL) == 0) {
		pthread_detach(t);
		sngl_http_ready = true;
	}
}

sngl_http_request *sngl_http_start(const char *method, const char *url, size_t nheaders, const char *const *names, const char *const *values, const char *body,
                                   size_t body_len, void (*done)(sngl_http_answer *a, void *ctx), void *ctx) {
	pthread_once(&sngl_http_once, sngl_http_init);
	sngl_http_request *r = calloc(1, sizeof *r);
	if (r == NULL) {
		return NULL;
	}
	pthread_mutex_init(&r->lock, NULL);
	r->refs = 2;
	r->method = sngl_http_copy(method, strlen(method));
	r->url = sngl_http_copy(url, strlen(url));
	if (body != NULL) {
		r->body = sngl_http_copy(body, body_len);
		r->body_len = body_len;
	}
	for (size_t i = 0; i < nheaders; i++) {
		size_t nn = strlen(names[i]), vn = strlen(values[i]);
		char *line = malloc(nn + 2 + vn + 1);
		if (line == NULL) {
			continue;
		}
		memcpy(line, names[i], nn);
		memcpy(line + nn, ": ", 2);
		memcpy(line + nn + 2, values[i], vn + 1);
		r->headers = curl_slist_append(r->headers, line);
		free(line);
	}
	r->done = done;
	r->ctx = ctx;
	if (!sngl_http_ready) {
		r->answer.failure = SNGL_HTTP_TRANSPORT;
		r->answer.message = sngl_http_copy("no thread to make the request on", 32);
		sngl_post(sngl_http_deliver, r, NULL);
		return r;
	}
	pthread_mutex_lock(&sngl_http_queue_lock);
	r->next = sngl_http_queue;
	sngl_http_queue = r;
	pthread_mutex_unlock(&sngl_http_queue_lock);
	curl_multi_wakeup(sngl_http_multi);
	return r;
}

/* Cancelling lets go of the caller's hold: the thread lets go of its own
 * once curl gives up. */
void sngl_http_cancel(sngl_http_request *r) {
	if (r == NULL) {
		return;
	}
	pthread_mutex_lock(&r->lock);
	r->cancelled = true;
	pthread_mutex_unlock(&r->lock);
	sngl_http_release(r);
}
