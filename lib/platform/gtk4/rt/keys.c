/* A window's keys on GTK: what a window's `@key` calls each key, the
 * controllers that hear them, and what a key nothing stopped does when a
 * test presses it by name. Compiled into every gtk4 program: pkg/go/gtk4rt
 * includes it for Go, and a C program compiles it beside its own. Each
 * language keeps the handler, and what Escape goes back through. */
#include "sngl_gtk.h"

#include <string.h>

/* The keys a window's `@key` names rather than types: a browser's
 * KeyboardEvent.key for each, and the keypad's key beside the main one,
 * which it reports the same. Shift+Tab is ISO_Left_Tab to GDK. */
static const struct {
    guint keyval;
    const char *name;
} sngl_named_keys[] = {
    {GDK_KEY_Return, "Enter"},
    {GDK_KEY_KP_Enter, "Enter"},
    {GDK_KEY_ISO_Enter, "Enter"},
    {GDK_KEY_Tab, "Tab"},
    {GDK_KEY_KP_Tab, "Tab"},
    {GDK_KEY_ISO_Left_Tab, "Tab"},
    {GDK_KEY_Escape, "Escape"},
    {GDK_KEY_BackSpace, "Backspace"},
    {GDK_KEY_Delete, "Delete"},
    {GDK_KEY_KP_Delete, "Delete"},
    {GDK_KEY_Insert, "Insert"},
    {GDK_KEY_KP_Insert, "Insert"},
    {GDK_KEY_Up, "ArrowUp"},
    {GDK_KEY_KP_Up, "ArrowUp"},
    {GDK_KEY_Down, "ArrowDown"},
    {GDK_KEY_KP_Down, "ArrowDown"},
    {GDK_KEY_Left, "ArrowLeft"},
    {GDK_KEY_KP_Left, "ArrowLeft"},
    {GDK_KEY_Right, "ArrowRight"},
    {GDK_KEY_KP_Right, "ArrowRight"},
    {GDK_KEY_Home, "Home"},
    {GDK_KEY_KP_Home, "Home"},
    {GDK_KEY_End, "End"},
    {GDK_KEY_KP_End, "End"},
    {GDK_KEY_Page_Up, "PageUp"},
    {GDK_KEY_KP_Page_Up, "PageUp"},
    {GDK_KEY_Page_Down, "PageDown"},
    {GDK_KEY_KP_Page_Down, "PageDown"},
    {GDK_KEY_F1, "F1"},
    {GDK_KEY_F2, "F2"},
    {GDK_KEY_F3, "F3"},
    {GDK_KEY_F4, "F4"},
    {GDK_KEY_F5, "F5"},
    {GDK_KEY_F6, "F6"},
    {GDK_KEY_F7, "F7"},
    {GDK_KEY_F8, "F8"},
    {GDK_KEY_F9, "F9"},
    {GDK_KEY_F10, "F10"},
    {GDK_KEY_F11, "F11"},
    {GDK_KEY_F12, "F12"},
};

/* The modifiers a press reports as `meta`: Meta, and the Super a Linux
 * keyboard's Windows key is. */
#define SNGL_META_MODS (GDK_META_MASK | GDK_SUPER_MASK)

/* The modifiers that make a key the key's own character rather than what it
 * types, and Escape no way back. */
#define SNGL_SHORTCUT_MODS (GDK_CONTROL_MASK | GDK_ALT_MASK | SNGL_META_MODS)

const char *sngl_key_name(guint keyval, GdkModifierType state, char buf[8]) {
    for (size_t i = 0; i < G_N_ELEMENTS(sngl_named_keys); i++) {
        if (sngl_named_keys[i].keyval == keyval) return sngl_named_keys[i].name;
    }
    gunichar r = gdk_keyval_to_unicode(keyval);
    if (r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || !g_unichar_validate(r)) return NULL;
    if (state & SNGL_SHORTCUT_MODS) {
        r = state & GDK_SHIFT_MASK ? g_unichar_toupper(r) : g_unichar_tolower(r);
    }
    buf[g_unichar_to_utf8(r, buf)] = '\0';
    return buf;
}

