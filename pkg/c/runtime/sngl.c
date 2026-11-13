#include "sngl.h"

#include <inttypes.h>
#include <locale.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* Under AddressSanitizer the arena tells it what is live: a chunk is
 * poisoned but for what sngl_alloc has handed out, so a value read after its
 * turn gave it back is reported where it is read, as a freed block would be. */
#if defined(__SANITIZE_ADDRESS__)
#define SNGL_ASAN 1
#elif defined(__has_feature)
#if __has_feature(address_sanitizer)
#define SNGL_ASAN 1
#endif
#endif
#ifdef SNGL_ASAN
#include <sanitizer/asan_interface.h>
#define sngl_poison(p, n) ASAN_POISON_MEMORY_REGION((p), (n))
#define sngl_unpoison(p, n) ASAN_UNPOISON_MEMORY_REGION((p), (n))
#else
#define sngl_poison(p, n) ((void)(p), (void)(n))
#define sngl_unpoison(p, n) ((void)(p), (void)(n))
#endif

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
		sngl_poison(c->data, size);
		sngl_arena = c;
	}
	void *p = (char *)sngl_arena->data + sngl_arena->used;
	sngl_arena->used += n;
	sngl_unpoison(p, n);
	return p;
}

/* The kept blocks, an open-addressed set of their addresses, and those
 * dropped this turn, freed as it ends. live counts the blocks in the set,
 * used the slots a probe cannot stop at: the blocks and the tombs. */
static uintptr_t *sngl_kept;
static size_t sngl_kept_cap, sngl_kept_live, sngl_kept_used;
static void **sngl_dropped;
static size_t sngl_dropped_len, sngl_dropped_cap;
static int sngl_depth;

/* A slot whose block was taken out, which a probe goes past. */
#define SNGL_TOMB ((uintptr_t)1)

static size_t sngl_slot(uintptr_t p) {
	uintptr_t h = p;
	h ^= h >> 17;
	h *= UINT64_C(0xed5ad4bb);
	return (size_t)h & (sngl_kept_cap - 1);
}

/* sngl_insert puts p in a set with room for it. */
static void sngl_insert(uintptr_t p) {
	size_t i = sngl_slot(p);
	while (sngl_kept[i] != 0 && sngl_kept[i] != SNGL_TOMB) {
		i = (i + 1) & (sngl_kept_cap - 1);
	}
	if (sngl_kept[i] == 0) {
		sngl_kept_used++;
	}
	sngl_kept[i] = p;
	sngl_kept_live++;
}

static void sngl_track(uintptr_t p) {
	if ((sngl_kept_used + 1) * 2 > sngl_kept_cap) {
		/* Rebuilt without its tombs, and twice the size only where the
		 * blocks themselves fill a quarter of it: a set whose blocks are
		 * dropped as fast as kept stays the size it is. */
		uintptr_t *old = sngl_kept;
		size_t old_cap = sngl_kept_cap;
		sngl_kept_cap = old_cap == 0 ? 64 : (sngl_kept_live + 1) * 4 > old_cap ? old_cap * 2 : old_cap;
		sngl_kept = calloc(sngl_kept_cap, sizeof *sngl_kept);
		if (sngl_kept == NULL) {
			sngl_panic("out of memory");
		}
		sngl_kept_live = 0;
		sngl_kept_used = 0;
		for (size_t i = 0; i < old_cap; i++) {
			if (old[i] != 0 && old[i] != SNGL_TOMB) {
				sngl_insert(old[i]);
			}
		}
		free(old);
	}
	sngl_insert(p);
}

/* sngl_untrack takes p out of the kept set, and is whether it was there. */
static bool sngl_untrack(uintptr_t p) {
	if (sngl_kept_cap == 0 || p == 0) {
		return false;
	}
	for (size_t i = sngl_slot(p); sngl_kept[i] != 0; i = (i + 1) & (sngl_kept_cap - 1)) {
		if (sngl_kept[i] == p) {
			sngl_kept[i] = SNGL_TOMB;
			sngl_kept_live--;
			return true;
		}
	}
	return false;
}

