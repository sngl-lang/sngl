package gtk4

import (
	"fmt"
	"io"
	"strings"

	"duckfam.us/sngl/codegen"
)

// model.go and callbacks.go are written straight to their FileEmitters
// rather than through text/template: text/template looks names up with
// reflect.Value.MethodByName, and a binary that can reach that call keeps
// every exported method of every type it holds -- half the playground's
// wasm. The emitter writes the package clause, the cgo preamble and the
// import block; what is written here is the body.

// goFile is a generated Go file before its FileEmitter assembles it.
type goFile struct {
	cgoPreamble string
	imports     []string
	body        string
}

// write emits f as the file name in sink.
func (f goFile) write(sink codegen.Sink, name, pkg string, req *codegen.Request) error {
	e := req.Lang.NewFileEmitter(sink, codegen.FileOptions{
		Name:        name,
		Source:      req.Source,
		Platform:    "gtk4",
		Maps:        req.Maps,
		PackageName: pkg,
		CgoPreamble: f.cgoPreamble,
	})
	for _, imp := range f.imports {
		e.RequireImport(imp)
	}
	if _, err := io.WriteString(e, f.body); err != nil {
		e.Close()
		return err
	}
	return e.Close()
}

// modelPreamble is model.go's cgo preamble, with its import "C"; empty in
// wrapped mode, which has none.
func modelPreamble(d scaffoldData) string {
	if d.Wrapped {
		return ""
	}
	var b strings.Builder
	b.Grow(len(cgoModelHead) + len(d.CLinks) + len(cgoPreamble) + len(cgoSlotAnchor) + len(cgoEntryText) + len(cgoGObjectSet) + len(cgoCanvas) + len(cgoModelTail))
	b.WriteString(cgoModelHead)
	b.WriteString(d.CLinks)
	b.WriteString(cgoPreamble)
	if d.NeedsSlotAnchor {
		b.WriteString(cgoSlotAnchor)
	}
	b.WriteString(cgoEntryText)
	if d.NeedsGObjectSet {
		b.WriteString(cgoGObjectSet)
	}
	if d.HasCanvas {
		b.WriteString(cgoCanvas)
	}
	b.WriteString(cgoModelTail)
	return b.String()
}

const (
	cgoModelHead = `/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <stdlib.h>
`
	cgoModelTail = `
*/
import "C"`
)

// writeModelHead writes the top of model.go's body: helpers, the Model
// struct, New and the accessors. The widget-building code follows it.
func writeModelHead(w io.Writer, d scaffoldData) {
	io.WriteString(w, d.LangHelpers)
	if d.NeedsCalendarDate {
		io.WriteString(w, calendarDateHelpers)
	}
	if d.NeedsDropDown {
		if d.Wrapped {
			io.WriteString(w, dropDownOfferWrapped)
		} else {
			io.WriteString(w, dropDownOfferCgo)
		}
	}
	if d.NeedsBoolToInt {
		io.WriteString(w, `
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
`)
	}
	io.WriteString(w, "\n\n")
	io.WriteString(w, d.UnitDecls)
	for _, s := range d.Structs {
		fmt.Fprintf(w, "\ntype %s struct {", s.Name)
		for _, f := range s.Fields {
			fmt.Fprintf(w, "\n\t%s %s", f.Name, f.Type)
		}
		io.WriteString(w, "\n}\n\n")
	}
	io.WriteString(w, "\n// Model holds the state and widget references for this SNGL UI.\ntype Model struct {")
	for _, bd := range d.Binds {
		fmt.Fprintf(w, "\n\t%s %s", bd.Name, bd.GoType)
	}
	for _, f := range d.WidgetFields {
		fmt.Fprintf(w, "\n\t%s %s", f.Name, f.GoType)
	}
	io.WriteString(w, `
}

// New creates a Model with default values. Binds initialize
// sequentially so later inits can reference earlier `+"`m.<name>`"+` fields.
func New() *Model {
	m := &Model{}`)
	for _, bd := range d.Binds {
		fmt.Fprintf(w, "\n\tm.%s = %s", bd.Name, bd.InitVal)
	}
	io.WriteString(w, "\n\treturn m\n}\n\n")
	for _, c := range d.Computeds {
		fmt.Fprintf(w, "\nfunc (m *Model) %s() %s {\n%s\n}\n\n", c.Name, c.GoType, c.Body)
	}
	io.WriteString(w, d.FunctionCode)
	for _, bd := range d.Binds {
		if bd.NoAccessors {
			continue
		}
		fmt.Fprintf(w, `
func (m *Model) %[1]s() %[2]s {
	return m.%[3]s
}

func (m *Model) Set%[1]s(v %[2]s) {
	m.%[3]s = v
}

`, bd.Getter, bd.GoType, bd.Name)
	}
	io.WriteString(w, "\n")
}

