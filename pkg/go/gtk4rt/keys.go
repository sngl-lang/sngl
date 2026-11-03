//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>

extern gboolean snglToplevelKey(gpointer box, guint keyval, GdkModifierType state, gboolean editing);
extern gboolean snglToplevelBack(gpointer box, guint keyval, GdkModifierType state);

// sngl_composing reports whether one of w's key controllers has an input
// method composing: text in its preedit, which the keys pressed now build.
// GtkText and GtkTextView hand their input method to their key controller,
// which is how one is found.
static gboolean sngl_composing(GtkWidget *w) {
    gboolean composing = FALSE;
    GListModel *controllers = gtk_widget_observe_controllers(w);
    guint n = g_list_model_get_n_items(controllers);
    for (guint i = 0; i < n && !composing; i++) {
        GObject *c = g_list_model_get_item(controllers, i);
        if (GTK_IS_EVENT_CONTROLLER_KEY(c)) {
            GtkIMContext *im = gtk_event_controller_key_get_im_context(GTK_EVENT_CONTROLLER_KEY(c));
            if (im != NULL) {
                char *preedit = NULL;
                gtk_im_context_get_preedit_string(im, &preedit, NULL, NULL);
                composing = preedit != NULL && preedit[0] != '\0';
                g_free(preedit);
            }
        }
        g_object_unref(c);
    }
    g_object_unref(controllers);
    return composing;
}

// key-pressed is (controller, keyval, keycode, state, user_data) -> gboolean.
// TRUE is the window taking the key, which ends its propagation. A key an
// input method is composing with goes by untouched, to the field whose it
// is; otherwise the window hears whether focus is in a field that takes
// typing -- an entry's focus is on the GtkText it holds, a GtkEditable too.
static gboolean sngl_top_key_pressed(GtkEventControllerKey *c, guint keyval, guint keycode, GdkModifierType state, gpointer box) {
    (void)keycode;
    GtkWidget *win = gtk_event_controller_get_widget(GTK_EVENT_CONTROLLER(c));
    gboolean editing = FALSE;
    for (GtkWidget *w = gtk_root_get_focus(GTK_ROOT(win)); w != NULL && w != win; w = gtk_widget_get_parent(w)) {
        if (sngl_composing(w)) {
            return FALSE;
        }
        if (GTK_IS_EDITABLE(w) || GTK_IS_TEXT_VIEW(w)) {
            editing = TRUE;
        }
    }
    return snglToplevelKey(box, keyval, state, editing);
}

static gboolean sngl_top_key_back(GtkEventControllerKey *c, guint keyval, guint keycode, GdkModifierType state, gpointer box) {
    (void)c;
    (void)keycode;
    return snglToplevelBack(box, keyval, state);
}

// The window has two controllers. One is in the capture phase: a key reaches
// it on its way down to the focused widget, before that widget or anything
// between them, which is where `@key` hears it. The other is in the bubble
// phase: a key reaches it on its way back up, after the handler and the
// focused widget both left it, which is where Escape goes back.
static void sngl_top_watch_keys(GtkWidget *win, gpointer box) {
    GtkEventController *c = gtk_event_controller_key_new();
    gtk_event_controller_set_propagation_phase(c, GTK_PHASE_CAPTURE);
    g_signal_connect(c, "key-pressed", G_CALLBACK(sngl_top_key_pressed), box);
    gtk_widget_add_controller(win, c);
    GtkEventController *back = gtk_event_controller_key_new();
    gtk_event_controller_set_propagation_phase(back, GTK_PHASE_BUBBLE);
    g_signal_connect(back, "key-pressed", G_CALLBACK(sngl_top_key_back), box);
    gtk_widget_add_controller(win, back);
}

// What a key does once nothing stopped it, for a test pressing one by its
// name: GTK makes no key event a program can send, so each is the signal the
// key is bound to on the widget focus is on, or the window's own move of
// focus. A button's activate presses it only after a moment, so a button is
// clicked as its activation ends.
static GtkWidget *sngl_key_focus(GtkWindow *win) {
    return gtk_root_get_focus(GTK_ROOT(win));
}

static gboolean sngl_key_is_text(GtkWidget *w) {
    return GTK_IS_TEXT(w) || GTK_IS_TEXT_VIEW(w);
}