void *sngl_keep_alloc(size_t n) {
	void *p = malloc(n == 0 ? 1 : n);
	if (p == NULL) {
		sngl_panic("out of memory");
	}
	sngl_track((uintptr_t)p);
	return p;
}

void *sngl_keep_copy(const void *p, size_t n) {
	void *q = sngl_keep_alloc(n);
	memcpy(q, p, n);
	return q;
}


int64_t sngl_kept_blocks(void) {
	return (int64_t)sngl_kept_live;
}

sngl_string sngl_string_keep(sngl_string s) {
	if (s.len == 0) {
		return SNGL_STR("");
	}
	char *p = sngl_keep_alloc(s.len + 1);
	memcpy(p, s.ptr, s.len);
	p[s.len] = '\0';
	return (sngl_string){p, s.len};
}

void sngl_drop(const void *p) {
	if (!sngl_untrack((uintptr_t)p)) {
		return;
	}
	if (sngl_dropped_len == sngl_dropped_cap) {
		size_t cap = sngl_dropped_cap == 0 ? 16 : sngl_dropped_cap * 2;
		void **grown = realloc(sngl_dropped, cap * sizeof *grown);
		if (grown == NULL) {
			sngl_panic("out of memory");
		}
		sngl_dropped = grown;
		sngl_dropped_cap = cap;
	}
	sngl_dropped[sngl_dropped_len++] = (void *)p;
}

sngl_mark sngl_call_enter(void) {
	sngl_mark m = {sngl_arena, sngl_arena == NULL ? 0 : sngl_arena->used};
	sngl_turn_enter();
	return m;
}

void sngl_call_leave(sngl_mark m) {
	if (sngl_depth > 1) {
		while (sngl_arena != NULL && (void *)sngl_arena != m.chunk) {
			sngl_chunk *next = sngl_arena->next;
			sngl_unpoison(sngl_arena->data, sngl_arena->size);
			free(sngl_arena);
			sngl_arena = next;
		}
		if (sngl_arena != NULL) {
			memset((char *)sngl_arena->data + m.used, 0x5a, sngl_arena->used - m.used);
			sngl_poison((char *)sngl_arena->data + m.used, sngl_arena->used - m.used);
			sngl_arena->used = m.used;
		}
	}
	sngl_turn_leave();
}

void sngl_turn_enter(void) {
	sngl_depth++;
}

void sngl_turn_leave(void) {
	if (sngl_depth > 0 && --sngl_depth == 0) {
		sngl_turn_end();
	}
}

/* A counted block's count, how it gives back what it holds, keeps it again
 * and visits it, whether it was given back, whether it is to be tried as a
 * cycle's, and its color in the try.
 * It is ahead of what it holds. */
typedef union sngl_counted {
	struct {
		int64_t count;
		void (*release)(void *);
		void (*rekeep)(void *);
		void (*visit)(void *, sngl_visit);
		bool gone;
		bool tried;
		unsigned char color;
	} h;
	max_align_t align;
} sngl_counted;

enum { SNGL_BLACK, SNGL_GRAY, SNGL_WHITE };

typedef struct sngl_blocks {
	sngl_counted **at;
	size_t len, cap;
} sngl_blocks;

static void sngl_push(sngl_blocks *l, sngl_counted *c) {
	if (l->len == l->cap) {
		size_t cap = l->cap == 0 ? 16 : l->cap * 2;
		sngl_counted **grown = realloc(l->at, cap * sizeof *grown);
		if (grown == NULL) {
			sngl_panic("out of memory");
		}
		l->at = grown;
		l->cap = cap;
	}
	l->at[l->len++] = c;
}

/* The counted blocks no holder has taken, which the turn's end gives back
 * if none has by then; those a holder let go of and another still holds,
 * which may be held only by a cycle; and those the try found are. */
