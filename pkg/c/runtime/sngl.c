#include "sngl.h"

#include <inttypes.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* The arena is a list of chunks, each a bump allocation. A reset keeps the
 * first, so a turn that makes a few strings allocates nothing. */
typedef struct sngl_chunk {
	struct sngl_chunk *next;
	size_t size, used;
	max_align_t data[];
} sngl_chunk;

enum { sngl_chunk_size = 64 * 1024 };

static sngl_chunk *sngl_arena;

void *sngl_alloc(size_t n) {
	size_t align = sizeof(max_align_t);
	n = (n + align - 1) / align * align;
	if (sngl_arena == NULL || sngl_arena->size - sngl_arena->used < n) {
		size_t size = n > sngl_chunk_size ? n : sngl_chunk_size;
		sngl_chunk *c = malloc(sizeof(sngl_chunk) + size);
		if (c == NULL) {
			sngl_panic("out of memory");
		}
		c->next = sngl_arena;
		c->size = size;
		c->used = 0;
		sngl_arena = c;
	}
	void *p = (char *)sngl_arena->data + sngl_arena->used;
	sngl_arena->used += n;
	return p;
}

void sngl_turn_end(void) {
	while (sngl_arena != NULL && sngl_arena->next != NULL) {
		sngl_chunk *next = sngl_arena->next;
		free(sngl_arena);
		sngl_arena = next;
	}
	if (sngl_arena != NULL) {
		sngl_arena->used = 0;
	}
}

_Noreturn void sngl_panic(const char *msg) {
	fprintf(stderr, "panic: %s\n", msg);
	fflush(stderr);
	abort();
}

/* sngl_bytes is a string of the n bytes at p, copied into the arena. */
static sngl_string sngl_bytes(const char *p, size_t n) {
	char *b = sngl_alloc(n + 1);
	memcpy(b, p, n);
	b[n] = '\0';
	return (sngl_string){b, n};
}

sngl_string sngl_concat(sngl_string a, sngl_string b) {
	if (a.len == 0) {
		return b;
	}
	if (b.len == 0) {
		return a;
	}
	char *p = sngl_alloc(a.len + b.len + 1);
	memcpy(p, a.ptr, a.len);
	memcpy(p + a.len, b.ptr, b.len);
	p[a.len + b.len] = '\0';
	return (sngl_string){p, a.len + b.len};
}

bool sngl_string_eq(sngl_string a, sngl_string b) {
	return a.len == b.len && (a.len == 0 || memcmp(a.ptr, b.ptr, a.len) == 0);
}

/* Bytewise, as Go and the interpreter compare. */
int sngl_string_cmp(sngl_string a, sngl_string b) {
	size_t n = a.len < b.len ? a.len : b.len;
	int c = n == 0 ? 0 : memcmp(a.ptr, b.ptr, n);
	if (c != 0) {
		return c < 0 ? -1 : 1;
	}
	return a.len < b.len ? -1 : a.len > b.len;
}

sngl_string sngl_int_format(int64_t v) {
	char buf[32];
	int n = snprintf(buf, sizeof buf, "%" PRId64, v);
	return sngl_bytes(buf, (size_t)n);
}

/* The shortest digits that read back as v, written as Go's strconv writes
 * them with 'g' and no precision, which the interpreter and every Go target
 * print: an exponent below -4 or from 6 up is written as one. */
sngl_string sngl_float_format(double v) {
	if (isnan(v)) {
		return SNGL_STR("NaN");
	}
	if (isinf(v)) {
		return v > 0 ? SNGL_STR("+Inf") : SNGL_STR("-Inf");
	}
	char buf[64];
	int digits = 1;
	for (; digits < 17; digits++) {
		snprintf(buf, sizeof buf, "%.*e", digits - 1, v);
		if (strtod(buf, NULL) == v) {
			break;
		}
	}
	snprintf(buf, sizeof buf, "%.*e", digits - 1, v);
	int exp = atoi(strchr(buf, 'e') + 1);
	int n;
	if (exp < -4 || exp >= 6) {
		n = (int)strlen(buf);
	} else {
		int places = digits - 1 - exp;
		n = snprintf(buf, sizeof buf, "%.*f", places > 0 ? places : 0, v);
	}
	return sngl_bytes(buf, (size_t)n);
}

sngl_string sngl_bool_format(bool v) {
	return v ? SNGL_STR("true") : SNGL_STR("false");
}

int64_t sngl_div(int64_t a, int64_t b) {
	if (b == 0) {
		sngl_panic("division by zero");
	}
	if (b == -1) {
		return (int64_t)(0 - (uint64_t)a);
	}
	return a / b;
}

int64_t sngl_mod(int64_t a, int64_t b) {
	if (b == 0) {
		sngl_panic("division by zero");
	}
	if (b == -1) {
		return 0;
	}
	return a % b;
}
