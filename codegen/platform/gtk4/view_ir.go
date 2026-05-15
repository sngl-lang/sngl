package gtk4

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/ir"
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
	fields     []widgetField
	depTracker *codegen.DepTracker

	// eventInvokers collects (id, sngl-event, field, gtk-signal, cType)
	// tuples for every signal connected during the walk. After emit
	// we generate one method per tuple: `func (m *Model) <id><Event>()`
	// that fires the GTK signal — used by the Go test runner to drive
	// `c.<id>.@<event>()` test syntax through the real bridge.
	eventInvokers []gtkEventInvoker
}

type gtkEventInvoker struct {
	IDLabel    string // SNGL #id (must be user-set; synthetic __n* ids skipped)
	SnglEvent  string // SNGL event name, e.g. "click"
	FieldName  string // Model widget field, e.g. "btn3"
	GTKSignal  string // GTK signal name, e.g. "clicked"
	WidgetType string // C type for the gpointer cast, e.g. "GtkButton"
	ValueParam string // optional `name T` param for events with payload (e.g. entry input)
	PreFire    string // optional pre-fire snippet (e.g. push entry text into widget state)
}

// userNodeID returns n.ID when it looks like a user-authored #id
// rather than a synthetic passReactivity id ("__nN").
func userNodeID(n *ir.NodeInst) string {
	if n == nil {
		return ""
	}
	if n.ID == "" || strings.HasPrefix(n.ID, "__n") {
		return ""
	}
	return n.ID
}

// --- Lookup tables (consumed by gtk4Translator) ---

// gtkSetterEntry describes the C setter (and cast type) for one prop
// on one widget class. RecvType is non-empty when the setter targets
// an interface (e.g. GtkEditable) rather than the widget's class.
type gtkSetterEntry struct {
	Setter   string
	RecvType string
}

var gtkSetterTable = map[string]map[string]gtkSetterEntry{
	"GtkButton": {
		"label": {Setter: "gtk_button_set_label"},
	},
	"GtkLabel": {
		"label": {Setter: "gtk_label_set_text"},
	},
	"GtkEntry": {
		"text": {Setter: "gtk_editable_set_text", RecvType: "GtkEditable"},
	},
	"GtkCheckButton": {
		"active": {Setter: "gtk_check_button_set_active"},
		"label":  {Setter: "gtk_check_button_set_label"},
	},
	"GtkApplicationWindow": {
		"title": {Setter: "gtk_window_set_title", RecvType: "GtkWindow"},
	},
	"GtkImage": {
		"file": {Setter: "gtk_image_set_from_file"},
	},
}

// gtkSetter returns the C setter for (cType, prop). Kept for callers
// that don't need the cast-type override.
func gtkSetter(cType, prop string) string {
	return gtkSetterFor(cType, prop).Setter
}

// gtkSetterFor returns both the setter and its receiver cast override
// (empty when the cast type is the widget's own C type).
func gtkSetterFor(cType, prop string) gtkSetterEntry {
	if m, ok := gtkSetterTable[cType]; ok {
		return m[prop]
	}
	return gtkSetterEntry{}
}
