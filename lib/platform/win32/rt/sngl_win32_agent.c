/* sngl_win32_agent.c is the channel a win32 program's test agent talks to the
 * driver over: a TCP connection to the port its one argument names, on the
 * host's loopback, which is Wine's own. Written beside the program only in
 * a build for `sngl test`, which links ws2_32 for it. */
#include <winsock2.h>

#include "sngl_test.h"
#include "sngl_win32.h"
#include "sngl_win32_agent.h"

#include <stdlib.h>
#include <string.h>

typedef struct {
	SOCKET s;
	char buf[1 << 16];
	size_t len;
} conn;

/* conn_line reads the next line into out, false once the driver is gone. */
static bool conn_line(char *out, size_t n, void *ctx) {
	conn *c = ctx;
	for (;;) {
		char *nl = memchr(c->buf, '\n', c->len);
		if (nl != NULL) {
			size_t take = (size_t)(nl - c->buf) + 1;
			size_t keep = take < n ? take : n - 1;
			memcpy(out, c->buf, keep);
			out[keep] = '\0';
			memmove(c->buf, c->buf + take, c->len - take);
			c->len -= take;
			return true;
		}
		if (c->len == sizeof c->buf) {
			/* A line longer than any request the driver makes. */
			return false;
		}
		int got = recv(c->s, c->buf + c->len, (int)(sizeof c->buf - c->len), 0);
		if (got <= 0) {
			return false;
		}
		c->len += (size_t)got;
	}
}

static void conn_write(const char *s, size_t n, void *ctx) {
	conn *c = ctx;
	while (n > 0) {
		int sent = send(c->s, s, (int)n, 0);
		if (sent <= 0) {
			return;
		}
		s += sent;
		n -= (size_t)sent;
	}
}

int sngl_win32_serve(const sngl_test *tests, size_t n, int argc, char **argv) {
	if (argc < 2) {
		return 2;
	}
	WSADATA wsa;
	if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) {
		return 1;
	}
	static conn c;
	c.s = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
	struct sockaddr_in addr;
	memset(&addr, 0, sizeof addr);
	addr.sin_family = AF_INET;
	addr.sin_port = htons((unsigned short)atoi(argv[1]));
	addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
	if (c.s == INVALID_SOCKET || connect(c.s, (struct sockaddr *)&addr, sizeof addr) != 0) {
		WSACleanup();
		return 1;
	}
	sngl_win32_init();
	sngl_test_io io = {conn_line, conn_write, &c};
	int status = sngl_test_serve(tests, n, &io);
	closesocket(c.s);
	WSACleanup();
	return status;
}