/* sngl_composing reports whether one of w's key controllers has an input
 * method composing: text in its preedit, which the keys pressed now build.
 * GtkText and GtkTextView hand their input method to their key controller,
 * which is how one is found. */
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

typedef struct {
    SnglKeyHear hear;
    SnglKeyBack back;
    void *data;
} SnglKeys;

/* key-pressed is (controller, keyval, keycode, state, user_data) -> gboolean.
 * TRUE is the window taking the key, which ends its propagation. A key an
 * input method is composing with goes by untouched, to the field whose it
 * is; otherwise the window hears whether focus is in a field that takes
 * typing -- an entry's focus is on the GtkText it holds, a GtkEditable too. */
static gboolean sngl_keys_pressed(GtkEventControllerKey *c, guint keyval, guint keycode, GdkModifierType state, gpointer data) {
    (void)keycode;
    SnglKeys *k = data;
    GtkWidget *win = gtk_event_controller_get_widget(GTK_EVENT_CONTROLLER(c));
    bool editing = false;
    for (GtkWidget *w = gtk_root_get_focus(GTK_ROOT(win)); w != NULL && w != win; w = gtk_widget_get_parent(w)) {
        if (sngl_composing(w)) return FALSE;
        if (GTK_IS_EDITABLE(w) || GTK_IS_TEXT_VIEW(w)) editing = true;
    }
    char buf[8];
    const char *name = sngl_key_name(keyval, state, buf);
    if (name == NULL) return FALSE;
    return k->hear(name, state & GDK_CONTROL_MASK, state & GDK_ALT_MASK, state & GDK_SHIFT_MASK,
        state & SNGL_META_MODS, editing, k->data);
}

static gboolean sngl_keys_back(GtkEventControllerKey *c, guint keyval, guint keycode, GdkModifierType state, gpointer data) {
    (void)c;
    (void)keycode;
    SnglKeys *k = data;
    if (keyval != GDK_KEY_Escape || state & SNGL_SHORTCUT_MODS) return FALSE;
    return k->back(k->data);
}

/* The window has two controllers. One is in the capture phase: a key
 * reaches it on its way down to the focused widget, before that widget or
 * anything between them, which is where `@key` hears it. The other is in the
 * bubble phase: a key reaches it on its way back up, after the handler and
 * the focused widget both left it, which is where Escape goes back. */
void sngl_keys_watch(GtkWidget *win, SnglKeyHear hear, SnglKeyBack back, void *data) {
    SnglKeys *k = g_new0(SnglKeys, 1);
    k->hear = hear;
    k->back = back;
    k->data = data;
    g_object_set_data_full(G_OBJECT(win), "sngl-keys", k, g_free);
    GtkEventController *c = gtk_event_controller_key_new();
    gtk_event_controller_set_propagation_phase(c, GTK_PHASE_CAPTURE);
    g_signal_connect(c, "key-pressed", G_CALLBACK(sngl_keys_pressed), k);
    gtk_widget_add_controller(win, c);
    if (back == NULL) return;
    GtkEventController *b = gtk_event_controller_key_new();
    gtk_event_controller_set_propagation_phase(b, GTK_PHASE_BUBBLE);
    g_signal_connect(b, "key-pressed", G_CALLBACK(sngl_keys_back), k);
    gtk_widget_add_controller(win, b);
}

/* What a key does once nothing stopped it, for a test pressing one by its
 * name: GTK makes no key event a program can send, so each is the signal the
 * key is bound to on the widget focus is on, or the window's own move of
 * focus. A button's activate presses it only after a moment, so a button is
 * clicked as its activation ends. */