// callbacksImports is what callbacks.go's own code imports; main() is
// written into it too, and needs runtime, fmt and os under cgo.
func callbacksImports(d scaffoldData) []string {
	switch {
	case d.Wrapped && d.Main:
		return []string{gtk4rtPkg}
	case d.Wrapped:
		return nil
	case d.Main:
		return []string{"unsafe", "runtime", "fmt", "os"}
	}
	return []string{"unsafe"}
}

// callbacksPreamble is callbacks.go's cgo preamble; empty in wrapped mode.
func callbacksPreamble(d scaffoldData) string {
	if d.Wrapped {
		return ""
	}
	return `/*
#include <gtk/gtk.h>

extern void sngl_gtk_idle_add(void* fn);
extern void snglActivate(GtkApplication* app, gpointer data);
*/
import "C"`
}

// writeCallbacksHead writes the exported trampolines GTK's C side calls back
// into Go through; nothing in wrapped mode, where gtk4rt holds them.
func writeCallbacksHead(w io.Writer, d scaffoldData) {
	if d.Wrapped {
		return
	}
	io.WriteString(w, `// snglCallbacks holds registered Go signal handlers indexed by int.
var snglCallbacks []func()

// The index arrives as GTK's user_data pointer round-tripped through
// GPOINTER_TO_INT, so a stray pointer lands here as an arbitrary int —
// negative included. Bound it on both sides. (//export must sit directly
// above the declaration, so this note goes above it.)
//
//export snglGoDispatch
func snglGoDispatch(idx C.int) {
	if idx >= 0 && int(idx) < len(snglCallbacks) {
		snglCallbacks[int(idx)]()
	}
}

//export snglIdleCallback
func snglIdleCallback(ptr unsafe.Pointer) {
	fn := *(*func())(ptr)
	fn()
}

func gtkPost(fn func()) {
	f := fn
	C.sngl_gtk_idle_add(unsafe.Pointer(&f))
}`)
	if d.HasCanvas {
		io.WriteString(w, `

// snglDrawFuncs holds registered cairo draw closures indexed by int. Each
// canvas's GtkDrawingArea registers one closure that invokes the model's
// _canvasDrawN(cr) method.
// The allocated size travels with the context: a canvas that scales needs the
// size it is being drawn at, and GTK hands it to the draw callback.
var snglDrawFuncs []func(*C.cairo_t, int, int)

//export sngl_draw_dispatch
func sngl_draw_dispatch(idx C.int, cr *C.cairo_t, width, height C.int) {
	if idx >= 0 && int(idx) < len(snglDrawFuncs) {
		snglDrawFuncs[int(idx)](cr, int(width), int(height))
	}
}`)
	}
	io.WriteString(w, "\n")
}

// cgoPreamble follows the c.link lines at the top of model.go's preamble.
const cgoPreamble = `
extern void snglGoDispatch(int idx);
// g_signal_connect callbacks are invoked with the signal's signature
// (self, ...args, user_data). This one is (instance, user_data), which
// is the signature of a void signal that carries no arguments of its
// own — and gir.Signal.Connectable is what holds every connection to
// that shape, since otherwise GTK would pass the signal's first real
// argument into ` + "`data`" + ` and snglGoDispatch would fire a wildly wrong
// callback. 155 of GTK's 400 signals qualify; the rest are neither
// declared nor connectable.
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

extern void snglIdleCallback(void* fn);
static gboolean sngl_idle_tramp(gpointer data) {
    snglIdleCallback(data);
    return G_SOURCE_REMOVE;
}
void sngl_gtk_idle_add(void* fn) {
    g_idle_add(sngl_idle_tramp, fn);
}

// Variadic wrapper — cgo cannot call ` + "`g_signal_emit_by_name`" + ` directly
// (the ` + "`...`" + ` in its prototype trips the cgo type checker). Used by
// per-id test invokers to fire signals with no extra args.
static void sngl_emit(gpointer instance, const char *signal) {
    g_signal_emit_by_name(instance, signal);
}

// Reactive-safe entry setter — GtkEditable's set_text fires "changed"
// even when the new text equals the current text, which loops back
// through any change handler that wrote the mutating var and re-applies
// reactivity, blowing the stack. Skip the call when the value already
// matches.
// g_object_unref takes a gpointer, and a declaration that names it directly
// would need a cast a SNGL override has no way to write. The shim is where the
// cast belongs -- it is C, which is what this preamble is.
static void sngl_unref_pixbuf(GdkPixbuf *p) {
    if (p != NULL) g_object_unref(p);
}

// Selects a day the way a click on it does, for gtk4's datepicker override
// (selectDay) and a test's invoker. select_day is the call every GTK 4 has;
// 4.20 deprecates it for set_date, which the older ones lack.
// The selection is the program's, not input, so "day-selected" is blocked;
// a test's invoker fires it itself after selecting.
static void sngl_calendar_select(GtkCalendar *c, int y, int m, int d) {
    GDateTime *dt = g_date_time_new_utc(y, m, d, 0, 0, 0);
    if (dt == NULL) return;
    guint id = g_signal_lookup("day-selected", GTK_TYPE_CALENDAR);
    g_signal_handlers_block_matched(c, G_SIGNAL_MATCH_ID, id, 0, NULL, NULL, NULL);
    G_GNUC_BEGIN_IGNORE_DEPRECATIONS
    gtk_calendar_select_day(c, dt);
    G_GNUC_END_IGNORE_DEPRECATIONS
    g_signal_handlers_unblock_matched(c, G_SIGNAL_MATCH_ID, id, 0, NULL, NULL, NULL);
    g_date_time_unref(dt);
}`

