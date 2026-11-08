//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#cgo CFLAGS: -I${SRCDIR}/../../../lib/platform/gtk4/rt
#include "sngl_gtk.h"

extern gboolean snglToplevelKey(gpointer box, char *key, gboolean ctrl, gboolean alt, gboolean shift, gboolean meta, gboolean editing);
extern gboolean snglToplevelBack(gpointer box);

static bool sngl_go_key(const char *key, bool ctrl, bool alt, bool shift, bool meta, bool editing, void *box) {
    return snglToplevelKey(box, (char *)key, ctrl, alt, shift, meta, editing);
}

static bool sngl_go_back(void *box) {
    return snglToplevelBack(box);
}

static void sngl_top_watch_keys(GtkWidget *win, gpointer box) {
    sngl_keys_watch(win, sngl_go_key, sngl_go_back, box);
}
*/
import "C"

import "unsafe"

// ToplevelOnKey is the window's `@key`, handed each key pressed in the window
// before anything in it: its name, the modifiers held, whether focus is in a
// field that takes typing, and the stop that keeps the key from going
// further.
func ToplevelOnKey(h Handle, fn func(key string, ctrl, alt, shift, meta, editing bool, stop func())) {
	if t := topOf(h); t != nil {
		t.onKey = fn
	}
}

// watchKeys gives win, a window made around the toplevel's box, the
// controllers that hear its keys.
func (t *toplevel) watchKeys(win *C.GtkWidget) {
	C.sngl_top_watch_keys(win, C.gpointer(p(t.box)))
}

//export snglToplevelKey
func snglToplevelKey(box C.gpointer, key *C.char, ctrl, alt, shift, meta, editing C.gboolean) C.gboolean {
	t := toplevels[Handle(unsafe.Pointer(box))]
	if t == nil {
		return 0
	}
	return gbool(t.hear(C.GoString(key), ctrl != 0, alt != 0, shift != 0, meta != 0, editing != 0, nil))
}

//export snglToplevelBack
func snglToplevelBack(box C.gpointer) C.gboolean {
	t := toplevels[Handle(unsafe.Pointer(box))]
	if t == nil {
		return 0
	}
	return gbool(t.back())
}

// hear runs the window's `@key` on a press, and is whether its handler
// stopped the key. then, when there is one, is called by the stop as well.
func (t *toplevel) hear(name string, ctrl, alt, shift, meta, editing bool, then func()) bool {
	if t.onKey == nil {
		return false
	}
	stopped := false
	t.onKey(name, ctrl, alt, shift, meta, editing, func() {
		stopped = true
		if then != nil {
			then()
		}
	})
	return stopped
}

// back is Escape that nothing took going back while the window can, as a
// back button's key, and is whether it went.
func (t *toplevel) back() bool {
	if !t.canGoBack() {
		return false
	}
	t.press()
	return true
}

// ToplevelKey presses a key in the window as a test describes it: the
// window's `@key` hears it, stop is called by its handler's stop, and an
// Escape the handler leaves goes back while the window can. For tests, which
// name what a key is and whether focus is in a field rather than press one
// on a keyboard; a focused widget's part is the toolkit's.
func ToplevelKey(h Handle, key string, ctrl, alt, shift, meta, editing bool, stop func()) {
	t := topOf(h)
	if t == nil {
		return
	}
	if t.hear(key, ctrl, alt, shift, meta, editing, stop) {
		return
	}
	held := ctrl || alt || meta
	if key == "Escape" && !held {
		t.back()
		return
	}
	if t.win == nil || held {
		return
	}
	c, free := cstr(key)
	defer free()
	C.sngl_keys_go_on(t.win, c, C.bool(shift))
}
