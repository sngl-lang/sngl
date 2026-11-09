/* The runtime every C program SNGL writes includes: strings, the arena they
 * are made in, and the operations C has no spelling for. It compiles as C11
 * and as C17, and needs nothing beyond the C library. */
#ifndef SNGL_H
#define SNGL_H

#include <setjmp.h>
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
 * back all at once. A value that outlives the turn is copied out of it.
 *
 * A turn is one pass through the event loop: start-up, a handler, a draw, a
 * timer's tick, a test. Each is bracketed by sngl_turn_enter and
 * sngl_turn_leave, which nest, as a handler a signal runs inside another
 * does; the outermost leave ends the turn. sngl_turn_end ends it whatever
 * the depth, as the test agent does after a test a failure left. */
void *sngl_alloc(size_t n);
void sngl_turn_enter(void);
void sngl_turn_leave(void);
void sngl_turn_end(void);

/* A callback C makes into the program -- a signal, a timer, an idle -- gives
 * back what it made of the arena as it returns, whatever turn it ran in:
 * sngl_call_enter marks where the arena stands and enters a turn, and
 * sngl_call_leave leaves it and gives back what was made since the mark.
 * What it stored was kept, and it returns nothing the arena holds. */
typedef struct sngl_mark {
	void *chunk;
	size_t used;
} sngl_mark;
sngl_mark sngl_call_enter(void);
void sngl_call_leave(sngl_mark m);

/* What a holder keeps -- a var, a field, an element, a closure's
 * environment -- outlives the turn, so it is copied out of the arena as it
 * is stored. sngl_keep_alloc is n bytes of the heap, kept; sngl_string_keep
 * is s copied there. sngl_drop gives a kept block back once the turn ends,
 * since a local of the turn may still read what a holder held; a block not
 * kept, or dropped already, is left alone. */
void *sngl_keep_alloc(size_t n);
sngl_string sngl_string_keep(sngl_string s);
void sngl_drop(const void *p);

/* sngl_heap_copy is a copy of the n bytes at p on the heap, which outlives
 * every turn: a closure's environment, a box a `ref` became. */
void *sngl_heap_copy(const void *p, size_t n);

/* sngl_cstring is s as a C string the callee may keep and the program frees,
 * as Go's C.CString makes one. */
char *sngl_cstring(sngl_string s);

/* sngl_string_bytes is a string of the n bytes at p, copied into the
 * arena with the NUL after them every string has. */
sngl_string sngl_string_bytes(const char *p, size_t n);

/* sngl_string_copy is the C string s as a string, copied into the turn's
 * arena: what a library hands back is its own to change or free. NULL is
 * the empty string. */
sngl_string sngl_string_copy(const char *s);

/* The program stops, writing msg, as a raise nothing catches does. */
_Noreturn void sngl_panic(const char *msg);

/* A catch point: a block whose raise the program answers, a boundary's or a
 * window's @error. sngl_catching is the innermost one, each pointing up to
 * the one around it. A block pushes its own before it runs and takes it off
 * however it leaves; a raise takes it off on its way to `at`, and leaves the
 * error in sngl_raised, which the handler copies into message and kind as it
 * starts: C leaves indeterminate a local of the function that called setjmp
 * written after it, as the raise would have written the point. */
typedef struct sngl_catch {
	jmp_buf at;
	struct sngl_catch *up;
	sngl_string message;
	sngl_string kind;
} sngl_catch;
extern sngl_catch *sngl_catching;
extern struct sngl_raised {
	sngl_string message;
	sngl_string kind;
} sngl_raised;

/* A handler reads nothing the block it catches for wrote: the block's
 * locals are out of its scope and a parameter is never written. So no value
 * a longjmp could have clobbered is read after one, which GCC cannot see,
 * and warns of for every temporary the block makes. */
#if defined(__GNUC__) && !defined(__clang__)
#pragma GCC diagnostic ignored "-Wclobbered"
#endif

/* sngl_raise jumps to the innermost catch point with the error, or ends the
 * program, as a panic, where there is none. */
_Noreturn void sngl_raise(sngl_string message, sngl_string kind);

/* sngl_panic_hook, where set, is called with a panic's message first: the
 * test agent fails the test running and goes on to the next. */
extern void (*sngl_panic_hook)(const char *msg);

sngl_string sngl_concat(sngl_string a, sngl_string b);
bool sngl_string_eq(sngl_string a, sngl_string b);
int sngl_string_cmp(sngl_string a, sngl_string b);

/* The string operations sngl:builtin declares, by character -- a Unicode
 * code point -- where they index, as every target counts. Case mapping is
 * C's own: ASCII, Latin-1, Greek and Cyrillic, which needs no table. */
int64_t sngl_string_length(sngl_string s);
int64_t sngl_string_index_of(sngl_string s, sngl_string sub);
sngl_string sngl_string_substring(sngl_string s, int64_t start, int64_t end);
sngl_string sngl_string_upper(sngl_string s);
sngl_string sngl_string_lower(sngl_string s);
sngl_string sngl_string_trim(sngl_string s);
sngl_string sngl_string_replace(sngl_string s, sngl_string old, sngl_string new_);
int64_t sngl_int_parse(sngl_string s, int64_t base);

/* sngl_string_find is the byte offset of the first sub in s at or after
 * from, or -1. */
int64_t sngl_string_find(sngl_string s, sngl_string sub, int64_t from);

/* A value's format, as `{x}` writes it and every target writes it. */
sngl_string sngl_int_format(int64_t v);
sngl_string sngl_float_format(double v);

/* One base's magnitude of a unit as it is displayed, `3px`: whole as an
 * integer, otherwise as a float writes. */
sngl_string sngl_unit_term(double mag, sngl_string base);

/* color.hex: `#rrggbb`, or `#rrggbbaa` where the color is not opaque. */
sngl_string sngl_color_hex(uint8_t r, uint8_t g, uint8_t b, uint8_t a);

/* int(s) and float(s): the number the whole of s writes, in decimal with a
 * dot whatever the locale, or 0 where it writes none, as Go's strconv.Atoi
 * and ParseFloat answer. */
int64_t sngl_int_of(sngl_string s);
double sngl_float_of(sngl_string s);
sngl_string sngl_bool_format(bool v);

/* int(f): f truncated toward zero, the nearest int where f is past either
 * end, and 0 for NaN, as Kotlin's toLong answers -- where C's own cast is
 * undefined. */
int64_t sngl_float_int(double v);

/* An async lifetime is held by its number, 0 for `forever`, which never
 * ends: sngl_async_begin makes one, sngl_async_end ends it, and
 * sngl_async_ended asks. */
int64_t sngl_async_begin(void);
void sngl_async_end(int64_t id);
bool sngl_async_ended(int64_t id);

/* Integer division and remainder, raising on a zero divisor as every target
 * does, rather than C's undefined behavior. */
int64_t sngl_div(int64_t a, int64_t b);
int64_t sngl_mod(int64_t a, int64_t b);

#endif
