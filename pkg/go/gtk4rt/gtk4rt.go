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
extern void snglGoDispatchOnce(int idx);

// Signal trampoline. GTK invokes simple signals as (instance, user_data);
// the idx we connected with arrives as user_data, unmangled.
static void sngl_cb(gpointer instance, gpointer data) {
    (void)instance;
    snglGoDispatch(GPOINTER_TO_INT(data));
}
// A property notification is (instance, pspec, user_data).
static void sngl_notify_cb(gpointer instance, GParamSpec *pspec, gpointer data) {
    (void)instance;
    (void)pspec;
    snglGoDispatch(GPOINTER_TO_INT(data));
}
static void sngl_connect(void* widget, const char* signal, int idx) {
    GCallback cb = g_str_has_prefix(signal, "notify::") ? G_CALLBACK(sngl_notify_cb) : G_CALLBACK(sngl_cb);
    g_signal_connect((gpointer)widget, signal, cb, GINT_TO_POINTER(idx));
}

// Idle trampoline: fire the registered callback once, then remove the source.
// DispatchOnce, not Dispatch: the source is gone after this and nothing will
// ever quote the index again, so the closure has to be dropped here or every
// Post leaks one.
static gboolean sngl_idle_tramp(gpointer data) {
    snglGoDispatchOnce(GPOINTER_TO_INT(data));
    return G_SOURCE_REMOVE;
}
static void sngl_gtk_idle_add(int idx) {
    g_idle_add(sngl_idle_tramp, GINT_TO_POINTER(idx));
}

// G_SOURCE_CONTINUE where the idle trampoline returns G_SOURCE_REMOVE: a timer
// is the same dispatch wired to a source that stays armed. Cancelling is
// g_source_remove on the id this returns.
static gboolean sngl_timeout_tramp(gpointer data) {
    snglGoDispatch(GPOINTER_TO_INT(data));
    return G_SOURCE_CONTINUE;
}
static guint sngl_gtk_timeout_add(int ms, int idx) {
    return g_timeout_add(ms, sngl_timeout_tramp, GINT_TO_POINTER(idx));
}
static void sngl_gtk_source_remove(guint id) {
    if (id != 0) g_source_remove(id);
}

static gboolean sngl_pump_tick(gpointer data);

// Runs the default main context until the deadline. Blocking iterations, with
// a tick source to keep one from parking past it -- the same shape
// sngl_pump_until_mapped uses, and for the same reason: a non-blocking poll
// returns immediately and is not a wait at all.
static void sngl_pump_for(int ms) {
    gint64 deadline = g_get_monotonic_time() + (gint64)ms * 1000;
    guint tick = g_timeout_add(5, sngl_pump_tick, NULL);
    while (g_get_monotonic_time() < deadline) {
        g_main_context_iteration(NULL, TRUE);
    }
    g_source_remove(tick);
}

// A bare GLib main loop, for exercising the idle source above without a
// display. See mainLoopNew below.
static GMainLoop *sngl_loop;
static void sngl_main_loop_new(void)  { sngl_loop = g_main_loop_new(NULL, FALSE); }
static void sngl_main_loop_run(void)  { g_main_loop_run(sngl_loop); }
static void sngl_main_loop_quit(void) { g_main_loop_quit(sngl_loop); }

// Variadic wrapper — cgo cannot call g_signal_emit_by_name directly (the `...`
// trips the cgo type checker). Used by test invokers to fire a signal.
static void sngl_emit(gpointer instance, const char *signal) {
    g_signal_emit_by_name(instance, signal);
}

// A program's write to an entry is not input, so "changed" is blocked: a
// handler rewriting its own bound var with a new value would otherwise
// re-enter itself from inside the emission until the stack ran out.
static void sngl_set_entry_text_quiet(GtkEditable *e, const char *t);

static void sngl_set_entry_text(GtkEditable *e, const char *t) {
    const char *cur = gtk_editable_get_text(e);
    if (t == NULL) t = "";
    if (cur != NULL && strcmp(cur, t) == 0) return;
    sngl_set_entry_text_quiet(e, t);
}

