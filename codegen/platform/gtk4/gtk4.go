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

// pickPrimaryConstructor returns the C identifier of the first
// constructor in the class, falling back to a synthesized "gtk_<lower>_new".
func pickPrimaryConstructor(info *gir.ClassInfo) string {
	return pickPrimaryConstructorInfo(info).Name
}

// pickPrimaryConstructorInfo returns the full ConstructorInfo for the
// constructor chosen by pickPrimaryConstructor. Falls back to a
// synthetic "gtk_<lower>_new" with no params when the class declared
// none in GIR.
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
func (g *Generator) SupportedLangs() []string               { return []string{"go"} }
func (g *Generator) Package() []*ast.Document               { return pkgDocs }
func (g *Generator) Capabilities() lower.Caps {
	// NoReactivity injects `nID.<prop> = <expr>` Assigns after every
	// mutation of a tracked Var. The gtk4 renderer translates those
	// to gtk_<widget>_set_<prop>(C-args) calls — same approach fyne
	// uses, just emitting C setter invocations instead of Go method
	// calls.
	return lower.Caps{StructComponents: true, StdlibContextParam: true, NoReactivity: true, NoDeclarative: true, NoStdlibWrappers: true, NoInlineComponents: true}
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
	c := &compilation{gen: g}
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
	return nil
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
		Name: info.CType,
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
	for _, s := range info.Signals {
		comp.Events = append(comp.Events, &ir.EventDecl{
			Name:         s.Name,
			NativeSignal: s.Name,
		})
	}
	return comp
}
