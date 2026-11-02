//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>

extern gboolean snglToplevelKey(gpointer box, guint keyval, GdkModifierType state);

// key-pressed is (controller, keyval, keycode, state, user_data) -> gboolean.
// TRUE is the window taking the key, which ends its propagation.
static gboolean sngl_top_key_pressed(GtkEventControllerKey *c, guint keyval, guint keycode, GdkModifierType state, gpointer box) {
    (void)c;
    (void)keycode;
    return snglToplevelKey(box, keyval, state);
}

// The controller is the window's, in the bubble phase: a key reaches it on
// its way back up from the focused widget, after that widget and everything
// between declined it.
static GtkEventController *sngl_top_watch_keys(GtkWidget *win, gpointer box) {
    GtkEventController *c = gtk_event_controller_key_new();
    gtk_event_controller_set_propagation_phase(c, GTK_PHASE_BUBBLE);
    g_signal_connect(c, "key-pressed", G_CALLBACK(sngl_top_key_pressed), box);
    gtk_widget_add_controller(win, c);
    return c;
}

static gboolean sngl_top_emit_key(GtkEventController *c, guint keyval) {
    gboolean taken = FALSE;
    g_signal_emit_by_name(c, "key-pressed", keyval, 0, (GdkModifierType)0, &taken);
    return taken;
}
*/
import "C"

import (
	"unicode/utf8"
	"unsafe"
)

// namedKeys are the keys a window's `@key` names rather than types: a
// browser's KeyboardEvent.key for each, and the keypad's key beside the main
// one, which it reports the same.
var namedKeys = map[C.guint]string{
	C.GDK_KEY_Return:       "Enter",
	C.GDK_KEY_KP_Enter:     "Enter",
	C.GDK_KEY_ISO_Enter:    "Enter",
	C.GDK_KEY_Escape:       "Escape",
	C.GDK_KEY_BackSpace:    "Backspace",
	C.GDK_KEY_Delete:       "Delete",
	C.GDK_KEY_KP_Delete:    "Delete",
	C.GDK_KEY_Up:           "ArrowUp",
	C.GDK_KEY_KP_Up:        "ArrowUp",
	C.GDK_KEY_Down:         "ArrowDown",
	C.GDK_KEY_KP_Down:      "ArrowDown",
	C.GDK_KEY_Left:         "ArrowLeft",
	C.GDK_KEY_KP_Left:      "ArrowLeft",
	C.GDK_KEY_Right:        "ArrowRight",
	C.GDK_KEY_KP_Right:     "ArrowRight",
	C.GDK_KEY_Home:         "Home",
	C.GDK_KEY_KP_Home:      "Home",
	C.GDK_KEY_End:          "End",
	C.GDK_KEY_KP_End:       "End",
	C.GDK_KEY_Page_Up:      "PageUp",
	C.GDK_KEY_KP_Page_Up:   "PageUp",
	C.GDK_KEY_Page_Down:    "PageDown",
	C.GDK_KEY_KP_Page_Down: "PageDown",
}

// keyvalOf is the keyval reporting name, the main keyboard's where the keypad
// has one too: what a test pressing the key by its name presses.
var keyvalOf = map[string]C.guint{
	"Enter":      C.GDK_KEY_Return,
	"Escape":     C.GDK_KEY_Escape,
	"Backspace":  C.GDK_KEY_BackSpace,
	"Delete":     C.GDK_KEY_Delete,
	"ArrowUp":    C.GDK_KEY_Up,
	"ArrowDown":  C.GDK_KEY_Down,
	"ArrowLeft":  C.GDK_KEY_Left,
	"ArrowRight": C.GDK_KEY_Right,
	"Home":       C.GDK_KEY_Home,
	"End":        C.GDK_KEY_End,
	"PageUp":     C.GDK_KEY_Page_Up,
	"PageDown":   C.GDK_KEY_Page_Down,
}

// shortcutMods are the modifiers that make a key a shortcut, which the
// system or GTK has and a window's `@key` does not hear. Shift and the locks
// change only what the key types.
const shortcutMods = C.GDK_CONTROL_MASK | C.GDK_ALT_MASK | C.GDK_SUPER_MASK | C.GDK_META_MASK | C.GDK_HYPER_MASK

// keyName is what a window's `@key` calls the key keyval is, or "" for one it
// does not report: Tab, a modifier on its own, a function key, a control
// character.
func keyName(keyval C.guint) string {
	if name, ok := namedKeys[keyval]; ok {
		return name
	}
	r := rune(C.gdk_keyval_to_unicode(keyval))
	if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || !utf8.ValidRune(r) {
		return ""
	}
	return string(r)
}

// ToplevelOnKey is the window's `@key`, handed the name of a key pressed in
// the window that nothing focused in it took.
func ToplevelOnKey(h Handle, fn func(key string)) {
	if t := topOf(h); t != nil {
		t.onKey = fn
	}
}

// watchKeys gives win, a window made around the toplevel's box, the
// controller that hears the keys nothing in it takes.
func (t *toplevel) watchKeys(win *C.GtkWidget) {
	t.keys = C.sngl_top_watch_keys(win, C.gpointer(p(t.box)))
}

//export snglToplevelKey
func snglToplevelKey(box C.gpointer, keyval C.guint, state C.GdkModifierType) C.gboolean {
	t := toplevels[Handle(unsafe.Pointer(box))]
	if t == nil {
		return 0
	}
	return gbool(t.key(keyval, state))
}

// key is a key the window's content left, reported unless it is a shortcut
// or no key `@key` names. Escape goes back while the window can, as a back
// button's key, and is the window's own then. Whether the window took it is
// the answer.
func (t *toplevel) key(keyval C.guint, state C.GdkModifierType) bool {
	if state&shortcutMods != 0 {
		return false
	}
	name := keyName(keyval)
	if name == "" {
		return false
	}
	if name == "Escape" && t.canGoBack() {
		t.press()
		return true
	}
	if t.onKey == nil {
		return false
	}
	t.onKey(name)
	return true
}

// ToplevelKey presses the key named name in the window, as a key the window's
// content left: the keyval it names reaches the window's own controller, and
// from there `@key`. For tests, which cannot say what a focused widget would
// have taken, and press it at the window.
func ToplevelKey(h Handle, name string) {
	t := topOf(h)
	if t == nil {
		return
	}
	keyval, ok := keyvalOf[name]
	if !ok {
		r, size := utf8.DecodeRuneInString(name)
		if r == utf8.RuneError || size != len(name) {
			return
		}
		keyval = C.gdk_unicode_to_keyval(C.guint32(r))
	}
	if t.keys != nil {
		C.sngl_top_emit_key(t.keys, keyval)
		return
	}
	t.key(keyval, 0)
}
