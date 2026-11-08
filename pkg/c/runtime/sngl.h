/* The runtime every C program SNGL writes includes: strings, the arena they
 * are made in, and the operations C has no spelling for. It compiles as C11
 * and as C17, and needs nothing beyond the C library. */
#ifndef SNGL_H
#define SNGL_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

/* A string is its bytes and their length, with a NUL after them, so it is
 * handed to a `char *` parameter as it is. It is a value: passed and
 * returned as two words, and never written once made. */
typedef struct sngl_string {
	const char *ptr;
	size_t len;
} sngl_string;

/* A string literal, its length counted by the compiler. */
#define SNGL_STR(lit) ((sngl_string){(lit), sizeof(lit) - 1})

/* sngl_alloc is n bytes from the turn's arena, which sngl_turn_end gives
 * back all at once. A value that outlives the turn is copied out of it. */
void *sngl_alloc(size_t n);
void sngl_turn_end(void);

/* sngl_heap_copy is a copy of the n bytes at p on the heap, which outlives
 * every turn: a closure's environment, a box a `ref` became. */
void *sngl_heap_copy(const void *p, size_t n);

/* sngl_cstring is s as a C string the callee may keep and the program frees,
 * as Go's C.CString makes one. */
char *sngl_cstring(sngl_string s);

/* The program stops, writing msg, as a raise nothing catches does. */
_Noreturn void sngl_panic(const char *msg);

/* sngl_panic_hook, where set, is called with a panic's message first: the
 * test agent fails the test running and goes on to the next. */
extern void (*sngl_panic_hook)(const char *msg);

sngl_string sngl_concat(sngl_string a, sngl_string b);
bool sngl_string_eq(sngl_string a, sngl_string b);
int sngl_string_cmp(sngl_string a, sngl_string b);

/* A value's format, as `{x}` writes it and every target writes it. */
sngl_string sngl_int_format(int64_t v);
sngl_string sngl_float_format(double v);
sngl_string sngl_bool_format(bool v);

/* Integer division and remainder, raising on a zero divisor as every target
 * does, rather than C's undefined behavior. */
int64_t sngl_div(int64_t a, int64_t b);
int64_t sngl_mod(int64_t a, int64_t b);

#endif
