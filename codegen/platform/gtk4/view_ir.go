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

var gtkSetterTable = map[string]map[string]string{
	"GtkButton": {
		"label": "gtk_button_set_label",
	},
	"GtkLabel": {
		"label": "gtk_label_set_text",
	},
	"GtkEntry": {
		"text": "gtk_editable_set_text",
	},
	"GtkCheckButton": {
		"active": "gtk_check_button_set_active",
		"label":  "gtk_check_button_set_label",
	},
	"GtkApplicationWindow": {
		"title": "gtk_window_set_title",
	},
	"GtkImage": {
		"file": "gtk_image_set_from_file",
	},
}

func gtkSetter(cType, prop string) string {
	if m, ok := gtkSetterTable[cType]; ok {
		return m[prop]
	}
	return ""
}
