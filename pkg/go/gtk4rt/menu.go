//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <stdlib.h>

extern void snglRtDispatch(int idx);
extern void snglMenuEntryGone(gpointer data, GObject *where);

// activate is (action, parameter, user_data) -> void, which the dispatcher's
// two-argument trampoline does not fit.
static void sngl_menu_activate(GSimpleAction *a, GVariant *param, gpointer data) {
    (void)a;
    (void)param;
    snglRtDispatch(GPOINTER_TO_INT(data));
}

static void sngl_menu_add(GMenu *menu, GSimpleActionGroup *group, const char *name, const char *label, gboolean enabled, gboolean checkable, gboolean checked, int idx) {
    GSimpleAction *a = checkable
        ? g_simple_action_new_stateful(name, NULL, g_variant_new_boolean(checked))
        : g_simple_action_new(name, NULL);
    g_simple_action_set_enabled(a, enabled);
    g_signal_connect(a, "activate", G_CALLBACK(sngl_menu_activate), GINT_TO_POINTER(idx));
    g_action_map_add_action(G_ACTION_MAP(group), G_ACTION(a));
    g_object_unref(a);
    char *detailed = g_strconcat("sngl.", name, NULL);
    g_menu_append(menu, label, detailed);
    g_free(detailed);
}

static void sngl_menu_watch(GObject *box, int idx) {
    g_object_weak_ref(box, snglMenuEntryGone, GINT_TO_POINTER(idx));
}
*/
import "C"

import (
	"strconv"
	"unsafe"

	"duckfam.us/sngl/pkg/go/cbind"
)

// A menu entry is one entry of a window's menu, sngl:ui's menuItem: a hidden
// box among the window's content, where it takes no room, holding the entries
// of its submenu as its children. The toplevel builds its menu from the
// entries it holds -- a GMenu behind the header bar's menu button -- and
// BoxAppend, BoxRemove and InsertBefore tell it when one comes or goes, as a
// render slot moves them; the setters say when one changed.
type menuEntry struct {
	box      Handle
	label    string
	disabled bool
	checked  bool
	onSelect func()
	// pick is the dispatcher index the entry's action activates, made once.
	pick int
	host *toplevel
}

var menuEntries = map[Handle]*menuEntry{}

// MenuEntryNew makes an entry with no label.
func MenuEntryNew() Handle {
	box := BoxNew(OrientationVertical, 0)
	C.gtk_widget_set_visible(widget(box), 0)
	e := &menuEntry{box: box}
	e.pick = cbind.Register(e.selected)
	menuEntries[box] = e
	gone := cbind.Register(func() {
		delete(menuEntries, box)
		cbind.Release(e.pick)
	})
	C.sngl_menu_watch((*C.GObject)(p(box)), C.int(gone))
	return box
}

//export snglMenuEntryGone
func snglMenuEntryGone(data C.gpointer, _ *C.GObject) {
	cbind.DispatchOnce(int(uintptr(data)))
}

// MenuEntrySetLabel sets what the entry shows.
func MenuEntrySetLabel(h Handle, label string) {
	if e := menuEntries[h]; e != nil && e.label != label {
		e.label = label
		e.changed()
	}
}

// MenuEntrySetDisabled sets whether the entry can be picked.
func MenuEntrySetDisabled(h Handle, v bool) {
	if e := menuEntries[h]; e != nil && e.disabled != v {
		e.disabled = v
		e.changed()
	}
}

// MenuEntrySetChecked sets whether the entry shows a check.
func MenuEntrySetChecked(h Handle, v bool) {
	if e := menuEntries[h]; e != nil && e.checked != v {
		e.checked = v
		e.changed()
	}
}

// MenuEntryOnSelect is the entry's `@select`.
func MenuEntryOnSelect(h Handle, fn func()) {
	if e := menuEntries[h]; e != nil {
		e.onSelect = fn
	}
}

// MenuEntrySelect picks the entry, as its action does; for tests.
func MenuEntrySelect(h Handle) {
	if e := menuEntries[h]; e != nil {
		e.selected()
	}
}

func (e *menuEntry) selected() {
	if !e.disabled && e.onSelect != nil {
		e.onSelect()
	}
}

func (e *menuEntry) changed() {
	if e.host != nil {
		e.host.applyMenu()
	}
}

func (e *menuEntry) adopt(t *toplevel) {
	e.host = t
	for _, k := range entriesIn(e.box) {
		k.adopt(t)
	}
}

func entriesIn(box Handle) []*menuEntry {
	var out []*menuEntry
	for _, c := range children(box) {
		if e := menuEntries[c]; e != nil {
			out = append(out, e)
		}
	}
	return out
}

// menuMoved tells the window holding box that an entry or a way back came or
// went.
func menuMoved(box, child Handle) {
	switch {
	case backs[child] != nil:
		if t := topOf(box); t != nil {
			t.applyBar()
		}
	case menuEntries[child] != nil:
		if t := topOf(box); t != nil {
			t.applyMenu()
		} else if e := menuEntries[box]; e != nil {
			e.changed()
		}
	}
}