static sngl_blocks sngl_pending, sngl_tried, sngl_garbage;

/* sngl_undrop takes p, dropped this turn, back: kept, and not freed as the
 * turn ends. */
static void sngl_undrop(void *p) {
	for (size_t i = 0; i < sngl_dropped_len; i++) {
		if (sngl_dropped[i] == p) {
			sngl_dropped[i] = sngl_dropped[--sngl_dropped_len];
			sngl_track((uintptr_t)p);
			return;
		}
	}
}

/* sngl_give_back gives back c and what it holds, freed as the turn ends. */
static void sngl_give_back(sngl_counted *c) {
	c->h.gone = true;
	c->h.release(c + 1);
	sngl_drop(c);
}

void *sngl_counted_new(const void *p, size_t n, void (*release)(void *), void (*rekeep)(void *), void (*visit)(void *, sngl_visit)) {
	sngl_counted *c = sngl_keep_alloc(sizeof(sngl_counted) + n);
	c->h.count = 0;
	c->h.release = release;
	c->h.rekeep = rekeep;
	c->h.visit = visit;
	c->h.gone = false;
	c->h.tried = false;
	c->h.color = SNGL_BLACK;
	memcpy(c + 1, p, n);
	sngl_push(&sngl_pending, c);
	return c + 1;
}

void *sngl_counted_retain(void *p) {
	if (p == NULL) {
		return p;
	}
	sngl_counted *c = (sngl_counted *)p - 1;
	if (c->h.gone) {
		/* Let go of by its last holder this turn, and taken again: what
		 * it held is still there to keep anew. */
		sngl_undrop(c);
		c->h.rekeep(p);
		c->h.gone = false;
	}
	c->h.count++;
	return p;
}

void sngl_counted_release(void *p) {
	if (p == NULL) {
		return;
	}
	sngl_counted *c = (sngl_counted *)p - 1;
	if (--c->h.count <= 0) {
		if (!c->h.gone) {
			sngl_give_back(c);
		}
	} else if (!c->h.gone && !c->h.tried && c->h.visit != NULL) {
		c->h.tried = true;
		sngl_push(&sngl_tried, c);
	}
}

/* Cycles are collected by trial deletion (Bacon and Rajan's synchronous
 * collector), at the turn's end, when the only holds left are holders',
 * each of them counted. From each block tried, every hold one block reached
 * has on another is taken off its count (gray); a block with a count left
 * is held from outside them, so it and all it reaches have their holds put
 * back (black), and those left at none (white) are garbage. Each walk
 * recurses as deep as the chain of blocks it follows. */
static sngl_counted *sngl_head(void *p) {
	return (sngl_counted *)p - 1;
}

/* sngl_visit_in hands fn each counted block c holds; one holding none has
 * no visit, and is never tried, since it closes no cycle. */
static void sngl_visit_in(sngl_counted *c, sngl_visit fn) {
	if (c->h.visit != NULL) {
		c->h.visit(c + 1, fn);
	}
}

static void sngl_mark_gray(sngl_counted *c);

static void sngl_gray_child(void *p) {
	sngl_counted *c = sngl_head(p);
	c->h.count--;
	sngl_mark_gray(c);
}

static void sngl_mark_gray(sngl_counted *c) {
	if (c->h.color != SNGL_GRAY) {
		c->h.color = SNGL_GRAY;
		sngl_visit_in(c, sngl_gray_child);
	}
}

static void sngl_scan_black(sngl_counted *c);

static void sngl_black_child(void *p) {
	sngl_counted *c = sngl_head(p);
	c->h.count++;
	if (c->h.color != SNGL_BLACK) {
		sngl_scan_black(c);
	}
}

static void sngl_scan_black(sngl_counted *c) {
	c->h.color = SNGL_BLACK;
	sngl_visit_in(c, sngl_black_child);
}

static void sngl_scan(sngl_counted *c);

static void sngl_scan_child(void *p) {
	sngl_scan(sngl_head(p));
}