const (
	// dropDownOfferWrapped is gtk4's select offering its choices
	// (offerChoices) through gtk4rt.
	dropDownOfferWrapped = `
// snglDropDownOffer is gtk4's select offering its choices (offerChoices).
func snglDropDownOffer(d gtk4rt.Handle, labels, chosen string) {
	gtk4rt.DropDownOffer(d, labels, chosen)
}
`
	// dropDownOfferCgo is dropDownOfferWrapped for a cgo drop-down: the C
	// that offers is gtk4rt's in either mode.
	dropDownOfferCgo = `
// snglDropDownOffer is gtk4's select offering its choices (offerChoices).
func snglDropDownOffer(d *C.GtkDropDown, labels, chosen string) {
	gtk4rt.DropDownOffer(gtk4rt.Handle(unsafe.Pointer(d)), labels, chosen)
}
`
)

const cgoSlotAnchor = `

static GtkWidget *sngl_slot_anchor(GtkBox *box, GtkWidget *a) {
    if (a != NULL && gtk_widget_get_parent(a) == GTK_WIDGET(box)) return a;
    GtkWidget *w = gtk_label_new(NULL);
    gtk_widget_set_visible(w, FALSE);
    g_object_ref_sink(w);
    gtk_box_append(box, w);
    return w;
}

static GtkBox *sngl_slot_box(GtkBox *b) {
    if (b != NULL) return b;
    GtkWidget *w = gtk_box_new(GTK_ORIENTATION_VERTICAL, 6);
    g_object_ref_sink(w);
    return GTK_BOX(w);
}`

const cgoEntryText = `

static void sngl_set_entry_text(GtkEditable *e, const char *t) {
    const char *cur = gtk_editable_get_text(e);
    if (t == NULL) t = "";
    if (cur != NULL && strcmp(cur, t) == 0) return;
    gtk_editable_set_text(e, t);
}

// A test invoker puts the payload's text in the entry and then fires the one
// signal it drives, so the text is set with "changed" blocked: setting it would
// otherwise run the input handler as well.
static void sngl_set_entry_text_quiet(GtkEditable *e, const char *t) {
    guint id = g_signal_lookup("changed", GTK_TYPE_EDITABLE);
    g_signal_handlers_block_matched(e, G_SIGNAL_MATCH_ID, id, 0, NULL, NULL, NULL);
    gtk_editable_set_text(e, t);
    g_signal_handlers_unblock_matched(e, G_SIGNAL_MATCH_ID, id, 0, NULL, NULL, NULL);
}`

