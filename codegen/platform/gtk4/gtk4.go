// Package gtk4 is the SNGL platform generator for GTK4 applications.
package gtk4

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

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
func (g *Generator) IsLanguageSupported(l ir.Language) bool { return l.LanguageIdentifier() == "go" }
func (g *Generator) Capabilities() lower.Caps {
	// NoReactivity injects `nID.<prop> = <expr>` Assigns after every
	// mutation of a tracked Var. The gtk4 renderer translates those
	// to gtk_<widget>_set_<prop>(C-args) calls — same approach fyne
	// uses, just emitting C setter invocations instead of Go method
	// calls.
	return lower.Caps{NoReactivity: true}
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

// Generate delegates to a per-request compilation.
func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	c := &compilation{gen: g}
	m, err := c.BuildMutationModel(req, codegen.AnalyzeCommon(req.Pkg))
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}
	return c.EmitFromMutation(m, req)
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
	}
	for _, p := range info.Props {
		t := p.IRType
		if t == nil {
			t = &ir.Type{Kind: ir.TypeDyn}
		}
		comp.Props = append(comp.Props, &ir.Prop{
			Name: p.Name,
			Type: t,
		})
	}
	for _, s := range info.Signals {
		comp.Events = append(comp.Events, &ir.EventDecl{
			Name: s.Name,
		})
	}
	return comp
}