static void sngl_scan(sngl_counted *c) {
	if (c->h.color != SNGL_GRAY) {
		return;
	}
	if (c->h.count > 0) {
		sngl_scan_black(c);
		return;
	}
	c->h.color = SNGL_WHITE;
	sngl_visit_in(c, sngl_scan_child);
}

static void sngl_gather(sngl_counted *c);

static void sngl_gather_child(void *p) {
	sngl_gather(sngl_head(p));
}

/* sngl_gather finds the garbage c reaches, marked gone, so letting go of
 * one of them gives nothing back. */
static void sngl_gather(sngl_counted *c) {
	if (c->h.color != SNGL_WHITE) {
		return;
	}
	c->h.color = SNGL_BLACK;
	c->h.gone = true;
	sngl_push(&sngl_garbage, c);
	sngl_visit_in(c, sngl_gather_child);
}

static void sngl_restore_child(void *p) {
	sngl_head(p)->h.count++;
}

static void sngl_collect_cycles(void) {
	/* A release giving the garbage back may leave another block still
	 * held, which the next turn's end tries. */
	sngl_blocks tried = sngl_tried;
	sngl_tried = (sngl_blocks){0};
	size_t n = 0;
	for (size_t i = 0; i < tried.len; i++) {
		sngl_counted *c = tried.at[i];
		c->h.tried = false;
		if (!c->h.gone && c->h.count > 0) {
			tried.at[n++] = c;
			sngl_mark_gray(c);
		}
	}
	for (size_t i = 0; i < n; i++) {
		sngl_scan(tried.at[i]);
	}
	for (size_t i = 0; i < n; i++) {
		sngl_gather(tried.at[i]);
	}
	free(tried.at);
	/* What the garbage holds is counted again, so giving each back lets go
	 * of each hold once: of another's, which is gone, and of a block held
	 * from outside it too, whose count is then right. */
	for (size_t i = 0; i < sngl_garbage.len; i++) {
		sngl_counted *c = sngl_garbage.at[i];
		sngl_visit_in(c, sngl_restore_child);
	}
	for (size_t i = 0; i < sngl_garbage.len; i++) {
		sngl_counted *c = sngl_garbage.at[i];
		c->h.release(c + 1);
		sngl_drop(c);
	}
	sngl_garbage.len = 0;
}

void sngl_turn_end(void) {
	sngl_depth = 0;
	/* What the turn made and no holder took is the turn's. Giving one back
	 * gives back what it holds, which may be another's last hold. */
	while (sngl_pending.len > 0) {
		sngl_counted *c = sngl_pending.at[--sngl_pending.len];
		if (c->h.count <= 0 && !c->h.gone) {
			sngl_give_back(c);
		}
	}
	sngl_collect_cycles();
	for (size_t i = 0; i < sngl_dropped_len; i++) {
		free(sngl_dropped[i]);
	}
	sngl_dropped_len = 0;
	while (sngl_arena != NULL && sngl_arena->next != NULL) {
		sngl_chunk *next = sngl_arena->next;
		sngl_unpoison(sngl_arena->data, sngl_arena->size);
		free(sngl_arena);
		sngl_arena = next;
	}
	if (sngl_arena != NULL) {
		/* What the turn made is overwritten, so a value a holder read
		 * without keeping it reads as the garbage it is rather than as
		 * what it was, until the arena is next written. */
		memset(sngl_arena->data, 0x5a, sngl_arena->used);
		sngl_poison(sngl_arena->data, sngl_arena->used);
		sngl_arena->used = 0;
	}
}

void *sngl_heap_copy(const void *p, size_t n) {
	void *q = malloc(n == 0 ? 1 : n);
	if (q == NULL) {
		sngl_panic("out of memory");
	}
	memcpy(q, p, n);
	return q;
}

void *sngl_alloc_copy(const void *p, size_t n) {
	void *q = sngl_alloc(n);
	memcpy(q, p, n);
	return q;
}

