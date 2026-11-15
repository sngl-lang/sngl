/* sngl:http's requests on Win32: WinHTTP on a thread of its own, its answer
 * posted back to the loop -- this runtime's sngl_http_start (sngl_http.h),
 * in place of C's libcurl. */
#include "sngl_win32.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <winhttp.h>

/* Held by the caller until sngl_http_cancel and by the thread until its
 * answer is delivered; cancelled is read and written on the loop alone. */
typedef struct sngl_http_request fetch;
struct sngl_http_request {
	LONG refs;
	bool cancelled;
	wchar_t *method, *url, *headers;
	char *body;
	DWORD body_len;
	sngl_http_answer answer;
	void (*done)(sngl_http_answer *a, void *ctx);
	void *ctx;
};

static wchar_t *wide(const char *s, int n) {
	int w = MultiByteToWideChar(CP_UTF8, 0, s, n, NULL, 0);
	wchar_t *out = calloc((size_t)w + 1, sizeof *out);
	if (out != NULL && w > 0) {
		MultiByteToWideChar(CP_UTF8, 0, s, n, out, w);
	}
	return out;
}

static char *narrow(const wchar_t *s, int n) {
	int w = WideCharToMultiByte(CP_UTF8, 0, s, n, NULL, 0, NULL, NULL);
	char *out = calloc((size_t)w + 1, 1);
	if (out != NULL && w > 0) {
		WideCharToMultiByte(CP_UTF8, 0, s, n, out, w, NULL, NULL);
	}
	return out;
}

static char *copy(const char *s) {
	size_t n = strlen(s);
	char *out = malloc(n + 1);
	if (out != NULL) {
		memcpy(out, s, n + 1);
	}
	return out;
}

static void fail(fetch *f, DWORD err) {
	char msg[512];
	wchar_t why[256] = {0};
	DWORD n = FormatMessageW(FORMAT_MESSAGE_FROM_HMODULE | FORMAT_MESSAGE_IGNORE_INSERTS, GetModuleHandleW(L"winhttp.dll"), err, 0, why, 255, NULL);
	if (n == 0) {
		n = FormatMessageW(FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS, NULL, err, 0, why, 255, NULL);
	}
	while (n > 0 && (why[n - 1] == L'\r' || why[n - 1] == L'\n' || why[n - 1] == L'.')) {
		why[--n] = 0;
	}
	char *text = narrow(why, -1);
	snprintf(msg, sizeof msg, "%s (WinHTTP error %lu)", text != NULL && *text ? text : "request failed", (unsigned long)err);
	free(text);
	f->answer.failure = err == ERROR_WINHTTP_TIMEOUT ? SNGL_HTTP_TIMEOUT : SNGL_HTTP_TRANSPORT;
	f->answer.message = copy(msg);
}

/* Each header line of the answer after its status, named in lower case, a
 * name given twice joined with ", ", as Go's net/http hands them. */