static gboolean sngl_key_is_view(GtkWidget *w) {
    return GTK_IS_TEXT_VIEW(w);
}

static gboolean sngl_key_takes_tab(GtkWidget *w) {
    return GTK_IS_TEXT_VIEW(w) && gtk_text_view_get_accepts_tab(GTK_TEXT_VIEW(w));
}

static void sngl_key_signal(GtkWidget *w, const char *signal) {
    g_signal_emit_by_name(w, signal);
}

static void sngl_key_insert(GtkWidget *w, const char *s) {
    g_signal_emit_by_name(w, "insert-at-cursor", s);
}

static void sngl_key_delete(GtkWidget *w, int count) {
    g_signal_emit_by_name(w, "delete-from-cursor", GTK_DELETE_CHARS, count);
}

static void sngl_key_move(GtkWidget *w, GtkMovementStep step, int count, gboolean extend) {
    g_signal_emit_by_name(w, "move-cursor", step, count, extend);
}

static void sngl_key_activate(GtkWidget *w) {
    if (GTK_IS_BUTTON(w)) {
        g_signal_emit_by_name(w, "clicked");
        return;
    }
    gtk_widget_activate(w);
}

// The window moves focus as its Tab and arrows do: past the last widget, Tab
// starts again from the first.
static void sngl_key_move_focus(GtkWindow *win, GtkDirectionType dir) {
    if (!gtk_widget_child_focus(GTK_WIDGET(win), dir)) {
        gtk_window_set_focus(win, NULL);
        gtk_widget_child_focus(GTK_WIDGET(win), dir);
    }
}
*/
import "C"