static void sngl_key_activate(GtkWidget *w) {
    if (GTK_IS_BUTTON(w)) {
        g_signal_emit_by_name(w, "clicked");
        return;
    }
    gtk_widget_activate(w);
}

/* The window moves focus as its Tab and arrows do: past the last widget, Tab
 * starts again from the first. */
static void sngl_key_move_focus(GtkWindow *win, GtkDirectionType dir) {
    if (!gtk_widget_child_focus(GTK_WIDGET(win), dir)) {
        gtk_window_set_focus(win, NULL);
        gtk_widget_child_focus(GTK_WIDGET(win), dir);
    }
}

/* sngl_text_key is key on the GtkText or GtkTextView w focus is on: what it
 * types inserted at the cursor, Enter activating an entry and starting a
 * line in a text view, and the editing keys their bindings' signals. Tab is
 * a text view's only while it takes Tab. */
static void sngl_text_key(GtkWidget *w, const char *key, bool shift) {
    if (strcmp(key, "Tab") == 0) {
        key = "\t";
    } else if (strcmp(key, "Enter") == 0) {
        if (!GTK_IS_TEXT_VIEW(w)) {
            g_signal_emit_by_name(w, "activate");
            return;
        }
        key = "\n";
    } else if (strcmp(key, "Backspace") == 0) {
        g_signal_emit_by_name(w, "backspace");
        return;
    } else if (strcmp(key, "Delete") == 0) {
        g_signal_emit_by_name(w, "delete-from-cursor", GTK_DELETE_CHARS, 1);
        return;
    } else if (strcmp(key, "ArrowLeft") == 0 || strcmp(key, "ArrowRight") == 0) {
        g_signal_emit_by_name(w, "move-cursor", GTK_MOVEMENT_VISUAL_POSITIONS, key[5] == 'L' ? -1 : 1, (gboolean)shift);
        return;
    } else if (strcmp(key, "Home") == 0 || strcmp(key, "End") == 0) {
        g_signal_emit_by_name(w, "move-cursor", GTK_MOVEMENT_DISPLAY_LINE_ENDS, key[0] == 'H' ? -1 : 1, (gboolean)shift);
        return;
    }
    /* One character, or a name that types nothing. */
    const char *end = NULL;
    if (!g_utf8_validate(key, -1, &end) || *key == '\0' || g_utf8_next_char(key) != end) return;
    g_signal_emit_by_name(w, "insert-at-cursor", key);
}

void sngl_keys_go_on(GtkWindow *win, const char *key, bool shift) {
    GtkWidget *focus = gtk_root_get_focus(GTK_ROOT(win));
    if (focus != NULL && (GTK_IS_TEXT(focus) || GTK_IS_TEXT_VIEW(focus)) &&
        (strcmp(key, "Tab") != 0 || (GTK_IS_TEXT_VIEW(focus) && gtk_text_view_get_accepts_tab(GTK_TEXT_VIEW(focus))))) {
        sngl_text_key(focus, key, shift);
        return;
    }
    if (strcmp(key, "Enter") == 0 || strcmp(key, " ") == 0) {
        if (focus != NULL) sngl_key_activate(focus);
    } else if (strcmp(key, "Tab") == 0) {
        sngl_key_move_focus(win, shift ? GTK_DIR_TAB_BACKWARD : GTK_DIR_TAB_FORWARD);
    } else if (strcmp(key, "ArrowUp") == 0) {
        sngl_key_move_focus(win, GTK_DIR_UP);
    } else if (strcmp(key, "ArrowDown") == 0) {
        sngl_key_move_focus(win, GTK_DIR_DOWN);
    } else if (strcmp(key, "ArrowLeft") == 0) {
        sngl_key_move_focus(win, GTK_DIR_LEFT);
    } else if (strcmp(key, "ArrowRight") == 0) {
        sngl_key_move_focus(win, GTK_DIR_RIGHT);
    }
}