/* sngl_tracked reports whether p is a kept block. */
static bool sngl_tracked(uintptr_t p) {
	if (sngl_kept_cap == 0 || p == 0) {
		return false;
	}
	for (size_t i = sngl_slot(p); sngl_kept[i] != 0; i = (i + 1) & (sngl_kept_cap - 1)) {
		if (sngl_kept[i] == p) {
			return true;
		}
	}
	return false;
}

void *sngl_env_keep(const void *env) {
	return env == NULL ? NULL : ((const sngl_env *)env)->keep(env);
}

void sngl_env_drop(void *env) {
	/* One the turn made kept nothing: what it captured is the turn's, or
	 * held by whatever it was read from. */
	if (env != NULL && sngl_tracked((uintptr_t)env)) {
		((sngl_env *)env)->drop(env);
	}
}

void sngl_env_visit(const void *env, sngl_visit fn) {
	if (env != NULL && sngl_tracked((uintptr_t)env)) {
		((const sngl_env *)env)->visit(env, fn);
	}
}

void sngl_closure_release(void *ctx) {
	struct {
		void (*fn)(void);
		void *env;
	} *f = ctx;
	sngl_env_drop(f->env);
	free(ctx);
}

/* sngl_utf8_next is the code point at s[*i], moving *i past it. */
static uint32_t sngl_utf8_next(sngl_string s, size_t *i) {
	/* As Go's utf8.DecodeRune: a byte no valid sequence starts, or that
	 * starts one the string ends inside or a byte breaks, is U+FFFD and
	 * one byte wide. lo and hi bound the second byte, which excludes the
	 * overlong forms and the surrogates. */
	const unsigned char *p = (const unsigned char *)s.ptr + *i;
	size_t left = s.len - *i;
	unsigned char c = p[0];
	if (c < 0x80) {
		(*i)++;
		return c;
	}
	int n = 0;
	unsigned char lo = 0x80, hi = 0xbf;
	uint32_t r = 0;
	if (c >= 0xc2 && c <= 0xdf) {
		n = 1, r = c & 0x1f;
	} else if (c >= 0xe0 && c <= 0xef) {
		n = 2, r = c & 0x0f;
		lo = c == 0xe0 ? 0xa0 : 0x80;
		hi = c == 0xed ? 0x9f : 0xbf;
	} else if (c >= 0xf0 && c <= 0xf4) {
		n = 3, r = c & 0x07;
		lo = c == 0xf0 ? 0x90 : 0x80;
		hi = c == 0xf4 ? 0x8f : 0xbf;
	}
	if (n == 0 || left < (size_t)n + 1 || p[1] < lo || p[1] > hi) {
		(*i)++;
		return 0xfffd;
	}
	for (int k = 1; k <= n; k++) {
		if (k > 1 && (p[k] < 0x80 || p[k] > 0xbf)) {
			(*i)++;
			return 0xfffd;
		}
		r = r << 6 | (p[k] & 0x3f);
	}
	*i += (size_t)n + 1;
	return r;
}

const unsigned short *sngl_utf16(sngl_string s) {
	/* No code point takes more units than its UTF-8 takes bytes. */
	unsigned short *out = sngl_alloc((s.len + 1) * sizeof *out);
	size_t n = 0;
	for (size_t i = 0; i < s.len;) {
		uint32_t c = sngl_utf8_next(s, &i);
		if (c >= 0x10000) {
			c -= 0x10000;
			out[n++] = (unsigned short)(0xd800 + (c >> 10));
			out[n++] = (unsigned short)(0xdc00 + (c & 0x3ff));
		} else {
			out[n++] = (unsigned short)c;
		}
	}
	out[n] = 0;
	return out;
}

char *sngl_cstring(sngl_string s) {
	char *c = malloc(s.len + 1);
	if (c == NULL) {
		sngl_panic("out of memory");
	}
	/* A string's zero has no bytes, and a NULL memcpy may not read. */
	if (s.len > 0) {
		memcpy(c, s.ptr, s.len);
	}
	c[s.len] = '\0';
	return c;
}

