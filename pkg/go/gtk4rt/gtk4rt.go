//go:build !js

// Package gtk4rt is the shared GTK4 cgo runtime that SNGL's gtk4 codegen
// targets. It wraps the bounded set of GTK functions the stdlib widget set
// uses behind a typed Go API over an opaque cbind.Handle, so generated
// programs contain no cgo of their own: the GTK bindings compile once, here,
// and Go's build cache reuses them across every generated build instead of
// recompiling the gtk.h preamble per program.
//
// The callback plumbing is delegated to pkg/go/cbind (library-agnostic); the
// C trampolines below are the only GTK-specific glue, forwarding signal and
// idle callbacks to cbind.Dispatch by index.
//
// Programs that use widgets outside this bounded surface fall back to the
// legacy inline-cgo codegen path; gtk4rt intentionally covers only the stdlib
// widget vocabulary.
package gtk4rt

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <gdk/gdk.h>
#include <gsk/gsk.h>
#include <graphene.h>
#include <stdlib.h>

extern void snglGoDispatch(int idx);

// Signal trampoline. GTK invokes simple signals as (instance, user_data);
// the idx we connected with arrives as user_data, unmangled.
static void sngl_cb(gpointer instance, gpointer data) {
    (void)instance;
    snglGoDispatch(GPOINTER_TO_INT(data));
}
static void sngl_connect(void* widget, const char* signal, int idx) {
    g_signal_connect((gpointer)widget, signal, G_CALLBACK(sngl_cb), GINT_TO_POINTER(idx));
}

// Idle trampoline: fire the registered callback once, then remove the source.
static gboolean sngl_idle_tramp(gpointer data) {
    snglGoDispatch(GPOINTER_TO_INT(data));
    return G_SOURCE_REMOVE;
}
static void sngl_gtk_idle_add(int idx) {
    g_idle_add(sngl_idle_tramp, GINT_TO_POINTER(idx));
}

// Variadic wrapper — cgo cannot call g_signal_emit_by_name directly (the `...`
// trips the cgo type checker). Used by test invokers to fire a signal.
static void sngl_emit(gpointer instance, const char *signal) {
    g_signal_emit_by_name(instance, signal);
}

// Reactive-safe entry setter: GtkEditable's set_text fires "changed" even when
// the new text equals the current, which loops back through any change handler
// that rewrote the bound var. Skip the call when the value already matches.
static void sngl_set_entry_text(GtkEditable *e, const char *t) {
    const char *cur = gtk_editable_get_text(e);
    if (t == NULL) t = "";
    if (cur != NULL && strcmp(cur, t) == 0) return;
    gtk_editable_set_text(e, t);
}

// Activate trampoline forwards to the exported Go snglActivate.
extern void snglActivate(GtkApplication* app, gpointer data);

// sngl_snapshot wraps the target widget in a GdkPaintable, snapshots it into a
// render node, and rasterises via the cairo GSK renderer (no GdkSurface
// needed). Writes a PNG to path; returns 0 on success, non-zero on failure.
static int sngl_snapshot(GtkWidget *widget, int width, int height, const char *path) {
    if (widget == NULL) return 1;
    GdkPaintable *paintable = gtk_widget_paintable_new(widget);
    if (paintable == NULL) return 2;

    GtkSnapshot *snap = gtk_snapshot_new();
    gdk_paintable_snapshot(paintable, GDK_SNAPSHOT(snap), (double)width, (double)height);
    GskRenderNode *node = gtk_snapshot_free_to_node(snap);
    if (node == NULL) {
        g_object_unref(paintable);
        return 3;
    }

    GskRenderer *renderer = gsk_cairo_renderer_new();
    GError *err = NULL;
    if (!gsk_renderer_realize(renderer, NULL, &err)) {
        if (err) g_error_free(err);
        gsk_render_node_unref(node);
        g_object_unref(paintable);
        g_object_unref(renderer);
        return 4;
    }

    graphene_rect_t bounds = GRAPHENE_RECT_INIT(0, 0, (float)width, (float)height);
    GdkTexture *tex = gsk_renderer_render_texture(renderer, node, &bounds);
    if (tex == NULL) {
        gsk_renderer_unrealize(renderer);
        g_object_unref(renderer);
        gsk_render_node_unref(node);
        g_object_unref(paintable);
        return 5;
    }

    gboolean ok = gdk_texture_save_to_png(tex, path);

    g_object_unref(tex);
    gsk_renderer_unrealize(renderer);
    g_object_unref(renderer);
    gsk_render_node_unref(node);
    g_object_unref(paintable);
    return ok ? 0 : 6;
}