static void read_headers(fetch *f, HINTERNET req) {
	DWORD size = 0;
	WinHttpQueryHeaders(req, WINHTTP_QUERY_RAW_HEADERS_CRLF, WINHTTP_HEADER_NAME_BY_INDEX, NULL, &size, WINHTTP_NO_HEADER_INDEX);
	if (size == 0) {
		return;
	}
	wchar_t *raw = malloc(size + sizeof(wchar_t));
	if (raw == NULL || !WinHttpQueryHeaders(req, WINHTTP_QUERY_RAW_HEADERS_CRLF, WINHTTP_HEADER_NAME_BY_INDEX, raw, &size, WINHTTP_NO_HEADER_INDEX)) {
		free(raw);
		return;
	}
	char *text = narrow(raw, (int)(size / sizeof(wchar_t)));
	free(raw);
	if (text == NULL) {
		return;
	}
	sngl_http_answer *a = &f->answer;
	char *line = strstr(text, "\r\n");
	while (line != NULL) {
		line += 2;
		char *end = strstr(line, "\r\n");
		if (end == NULL || end == line) {
			break;
		}
		*end = 0;
		char *colon = strchr(line, ':');
		if (colon != NULL) {
			*colon = 0;
			for (char *p = line; *p; p++) {
				if (*p >= 'A' && *p <= 'Z') {
					*p = (char)(*p - 'A' + 'a');
				}
			}
			char *value = colon + 1;
			while (*value == ' ' || *value == '\t') {
				value++;
			}
			size_t vn = strlen(value);
			while (vn > 0 && (value[vn - 1] == ' ' || value[vn - 1] == '\t')) {
				value[--vn] = 0;
			}
			size_t i = 0;
			while (i < a->nheaders && strcmp(a->names[i], line) != 0) {
				i++;
			}
			if (i < a->nheaders) {
				size_t old = strlen(a->values[i]);
				char *joined = malloc(old + 2 + vn + 1);
				if (joined != NULL) {
					memcpy(joined, a->values[i], old);
					memcpy(joined + old, ", ", 2);
					memcpy(joined + old + 2, value, vn + 1);
					free(a->values[i]);
					a->values[i] = joined;
				}
			} else {
				a->names = realloc(a->names, sizeof *a->names * (a->nheaders + 1));
				a->values = realloc(a->values, sizeof *a->values * (a->nheaders + 1));
				a->names[a->nheaders] = copy(line);
				a->values[a->nheaders] = copy(value);
				a->nheaders++;
			}
		}
		line = end;
	}
	free(text);
}

/* One session for the run, which keeps its connections alive between
 * requests: one a request made each a handshake of its own. */
static HINTERNET session;

static void request(fetch *f) {
	URL_COMPONENTS u;
	memset(&u, 0, sizeof u);
	u.dwStructSize = sizeof u;
	u.dwHostNameLength = (DWORD)-1;
	u.dwUrlPathLength = (DWORD)-1;
	u.dwExtraInfoLength = (DWORD)-1;
	if (!WinHttpCrackUrl(f->url, 0, 0, &u) || u.dwHostNameLength == 0) {
		fail(f, GetLastError() ? GetLastError() : ERROR_WINHTTP_INVALID_URL);
		return;
	}
	wchar_t *host = calloc(u.dwHostNameLength + 1, sizeof *host);
	wchar_t *path = calloc(u.dwUrlPathLength + u.dwExtraInfoLength + 2, sizeof *path);
	if (host == NULL || path == NULL) {
		free(host);
		free(path);
		fail(f, ERROR_NOT_ENOUGH_MEMORY);
		return;
	}
	memcpy(host, u.lpszHostName, u.dwHostNameLength * sizeof *host);
	if (u.dwUrlPathLength > 0) {
		memcpy(path, u.lpszUrlPath, u.dwUrlPathLength * sizeof *path);
	} else {
		path[0] = L'/';
	}
	if (u.dwExtraInfoLength > 0) {
		wcsncat(path, u.lpszExtraInfo, u.dwExtraInfoLength);
	}
	HINTERNET conn = session ? WinHttpConnect(session, host, u.nPort, 0) : NULL;
	HINTERNET req = conn ? WinHttpOpenRequest(conn, f->method, path, NULL, WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES, u.nScheme == INTERNET_SCHEME_HTTPS ? WINHTTP_FLAG_SECURE : 0) : NULL;
	free(host);
	free(path);
	if (req == NULL) {
		fail(f, GetLastError());
	} else if (!WinHttpSendRequest(req, f->headers && *f->headers ? f->headers : WINHTTP_NO_ADDITIONAL_HEADERS, f->headers && *f->headers ? (DWORD)-1L : 0,
	                               f->body_len ? f->body : WINHTTP_NO_REQUEST_DATA, f->body_len, f->body_len, 0) ||
	           !WinHttpReceiveResponse(req, NULL)) {
		fail(f, GetLastError());
	} else {
		DWORD status = 0, size = sizeof status;
		WinHttpQueryHeaders(req, WINHTTP_QUERY_STATUS_CODE | WINHTTP_QUERY_FLAG_NUMBER, WINHTTP_HEADER_NAME_BY_INDEX, &status, &size, WINHTTP_NO_HEADER_INDEX);
		f->answer.status = (int)status;
		read_headers(f, req);
		size_t len = 0, cap = 0;
		char *body = NULL;
		for (;;) {
			DWORD avail = 0, got = 0;
			if (!WinHttpQueryDataAvailable(req, &avail)) {
				fail(f, GetLastError());
				break;
			}
			if (avail == 0) {
				break;
			}
			if (len + avail + 1 > cap) {
				cap = (len + avail + 1) * 2;
				char *grown = realloc(body, cap);
				if (grown == NULL) {
					fail(f, ERROR_NOT_ENOUGH_MEMORY);
					break;
				}
				body = grown;
			}
			if (!WinHttpReadData(req, body + len, avail, &got)) {
				fail(f, GetLastError());
				break;
			}
			if (got == 0) {
				break;
			}
			len += got;
		}
		f->answer.body = body;
		f->answer.body_len = len;
	}
	if (req) {
		WinHttpCloseHandle(req);
	}
	if (conn) {
		WinHttpCloseHandle(conn);
	}
}