const cgoGObjectSet = `

// A GTK property with no setter function of its own is set by name through
// GObject. g_object_set is variadic, which cgo cannot call at all, so the
// value goes through g_object_set_property — which takes a GValue of the
// property's own GType. Only the four scalar GValues below are built here;
// g_value_transform lands each on whatever the class's GParamSpec declares,
// which is how an enum, a float or an unsigned property is reached from one
// of them. A construct-only property fails inside GTK with a warning, since
// only the class knows which those are.
static void sngl_set_prop(void *obj, const char *name, GValue *src) {
    GObject *o = G_OBJECT(obj);
    GParamSpec *ps = g_object_class_find_property(G_OBJECT_GET_CLASS(o), name);
    if (ps == NULL) {
        g_warning("sngl: %s has no property \"%s\"", G_OBJECT_TYPE_NAME(o), name);
    } else if (g_value_type_compatible(G_VALUE_TYPE(src), ps->value_type)) {
        g_object_set_property(o, name, src);
    } else {
        GValue dst = G_VALUE_INIT;
        g_value_init(&dst, ps->value_type);
        if (g_value_transform(src, &dst)) {
            g_object_set_property(o, name, &dst);
        } else {
            g_warning("sngl: cannot set %s.%s from a %s", G_OBJECT_TYPE_NAME(o), name, G_VALUE_TYPE_NAME(src));
        }
        g_value_unset(&dst);
    }
    g_value_unset(src);
}
static void sngl_set_prop_string(void *obj, const char *name, const char *v) {
    GValue src = G_VALUE_INIT;
    g_value_init(&src, G_TYPE_STRING);
    g_value_set_string(&src, v);
    sngl_set_prop(obj, name, &src);
}
static void sngl_set_prop_bool(void *obj, const char *name, int v) {
    GValue src = G_VALUE_INIT;
    g_value_init(&src, G_TYPE_BOOLEAN);
    g_value_set_boolean(&src, v ? TRUE : FALSE);
    sngl_set_prop(obj, name, &src);
}
static void sngl_set_prop_int(void *obj, const char *name, int v) {
    GValue src = G_VALUE_INIT;
    g_value_init(&src, G_TYPE_INT);
    g_value_set_int(&src, v);
    sngl_set_prop(obj, name, &src);
}
static void sngl_set_prop_double(void *obj, const char *name, double v) {
    GValue src = G_VALUE_INIT;
    g_value_init(&src, G_TYPE_DOUBLE);
    g_value_set_double(&src, v);
    sngl_set_prop(obj, name, &src);
}`

const cgoCanvas = `

// Canvas2D draw-func trampoline. gtk_drawing_area_set_draw_func wants a
// C function pointer, which cannot close over Go state — so we register
// each draw closure in the snglDrawFuncs slice and pass its index as the
// user_data. sngl_draw_tramp extracts the index and dispatches into Go,
// handing through the cairo_t. Mirrors the sngl_connect/snglGoDispatch
// signal pattern: the static helpers are defined here so they are visible
// to the C.sngl_drawing_area_set_draw call sites this file emits (cgo
// preambles are per-file), while sngl_draw_dispatch is //export-ed once in
// callbacks.go and forward-declared extern here.
extern void sngl_draw_dispatch(int idx, cairo_t* cr, int width, int height);
static void sngl_draw_tramp(GtkDrawingArea* area, cairo_t* cr, int width, int height, gpointer data) {
    (void)area;
    sngl_draw_dispatch(GPOINTER_TO_INT(data), cr, width, height);
}
static void sngl_drawing_area_set_draw(void* area, int idx) {
    gtk_drawing_area_set_draw_func(GTK_DRAWING_AREA(area), sngl_draw_tramp, GINT_TO_POINTER(idx), NULL);
}

// A drawing area has no click signal of its own; a GtkGestureClick does, and
// its ` + "`pressed`" + ` passes (n_press, x, y) before user_data.
static void sngl_pressed_cb(GtkGestureClick *g, gint n, gdouble x, gdouble y, gpointer data) {
    (void)g; (void)n; (void)x; (void)y;
    snglGoDispatch(GPOINTER_TO_INT(data));
}
static GtkGesture* sngl_connect_click(void* widget, int idx) {
    GtkGesture *g = gtk_gesture_click_new();
    gtk_widget_add_controller(GTK_WIDGET(widget), GTK_EVENT_CONTROLLER(g));
    g_signal_connect(g, "pressed", G_CALLBACK(sngl_pressed_cb), GINT_TO_POINTER(idx));
    return g;
}
static void sngl_emit_pressed(void* gesture) {
    g_signal_emit_by_name(gesture, "pressed", 1, 0.0, 0.0);
}`

const calendarDateHelpers = `
// snglCalendarDate is the day a GtkCalendar has selected, as the *time.Time an
// option[date] is in Go. A calendar always has a day selected.
func snglCalendarDate(c *C.GtkCalendar) *time.Time {
	dt := C.gtk_calendar_get_date(c)
	defer C.g_date_time_unref(dt)
	t := time.Date(int(C.g_date_time_get_year(dt)), time.Month(C.g_date_time_get_month(dt)),
		int(C.g_date_time_get_day_of_month(dt)), 0, 0, 0, 0, time.UTC)
	return &t
}

// snglCalendarSelect selects t's day on a GtkCalendar; nil selects nothing.
func snglCalendarSelect(c *C.GtkCalendar, t *time.Time) {
	if t == nil {
		return
	}
	C.sngl_calendar_select(c, C.int(t.Year()), C.int(t.Month()), C.int(t.Day()))
}
`