// Drain the default GLib main context until the widget is mapped and has a
// non-zero allocated size, or maxIter is exhausted.
static void sngl_pump_until_mapped(GtkWidget *widget, int maxIter) {
    g_main_context_iteration(NULL, TRUE);
    for (int i = 0; i < maxIter; i++) {
        if (gtk_widget_get_mapped(widget) && gtk_widget_get_width(widget) > 0) break;
        g_main_context_iteration(NULL, FALSE);
    }
    for (int i = 0; i < maxIter; i++) {
        if (!g_main_context_iteration(NULL, FALSE)) break;
    }
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"git.duckfam.us/jonathan/sngl/pkg/go/cbind"
)

// Handle re-exports cbind.Handle so generated code references a single opaque
// widget type from this package.
type Handle = cbind.Handle

// Orientation mirrors GtkOrientation.
type Orientation int

var (
	OrientationHorizontal = Orientation(C.GTK_ORIENTATION_HORIZONTAL)
	OrientationVertical   = Orientation(C.GTK_ORIENTATION_VERTICAL)
)

// p converts a handle to unsafe.Pointer at the cgo boundary.
func p(h Handle) unsafe.Pointer { return unsafe.Pointer(h) }

// widget casts a handle to *C.GtkWidget for the many functions that accept the
// base widget type.
func widget(h Handle) *C.GtkWidget { return (*C.GtkWidget)(p(h)) }

func gbool(b bool) C.gboolean {
	if b {
		return 1
	}
	return 0
}

// cstr returns a C string plus a free func; callers `defer free()`.
func cstr(s string) (*C.char, func()) {
	c := C.CString(s)
	return c, func() { C.free(unsafe.Pointer(c)) }
}

// ---- Application / windows ----

// ApplicationWindowNew creates a GtkApplicationWindow for app.
func ApplicationWindowNew(app Handle) Handle {
	return Handle(unsafe.Pointer(C.gtk_application_window_new((*C.GtkApplication)(p(app)))))
}

// WindowSetDefaultSize sets a window's default size.
func WindowSetDefaultSize(win Handle, width, height int) {
	C.gtk_window_set_default_size((*C.GtkWindow)(p(win)), C.int(width), C.int(height))
}

// WindowSetChild sets a window's single child.
func WindowSetChild(win, child Handle) {
	C.gtk_window_set_child((*C.GtkWindow)(p(win)), widget(child))
}

// WindowPresent presents (shows) a window.
func WindowPresent(win Handle) {
	C.gtk_window_present((*C.GtkWindow)(p(win)))
}

// ---- Box ----

func BoxNew(o Orientation, spacing int) Handle {
	return Handle(unsafe.Pointer(C.gtk_box_new(C.GtkOrientation(o), C.int(spacing))))
}

func BoxAppend(box, child Handle) {
	C.gtk_box_append((*C.GtkBox)(p(box)), widget(child))
}

func BoxRemove(box, child Handle) {
	C.gtk_box_remove((*C.GtkBox)(p(box)), widget(child))
}

func BoxSetSpacing(box Handle, spacing int) {
	C.gtk_box_set_spacing((*C.GtkBox)(p(box)), C.int(spacing))
}

// ---- Label ----

func LabelNew(text string) Handle {
	c, free := cstr(text)
	defer free()
	return Handle(unsafe.Pointer(C.gtk_label_new(c)))
}

func LabelSetText(label Handle, text string) {
	c, free := cstr(text)
	defer free()
	C.gtk_label_set_text((*C.GtkLabel)(p(label)), c)
}

// ---- Button ----

func ButtonNew() Handle {
	return Handle(unsafe.Pointer(C.gtk_button_new()))
}

func ButtonSetLabel(button Handle, label string) {
	c, free := cstr(label)
	defer free()
	C.gtk_button_set_label((*C.GtkButton)(p(button)), c)
}

// ---- CheckButton ----

func CheckButtonNew() Handle {
	return Handle(unsafe.Pointer(C.gtk_check_button_new()))
}

func CheckButtonSetLabel(button Handle, label string) {
	c, free := cstr(label)
	defer free()
	C.gtk_check_button_set_label((*C.GtkCheckButton)(p(button)), c)
}

func CheckButtonSetActive(button Handle, active bool) {
	C.gtk_check_button_set_active((*C.GtkCheckButton)(p(button)), gbool(active))
}

func CheckButtonGetActive(button Handle) bool {
	return C.gtk_check_button_get_active((*C.GtkCheckButton)(p(button))) != 0
}

// ---- Entry / Editable ----

func EntryNew() Handle {
	return Handle(unsafe.Pointer(C.gtk_entry_new()))
}

func EditableGetText(editable Handle) string {
	return C.GoString(C.gtk_editable_get_text((*C.GtkEditable)(p(editable))))
}