static void release(fetch *f) {
	if (InterlockedDecrement(&f->refs) != 0) {
		return;
	}
	free(f->method);
	free(f->url);
	free(f->headers);
	free(f->body);
	sngl_http_answer_free(&f->answer);
	free(f);
}

static void deliver(void *p) {
	fetch *f = p;
	if (!f->cancelled) {
		f->done(&f->answer, f->ctx);
	}
	release(f);
}

static DWORD WINAPI run(LPVOID p) {
	fetch *f = p;
	request(f);
	sngl_post(deliver, f, NULL);
	return 0;
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

sngl_http_request *sngl_http_start(const char *method, const char *url, size_t nheaders, const char *const *names, const char *const *values, const char *body,
                                   size_t body_len, void (*done)(sngl_http_answer *a, void *ctx), void *ctx) {
	sngl_win32_init();
	if (session == NULL) {
		session = WinHttpOpen(L"sngl", WINHTTP_ACCESS_TYPE_DEFAULT_PROXY, WINHTTP_NO_PROXY_NAME, WINHTTP_NO_PROXY_BYPASS, 0);
	}
	fetch *f = calloc(1, sizeof *f);
	if (f == NULL) {
		return NULL;
	}
	f->refs = 2;
	f->done = done;
	f->ctx = ctx;
	f->method = wide(method, -1);
	f->url = wide(url, -1);
	size_t hn = 0;
	for (size_t i = 0; i < nheaders; i++) {
		hn += strlen(names[i]) + strlen(values[i]) + 4;
	}
	char *lines = calloc(hn + 1, 1);
	for (size_t i = 0; lines != NULL && i < nheaders; i++) {
		strcat(lines, names[i]);
		strcat(lines, ": ");
		strcat(lines, values[i]);
		strcat(lines, "\r\n");
	}
	f->headers = lines != NULL ? wide(lines, -1) : NULL;
	free(lines);
	if (body != NULL && body_len > 0) {
		f->body = malloc(body_len);
		if (f->body != NULL) {
			memcpy(f->body, body, body_len);
			f->body_len = (DWORD)body_len;
		}
	}
	HANDLE t = CreateThread(NULL, 0, run, f, 0, NULL);
	if (t == NULL) {
		f->answer.failure = SNGL_HTTP_TRANSPORT;
		f->answer.message = copy("no thread to make the request on");
		sngl_post(deliver, f, NULL);
		return f;
	}
	CloseHandle(t);
	return f;
}

void sngl_http_cancel(sngl_http_request *r) {
	if (r == NULL) {
		return;
	}
	r->cancelled = true;
	release(r);
}
