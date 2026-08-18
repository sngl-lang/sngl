// Package gtk4 is the SNGL platform generator for GTK4 applications.
package gtk4

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// gtk4NativeComponent is the gtk4-specific metadata attached to an
// ir.Component resolved from GIR. Read by gtk4Translator at codegen
// time; nil for non-GIR components.
type gtk4NativeComponent struct {
	CType       string // "GtkButton"
	Constructor string // "gtk_button_new_with_label"
	// CtorParams carries the GIR ConstructorInfo.Params for the
	// chosen constructor. Codegen uses this to emit typed-zero
	// argument placeholders so the cgo call type-checks; user-supplied
	// prop values are applied immediately afterward via dedicated
	// setter calls.
	CtorParams []gir.ConstructorParam
}

// pickPrimaryConstructorInfo returns the full ConstructorInfo for the
// first constructor in the class. Falls back to a synthetic
// "gtk_<lower>_new" with no params when the class declared none in GIR.
func pickPrimaryConstructorInfo(info *gir.ClassInfo) gir.ConstructorInfo {
	if len(info.Constructors) == 0 {
		return gir.ConstructorInfo{Name: "gtk_" + lowerCType(info.CType) + "_new"}
	}
	return info.Constructors[0]
}

// girTypeIsNamedNonPrimitive reports whether the GIR raw type name
// refers to a non-primitive declared type (enum, class, struct) rather
// than a built-in scalar. Used to map GIR property types like
// "Orientation" → cgo "GtkOrientation".
func girTypeIsNamedNonPrimitive(name string) bool {
	if name == "" {
		return false
	}
	switch name {
	case "utf8", "filename", "gchararray",
		"gboolean",
		"gint", "gint32", "gint64",
		"guint", "guint32", "guint64", "gsize",
		"gdouble", "gfloat",
		"none":
		return false
	}
	// Heuristic: GIR enum/class names within the current namespace are
	// bare CamelCase identifiers (no namespace dot). Cross-namespace
	// names contain a "." (e.g. "Gio.File") and need different handling
	// — return false there so the codegen falls back to defaults.
	return !strings.Contains(name, ".") && name[0] >= 'A' && name[0] <= 'Z'
}

// lowerCType maps "GtkLabel" → "label", "GtkApplicationWindow" → "application_window".
func lowerCType(cType string) string {
	bare := strings.TrimPrefix(cType, "Gtk")
	var out strings.Builder
	for i, r := range bare {
		if i > 0 && unicode.IsUpper(r) {
			out.WriteByte('_')
		}
		out.WriteRune(unicode.ToLower(r))
	}
	return out.String()
}

//go:embed gtk4.sngl
var pkgSource string

var pkgDocs []*ast.Document

// girAutoPaths are the standard locations checked when --opt gir= is not set.
var girAutoPaths = []string{
	"/usr/share/gir-1.0/Gtk-4.0.gir",
	"/usr/local/share/gir-1.0/Gtk-4.0.gir",
	"/opt/homebrew/share/gir-1.0/Gtk-4.0.gir",
}