// A back is a window's way back, while it can go back: a hidden widget among
// the window's content, which has the window show a back button -- in its
// header bar where it has one, over the content where it has none -- that
// presses it.
type back struct {
	onPress func()
}

var backs = map[Handle]*back{}

// BackNew makes a way back.
func BackNew() Handle {
	h := Nothing()
	b := &back{}
	backs[h] = b
	gone := cbind.Register(func() { delete(backs, h) })
	C.sngl_menu_watch((*C.GObject)(p(h)), C.int(gone))
	return h
}

// BackOnPress is the way back's handler.
func BackOnPress(h Handle, fn func()) {
	if b := backs[h]; b != nil {
		b.onPress = fn
	}
}

// BackPress presses the way back; for tests.
func BackPress(h Handle) {
	if b := backs[h]; b != nil && b.onPress != nil {
		b.onPress()
	}
}

// press is the window's back button pressed: the first way back among its
// content.
func (t *toplevel) press() {
	for _, c := range children(t.box) {
		if b := backs[c]; b != nil {
			if b.onPress != nil {
				b.onPress()
			}
			return
		}
	}
}

func (t *toplevel) canGoBack() bool {
	for _, c := range children(t.box) {
		if backs[c] != nil {
			return true
		}
	}
	return false
}

// applyBar gives the window its header bar as it is made, where it has a
// menu or a way back by then: GTK sets a title bar only on a window not yet
// shown. The back button goes in the bar, or over the content of a window
// made without one, and shows while a way back is among the content.
func (t *toplevel) applyBar() {
	if t.win == nil {
		return
	}
	entries := entriesIn(t.box)
	if !t.barDecided {
		t.barDecided = true
		if len(entries) > 0 || t.canGoBack() {
			t.bar = C.gtk_header_bar_new()
			C.gtk_window_set_titlebar(t.win, t.bar)
			t.menuButton = C.gtk_menu_button_new()
			name, free := cstr("open-menu-symbolic")
			C.gtk_menu_button_set_icon_name((*C.GtkMenuButton)(unsafe.Pointer(t.menuButton)), name)
			free()
			C.gtk_header_bar_pack_end((*C.GtkHeaderBar)(unsafe.Pointer(t.bar)), t.menuButton)
		}
	}
	if t.backButton == nil && t.canGoBack() {
		name, free := cstr("go-previous-symbolic")
		t.backButton = C.gtk_button_new_from_icon_name(name)
		free()
		Connect(Handle(unsafe.Pointer(t.backButton)), "clicked", t.press)
		if t.bar != nil {
			C.gtk_header_bar_pack_start((*C.GtkHeaderBar)(unsafe.Pointer(t.bar)), t.backButton)
		} else {
			C.gtk_widget_set_halign(t.backButton, C.GTK_ALIGN_START)
			C.gtk_box_prepend((*C.GtkBox)(p(t.box)), t.backButton)
		}
	}
	if t.backButton != nil {
		C.gtk_widget_set_visible(t.backButton, gbool(t.canGoBack()))
	}
	if t.menuButton != nil {
		C.gtk_widget_set_visible(t.menuButton, gbool(len(entries) > 0))
	}
}

// applyMenu builds the window's menu from the entries among its content: a
// submenu per top-level entry holding entries, or the entry itself where it
// holds none. A submenu where one entry is checked draws them all as checks.
func (t *toplevel) applyMenu() {
	top := entriesIn(t.box)
	for _, e := range top {
		e.adopt(t)
	}
	t.applyBar()
	if t.win == nil || t.menuButton == nil {
		return
	}
	menu := C.g_menu_new()
	group := C.g_simple_action_group_new()
	n := 0
	add := func(into *C.GMenu, e *menuEntry, checkable bool) {
		name, freeName := cstr("e" + strconv.Itoa(n))
		label, freeLabel := cstr(e.label)
		C.sngl_menu_add(into, group, name, label, gbool(!e.disabled), gbool(checkable), gbool(e.checked), C.int(e.pick))
		freeName()
		freeLabel()
		n++
	}
	for _, e := range top {
		kids := entriesIn(e.box)
		if len(kids) == 0 {
			add(menu, e, e.checked)
			continue
		}
		checkable := false
		for _, k := range kids {
			checkable = checkable || k.checked
		}
		sub := C.g_menu_new()
		for _, k := range kids {
			add(sub, k, checkable)
		}
		label, free := cstr(e.label)
		C.g_menu_append_submenu(menu, label, (*C.GMenuModel)(unsafe.Pointer(sub)))
		free()
		C.g_object_unref(C.gpointer(unsafe.Pointer(sub)))
	}
	prefix, free := cstr("sngl")
	C.gtk_widget_insert_action_group((*C.GtkWidget)(unsafe.Pointer(t.win)), prefix, (*C.GActionGroup)(unsafe.Pointer(group)))
	free()
	C.gtk_menu_button_set_menu_model((*C.GtkMenuButton)(unsafe.Pointer(t.menuButton)), (*C.GMenuModel)(unsafe.Pointer(menu)))
	C.g_object_unref(C.gpointer(unsafe.Pointer(group)))
	C.g_object_unref(C.gpointer(unsafe.Pointer(menu)))
}