static void sngl_set_entry_text_quiet(GtkEditable *e, const char *t) {
    guint id = g_signal_lookup("changed", GTK_TYPE_EDITABLE);
    g_signal_handlers_block_matched(e, G_SIGNAL_MATCH_ID, id, 0, NULL, NULL, NULL);
    gtk_editable_set_text(e, t);
    g_signal_handlers_unblock_matched(e, G_SIGNAL_MATCH_ID, id, 0, NULL, NULL, NULL);
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

// Waits for map *and* a non-zero allocation: the compositor sends the map
// event before GTK has run size-allocate, and a zero-width widget snapshots to
// an empty (NULL) render node.
//
// The wait is a wall-clock deadline over blocking iterations because a
// non-blocking poll is not a wait at all — 1000 rounds of it returned in under
// 2ms on a quiet socket, so under load the allocation had simply not arrived.
// The tick source only keeps a blocking iteration from parking past the
// deadline.
static gboolean sngl_pump_tick(gpointer data) { return G_SOURCE_CONTINUE; }

static void sngl_pump_until_mapped(GtkWidget *widget, int timeoutMs) {
    gint64 deadline = g_get_monotonic_time() + (gint64)timeoutMs * 1000;
    guint tick = g_timeout_add(5, sngl_pump_tick, NULL);
    while (!(gtk_widget_get_mapped(widget) && gtk_widget_get_width(widget) > 0)) {
        if (g_get_monotonic_time() >= deadline) break;
        g_main_context_iteration(NULL, TRUE);
    }
    g_source_remove(tick);
    for (int i = 0; i < 1000; i++) {
        if (!g_main_context_iteration(NULL, FALSE)) break;
    }
}
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"git.duckfam.us/jonathan/sngl/pkg/go/cbind"
	"git.duckfam.us/jonathan/sngl/pkg/go/snglcolor"
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
// WindowSetTitle sets a window's title bar text.
func WindowSetTitle(win Handle, title string) {
	cs := C.CString(title)
	defer C.free(unsafe.Pointer(cs))
	C.gtk_window_set_title((*C.GtkWindow)(unsafe.Pointer(win)), cs)
}

func WindowSetChild(win, child Handle) {
	C.gtk_window_set_child((*C.GtkWindow)(p(win)), widget(child))
}

// WindowPresent shows a window and raises it.
func WindowPresent(win Handle) {
	C.gtk_window_present((*C.GtkWindow)(p(win)))
}

// Retain takes a strong reference on a widget, so that removing it from its
// parent does not free it.
func Retain(h Handle) {
	C.g_object_ref_sink(C.gpointer(p(h)))
}

// Release drops the reference Retain took.
func Release(h Handle) {
	C.g_object_unref(C.gpointer(p(h)))
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

// SlotAnchor returns the anchor a render slot inserts its entries before: a,
// when box holds it, or a new hidden child appended to box. The slot's first
// render runs while box is being built, at the position the slot was written.
func SlotAnchor(box, a Handle) Handle {
	if a != nil && C.gtk_widget_get_parent(widget(a)) == widget(box) {
		return a
	}
	w := C.gtk_label_new(nil)
	C.gtk_widget_set_visible(w, 0)
	C.g_object_ref_sink(C.gpointer(unsafe.Pointer(w)))
	C.gtk_box_append((*C.GtkBox)(p(box)), w)
	return Handle(unsafe.Pointer(w))
}

// SlotBox is the box a render slot in a single-child container renders into:
// b, or a new one this package holds a reference on.
func SlotBox(b Handle) Handle {
	if b != nil {
		return b
	}
	b = BoxNew(OrientationVertical, 6)
	Retain(b)
	return b
}

// ParentOf is w's parent widget, or nil.
func ParentOf(w Handle) Handle {
	return Handle(unsafe.Pointer(C.gtk_widget_get_parent(widget(w))))
}

// InsertBefore puts child into box immediately before anchor, or at the end
// of box when box does not hold anchor.
func InsertBefore(box, anchor, child Handle) {
	if anchor == nil || C.gtk_widget_get_parent(widget(anchor)) != widget(box) {
		BoxAppend(box, child)
		return
	}
	prev := C.gtk_widget_get_prev_sibling(widget(anchor))
	C.gtk_box_insert_child_after((*C.GtkBox)(p(box)), widget(child), prev)
}

func children(parent Handle) []Handle {
	var out []Handle
	for w := C.gtk_widget_get_first_child(widget(parent)); w != nil; w = C.gtk_widget_get_next_sibling(w) {
		out = append(out, Handle(unsafe.Pointer(w)))
	}
	return out
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

// EditableSetTextQuiet sets the text with "changed" blocked, for a test
// invoker that then fires the one signal it drives.
func EditableSetTextQuiet(editable Handle, text string) {
	c, free := cstr(text)
	defer free()
	C.sngl_set_entry_text_quiet((*C.GtkEditable)(p(editable)), c)
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

// ---- Frame ----

func FrameNew() Handle {
	return Handle(unsafe.Pointer(C.gtk_frame_new(nil)))
}

func FrameSetChild(f, child Handle) {
	C.gtk_frame_set_child((*C.GtkFrame)(p(f)), widget(child))
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

// Schedule is one armed GLib timeout source. gtk4.sngl names it as an opaque
// SNGL type, so the caller holds the schedule itself: the source id and the
// cbind slot it dispatches through travel together rather than being paired in
// a table here.
//
// Such a source already runs its callback on the main thread, so a tick body
// mutates the model in the same place a signal handler does and needs no
// marshalling of its own.
//
// The cbind index is not the same kind of thing and stays an index: a GLib
// callback carries an `int` user_data and cannot hold a Go pointer at all, so
// that registry is the C ABI rather than a name this language could not spell.
type Schedule struct {
	id  C.guint
	idx int
}

// Every schedules fn on the GLib main loop every ms milliseconds until the
// returned schedule is cancelled. A period of zero or less arms nothing and
// answers nil, which Cancel accepts.
func Every(ms int, fn func()) *Schedule {
	if ms <= 0 {
		return nil
	}
	idx := cbind.Register(fn)
	return &Schedule{id: C.sngl_gtk_timeout_add(C.int(ms), C.int(idx)), idx: idx}
}

// Cancel removes the source and drops its callback. A nil receiver and a second
// call are both no-ops, so an unmount need not track whether a mount ever armed
// one.
//
// Releasing matters because the timer is an `effect`: it mounts again on every
// gate toggle and every interval change, where it used to be armed once from
// New. A registration nothing releases retains the closure, and through it the
// whole Model, for the life of the process.
func (s *Schedule) Cancel() {
	if s == nil {
		return
	}
	// The slot is released even where no source was ever added. Register runs
	// before g_timeout_add answers, so an id of 0 still has a registration
	// behind it -- returning early there retains the closure, and through it
	// the whole Model, for the life of the process.
	if s.id != 0 {
		C.sngl_gtk_source_remove(s.id)
		s.id = 0
	}
	cbind.Release(s.idx)
}

// PumpFor runs the GLib main loop for ms milliseconds and returns. It is the
// seam a test needs to observe anything the loop drives -- a timer above all --
// without opening a window and never coming back.
func PumpFor(ms int) {
	if ms <= 0 {
		return
	}
	C.sngl_pump_for(C.int(ms))
}

// A bare GLib main loop, which is what Post's idle source is scheduled
// against. It exists so Post can be tested at all: an idle source only fires
// while something pumps the loop, and generated programs get theirs from
// gtk_application_run, which needs a display. cgo is not allowed in a _test.go
// file, so the three calls live here.
func mainLoopNew()  { C.sngl_main_loop_new() }
func mainLoopRun()  { C.sngl_main_loop_run() }
func mainLoopQuit() { C.sngl_main_loop_quit() }

// Post schedules fn to run once on the next GLib main-loop idle tick. It is
// what this platform's async.post emitter calls rather than a second answer
// beside it: a C callback cannot carry a Go closure, so there is no spelling of
// an idle source for that emitter to inline.
func Post(fn func()) {
	idx := cbind.Register(fn)
	C.sngl_gtk_idle_add(C.int(idx))
}

//export snglGoDispatch
func snglGoDispatch(idx C.int) { cbind.Dispatch(int(idx)) }

//export snglGoDispatchOnce
func snglGoDispatchOnce(idx C.int) { cbind.DispatchOnce(int(idx)) }

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

// Init initializes GTK. It is idempotent (gtk_init may be called repeatedly)
// and is used by the test-agent harness to materialise widgets outside a
// running application (e.g. to fire event invokers against real GTK objects).
func Init() { C.gtk_init() }

// SnapshotModel builds a widget tree via build, presents it, waits for layout,
// and writes a width×height PNG of the top-level window to outPath. It requires
// an X11/Wayland display.
func SnapshotModel(build func(app Handle) Handle, width, height int, outPath string) error {
	data, err := SnapshotModelBytes(build, width, height)
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, data, 0o644)
}

// SnapshotModelBytes is SnapshotModel returning the PNG as bytes rather than
// writing a file. Used by the test-agent snapshot bridge. The GTK bindings it
// needs are compiled once here rather than per generated program.
func SnapshotModelBytes(build func(app Handle) Handle, width, height int) ([]byte, error) {
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
		return nil, fmt.Errorf("g_application_register failed")
	}

	win := build(Handle(unsafe.Pointer(app)))
	if win == nil {
		return nil, fmt.Errorf("build returned nil window")
	}
	C.gtk_window_set_default_size((*C.GtkWindow)(p(win)), C.int(width), C.int(height))
	C.gtk_window_present((*C.GtkWindow)(p(win)))
	C.sngl_pump_until_mapped(widget(win), 1000)

	f, err := os.CreateTemp("", "sngl-snap-*.png")
	if err != nil {
		return nil, err
	}
	f.Close()
	defer os.Remove(f.Name())

	cPath, freePath := cstr(f.Name())
	defer freePath()
	if rc := C.sngl_snapshot(widget(win), C.int(width), C.int(height), cPath); rc != 0 {
		return nil, fmt.Errorf("sngl_snapshot rc=%d", int(rc))
	}
	return os.ReadFile(f.Name())
}

// ---- Rich text ----

// When is the markup of runs an `if` in a flow guards: s when cond holds.
func When(cond bool, s string) string {
	if cond {
		return s
	}
	return ""
}

// Escape is Pango markup's escaping, for the words an author wrote.
//
// A flow's markup is assembled as a Go string, so every run that is not a
// literal the emitter could escape at build time is escaped here instead.
// The five characters are the ones g_markup_escape_text answers for: the
// three that open and close an element or an entity, and the two quotes,
// which matter because a run's words may end up inside an attribute.
func Escape(s string) string {
	if !strings.ContainsAny(s, `&<>"'`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Foreground is a span's ` foreground="…"` attribute for a color the build
// could not read, or nothing for the color that means the run set none.
func Foreground(c snglcolor.Color) string {
	if c.A == 0 {
		return ""
	}
	return fmt.Sprintf(` foreground="#%02X%02X%02X"`, c.R, c.G, c.B)
}

// LabelSetMarkup sets a label's text from Pango markup, which is what makes a
// flow of rich text one widget rather than a box of them.
func LabelSetMarkup(label Handle, markup string) {
	c, free := cstr(markup)
	defer free()
	C.gtk_label_set_markup((*C.GtkLabel)(p(label)), c)
}

// LabelSetWrap turns on line breaking. A flow is a paragraph, and a label that
// does not wrap is one very long line.
func LabelSetWrap(label Handle, wrap bool) {
	C.gtk_label_set_wrap((*C.GtkLabel)(p(label)), gbool(wrap))
}

// LabelSetXAlign puts the words at the start of the line rather than centred,
// which is what a paragraph is and what every other target does with one.
func LabelSetXAlign(label Handle, align float64) {
	C.gtk_label_set_xalign((*C.GtkLabel)(p(label)), C.float(align))
}
