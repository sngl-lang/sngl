package gtk4

import (
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/codegen/platform/gtk4/gir"
)

// widgetField is a persistent widget reference stored on the Model struct.
type widgetField struct {
	name   string // e.g. "btn0"
	goType string // e.g. "*C.GtkButton"
}

// viewContext carries the residual state still consulted after the
// parallel-reactive pipeline was retired (Plan C). Today it only
// collects event-invoker tuples from translator-driven signal
// connection — the BuildUI body itself comes from WalkLowered.
type viewContext struct {
	gc         *golang.GoIRContext
	registry   *gir.TypeRegistry // may be nil when GIR unavailable
	ctx        *codegen.CodegenCtx
	buf        *strings.Builder
	indent     int
	depTracker *codegen.DepTracker

	// eventInvokers collects (id, sngl-event, field, gtk-signal, cType)
	// tuples for every signal connected during the walk. After emit
	// we generate one method per tuple: `func (m *Model) <id><Event>()`
	// that fires the GTK signal — used by the Go test runner to drive
	// `c.<id>.<event>()` test syntax through the real bridge.
	eventInvokers []gtkEventInvoker
}

type gtkEventInvoker struct {
	IDLabel    string // SNGL #id (must be user-set; synthetic __n* ids skipped)
	SnglEvent  string // SNGL event name, e.g. "click"
	FieldName  string // Model widget field, e.g. "btn3"
	GTKSignal  string // GTK signal name, e.g. "clicked"
	WidgetType string // C type for the gpointer cast, e.g. "GtkButton"
	// State says the widget holds an on/off state a test sets from the bool
	// it passes, which is what a user's flip does.
	State bool
	// List says a test indexes the id, so its copies are kept by key
	// (copiesField) and an `At` invoker fires the one it is handed the key of.
	List bool
	// RtFire is the gtk4rt function firing the event, for a primitive of
	// gtk4rt's whose event no GTK signal carries; GTKSignal is then empty.
	RtFire string
}

// --- Lookup tables (consumed by gtk4Translator) ---

// gtkSetterEntry describes the C setter (and cast types) for one prop
// on one widget class. RecvType is non-empty when the setter targets
// an interface (e.g. GtkEditable) rather than the widget's class.
// ValType names the C type the value argument takes (e.g. GtkOrientation)
// and is empty when the SNGL value type drives the coercion.
type gtkSetterEntry struct {
	Setter   string
	RecvType string
	ValType  string
}