sngl_string sngl_string_copy(const char *s) {
	size_t n = s == NULL ? 0 : strlen(s);
	char *p = sngl_alloc(n + 1);
	if (n > 0) {
		memcpy(p, s, n);
	}
	p[n] = '\0';
	return (sngl_string){p, n};
}

void (*sngl_panic_hook)(const char *msg);

_Noreturn void sngl_panic(const char *msg) {
	if (sngl_panic_hook != NULL) {
		sngl_panic_hook(msg);
	}
	fprintf(stderr, "panic: %s\n", msg);
	fflush(stderr);
	abort();
}

sngl_catch *sngl_catching;
struct sngl_raised sngl_raised;

_Noreturn void sngl_raise(sngl_string message, sngl_string kind) {
	sngl_catch *c = sngl_catching;
	if (c == NULL) {
		sngl_panic(message.ptr);
	}
	sngl_catching = c->up;
	sngl_raised.message = message;
	sngl_raised.kind = kind;
	longjmp(c->at, 1);
}

sngl_string sngl_string_bytes(const char *p, size_t n) {
	if (n == 0) {
		return SNGL_STR("");
	}
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
	return sngl_string_bytes(buf, (size_t)n);
}

/* The point the C library writes and reads in a number under the program's
 * locale, which a string SNGL makes or reads has as a dot. */
static char sngl_point(void) {
	const char *p = localeconv()->decimal_point;
	return p != NULL && p[0] != '\0' && p[1] == '\0' ? p[0] : '.';
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
	char *p = strchr(buf, sngl_point());
	if (p != NULL) {
		*p = '.';
	}
	return sngl_string_bytes(buf, (size_t)n);
}

/* sngl_number is s, a number written with a dot, as the C library reads it
 * under the program's locale -- GTK sets the locale from the environment,
 * and a German one reads a comma -- or NULL where s is not one: empty,
 * beginning with a space the library would skip, or holding the locale's own
 * point. The program frees it. */
static char *sngl_number(sngl_string s) {
	char point = sngl_point();
	if (s.len == 0 || s.ptr[0] == ' ' || (s.ptr[0] >= '\t' && s.ptr[0] <= '\r') ||
	    (point != '.' && memchr(s.ptr, point, s.len) != NULL)) {
		return NULL;
	}
	char *c = sngl_cstring(s);
	char *dot = strchr(c, '.');
	if (dot != NULL) {
		*dot = point;
	}
	return c;
}

double sngl_float_of(sngl_string s) {
	char *c = sngl_number(s);
	if (c == NULL) {
		return 0;
	}
	char *end;
	double v = strtod(c, &end);
	bool whole = *end == '\0';
	free(c);
	return whole ? v : 0;
}

int64_t sngl_int_of(sngl_string s) {
	char *c = sngl_number(s);
	if (c == NULL || strchr(c, sngl_point()) != NULL) {
		free(c);
		return 0;
	}
	char *end;
	/* Out of range is the nearer end, as strconv.Atoi answers it. */
	long long v = strtoll(c, &end, 10);
	bool whole = *end == '\0';
	free(c);
	return whole ? (int64_t)v : 0;
}

sngl_string sngl_color_hex(uint8_t r, uint8_t g, uint8_t b, uint8_t a) {
	char buf[10];
	int n = a == 255 ? snprintf(buf, sizeof buf, "#%02x%02x%02x", r, g, b)
	                 : snprintf(buf, sizeof buf, "#%02x%02x%02x%02x", r, g, b, a);
	return sngl_string_bytes(buf, (size_t)n);
}

int64_t sngl_float_int(double v) {
	if (v != v) {
		return 0;
	}
	/* 2^63, which a double holds exactly, where INT64_MAX it rounds up to. */
	if (v >= 9223372036854775808.0) {
		return INT64_MAX;
	}
	if (v <= -9223372036854775808.0) {
		return INT64_MIN;
	}
	return (int64_t)v;
}

