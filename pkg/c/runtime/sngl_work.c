/* What async.start begins runs on a stack of its own, on the loop's thread
 * (sngl.h, sngl_work_start): ucontext on POSIX, a fiber on Windows. */
#if !defined(_WIN32) && !defined(_XOPEN_SOURCE)
#define _XOPEN_SOURCE 700
#define _DEFAULT_SOURCE
#endif

#include "sngl.h"

#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <windows.h>
#else
#include <sys/mman.h>
#include <ucontext.h>
#include <unistd.h>
#endif

#if defined(_WIN32)
#elif defined(__SANITIZE_ADDRESS__)
#define SNGL_WORK_ASAN 1
#elif defined(__has_feature)
#if __has_feature(address_sanitizer)
#define SNGL_WORK_ASAN 1
#endif
#endif
#ifdef SNGL_WORK_ASAN
#include <sanitizer/common_interface_defs.h>
#endif

/* Deep enough for a request's decoding and whatever a handler calls; the
 * pages are only committed as they are touched. */
enum { sngl_work_stack = 1 << 20 };

typedef struct sngl_work {
	void (*fn)(void *ctx);
	void *ctx;
	void (*release)(void *ctx);
	/* Its own arena and catch points while it waits, its resumer's while it
	 * runs (sngl_stack_swap). */
	void *arena;
	sngl_catch *catching;
	bool done;
#ifdef _WIN32
	LPVOID fiber, back;
#else
	ucontext_t self, back;
	char *stack;
	size_t size;
#endif
#ifdef SNGL_WORK_ASAN
	void *fake;
	const void *back_bottom;
	size_t back_size;
#endif
} sngl_work;

static sngl_work *sngl_working;

void *sngl_work_self(void) {
	return sngl_working;
}

/* sngl_work_leave goes back to whoever resumed the work running. */
static void sngl_work_leave(sngl_work *w) {
#ifdef SNGL_WORK_ASAN
	__sanitizer_start_switch_fiber(w->done ? NULL : &w->fake, w->back_bottom, w->back_size);
#endif
#ifdef _WIN32
	SwitchToFiber(w->back);
#else
	swapcontext(&w->self, &w->back);
#endif
#ifdef SNGL_WORK_ASAN
	__sanitizer_finish_switch_fiber(w->fake, &w->back_bottom, &w->back_size);
#endif
}

#ifdef _WIN32
static void WINAPI sngl_work_run(LPVOID p) {
	sngl_work *w = p;
#else
static void sngl_work_run(void) {
	sngl_work *w = sngl_working;
#endif
#ifdef SNGL_WORK_ASAN
	__sanitizer_finish_switch_fiber(NULL, &w->back_bottom, &w->back_size);
#endif
	w->fn(w->ctx);
	if (w->release != NULL) {
		w->release(w->ctx);
	}
	w->done = true;
	sngl_work_leave(w);
}

void sngl_work_wait(void) {
	sngl_work *w = sngl_working;
	if (w == NULL) {
		sngl_panic("waiting off a work's stack");
	}
	sngl_work_leave(w);
}

void sngl_work_wake(void *p) {
	sngl_work *w = p;
	sngl_work *outer = sngl_working;
	sngl_working = w;
	sngl_stack_swap(&w->arena, &w->catching);
#ifdef SNGL_WORK_ASAN
	void *fake = NULL;
	__sanitizer_start_switch_fiber(&fake, w->stack, w->size);
#endif
#ifdef _WIN32
	w->back = GetCurrentFiber();
	SwitchToFiber(w->fiber);
#else
	swapcontext(&w->back, &w->self);
#endif
#ifdef SNGL_WORK_ASAN
	__sanitizer_finish_switch_fiber(fake, NULL, NULL);
#endif
	sngl_stack_swap(&w->arena, &w->catching);
	sngl_working = outer;
	if (!w->done) {
		return;
	}
	sngl_arena_free(w->arena);
#ifdef _WIN32
	DeleteFiber(w->fiber);
#else
	munmap(w->stack, w->size);
#endif
	free(w);
}

static void sngl_work_begin(void *p) {
	sngl_work *w = p;
#ifdef _WIN32
	static bool converted;
	if (!converted) {
		ConvertThreadToFiber(NULL);
		converted = true;
	}
	w->fiber = CreateFiber(sngl_work_stack, sngl_work_run, w);
	if (w->fiber == NULL) {
		sngl_panic("no stack for the work");
	}
#else
	/* The lowest page is a guard, which the stack's probes stop at
	 * (-fstack-clash-protection) rather than write past. */
	size_t page = (size_t)sysconf(_SC_PAGESIZE);
	w->size = sngl_work_stack + page;
	w->stack = mmap(NULL, w->size, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS | MAP_NORESERVE, -1, 0);
	if (w->stack == MAP_FAILED || mprotect(w->stack, page, PROT_NONE) != 0 || getcontext(&w->self) != 0) {
		sngl_panic("no stack for the work");
	}
	w->self.uc_stack.ss_sp = w->stack;
	w->self.uc_stack.ss_size = w->size;
	w->self.uc_link = NULL;
	makecontext(&w->self, sngl_work_run, 0);
#endif
	sngl_work_wake(w);
}

void sngl_work_start(void (*fn)(void *ctx), void *ctx, void (*release)(void *ctx)) {
	sngl_work *w = calloc(1, sizeof *w);
	if (w == NULL) {
		sngl_panic("out of memory");
	}
	w->fn = fn;
	w->ctx = ctx;
	w->release = release;
	sngl_post(sngl_work_begin, w, NULL);
}