func init() {
	doc, err := parser.Parse("gtk4.sngl", []byte(pkgSource))
	if err != nil {
		panic(fmt.Errorf("platform gtk4 init: parsing gtk4.sngl: %w", err))
	}
	pkgDocs = []*ast.Document{doc}
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for GTK4.
type Generator struct {
	once     sync.Once
	registry *gir.TypeRegistry
	initErr  error // set if autodetect GIR load fails
	girOpt   string
}

// Configure implements codegen.OptionConfigurable. Reads the "gir" option so
// Resolve (called during type-check, before code generation) can honor the
// CLI-supplied GIR path. Resets cached registry state so subsequent Resolve
// calls re-load against the new path.
func (g *Generator) Configure(opts map[string]string) error {
	g.girOpt = opts["gir"]
	g.once = sync.Once{}
	g.registry = nil
	g.initErr = nil
	return nil
}

func (g *Generator) PlatformIdentifier() string { return "gtk4" }
func (g *Generator) Description() string {
	return "Native Linux/GNOME desktop GUI using GTK4."
}
func (g *Generator) SupportedLangs() []string { return []string{"go"} }
func (g *Generator) Package() []*ast.Document { return pkgDocs }
func (g *Generator) Capabilities(lang codegen.LangTranslator) lower.Features {
	f := lang.Capabilities()
	// NoReactivity injects `nID.<prop> = <expr>` Assigns after every
	// mutation of a tracked Var. The gtk4 renderer translates those
	// to gtk_<widget>_set_<prop>(C-args) calls — same approach fyne uses.
	f.Reactivity = false
	f.Declarative = false
	f.StdlibWrappers = false
	f.InlineComponents = false
	f.StructSpread = false
	f.StructComponents = true
	f.StdlibContextParam = true
	// Canvas2D: gtk4 renders shape subtrees via cairo inside a
	// GtkDrawingArea draw callback. ReactiveCanvas injects CanvasRedrawStmt
	// (→ gtk_widget_queue_draw) into handler/timer bodies that mutate a var
	// a draw func reads.
	f.Canvas = true
	f.ReactiveCanvas = true
	return f
}

// Resolve looks up a GTK widget by its C type name (e.g. "GtkButton").
// It lazy-loads the GIR file on first call. Returns nil if the GIR file is
// not available on this machine or the identifier is not a known widget.
func (g *Generator) Resolve(identifier string) ir.Symbol {
	g.once.Do(func() {
		p, err := resolveGIRPath(g.girOpt)
		if err != nil {
			g.initErr = err
			return
		}
		g.registry, g.initErr = gir.ParseGIR(p)
	})
	if g.initErr != nil {
		// GIR unavailable — caller gets nil, checker will report unknown identifier.
		return nil
	}
	name := stripGtkPrefix(identifier)
	info, ok := g.registry.Classes[name]
	if !ok {
		return nil
	}
	return girClassToComponent(info)
}

// Generate writes gtk4 source files directly into sink. This is the
// sink-based path platforms migrate to during the codegen unification.
func (g *Generator) Generate(req *codegen.Request, sink codegen.Sink) error {
	// Agent-mode test build: suppress the user's main() loop and force
	// the package name to "main" so the agent's func main() compiles
	// alongside it. The launcher's `go build .` step expects a main
	// package on disk.
	agentMode := codegen.OptionString(req.Options, "testMode") == "agent"
	if agentMode {
		if req.Options == nil {
			req.Options = &ir.StructLit{}
		}
		codegen.SetOptionField(req.Options, "main", false)
		codegen.SetOptionField(req.Options, "package", "main")
	}

	c := &compilation{gen: g}
	// Agent-mode and Snapshot builds append a cgo harness that calls the
	// generated Model.BuildUI with cgo pointer types, so the model must stay
	// on the inline-cgo path (BuildUI's wrapped signature takes gtk4rt.Handle).
	c.disableWrapped = agentMode || codegen.OptionBool(req.Options, "gtk4NoWrap")
	m, err := c.BuildMutationModel(req, codegen.AnalyzeCommon(req.Pkg))
	if err != nil {
		return err
	}
	if err := c.EmitFromMutation(m, req, sink); err != nil {
		return err
	}
	if golang.PackageUsesI18n(req.Pkg) {
		if err := golang.EmitI18nManifestEmbed(sink, c.cfg.Package, req.ProjectFS, ""); err != nil {
			return fmt.Errorf("gtk4: i18n manifest embed: %w", err)
		}
	}

	if codegen.OptionBool(req.Options, "test") {
		testFns, suffixes, methodFields := codegen.CollectTestFuncs(req.Pkg)
		if len(testFns) > 0 {
			if agentMode {
				src := golang.LowerTestFile(c.cfg.Package, testFns, suffixes, methodFields, golang.TestEmitAgent)
				if err := writeRawFile(sink, "testagent_main.go", []byte(src)); err != nil {
					return err
				}
				// gtk4's New() returns *Model; the agent helper file boots
				// GTK in headless mode and calls buildWidgetTree() to
				// materialize widgets so per-id event invokers fire against
				// real GTK objects. BuildUI is intentionally NOT called here —
				// sngl_test_activate calls it with the snapshot app so that
				// m.__root has no prior parent when gtk_window_set_child runs.
				mainSrc := []byte(`package ` + c.cfg.Package + `

/*
#include <gtk/gtk.h>
*/
import "C"

import (
	"sync"

	"git.duckfam.us/jonathan/sngl/pkg/go/testagent"
)

var gtkInit sync.Once

var currentModel *Model

func setCurrentTestModel(m *Model) { currentModel = m }

func newTestComponent() *Model {
	gtkInit.Do(func() { C.gtk_init() })
	m := New()
	m.buildWidgetTree()
	return m
}

// Stdlib event payload structs — surfaced for test bodies that
// construct InputEvent{...} / ChangeEvent{...} / SubmitEvent{...}.
type InputEvent struct{ Value string }
type ChangeEvent struct{ Value string }
type SubmitEvent struct{ Value string }

func main() { testagent.Main() }
`)
				if err := writeRawFile(sink, "agent_main.go", mainSrc); err != nil {
					return err
				}
				// snapshot.go: reuses the gtk4SnapshotCgo preamble.
				// Uses g_application_register (not g_application_run) so
				// BuildUI and sngl_pump_until_mapped run in the Go call
				// stack — outside any signal callback — making it safe to
				// call blocking g_main_context_iteration calls there.
				snapshotSrc := []byte("package " + c.cfg.Package + "\n\n" + gtk4SnapshotCgo + `
import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"git.duckfam.us/jonathan/sngl/pkg/go/testagent"
)

func currentTestModel() *Model { return currentModel }

func snapshotBytesGtk(m *Model) (string, []byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if m == nil {
		return "", nil, fmt.Errorf("no current model registered")
	}

	f, err := os.CreateTemp("", "sngl-snap-*.png")
	if err != nil {
		return "", nil, err
	}
	f.Close()
	defer os.Remove(f.Name())

	cAppID := C.CString("dev.sngl.test.snapshot")
	defer C.free(unsafe.Pointer(cAppID))
	app := C.gtk_application_new(cAppID, C.G_APPLICATION_NON_UNIQUE)
	defer C.g_object_unref(C.gpointer(unsafe.Pointer(app)))

	// Register without running the main loop so BuildUI and the pump
	// execute in the current (locked) OS thread without re-entrancy.
	var gerr *C.GError
	if C.g_application_register((*C.GApplication)(unsafe.Pointer(app)), nil, &gerr) == 0 {
		if gerr != nil {
			C.g_error_free(gerr)
		}
		return "", nil, fmt.Errorf("g_application_register failed")
	}

	win := m.BuildUI(app)
	if win == nil {
		return "", nil, fmt.Errorf("BuildUI returned nil")
	}
	C.gtk_window_set_default_size((*C.GtkWindow)(unsafe.Pointer(win)), 800, 600)
	C.gtk_window_present((*C.GtkWindow)(unsafe.Pointer(win)))
	C.sngl_pump_until_mapped(win, 1000)

	cPath := C.CString(f.Name())
	defer C.free(unsafe.Pointer(cPath))
	if rc := C.sngl_snapshot(win, 800, 600, cPath); rc != 0 {
		return "", nil, fmt.Errorf("sngl_snapshot rc=%d", int(rc))
	}

	data, err := os.ReadFile(f.Name())
	if err != nil {
		return "", nil, err
	}
	return "image/png", data, nil
}

func init() {
	testagent.RegisterSnapshot(func() (string, []byte, error) {
		return snapshotBytesGtk(currentTestModel())
	})
}
`)
				if err := writeRawFile(sink, "snapshot.go", snapshotSrc); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// writeRawFile writes pre-formatted content directly to sink without
// running it through the language file emitter (which would re-attach
// source maps and headers we don't want on synthetic test/agent files).
func writeRawFile(sink codegen.Sink, name string, content []byte) error {
	wc, err := sink.Create(name)
	if err != nil {
		return err
	}
	if _, err := wc.Write(content); err != nil {
		wc.Close()
		return err
	}
	return wc.Close()
}

// resolveGIRPath returns the path to the Gtk-4.0.gir file.
// If girPath is non-empty it validates that path. Otherwise it probes the
// standard autodetect locations.
func resolveGIRPath(girPath string) (string, error) {
	if girPath != "" {
		if _, err := os.Stat(girPath); err != nil {
			return "", err
		}
		return girPath, nil
	}
	for _, p := range girAutoPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("gtk4 GIR file not found — set --opt gir=/path/to/Gtk-4.0.gir or install libgtk-4-dev")
}

// stripGtkPrefix removes the "Gtk" prefix so "GtkButton" → "Button".
// Identifiers that don't start with "Gtk" are returned unchanged.
func stripGtkPrefix(identifier string) string {
	if strings.HasPrefix(identifier, "Gtk") {
		return identifier[3:]
	}
	return identifier
}

// girClassToComponent converts GIR class metadata to an ir.Component.
// Props become *ir.Prop entries; signals become *ir.EventDecl entries.
func girClassToComponent(info *gir.ClassInfo) *ir.Component {
	comp := &ir.Component{
		Name:   info.CType,
		Stdlib: true, // GIR-derived platform components: props are optional by convention
		// GIR doesn't model "accepts children" — but every GTK
		// container widget can take children, and rejecting children
		// at the checker level would block GtkBox/GtkWindow/etc.
		// Allow any children at the IR level; the gtk4 codegen knows
		// which parent types actually have child-append APIs.
		ChildrenType: &ir.Type{Kind: ir.TypeDyn},
		Native: func() *gtk4NativeComponent {
			ci := pickPrimaryConstructorInfo(info)
			return &gtk4NativeComponent{
				CType:       info.CType,
				Constructor: ci.Name,
				CtorParams:  ci.Params,
			}
		}(),
	}
	lower := lowerCType(info.CType)
	for _, p := range info.Props {
		t := p.IRType
		if t == nil {
			t = &ir.Type{Kind: ir.TypeDyn}
		}
		// Property names like "default-width" become C setter
		// "gtk_<class>_set_default_width" (hyphens → underscores).
		// Interface-inherited props (e.g. `orientation` on GtkBox via
		// GtkOrientable) bind to the interface's namespaced setter
		// (`gtk_orientable_set_orientation`), not the class's.
		setterProp := strings.ReplaceAll(p.Name, "-", "_")
		setterNS := lower
		var recvType string
		if p.InterfaceName != "" {
			setterNS = lowerCType(p.InterfaceName)
			recvType = "Gtk" + p.InterfaceName
		}
		// Map GIR raw type → cgo value type when it's a non-primitive
		// (enum or struct): bare names like "Orientation" become
		// "GtkOrientation". Primitives (utf8, gint, gboolean, …) stay
		// empty so the setter falls back to IR-type-driven coercion.
		var valType string
		if gt := p.GIRType; gt != "" && girTypeIsNamedNonPrimitive(gt) {
			valType = "Gtk" + gt
		}
		comp.Props = append(comp.Props, &ir.Prop{
			Name:               p.Name,
			Type:               t,
			NativeSetter:       "gtk_" + setterNS + "_set_" + setterProp,
			NativeReceiverType: recvType,
			NativeValueType:    valType,
		})
	}
	// SNGL `style` is forwarded onto every widget root by the stdlib wrapper
	// bodies (`gtk4.GtkBox(..., style={...style})`). gtk4 has no style→GTK-CSS
	// transform yet (deferred), so the prop is accepted for type-checking and
	// skipped at codegen — it carries no NativeSetter.
	comp.Props = append(comp.Props, &ir.Prop{
		Name: "style",
		Type: &ir.Type{Kind: ir.TypeDyn},
	})
	for _, s := range info.Signals {
		comp.Events = append(comp.Events, &ir.EventDecl{
			Name:         s.Name,
			NativeSignal: s.Name,
		})
	}
	return comp
}