sngl_string sngl_unit_term(double mag, sngl_string base) {
	if (mag == (double)sngl_float_int(mag)) {
		return sngl_concat(sngl_int_format(sngl_float_int(mag)), base);
	}
	return sngl_concat(sngl_float_format(mag), base);
}

sngl_string sngl_bool_format(bool v) {
	return v ? SNGL_STR("true") : SNGL_STR("false");
}

/* Each lifetime begun, by its number less one: whether it has ended. */
static bool *sngl_lifetimes;
static int64_t sngl_lifetimes_len, sngl_lifetimes_cap;

int64_t sngl_async_begin(void) {
	if (sngl_lifetimes_len == sngl_lifetimes_cap) {
		int64_t cap = sngl_lifetimes_cap < 16 ? 16 : sngl_lifetimes_cap * 2;
		bool *grown = realloc(sngl_lifetimes, sizeof(bool) * (size_t)cap);
		if (grown == NULL) {
			sngl_panic("out of memory");
		}
		sngl_lifetimes = grown;
		sngl_lifetimes_cap = cap;
	}
	sngl_lifetimes[sngl_lifetimes_len++] = false;
	return sngl_lifetimes_len;
}

void sngl_async_end(int64_t id) {
	if (id > 0 && id <= sngl_lifetimes_len) {
		sngl_lifetimes[id - 1] = true;
	}
}