import (
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// namedKeys are the keys a window's `@key` names rather than types: a
// browser's KeyboardEvent.key for each, and the keypad's key beside the main
// one, which it reports the same. Shift+Tab is ISO_Left_Tab to GDK.
var namedKeys = map[C.guint]string{
	C.GDK_KEY_Return:       "Enter",
	C.GDK_KEY_KP_Enter:     "Enter",
	C.GDK_KEY_ISO_Enter:    "Enter",
	C.GDK_KEY_Tab:          "Tab",
	C.GDK_KEY_KP_Tab:       "Tab",
	C.GDK_KEY_ISO_Left_Tab: "Tab",
	C.GDK_KEY_Escape:       "Escape",
	C.GDK_KEY_BackSpace:    "Backspace",
	C.GDK_KEY_Delete:       "Delete",
	C.GDK_KEY_KP_Delete:    "Delete",
	C.GDK_KEY_Insert:       "Insert",
	C.GDK_KEY_KP_Insert:    "Insert",
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
	C.GDK_KEY_F1:           "F1",
	C.GDK_KEY_F2:           "F2",
	C.GDK_KEY_F3:           "F3",
	C.GDK_KEY_F4:           "F4",
	C.GDK_KEY_F5:           "F5",
	C.GDK_KEY_F6:           "F6",
	C.GDK_KEY_F7:           "F7",
	C.GDK_KEY_F8:           "F8",
	C.GDK_KEY_F9:           "F9",
	C.GDK_KEY_F10:          "F10",
	C.GDK_KEY_F11:          "F11",
	C.GDK_KEY_F12:          "F12",
}

// metaMods are the modifiers a press reports as `meta`: Meta, and the Super
// a Linux keyboard's Windows key is.
const metaMods = C.GDK_META_MASK | C.GDK_SUPER_MASK

// shortcutMods are the modifiers that make a key the key's own character
// rather than what it types, and Escape no way back.
const shortcutMods = C.GDK_CONTROL_MASK | C.GDK_ALT_MASK | metaMods

// keyName is what a window's `@key` calls the key keyval is, or "" for one it
// does not report: a modifier on its own, a control character, a key no
// browser names. With a shortcut's modifier held a letter is the key's own,
// lowercase unless Shift is held, as Caps Lock leaves it.
func keyName(keyval C.guint, state C.GdkModifierType) string {
	if name, ok := namedKeys[keyval]; ok {
		return name
	}
	r := rune(C.gdk_keyval_to_unicode(keyval))
	if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || !utf8.ValidRune(r) {
		return ""
	}
	if state&shortcutMods != 0 {
		if state&C.GDK_SHIFT_MASK != 0 {
			r = unicode.ToUpper(r)
		} else {
			r = unicode.ToLower(r)
		}
	}
	return string(r)
}

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
func snglToplevelKey(box C.gpointer, keyval C.guint, state C.GdkModifierType, editing C.gboolean) C.gboolean {
	t := toplevels[Handle(unsafe.Pointer(box))]
	if t == nil {
		return 0
	}
	name := keyName(keyval, state)
	if name == "" {
		return 0
	}
	return gbool(t.hear(name, state&C.GDK_CONTROL_MASK != 0, state&C.GDK_ALT_MASK != 0,
		state&C.GDK_SHIFT_MASK != 0, state&metaMods != 0, editing != 0, nil))
}

//export snglToplevelBack
func snglToplevelBack(box C.gpointer, keyval C.guint, state C.GdkModifierType) C.gboolean {
	t := toplevels[Handle(unsafe.Pointer(box))]
	if t == nil || keyval != C.GDK_KEY_Escape || state&shortcutMods != 0 {
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
	t.goOn(key, ctrl || alt || meta, shift)
}

// goOn is a key nothing stopped going where it would have gone, as a
// keyboard's would: typed into a focused field, pressing a focused button,
// moving focus, or Escape going back. held is whether a shortcut's modifier is,
// which leaves a key nothing to do here but a field's own shortcuts'.
func (t *toplevel) goOn(key string, held, shift bool) {
	if key == "Escape" && !held {
		t.back()
		return
	}
	if t.win == nil || held {
		return
	}
	focus := C.sngl_key_focus(t.win)
	if focus != nil && C.sngl_key_is_text(focus) != 0 && (key != "Tab" || C.sngl_key_takes_tab(focus) != 0) {
		textKey(focus, key, shift)
		return
	}
	switch key {
	case "Enter", " ":
		if focus != nil {
			C.sngl_key_activate(focus)
		}
	case "Tab":
		dir := C.GtkDirectionType(C.GTK_DIR_TAB_FORWARD)
		if shift {
			dir = C.GTK_DIR_TAB_BACKWARD
		}
		C.sngl_key_move_focus(t.win, dir)
	case "ArrowUp":
		C.sngl_key_move_focus(t.win, C.GTK_DIR_UP)
	case "ArrowDown":
		C.sngl_key_move_focus(t.win, C.GTK_DIR_DOWN)
	case "ArrowLeft":
		C.sngl_key_move_focus(t.win, C.GTK_DIR_LEFT)
	case "ArrowRight":
		C.sngl_key_move_focus(t.win, C.GTK_DIR_RIGHT)
	}
}

// textKey is key on the GtkText or GtkTextView w focus is on: what it types
// inserted at the cursor, Enter activating an entry and starting a line in a
// text view, and the editing keys their bindings' signals. Tab is a text
// view's only while it takes Tab.
func textKey(w *C.GtkWidget, key string, shift bool) {
	switch key {
	case "Tab":
		key = "\t"
	case "Enter":
		if C.sngl_key_is_view(w) == 0 {
			C.sngl_key_signal(w, sigActivate)
			return
		}
		key = "\n"
	case "Backspace":
		C.sngl_key_signal(w, sigBackspace)
		return
	case "Delete":
		C.sngl_key_delete(w, 1)
		return
	case "ArrowLeft", "ArrowRight":
		count := C.int(1)
		if key == "ArrowLeft" {
			count = -1
		}
		C.sngl_key_move(w, C.GTK_MOVEMENT_VISUAL_POSITIONS, count, gbool(shift))
		return
	case "Home", "End":
		count := C.int(1)
		if key == "Home" {
			count = -1
		}
		C.sngl_key_move(w, C.GTK_MOVEMENT_DISPLAY_LINE_ENDS, count, gbool(shift))
		return
	}
	if r, size := utf8.DecodeRuneInString(key); r == utf8.RuneError || size != len(key) {
		return
	}
	c, free := cstr(key)
	defer free()
	C.sngl_key_insert(w, c)
}

var (
	sigActivate  = C.CString("activate")
	sigBackspace = C.CString("backspace")
)