func EditableSetText(editable Handle, text string) {
	c, free := cstr(text)
	defer free()
	C.sngl_set_entry_text((*C.GtkEditable)(p(editable)), c)
}

// ---- Image ----

func ImageNew() Handle {
	return Handle(unsafe.Pointer(C.gtk_image_new()))
}

func ImageSetFromFile(image Handle, path string) {
	c, free := cstr(path)
	defer free()
	C.gtk_image_set_from_file((*C.GtkImage)(p(image)), c)
}

// ---- ScrolledWindow ----

func ScrolledWindowNew() Handle {
	return Handle(unsafe.Pointer(C.gtk_scrolled_window_new()))
}

func ScrolledWindowSetChild(sw, child Handle) {
	C.gtk_scrolled_window_set_child((*C.GtkScrolledWindow)(p(sw)), widget(child))
}

// ---- Orientable ----

func OrientableSetOrientation(o Handle, orientation Orientation) {
	C.gtk_orientable_set_orientation((*C.GtkOrientable)(p(o)), C.GtkOrientation(orientation))
}

// ---- Signals / callbacks ----

// Connect registers fn and wires it to widget's signal. fn fires on the GTK
// main thread each time the signal is emitted.
func Connect(w Handle, signal string, fn func()) {
	idx := cbind.Register(fn)
	c, free := cstr(signal)
	defer free()
	C.sngl_connect(p(w), c, C.int(idx))
}

// Emit fires a no-argument signal synchronously (used by generated test
// invokers to simulate interaction).
func Emit(w Handle, signal string) {
	c, free := cstr(signal)
	defer free()
	C.sngl_emit(C.gpointer(p(w)), c)
}

// Post schedules fn to run once on the next GLib main-loop idle tick.
func Post(fn func()) {
	idx := cbind.Register(fn)
	C.sngl_gtk_idle_add(C.int(idx))
}

//export snglGoDispatch
func snglGoDispatch(idx C.int) { cbind.Dispatch(int(idx)) }

// ---- Application run ----

var activateFn func(app Handle) Handle

//export snglActivate
func snglActivate(app *C.GtkApplication, _ C.gpointer) {
	if activateFn == nil {
		return
	}
	win := activateFn(Handle(unsafe.Pointer(app)))
	if win != nil {
		C.gtk_window_present((*C.GtkWindow)(p(win)))
	}
}

// Run boots a GtkApplication, invokes build on "activate" to construct the
// top-level window, presents it, and runs the main loop. Returns the
// application's exit status.
func Run(build func(app Handle) Handle) int {
	runtime.LockOSThread()
	activateFn = build
	app := C.gtk_application_new(nil, C.G_APPLICATION_NON_UNIQUE)
	sig, free := cstr("activate")
	defer free()
	C.g_signal_connect_data(
		C.gpointer(unsafe.Pointer(app)),
		sig,
		C.GCallback(C.snglActivate),
		nil, nil, 0,
	)
	return int(C.g_application_run((*C.GApplication)(unsafe.Pointer(app)), 0, nil))
}

// SnapshotModel builds a widget tree via build, presents it, waits for layout,
// and writes a width×height PNG of the top-level window to outPath. It requires
// an X11/Wayland display. Used by the gtk4 snapshot/test harness; the GTK
// bindings it needs are compiled once here rather than per generated program.
func SnapshotModel(build func(app Handle) Handle, width, height int, outPath string) error {
	runtime.LockOSThread()
	C.gtk_init()

	appID, freeID := cstr("dev.sngl.snapshot")
	defer freeID()
	app := C.gtk_application_new(appID, C.G_APPLICATION_NON_UNIQUE)
	defer C.g_object_unref(C.gpointer(unsafe.Pointer(app)))

	// Register without running the main loop so build + the pump execute in
	// the current (locked) OS thread without re-entrancy.
	var gerr *C.GError
	if C.g_application_register((*C.GApplication)(unsafe.Pointer(app)), nil, &gerr) == 0 {
		if gerr != nil {
			C.g_error_free(gerr)
		}
		return fmt.Errorf("g_application_register failed")
	}

	win := build(Handle(unsafe.Pointer(app)))
	if win == nil {
		return fmt.Errorf("build returned nil window")
	}
	C.gtk_window_set_default_size((*C.GtkWindow)(p(win)), C.int(width), C.int(height))
	C.gtk_window_present((*C.GtkWindow)(p(win)))
	C.sngl_pump_until_mapped(widget(win), 1000)

	cPath, freePath := cstr(outPath)
	defer freePath()
	if rc := C.sngl_snapshot(widget(win), C.int(width), C.int(height), cPath); rc != 0 {
		return fmt.Errorf("sngl_snapshot rc=%d", int(rc))
	}
	return nil
}