bool sngl_async_ended(int64_t id) {
	return id > 0 && id <= sngl_lifetimes_len && sngl_lifetimes[id - 1];
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

/* sngl_encode writes r into buf as UTF-8, and is how many bytes it took. */
static size_t sngl_encode(char *buf, uint32_t r) {
	if (r < 0x80) {
		buf[0] = (char)r;
		return 1;
	}
	if (r < 0x800) {
		buf[0] = (char)(0xc0 | r >> 6);
		buf[1] = (char)(0x80 | (r & 0x3f));
		return 2;
	}
	if (r < 0x10000) {
		buf[0] = (char)(0xe0 | r >> 12);
		buf[1] = (char)(0x80 | (r >> 6 & 0x3f));
		buf[2] = (char)(0x80 | (r & 0x3f));
		return 3;
	}
	buf[0] = (char)(0xf0 | r >> 18);
	buf[1] = (char)(0x80 | (r >> 12 & 0x3f));
	buf[2] = (char)(0x80 | (r >> 6 & 0x3f));
	buf[3] = (char)(0x80 | (r & 0x3f));
	return 4;
}

int64_t sngl_string_length(sngl_string s) {
	int64_t n = 0;
	for (size_t i = 0; i < s.len;) {
		sngl_utf8_next(s, &i);
		n++;
	}
	return n;
}

int64_t sngl_string_find(sngl_string s, sngl_string sub, int64_t from) {
	if (sub.len == 0) {
		return from <= (int64_t)s.len ? from : -1;
	}
	for (size_t i = (size_t)from; i + sub.len <= s.len; i++) {
		if (memcmp(s.ptr + i, sub.ptr, sub.len) == 0) {
			return (int64_t)i;
		}
	}
	return -1;
}

int64_t sngl_string_index_of(sngl_string s, sngl_string sub) {
	int64_t at = sngl_string_find(s, sub, 0);
	if (at < 0) {
		return -1;
	}
	int64_t n = 0;
	for (size_t i = 0; i < (size_t)at;) {
		sngl_utf8_next(s, &i);
		n++;
	}
	return n;
}

sngl_string sngl_string_substring(sngl_string s, int64_t start, int64_t end) {
	int64_t n = sngl_string_length(s);
	if (start < 0) {
		start = 0;
	}
	if (end > n) {
		end = n;
	}
	if (start > n) {
		start = n;
	}
	if (end < start) {
		end = start;
	}
	size_t from = 0, to = 0, i = 0;
	for (int64_t k = 0; i <= s.len; k++) {
		if (k == start) {
			from = i;
		}
		if (k == end) {
			to = i;
			break;
		}
		if (i == s.len) {
			break;
		}
		sngl_utf8_next(s, &i);
	}
	return sngl_string_bytes(s.ptr + from, to - from);
}

static uint32_t sngl_upper(uint32_t r) {
	if ((r >= 'a' && r <= 'z') || (r >= 0xe0 && r <= 0xfe && r != 0xf7) || (r >= 0x3b1 && r <= 0x3c9 && r != 0x3c2) || (r >= 0x430 && r <= 0x44f)) {
		return r - 0x20;
	}
	if (r >= 0x450 && r <= 0x45f) {
		return r - 0x50;
	}
	return r;
}

static uint32_t sngl_lower(uint32_t r) {
	if ((r >= 'A' && r <= 'Z') || (r >= 0xc0 && r <= 0xde && r != 0xd7) || (r >= 0x391 && r <= 0x3a9 && r != 0x3a2) || (r >= 0x410 && r <= 0x42f)) {
		return r + 0x20;
	}
	if (r >= 0x400 && r <= 0x40f) {
		return r + 0x50;
	}
	return r;
}

static sngl_string sngl_map_runes(sngl_string s, uint32_t (*f)(uint32_t)) {
	/* A byte at most three: U+FFFD for one that begins nothing. */
	char *out = sngl_alloc(s.len * 3 + 1);
	size_t n = 0;
	for (size_t i = 0; i < s.len;) {
		n += sngl_encode(out + n, f(sngl_utf8_next(s, &i)));
	}
	out[n] = '\0';
	return (sngl_string){out, n};
}

sngl_string sngl_string_upper(sngl_string s) { return sngl_map_runes(s, sngl_upper); }
sngl_string sngl_string_lower(sngl_string s) { return sngl_map_runes(s, sngl_lower); }

static bool sngl_space(uint32_t r) {
	return r == ' ' || (r >= '\t' && r <= '\r') || r == 0x85 || r == 0xa0 || r == 0x1680 || (r >= 0x2000 && r <= 0x200a) ||
	       r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000;
}

sngl_string sngl_string_trim(sngl_string s) {
	size_t from = s.len, to = 0;
	for (size_t i = 0; i < s.len;) {
		size_t at = i;
		if (!sngl_space(sngl_utf8_next(s, &i))) {
			if (from == s.len) {
				from = at;
			}
			to = i;
		}
	}
	if (from >= to) {
		return SNGL_STR("");
	}
	return sngl_string_bytes(s.ptr + from, to - from);
}

sngl_string sngl_string_replace(sngl_string s, sngl_string old, sngl_string new_) {
	if (s.len == 0) {
		return old.len == 0 ? new_ : SNGL_STR("");
	}
	if (old.len == 0) {
		/* As Go's ReplaceAll: new before each character and at the end. */
		sngl_string out = new_;
		for (size_t i = 0; i < s.len;) {
			size_t at = i;
			sngl_utf8_next(s, &i);
			out = sngl_concat(out, sngl_concat(sngl_string_bytes(s.ptr + at, i - at), new_));
		}
		return out;
	}
	sngl_string out = SNGL_STR("");
	int64_t from = 0;
	for (;;) {
		int64_t at = sngl_string_find(s, old, from);
		if (at < 0) {
			break;
		}
		out = sngl_concat(out, sngl_concat(sngl_string_bytes(s.ptr + from, (size_t)(at - from)), new_));
		from = at + (int64_t)old.len;
	}
	return sngl_concat(out, sngl_string_bytes(s.ptr + from, s.len - (size_t)from));
}

int64_t sngl_int_parse(sngl_string s, int64_t base) {
	char *c = sngl_cstring(s);
	int64_t v = (int64_t)strtoll(c, NULL, (int)base);
	free(c);
	return v;
}
