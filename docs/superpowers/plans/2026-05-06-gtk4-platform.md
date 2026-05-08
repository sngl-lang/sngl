# GTK4 Platform Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `gtk4` platform to SNGL that compiles SNGL code to GTK4 desktop apps in Go+cgo, exercising the `c://` import scheme end-to-end via GIR-driven widget resolution.

**Architecture:** A new `codegen/platform/gtk4/` package registers a `PlatformGenerator` that parses `Gtk-4.0.gir` at compile time to synthesize `ir.Component` symbols for every GTK widget class. `gtk4.sngl` defines stdlib SNGL components (`button`, `label`, etc.) in terms of those GIR-resolved symbols. The MutationModel emitter walks the IR and emits `C.gtk_*` constructor calls, signal connections via a global callback registry, and reactive setter calls in updater methods.

**Tech Stack:** Go, `encoding/xml` (GIR parsing), cgo (`#include <gtk/gtk.h>`, `#cgo pkg-config: gtk4`), `codegen.MutationModel`, `codegen.CCompiler`, `codegen.ApplyOptions`.

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `codegen/platform/gtk4/gir/gir.go` | Create | GIR XML parser → `TypeRegistry` |
| `codegen/platform/gtk4/gir/gir_test.go` | Create | Unit tests for GIR parsing |
| `codegen/platform/gtk4/gtk4.go` | Create | `Generator`: register, Resolve, Generate |
| `codegen/platform/gtk4/gtk4_test.go` | Create | Unit tests for Resolve |
| `codegen/platform/gtk4/scaffold.go` | Create | `Config`, `templateData`, embedded FS |
| `codegen/platform/gtk4/view_ir.go` | Create | `viewContext` + GTK widget renderer |
| `codegen/platform/gtk4/compiler_ir.go` | Create | `compilation` MutationModel emitter |
| `codegen/platform/gtk4/gtk4.sngl` | Create | Stdlib component declarations |
| `codegen/platform/gtk4/templates/model.go.tmpl` | Create | State struct, helpers, updaters |
| `codegen/platform/platforms.go` | Modify | Add blank import for `gtk4` |
| `cmd/sngl/testdata/compile_gtk4_button.txt` | Create | End-to-end txtar test |

---

## Task 1: GIR parser

**Files:**
- Create: `codegen/platform/gtk4/gir/gir.go`
- Create: `codegen/platform/gtk4/gir/gir_test.go`

- [ ] **Step 1: Create the gir package with failing tests**

```go
// codegen/platform/gtk4/gir/gir_test.go
package gir_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/ir"
)

const minimalGIR = `<?xml version="1.0"?>
<repository version="1.2"
  xmlns="http://www.gtk.org/introspection/core/1.0"
  xmlns:c="http://www.gtk.org/introspection/c/1.0"
  xmlns:glib="http://www.gtk.org/introspection/glib/1.0">
  <namespace name="Gtk" version="4.0">
    <class name="Button" c:type="GtkButton">
      <constructor name="new_with_label" c:identifier="gtk_button_new_with_label">
        <parameters>
          <parameter name="label" transfer-ownership="none" nullable="1">
            <type name="utf8" c:type="const gchar*"/>
          </parameter>
        </parameters>
      </constructor>
      <property name="label" writable="1">
        <type name="utf8" c:type="gchar*"/>
      </property>
      <glib:signal name="clicked">
        <return-value transfer-ownership="none">
          <type name="none" c:type="void"/>
        </return-value>
      </glib:signal>
    </class>
    <class name="Label" c:type="GtkLabel">
      <constructor name="new" c:identifier="gtk_label_new">
        <parameters>
          <parameter name="str" transfer-ownership="none" nullable="1">
            <type name="utf8" c:type="const gchar*"/>
          </parameter>
        </parameters>
      </constructor>
      <property name="label" writable="1">
        <type name="utf8" c:type="gchar*"/>
      </property>
    </class>
  </namespace>
</repository>`

func TestParseGIR_Button(t *testing.T) {
	reg, err := gir.ParseGIRBytes([]byte(minimalGIR))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, ok := reg.Classes["Button"]
	if !ok {
		t.Fatal("Button not found")
	}
	if info.CType != "GtkButton" {
		t.Errorf("CType = %q, want GtkButton", info.CType)
	}
	if info.Constructor.Name != "gtk_button_new_with_label" {
		t.Errorf("Constructor.Name = %q, want gtk_button_new_with_label", info.Constructor.Name)
	}
	if len(info.Constructor.Params) != 1 || info.Constructor.Params[0].Name != "label" {
		t.Errorf("Constructor.Params = %v", info.Constructor.Params)
	}
	if len(info.Props) != 1 || info.Props[0].Name != "label" {
		t.Errorf("Props = %v", info.Props)
	}
	if info.Props[0].IRType.Kind != ir.TypeString {
		t.Errorf("Props[0].IRType.Kind = %v, want TypeString", info.Props[0].IRType.Kind)
	}
	if len(info.Signals) != 1 || info.Signals[0].Name != "clicked" {
		t.Errorf("Signals = %v", info.Signals)
	}
}

func TestParseGIR_Label(t *testing.T) {
	reg, err := gir.ParseGIRBytes([]byte(minimalGIR))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, ok := reg.Classes["Label"]
	if !ok {
		t.Fatal("Label not found")
	}
	if info.Constructor.Name != "gtk_label_new" {
		t.Errorf("Constructor.Name = %q, want gtk_label_new", info.Constructor.Name)
	}
}

func TestParseGIR_Unknown(t *testing.T) {
	reg, _ := gir.ParseGIRBytes([]byte(minimalGIR))
	if _, ok := reg.Classes["Nonexistent"]; ok {
		t.Error("unexpected class found")
	}
}

func TestParseGIR_UnmappableType(t *testing.T) {
	const src = `<?xml version="1.0"?>
<repository version="1.2"
  xmlns="http://www.gtk.org/introspection/core/1.0"
  xmlns:c="http://www.gtk.org/introspection/c/1.0"
  xmlns:glib="http://www.gtk.org/introspection/glib/1.0">
  <namespace name="Gtk" version="4.0">
    <class name="Weird" c:type="GtkWeird">
      <constructor name="new" c:identifier="gtk_weird_new"/>
      <property name="custom" writable="1">
        <type name="SomeUnknownType"/>
      </property>
    </class>
  </namespace>
</repository>`
	reg, err := gir.ParseGIRBytes([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info := reg.Classes["Weird"]
	if info == nil {
		t.Fatal("Weird not found")
	}
	// Unmappable type falls back to TypeDyn
	if len(info.Props) != 1 || info.Props[0].IRType.Kind != ir.TypeDyn {
		t.Errorf("expected TypeDyn fallback, got %v", info.Props)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /home/jonathan/src/git.duckfam.us/jonathan/sngl
go test ./codegen/platform/gtk4/... 2>&1 | head -20
```

Expected: compile error (package doesn't exist yet).

- [ ] **Step 3: Create the GIR parser**

```go
// codegen/platform/gtk4/gir/gir.go
package gir

import (
	"encoding/xml"
	"os"

	"git.duckfam.us/jonathan/sngl/ir"
)

// ConstructorParam is one parameter in a GIR constructor.
type ConstructorParam struct {
	Name   string
	IRType *ir.Type
}

// ConstructorInfo holds the C identifier and parameters for a widget constructor.
type ConstructorInfo struct {
	Name   string             // e.g. "gtk_button_new_with_label"
	Params []ConstructorParam // positional params (instance-parameter excluded)
}

// Prop is a writable property on a GIR class.
type Prop struct {
	Name   string
	IRType *ir.Type
}

// Signal is a GLib signal on a GIR class.
type Signal struct {
	Name string
}

// ClassInfo holds resolved metadata for one GTK widget class.
type ClassInfo struct {
	CType       string          // e.g. "GtkButton"
	Constructor ConstructorInfo // first constructor found
	Props       []Prop          // writable properties
	Signals     []Signal
}

// TypeRegistry maps GIR class name (e.g. "Button") to ClassInfo.
type TypeRegistry struct {
	Classes map[string]*ClassInfo
}

// ParseGIR reads and parses a GIR file at path.
func ParseGIR(path string) (*TypeRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseGIRBytes(data)
}

// ParseGIRBytes parses GIR XML from a byte slice (used in tests).
func ParseGIRBytes(data []byte) (*TypeRegistry, error) {
	// GIR XML uses namespace prefixes; decode with xml.Decoder.
	type xmlParam struct {
		XMLName xml.Name `xml:"parameter"`
		Name    string   `xml:"name,attr"`
		Type    struct {
			Name string `xml:"name,attr"`
		} `xml:"type"`
	}
	type xmlCtor struct {
		XMLName    xml.Name   `xml:"constructor"`
		Identifier string     `xml:"identifier,attr"`
		Params     []xmlParam `xml:"parameters>parameter"`
	}
	type xmlProp struct {
		XMLName  xml.Name `xml:"property"`
		Name     string   `xml:"name,attr"`
		Writable string   `xml:"writable,attr"`
		Type     struct {
			Name string `xml:"name,attr"`
		} `xml:"type"`
	}
	type xmlSignal struct {
		XMLName xml.Name `xml:"signal"`
		Name    string   `xml:"name,attr"`
	}
	type xmlClass struct {
		XMLName    xml.Name    `xml:"class"`
		Name       string      `xml:"name,attr"`
		CType      string      `xml:"type,attr"`
		Ctors      []xmlCtor   `xml:"constructor"`
		Properties []xmlProp   `xml:"property"`
		Signals    []xmlSignal `xml:"signal"`
	}
	type xmlNamespace struct {
		XMLName xml.Name   `xml:"namespace"`
		Classes []xmlClass `xml:"class"`
	}
	type xmlRepo struct {
		XMLName   xml.Name     `xml:"repository"`
		Namespace xmlNamespace `xml:"namespace"`
	}

	var repo xmlRepo
	if err := xml.Unmarshal(data, &repo); err != nil {
		return nil, err
	}

	reg := &TypeRegistry{Classes: make(map[string]*ClassInfo)}
	for _, cls := range repo.Namespace.Classes {
		info := &ClassInfo{CType: cls.CType}
		if len(cls.Ctors) > 0 {
			c := cls.Ctors[0]
			info.Constructor.Name = c.Identifier
			for _, p := range c.Params {
				info.Constructor.Params = append(info.Constructor.Params, ConstructorParam{
					Name:   p.Name,
					IRType: girTypeToIR(p.Type.Name),
				})
			}
		}
		for _, p := range cls.Properties {
			if p.Writable != "1" {
				continue
			}
			info.Props = append(info.Props, Prop{
				Name:   p.Name,
				IRType: girTypeToIR(p.Type.Name),
			})
		}
		for _, s := range cls.Signals {
			info.Signals = append(info.Signals, Signal{Name: s.Name})
		}
		reg.Classes[cls.Name] = info
	}
	return reg, nil
}

// girTypeToIR maps a GIR type name to the closest ir.Type.
func girTypeToIR(name string) *ir.Type {
	switch name {
	case "utf8", "gchararray", "filename":
		return &ir.Type{Kind: ir.TypeString}
	case "gboolean":
		return &ir.Type{Kind: ir.TypeBool}
	case "gint", "gint32", "gint64", "guint", "guint32", "guint64", "gsize":
		return &ir.Type{Kind: ir.TypeInt}
	case "gdouble", "gfloat":
		return &ir.Type{Kind: ir.TypeFloat}
	case "none":
		return nil
	default:
		return &ir.Type{Kind: ir.TypeDyn}
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./codegen/platform/gtk4/gir/... -v 2>&1 | tail -20
```

Expected: all 4 tests pass.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/gtk4/
git commit -m "feat: gtk4 GIR parser"
```

---

## Task 2: Platform skeleton + `Resolve`

**Files:**
- Create: `codegen/platform/gtk4/gtk4.go`
- Create: `codegen/platform/gtk4/gtk4_test.go`
- Modify: `codegen/platform/platforms.go`

- [ ] **Step 1: Write failing tests**

```go
// codegen/platform/gtk4/gtk4_test.go
package gtk4_test

import (
	"testing"

	_ "git.duckfam.us/jonathan/sngl/codegen/platform/gtk4"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestResolve_KnownWidget(t *testing.T) {
	// Load platform from registry.
	gen := codegen.LookupPlatform("gtk4")
	if gen == nil {
		t.Fatal("gtk4 platform not registered")
	}

	// Resolve uses the registry loaded from GIR. We inject a test registry
	// via the exported TestSetRegistry helper.
	sym := gen.Resolve("GtkButton")
	if sym == nil {
		t.Fatal("GtkButton should resolve")
	}
	comp, ok := sym.(*ir.Component)
	if !ok {
		t.Fatalf("expected *ir.Component, got %T", sym)
	}
	if comp.Name != "GtkButton" {
		t.Errorf("Name = %q, want GtkButton", comp.Name)
	}
}

func TestResolve_Unknown(t *testing.T) {
	gen := codegen.LookupPlatform("gtk4")
	if gen == nil {
		t.Skip("gtk4 not registered")
	}
	if sym := gen.Resolve("NotAWidget"); sym != nil {
		t.Errorf("expected nil for unknown widget, got %v", sym)
	}
}
```

Note: `TestResolve_KnownWidget` requires a GIR file to be installed on the test machine. If it fails because GIR is absent, skip rather than fail.

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./codegen/platform/gtk4/... 2>&1 | head -10
```

Expected: compile error (`gtk4` package doesn't exist).

- [ ] **Step 3: Create gtk4.go**

```go
// codegen/platform/gtk4/gtk4.go
package gtk4

import (
	_ "embed"
	"fmt"
	"strings"

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

func init() {
	doc, _ := parser.Parse("gtk4.sngl", []byte(pkgSource))
	if doc != nil {
		pkgDocs = []*ast.Document{doc}
	}
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for GTK4.
type Generator struct {
	registry *gir.TypeRegistry
	girErr   error // set if GIR load failed
}

func (g *Generator) PlatformIdentifier() string { return "gtk4" }
func (g *Generator) Description() string {
	return "GTK4 native desktop GUI via Go+cgo."
}
func (g *Generator) SupportedLangs() []string               { return []string{"go"} }
func (g *Generator) Package() []*ast.Document               { return pkgDocs }
func (g *Generator) IsLanguageSupported(l ir.Language) bool { return l.LanguageIdentifier() == "go" }
func (g *Generator) Capabilities() lower.Caps               { return lower.Caps{} }
func (g *Generator) PreviewCSS() string                     { return "" }

// Resolve synthesizes ir.Component from the GIR registry.
// Returns nil when the registry is unloaded or the class is unknown.
func (g *Generator) Resolve(identifier string) ir.Symbol {
	if g.registry == nil {
		return nil
	}
	name := stripGtkPrefix(identifier)
	info, ok := g.registry.Classes[name]
	if !ok {
		return nil
	}
	return girClassToComponent(info)
}

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	c := &compilation{}
	if err := c.loadGIR(req); err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}
	// Sync registry to generator so Resolve works during checking.
	// (Checker runs before Generate, so we also load at first Resolve call.)
	m, err := c.BuildMutationModel(req, codegen.AnalyzeCommon(req.Pkg))
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}
	return c.EmitFromMutation(m, req)
}

// stripGtkPrefix removes the "Gtk" prefix from a class name.
// "GtkButton" → "Button", "Button" → "Button".
func stripGtkPrefix(name string) string {
	if after, ok := strings.CutPrefix(name, "Gtk"); ok {
		return after
	}
	return name
}

// girClassToComponent converts a ClassInfo to an ir.Component.
func girClassToComponent(info *gir.ClassInfo) *ir.Component {
	comp := &ir.Component{Name: info.CType}
	for _, p := range info.Props {
		t := p.IRType
		if t == nil {
			t = &ir.Type{Kind: ir.TypeDyn}
		}
		comp.Params = append(comp.Params, &ir.Param{Name: p.Name, Type: t})
	}
	for _, s := range info.Signals {
		// Signals become func()-typed params (no args for now).
		comp.Params = append(comp.Params, &ir.Param{
			Name: s.Name,
			Type: &ir.Type{Kind: ir.TypeFunc},
		})
	}
	return comp
}

// girAutodetectPaths is the ordered list of system GIR file locations.
var girAutodetectPaths = []string{
	"/usr/share/gir-1.0/Gtk-4.0.gir",
	"/usr/local/share/gir-1.0/Gtk-4.0.gir",
	"/opt/homebrew/share/gir-1.0/Gtk-4.0.gir",
}

// resolveGIRPath returns the GIR file path to use, given the Config.GIRPath option.
// Empty girPath triggers autodetection.
func resolveGIRPath(girPath string) (string, error) {
	if girPath != "" {
		return girPath, nil
	}
	for _, p := range girAutodetectPaths {
		if _, err := fmt.Sscanf(p, "%s"); err == nil {
			// just check existence:
		}
		if fileExists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("gtk4 GIR file not found — set --opt gir=/path/to/Gtk-4.0.gir or install libgtk-4-dev")
}

func fileExists(path string) bool {
	_, err := parser.Parse(path, nil) // not a real check — use os.Stat
	_ = err
	return false // placeholder replaced below
}
```

Wait — `fileExists` needs `os.Stat`. Let me write the full correct version:

```go
// codegen/platform/gtk4/gtk4.go
package gtk4

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

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

func init() {
	doc, _ := parser.Parse("gtk4.sngl", []byte(pkgSource))
	if doc != nil {
		pkgDocs = []*ast.Document{doc}
	}
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for GTK4.
type Generator struct {
	registry *gir.TypeRegistry
}

func (g *Generator) PlatformIdentifier() string { return "gtk4" }
func (g *Generator) Description() string {
	return "GTK4 native desktop GUI via Go+cgo."
}
func (g *Generator) SupportedLangs() []string               { return []string{"go"} }
func (g *Generator) Package() []*ast.Document               { return pkgDocs }
func (g *Generator) IsLanguageSupported(l ir.Language) bool { return l.LanguageIdentifier() == "go" }
func (g *Generator) Capabilities() lower.Caps               { return lower.Caps{} }
func (g *Generator) PreviewCSS() string                     { return "" }

// Resolve synthesizes ir.Component from the GIR registry.
// Returns nil when the registry is unloaded or the class is unknown.
func (g *Generator) Resolve(identifier string) ir.Symbol {
	if g.registry == nil {
		// Attempt autodetect for the checker's benefit (options not available here).
		if p, err := resolveGIRPath(""); err == nil {
			g.registry, _ = gir.ParseGIR(p)
		}
		if g.registry == nil {
			return nil
		}
	}
	info, ok := g.registry.Classes[stripGtkPrefix(identifier)]
	if !ok {
		return nil
	}
	return girClassToComponent(info)
}

func (g *Generator) Generate(req *codegen.Request) (*codegen.Response, error) {
	c := &compilation{gen: g}
	m, err := c.BuildMutationModel(req, codegen.AnalyzeCommon(req.Pkg))
	if err != nil {
		return &codegen.Response{Error: err.Error()}, nil
	}
	return c.EmitFromMutation(m, req)
}

func stripGtkPrefix(name string) string {
	if after, ok := strings.CutPrefix(name, "Gtk"); ok {
		return after
	}
	return name
}

func girClassToComponent(info *gir.ClassInfo) *ir.Component {
	comp := &ir.Component{Name: info.CType}
	for _, p := range info.Props {
		t := p.IRType
		if t == nil {
			t = &ir.Type{Kind: ir.TypeDyn}
		}
		comp.Params = append(comp.Params, &ir.Param{Name: p.Name, Type: t})
	}
	for _, s := range info.Signals {
		comp.Params = append(comp.Params, &ir.Param{
			Name: s.Name,
			Type: &ir.Type{Kind: ir.TypeFunc},
		})
	}
	return comp
}

var girAutodetectPaths = []string{
	"/usr/share/gir-1.0/Gtk-4.0.gir",
	"/usr/local/share/gir-1.0/Gtk-4.0.gir",
	"/opt/homebrew/share/gir-1.0/Gtk-4.0.gir",
}

func resolveGIRPath(girPath string) (string, error) {
	if girPath != "" {
		if _, err := os.Stat(girPath); err != nil {
			return "", fmt.Errorf("gtk4: cannot read GIR file %s: %w", girPath, err)
		}
		return girPath, nil
	}
	for _, p := range girAutodetectPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("gtk4 GIR file not found — set --opt gir=/path/to/Gtk-4.0.gir or install libgtk-4-dev")
}
```

- [ ] **Step 4: Add a placeholder `gtk4.sngl`** (empty — real content in Task 4)

Create `codegen/platform/gtk4/gtk4.sngl`:
```
// GTK4 platform package.
struct Options {
    package string
    main bool
    gir string
}
```

- [ ] **Step 5: Add blank import to platforms.go**

Edit `codegen/platform/platforms.go`, add:
```go
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/gtk4"
```

- [ ] **Step 6: Fix test — skip when GIR absent, not fail**

Update `gtk4_test.go` to gracefully skip:
```go
func TestResolve_KnownWidget(t *testing.T) {
	gen := codegen.LookupPlatform("gtk4")
	if gen == nil {
		t.Fatal("gtk4 platform not registered")
	}
	sym := gen.Resolve("GtkButton")
	if sym == nil {
		t.Skip("GIR file not available on this machine")
	}
	comp, ok := sym.(*ir.Component)
	if !ok {
		t.Fatalf("expected *ir.Component, got %T", sym)
	}
	if comp.Name != "GtkButton" {
		t.Errorf("Name = %q, want GtkButton", comp.Name)
	}
}

func TestResolve_Unknown(t *testing.T) {
	gen := codegen.LookupPlatform("gtk4")
	if gen == nil {
		t.Skip("gtk4 not registered")
	}
	if sym := gen.Resolve("NotAWidget"); sym != nil {
		t.Errorf("expected nil for unknown widget, got %v", sym)
	}
}
```

Note: `codegen.LookupPlatform` may need to be added if it doesn't exist. Check `codegen/registry.go` first — if `LookupPlatform` doesn't exist, add it.

- [ ] **Step 7: Check if LookupPlatform exists**

```bash
grep -n "LookupPlatform\|func Lookup" /home/jonathan/src/git.duckfam.us/jonathan/sngl/codegen/registry.go
```

If it doesn't exist, add to `codegen/registry.go`:
```go
// LookupPlatform returns the registered PlatformGenerator for the given id, or nil.
func LookupPlatform(id string) PlatformGenerator {
    platformsMu.RLock()
    defer platformsMu.RUnlock()
    return platforms[id]
}
```

- [ ] **Step 8: Add stub compilation struct (required for compile)**

Create `codegen/platform/gtk4/compiler_ir.go` with just enough to compile (real impl in Task 6):
```go
// codegen/platform/gtk4/compiler_ir.go
package gtk4

import (
	"git.duckfam.us/jonathan/sngl/codegen"
)

type compilation struct {
	gen *Generator
	cfg Config
}

func (c *compilation) BuildMutationModel(req *codegen.Request, analysis *codegen.CommonAnalysis) (*codegen.MutationModel, error) {
	return nil, nil
}

func (c *compilation) EmitFromMutation(_ *codegen.MutationModel, _ *codegen.Request) (*codegen.Response, error) {
	return &codegen.Response{}, nil
}
```

- [ ] **Step 9: Run tests**

```bash
go build ./codegen/platform/gtk4/... && go test ./codegen/platform/gtk4/... -v 2>&1 | tail -20
```

Expected: tests pass (or skip if GIR absent).

- [ ] **Step 10: Commit**

```bash
git add codegen/platform/gtk4/ codegen/platform/platforms.go codegen/registry.go
git commit -m "feat: gtk4 platform skeleton + Resolve"
```

---

## Task 3: Config, scaffold, and templates

**Files:**
- Create: `codegen/platform/gtk4/scaffold.go`
- Create: `codegen/platform/gtk4/templates/model.go.tmpl`

- [ ] **Step 1: Create scaffold.go**

```go
// codegen/platform/gtk4/scaffold.go
package gtk4

import "embed"

// Config controls GTK4 code generation.
type Config struct {
	Package string `option:"package"` // default: "main"
	Main    bool   `option:"main"`    // default: true — emit main() entrypoint
	GIRPath string `option:"gir"`     // default: "" — autodetect
}

func (c Config) withDefaults() Config {
	if c.Package == "" {
		c.Package = "main"
	}
	return c
}

//go:embed templates/*
var templateFS embed.FS

// templateData is passed to model.go.tmpl.
type templateData struct {
	Package      string
	Main         bool
	Structs      []structData
	Binds        []bindData
	Computeds    []computedData
	WidgetFields []widgetFieldData
	UpdaterNames []string
	FunctionCode string
	Imports      map[string]bool
}

type structData struct {
	Name   string
	Fields []structFieldData
}

type structFieldData struct {
	Name string
	Type string
}

type bindData struct {
	Name        string
	GoType      string
	InitVal     string
	Getter      string
	SetterExtra string
}

type computedData struct {
	Name   string
	GoType string
	Body   string
}

type widgetFieldData struct {
	Name   string
	GoType string
}
```

- [ ] **Step 2: Create model.go.tmpl**

```
{{/* codegen/platform/gtk4/templates/model.go.tmpl */}}
package {{.Package}}

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <stdlib.h>

extern void snglGoDispatch(int idx);
static void sngl_cb(gpointer data) {
    snglGoDispatch(GPOINTER_TO_INT(data));
}
static void sngl_connect(gpointer widget, const char* signal, int idx) {
    g_signal_connect(widget, signal, G_CALLBACK(sngl_cb), GINT_TO_POINTER(idx));
}

extern void snglIdleCallback(void* fn);
static gboolean sngl_idle_tramp(gpointer data) {
    snglIdleCallback(data);
    return G_SOURCE_REMOVE;
}
static void sngl_gtk_idle_add(void* fn) {
    g_idle_add(sngl_idle_tramp, fn);
}
*/
import "C"

import (
	"runtime"
	"unsafe"
{{- range $path, $_ := .Imports}}
	"{{$path}}"
{{- end}}
)

// snglCallbacks holds registered Go signal handlers indexed by int.
var snglCallbacks []func()

//export snglGoDispatch
func snglGoDispatch(idx C.int) {
	if int(idx) < len(snglCallbacks) {
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
}

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

{{- range .Structs}}
type {{.Name}} struct {
{{- range .Fields}}
	{{.Name}} {{.Type}}
{{- end}}
}

{{end}}
// Model holds the state and widget references for this SNGL UI.
type Model struct {
{{- range .Binds}}
	{{.Name}} {{.GoType}}
{{- end}}
{{- range .WidgetFields}}
	{{.Name}} {{.GoType}}
{{- end}}
}

// New creates a Model with default values.
func New() *Model {
	m := &Model{
{{- range .Binds}}
		{{.Name}}: {{.InitVal}},
{{- end}}
	}
	return m
}

func (m *Model) doRefresh() {
{{- range .UpdaterNames}}
	m.{{.}}()
{{- end}}
}

{{- range .Computeds}}
func (m *Model) {{.Name}}() {{.GoType}} {
	return {{.Body}}
}

{{end}}
{{- .FunctionCode}}
{{- range .Binds}}
func (m *Model) {{.Getter}}() {{.GoType}} {
	return m.{{.Name}}
}

func (m *Model) Set{{.Getter}}(v {{.GoType}}) {
	m.{{.Name}} = v
{{ .SetterExtra -}}
}

{{end}}
```

Note: `runtime` is imported because `main.go` code (emitted separately, not in template) uses `runtime.LockOSThread()`. The template imports it unconditionally; if unused, `go/format` would complain — so we add a `_ = runtime.LockOSThread` or only include it when `Main == true`. Actually: `runtime` is only needed in the generated `main()` function, which is appended after the template. The template itself doesn't use `runtime`. So remove it from the template imports and add it to the dynamic import set in `newGTK4TemplateData` when `cfg.Main == true`.

Update the template — replace the `runtime` import with a conditional:

```
{{- if .Main}}
	"runtime"
{{- end}}
```

Actually the `//export` functions and `snglCallbacks` etc. need to be in the file with `import "C"`. And `main()` also needs `runtime`. Since everything is in `model.go`, `runtime` is always needed when `Main == true`. Handle via the `Imports` map in `newGTK4TemplateData`.

- [ ] **Step 3: Verify the template compiles (structural check)**

```bash
go build ./codegen/platform/gtk4/... 2>&1
```

Expected: builds cleanly (templates are not executed yet).

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/gtk4/scaffold.go codegen/platform/gtk4/templates/
git commit -m "feat: gtk4 scaffold, config, and model template"
```

---

## Task 4: `gtk4.sngl` stdlib

**Files:**
- Modify: `codegen/platform/gtk4/gtk4.sngl`

- [ ] **Step 1: Replace placeholder with real stdlib**

```
// GTK4 platform package.

struct Options {
    package string
    main bool
    gir string
}

// --- Core widgets ---

component button(label string = "", onClick () => void = {}) {
    GtkButton(label=label, clicked=onClick) {}
}

component label(text string = "") {
    GtkLabel(label=text) {}
}

component entry(value string = "", onChange (string) => void = {}) {
    GtkEntry(text=value, changed=onChange) {}
}

component checkbox(label string = "", checked bool = false, onChange (bool) => void = {}) {
    GtkCheckButton(label=label, active=checked, toggled=onChange) {}
}

component image(src string = "") {
    GtkImage(file=src) {}
}

// --- Containers ---

component vbox(spacing int = 6) {
    GtkBox(orientation="vertical", spacing=spacing) {
        children...
    }
}

component hbox(spacing int = 6) {
    GtkBox(orientation="horizontal", spacing=spacing) {
        children...
    }
}

component scroll() {
    GtkScrolledWindow() {
        children...
    }
}

// --- Window ---

component window(title string = "SNGL App", width int = 800, height int = 600) {
    GtkApplicationWindow(title=title, defaultWidth=width, defaultHeight=height) {
        children...
    }
}
```

Note: SNGL syntax for default params and `children...` may need adjustment based on the actual parser. Check `lib/*.sngl` and `codegen/platform/fyne/fyne.sngl` for the correct syntax for optional params and slot children. Use exactly the same syntax patterns found there.

- [ ] **Step 2: Verify it parses**

```bash
go build ./codegen/platform/gtk4/... 2>&1
```

If parse fails, examine the error and adjust the `.sngl` syntax to match existing patterns in `fyne.sngl`.

- [ ] **Step 3: Commit**

```bash
git add codegen/platform/gtk4/gtk4.sngl
git commit -m "feat: gtk4.sngl stdlib components"
```

---

## Task 5: View renderer

**Files:**
- Create: `codegen/platform/gtk4/view_ir.go`

This is the core complexity: rendering SNGL component trees to GTK4 C calls.

- [ ] **Step 1: Create view_ir.go**

```go
// codegen/platform/gtk4/view_ir.go
package gtk4

import (
	"fmt"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/ir"
)

// widgetField tracks a widget stored on the Model struct.
type widgetField struct {
	name   string // e.g. "btn0"
	goType string // e.g. "*C.GtkButton"
}

// widgetUpdater tracks a reactive setter for one widget property.
type widgetUpdater struct {
	name string          // method name, e.g. "updateBtn0Label"
	body string          // pre-rendered body, e.g. "C.gtk_button_set_label(m.btn0, C.CString(m.GetName()))"
	deps map[string]bool // state fields this depends on
}

func (u widgetUpdater) DepFields() map[string]bool { return u.deps }

// viewContext tracks state during BuildUI code generation.
type viewContext struct {
	gc           *golang.GoIRContext
	ctx          *codegen.CodegenCtx
	registry     *gir.TypeRegistry
	buf          *strings.Builder
	indent       int
	widgetCount  int
	fields       []widgetField
	updaters     []widgetUpdater
	// propScope holds prop name→expr substitutions for inline stdlib component rendering.
	propScope map[string]ir.Expr
}

func (vc *viewContext) line(format string, args ...any) {
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
}

func (vc *viewContext) addField(name, goType string) {
	vc.fields = append(vc.fields, widgetField{name, goType})
}

// renderStmt dispatches on IR statement type.
func (vc *viewContext) renderStmt(stmt ir.Stmt, resultVar string) {
	switch s := stmt.(type) {
	case *ir.NodeInst:
		vc.renderNode(s, resultVar)
	case *ir.IfStmt:
		cond := vc.gc.EvalExpr(s.Cond)
		vc.line("if %s {", cond)
		vc.indent++
		for _, child := range s.Body {
			vc.renderStmt(child, resultVar)
		}
		vc.indent--
		if len(s.Else) > 0 {
			vc.line("} else {")
			vc.indent++
			for _, child := range s.Else {
				vc.renderStmt(child, resultVar)
			}
			vc.indent--
		}
		vc.line("}")
	default:
		// Non-visual statements: evaluate but don't assign to resultVar.
		for _, line := range vc.gc.EvalStmt(s) {
			vc.line("%s", line)
		}
	}
}

// renderNode dispatches on the resolved component.
func (vc *viewContext) renderNode(n *ir.NodeInst, resultVar string) {
	// GIR-resolved widget: name starts with "Gtk"
	if strings.HasPrefix(n.Name, "Gtk") {
		className := strings.TrimPrefix(n.Name, "Gtk")
		if info, ok := vc.registry.Classes[className]; ok {
			vc.renderGtkWidget(n, info, resultVar)
			return
		}
	}

	// User-defined component (in the user's .sngl file).
	if n.Component != nil && isUserDefined(n.Component, vc.ctx) {
		vc.renderUserComponent(n, resultVar)
		return
	}

	// gtk4.sngl stdlib component: inline its body with prop substitution.
	if n.Component != nil && len(n.Component.Body) > 0 {
		vc.renderStdlibInline(n, resultVar)
		return
	}

	// Fallback: emit nothing (unknown component).
	vc.line("%s = nil", resultVar)
}

func isUserDefined(comp *ir.Component, ctx *codegen.CodegenCtx) bool {
	for _, c := range ctx.Pkg.Components {
		if c == comp {
			return true
		}
	}
	return false
}

// renderStdlibInline inlines a gtk4.sngl stdlib component body with prop substitution.
func (vc *viewContext) renderStdlibInline(n *ir.NodeInst, resultVar string) {
	// Build a prop scope mapping param names to the passed expressions.
	saved := vc.propScope
	scope := make(map[string]ir.Expr)
	if saved != nil {
		for k, v := range saved {
			scope[k] = v
		}
	}
	if n.Component != nil {
		for i, param := range n.Component.Params {
			if i < len(n.Props) {
				scope[param.Name] = n.Props[i].Value
			}
		}
		for _, prop := range n.Props {
			if prop.Name != "" {
				scope[prop.Name] = prop.Value
			}
		}
	}
	vc.propScope = scope
	defer func() { vc.propScope = saved }()

	// Inline each body statement.
	for _, stmt := range n.Component.Body {
		vc.renderStmt(stmt, resultVar)
	}
}

// renderUserComponent calls a pre-rendered method on the Model.
func (vc *viewContext) renderUserComponent(n *ir.NodeInst, resultVar string) {
	methodName := "render" + golang.ExportName(n.Name)
	var args []string
	for _, prop := range n.Props {
		args = append(args, vc.gc.EvalExpr(prop.Value))
	}
	vc.line("%s = m.%s(%s)", resultVar, methodName, strings.Join(args, ", "))
}

// renderGtkWidget emits GTK widget construction + signals + reactive setup.
func (vc *viewContext) renderGtkWidget(n *ir.NodeInst, info *gir.ClassInfo, resultVar string) {
	id := vc.widgetCount
	vc.widgetCount++
	fieldName := fmt.Sprintf("%s%d", girFieldPrefix(info.CType), id)
	cType := "*C." + info.CType
	vc.addField(fieldName, cType)

	// Build constructor call.
	ctorArgs := vc.buildCtorArgs(n, info)
	vc.line("m.%s = (*C.%s)(unsafe.Pointer(C.%s(%s)))",
		fieldName, info.CType, info.Constructor.Name, strings.Join(ctorArgs, ", "))
	vc.line("%s = (*C.GtkWidget)(unsafe.Pointer(m.%s))", resultVar, fieldName)

	// Connect signals.
	for _, sig := range info.Signals {
		expr := vc.lookupPropExpr(n, sig.Name)
		if expr == nil {
			continue
		}
		fnCode := vc.gc.EvalExpr(expr)
		vc.line("{")
		vc.line("\t_idx := len(snglCallbacks)")
		vc.line("\tsnglCallbacks = append(snglCallbacks, %s)", fnCode)
		vc.line("\tC.sngl_connect(unsafe.Pointer(m.%s), C.CString(%q), C.int(_idx))", fieldName, sig.Name)
		vc.line("}")
	}

	// Register reactive updaters for bound props.
	for _, prop := range info.Props {
		expr := vc.lookupPropExpr(n, prop.Name)
		if expr == nil {
			continue
		}
		deps := golang.ExprDeps(expr, vc.gc)
		if len(deps) == 0 {
			continue // static value, no updater needed
		}
		setter := gtkSetter(info.CType, prop.Name)
		if setter == "" {
			continue
		}
		updaterName := fmt.Sprintf("update%s%s%d", golang.ExportName(prop.Name), golang.ExportName(girFieldPrefix(info.CType)), id)
		body := vc.buildSetterCall(setter, info.CType, fieldName, expr, prop.IRType)
		vc.updaters = append(vc.updaters, widgetUpdater{
			name: updaterName,
			body: body,
			deps: deps,
		})
	}

	// Container children.
	if len(n.Children) > 0 {
		childAdd := gtkChildAdd(info.CType)
		for i, child := range n.Children {
			childVar := fmt.Sprintf("child%d_%d", id, i)
			vc.line("var %s *C.GtkWidget", childVar)
			vc.renderStmt(child, childVar)
			if childAdd != "" {
				vc.line("if %s != nil { C.%s((*C.%s)(unsafe.Pointer(m.%s)), %s) }",
					childVar, childAdd, info.CType, fieldName, childVar)
			}
		}
		// For window: set single child.
		if info.CType == "GtkApplicationWindow" && len(n.Children) > 0 {
			lastChild := fmt.Sprintf("child%d_%d", id, len(n.Children)-1)
			vc.line("C.gtk_window_set_child((*C.GtkWindow)(unsafe.Pointer(m.%s)), %s)", fieldName, lastChild)
		}
	}
}

// buildCtorArgs maps NodeInst props to constructor params in order.
func (vc *viewContext) buildCtorArgs(n *ir.NodeInst, info *gir.ClassInfo) []string {
	var args []string
	for _, param := range info.Constructor.Params {
		expr := vc.lookupPropExpr(n, param.Name)
		if expr == nil {
			args = append(args, girDefaultCArg(param.IRType))
		} else {
			args = append(args, vc.irExprToC(expr, param.IRType))
		}
	}
	return args
}

// lookupPropExpr finds the expression for a named prop in a NodeInst,
// checking propScope first (for inlined stdlib components).
func (vc *viewContext) lookupPropExpr(n *ir.NodeInst, name string) ir.Expr {
	if vc.propScope != nil {
		if expr, ok := vc.propScope[name]; ok {
			return expr
		}
	}
	for _, prop := range n.Props {
		if prop.Name == name {
			return prop.Value
		}
	}
	return nil
}

// irExprToC converts a SNGL ir.Expr to a C argument expression string.
func (vc *viewContext) irExprToC(expr ir.Expr, targetType *ir.Type) string {
	goExpr := vc.gc.EvalExpr(expr)
	if targetType == nil {
		return goExpr
	}
	switch targetType.Kind {
	case ir.TypeString:
		return "C.CString(" + goExpr + ")"
	case ir.TypeBool:
		return fmt.Sprintf("func() C.gboolean { if %s { return 1 }; return 0 }()", goExpr)
	case ir.TypeInt:
		return "C.gint(" + goExpr + ")"
	case ir.TypeFloat:
		return "C.gdouble(" + goExpr + ")"
	}
	return goExpr
}

// buildSetterCall emits the body of a reactive updater function.
func (vc *viewContext) buildSetterCall(setter, cType, fieldName string, expr ir.Expr, irType *ir.Type) string {
	arg := vc.irExprToC(expr, irType)
	return fmt.Sprintf("C.%s((*C.%s)(unsafe.Pointer(m.%s)), %s)", setter, cType, fieldName, arg)
}

// girDefaultCArg returns the zero C argument for a GIR type.
func girDefaultCArg(t *ir.Type) string {
	if t == nil {
		return "nil"
	}
	switch t.Kind {
	case ir.TypeString:
		return `C.CString("")`
	case ir.TypeInt:
		return "0"
	case ir.TypeBool:
		return "0"
	case ir.TypeFloat:
		return "0.0"
	}
	return "nil"
}

// girFieldPrefix derives a short Model field prefix from the GTK C type.
// "GtkButton" → "btn", "GtkLabel" → "lbl", "GtkApplicationWindow" → "win".
func girFieldPrefix(cType string) string {
	name := strings.TrimPrefix(cType, "Gtk")
	switch name {
	case "Button":
		return "btn"
	case "Label":
		return "lbl"
	case "Entry":
		return "entry"
	case "CheckButton":
		return "chk"
	case "Box":
		return "box"
	case "ScrolledWindow":
		return "scroll"
	case "ApplicationWindow", "Window":
		return "win"
	case "Image":
		return "img"
	}
	// Generic: first three lowercase letters.
	runes := []rune(strings.ToLower(name))
	if len(runes) > 3 {
		runes = runes[:3]
	}
	return string(runes)
}

// gtkSetter returns the C setter function name for a widget property.
// Uses a hardcoded table for the supported widget set.
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
}

func gtkSetter(cType, prop string) string {
	if m, ok := gtkSetterTable[cType]; ok {
		return m[prop]
	}
	return ""
}

// gtkChildAdd returns the C function to append a child to a container.
var gtkChildAddTable = map[string]string{
	"GtkBox":            "gtk_box_append",
	"GtkScrolledWindow": "gtk_scrolled_window_set_child",
}

func gtkChildAdd(cType string) string {
	return gtkChildAddTable[cType]
}

// camelToSnake converts "GtkCheckButton" → "gtk_check_button".
func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteRune('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
```

- [ ] **Step 2: Check if `golang.ExprDeps` exists**

```bash
grep -rn "func ExprDeps\|ExprDeps" /home/jonathan/src/git.duckfam.us/jonathan/sngl/codegen/lang/golang/
```

If `ExprDeps` doesn't exist, replace the dep-tracking logic with a simpler approach: use `vc.gc.EvalExpr(expr)` and check if the result contains `m.Get` (i.e., accesses model state). Or use `codegen.MutatedFields` / `codegen.FindAffected` patterns from Fyne. For the initial version, register an updater for ALL non-nil non-signal props (not just reactive ones) — this is conservative but correct:

```go
// Instead of deps check, register updater for any prop that evaluates to a model getter call.
goExpr := vc.gc.EvalExpr(expr)
if strings.Contains(goExpr, "m.Get") || strings.Contains(goExpr, "m.") {
    // likely reactive
    deps := map[string]bool{} // simplified: trigger doRefresh
    // ...
}
```

For initial correctness, skip reactive updaters entirely and just note them as TODO. The platform will still construct widgets and connect signals correctly; state changes just won't auto-update widget text until reactive is added.

Actually, use `codegen.DepTracker` from `irAnalysis.CommonAnalysis` as Fyne does. The `CommonAnalysis` has `DepTracker()`. Pass `info *irAnalysis` to viewContext and use `info.depTracker()` to find affected updaters after all widgetUpdaters are collected.

For simplicity in this initial version: skip reactive updater registration in view_ir.go. Add a TODO comment. The compiler_ir.go (Task 6) will handle calling `doRefresh()` from all setters as a fallback.

- [ ] **Step 3: Build to check for compile errors**

```bash
go build ./codegen/platform/gtk4/... 2>&1
```

Fix any compile errors. Common issues:
- `unsafe` must be imported
- `C.CString` not available outside cgo file — view_ir.go is NOT a cgo file; it generates code strings that reference C. types. The strings themselves are fine; no actual C calls happen in view_ir.go.
- `golang.ExportName` — verify it's exported from the golang package

```bash
grep -n "func ExportName" /home/jonathan/src/git.duckfam.us/jonathan/sngl/codegen/lang/golang/*.go
```

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/gtk4/view_ir.go
git commit -m "feat: gtk4 view renderer (GTK widget construction + signals)"
```

---

## Task 6: MutationModel emitter

**Files:**
- Modify (replace stub): `codegen/platform/gtk4/compiler_ir.go`

- [ ] **Step 1: Replace the stub with full implementation**

```go
// codegen/platform/gtk4/compiler_ir.go
package gtk4

import (
	"fmt"
	"go/format"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/ir"
)

type irAnalysis struct {
	*codegen.CommonAnalysis
	binds     []irBind
	computeds []irComputed
	goImports map[string]bool
}

type irBind struct {
	name   string
	goType string
	init   string
}

type irComputed struct {
	name   string
	goType string
	fn     *ir.Func
}

func analyzeIR(ctx *codegen.CodegenCtx) *irAnalysis {
	info := &irAnalysis{
		CommonAnalysis: ctx.Analysis,
		goImports:      make(map[string]bool),
	}
	allVars := ctx.Pkg.Vars
	if main := ctx.MainComponent(); main != nil {
		allVars = append(allVars, main.Vars...)
	}
	for _, v := range allVars {
		if v.IsConst {
			continue
		}
		info.binds = append(info.binds, irBind{
			name:   v.Name,
			goType: golang.IRTypeToGo(v.Type),
			init:   irVarInit(v),
		})
	}
	allFuncs := ctx.Pkg.Funcs
	if main := ctx.MainComponent(); main != nil {
		allFuncs = append(allFuncs, main.Funcs...)
	}
	for _, f := range allFuncs {
		if codegen.IsComputed(f) {
			info.computeds = append(info.computeds, irComputed{
				name:   f.Name,
				goType: irFuncReturnType(f),
				fn:     f,
			})
		}
	}
	return info
}

// compilation holds per-request build state.
type compilation struct {
	gen      *Generator
	ctx      *codegen.CodegenCtx
	info     *irAnalysis
	cfg      Config
	registry *gir.TypeRegistry
}

var _ codegen.MutationModelEmitter = (*compilation)(nil)

func (c *compilation) BuildMutationModel(req *codegen.Request, analysis *codegen.CommonAnalysis) (*codegen.MutationModel, error) {
	if req.Lang.LanguageIdentifier() != "go" {
		return nil, fmt.Errorf("gtk4: only lang=go is supported")
	}
	if err := codegen.ApplyOptions(&c.cfg, req.Options); err != nil {
		return nil, fmt.Errorf("gtk4: %w", err)
	}
	c.cfg = c.cfg.withDefaults()

	// Load GIR registry.
	girPath, err := resolveGIRPath(c.cfg.GIRPath)
	if err != nil {
		return nil, err
	}
	c.registry, err = gir.ParseGIR(girPath)
	if err != nil {
		return nil, fmt.Errorf("gtk4: cannot parse GIR: %w", err)
	}
	// Sync to generator so Resolve works during subsequent checker calls.
	if c.gen != nil {
		c.gen.registry = c.registry
	}

	c.ctx = codegen.NewCodegenCtx(req, "gtk4")
	c.info = analyzeIR(c.ctx)

	var stmts []ir.Stmt
	if main := c.ctx.MainComponent(); main != nil {
		stmts = main.Body
	}
	return c.ctx.BuildMutation(stmts), nil
}

func (c *compilation) EmitFromMutation(_ *codegen.MutationModel, req *codegen.Request) (*codegen.Response, error) {
	src, err := c.emitIR()
	if err != nil {
		return nil, err
	}
	formatted, err := format.Source(src)
	if err != nil {
		return &codegen.Response{Error: fmt.Sprintf("generated code formatting error: %v\n%s", err, src)}, nil
	}
	if h := codegen.Header("gtk4", req.Source, "// ", ""); h != "" {
		formatted = append([]byte(h), formatted...)
	}
	return &codegen.Response{
		Files: []*codegen.OutputFile{
			codegen.BytesFile("model.go", formatted),
		},
	}, nil
}

func (c *compilation) emitIR() ([]byte, error) {
	gc := golang.NewIRContext(c.ctx.ExprCtx)
	if main := c.ctx.MainComponent(); main != nil {
		gc = golang.NewIRContext(c.ctx.ExprCtx.ForComponent(main))
	}

	// --- Phase 1: Render BuildUI into buffer, collecting widget fields + updaters ---
	var buildBuf strings.Builder
	wins := c.ctx.Windows()

	vc := &viewContext{
		gc:       gc,
		ctx:      c.ctx,
		registry: c.registry,
		buf:      &buildBuf,
		indent:   1,
	}

	if len(wins) > 0 && len(wins[0].Body) > 0 {
		bodyStmts := wins[0].Body
		if len(bodyStmts) == 1 {
			vc.line("var content *C.GtkWidget")
			vc.renderStmt(bodyStmts[0], "content")
		} else {
			for i, stmt := range bodyStmts {
				childVar := fmt.Sprintf("part%d", i)
				vc.line("var %s *C.GtkWidget", childVar)
				vc.renderStmt(stmt, childVar)
			}
		}
	}

	// --- Phase 2: Pre-render user component methods ---
	var componentCodes []string
	for _, cc := range c.ctx.NonMainComponents() {
		code, compFields, compUpdaters := c.renderUserComponentMethod(cc, gc)
		componentCodes = append(componentCodes, code)
		vc.fields = append(vc.fields, compFields...)
		vc.updaters = append(vc.updaters, compUpdaters...)
	}

	// --- Phase 3: Build template data ---
	td, err := c.newGTK4TemplateData(vc)
	if err != nil {
		return nil, err
	}

	// Pre-render computed bodies.
	for _, comp := range c.info.computeds {
		body := `""`
		if comp.fn != nil && len(comp.fn.Block) == 1 {
			if ret, ok := comp.fn.Block[0].(*ir.Return); ok && ret.Value != nil {
				body = gc.EvalExpr(ret.Value)
			}
		}
		td.Computeds = append(td.Computeds, computedData{
			Name:   comp.name,
			GoType: comp.goType,
			Body:   body,
		})
	}

	// Pre-render user functions.
	allFuncs := c.ctx.Pkg.Funcs
	if main := c.ctx.MainComponent(); main != nil {
		allFuncs = append(allFuncs, main.Funcs...)
	}
	var funcBuf strings.Builder
	for _, fn := range allFuncs {
		if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
			continue
		}
		emitGTK4Func(&funcBuf, fn, gc)
	}
	td.FunctionCode = funcBuf.String()

	// --- Phase 4: Render template + append dynamic code ---
	tmplFiles := codegen.RenderTemplates(templateFS, "templates", td)
	var b strings.Builder
	if len(tmplFiles) > 0 {
		tmplFiles[0].WriteTo(&b)
	}

	// Append BuildUI.
	b.WriteString("// BuildUI creates the widget tree. Call with the GtkApplication.\n")
	b.WriteString("func (m *Model) BuildUI(app *C.GtkApplication) *C.GtkWidget {\n")
	b.WriteString(buildBuf.String())
	b.WriteString("\treturn content\n")
	b.WriteString("}\n\n")

	// Append reactive updaters.
	for _, u := range vc.updaters {
		fmt.Fprintf(&b, "func (m *Model) %s() {\n\t%s\n}\n\n", u.name, u.body)
	}
	td.UpdaterNames = updaterNames(vc.updaters)

	// Append component methods.
	for _, code := range componentCodes {
		b.WriteString(code)
	}

	// Append main().
	if c.cfg.Main {
		emitGTK4Main(&b, c.cfg)
	}

	return []byte(b.String()), nil
}

func (c *compilation) newGTK4TemplateData(vc *viewContext) (templateData, error) {
	td := templateData{
		Package: c.cfg.Package,
		Main:    c.cfg.Main,
	}
	td.Imports = map[string]bool{
		"unsafe": true,
	}
	if c.cfg.Main {
		td.Imports["runtime"] = true
		td.Imports["fmt"] = true
	}
	for p := range c.info.goImports {
		td.Imports[p] = true
	}

	// Structs.
	gc := golang.NewIRContext(c.ctx.ExprCtx)
	for _, sd := range c.info.Structs {
		s := structData{Name: golang.ExportName(sd.Name)}
		for _, f := range sd.Fields {
			s.Fields = append(s.Fields, structFieldData{
				Name: golang.ExportName(f.Name),
				Type: golang.IRTypeToGo(f.Type),
			})
		}
		td.Structs = append(td.Structs, s)
	}

	// Binds.
	for _, bind := range c.info.binds {
		getter := golang.ExportName(bind.name)
		bd := bindData{
			Name:    bind.name,
			GoType:  bind.goType,
			InitVal: bind.init,
			Getter:  getter,
		}
		// Add doRefresh() call in setter (conservative: always refresh on any state change).
		bd.SetterExtra = "\tm.doRefresh()\n"
		td.Binds = append(td.Binds, bd)
	}

	// Widget fields from view rendering.
	for _, wf := range vc.fields {
		td.WidgetFields = append(td.WidgetFields, widgetFieldData{
			Name:   wf.name,
			GoType: wf.goType,
		})
	}

	// Updater names.
	td.UpdaterNames = updaterNames(vc.updaters)

	_ = gc
	return td, nil
}

func updaterNames(updaters []widgetUpdater) []string {
	var names []string
	for _, u := range updaters {
		names = append(names, u.name)
	}
	return names
}

func (c *compilation) renderUserComponentMethod(
	cc *codegen.ComponentCtx,
	gc *golang.GoIRContext,
) (code string, fields []widgetField, updaters []widgetUpdater) {
	methodName := "render" + golang.ExportName(cc.Component.Name)

	var params []string
	for _, p := range cc.Props {
		params = append(params, p.Name+" "+golang.IRTypeToGo(p.Type))
	}

	compGC := gc.ForComponent(cc.Component)
	vc := &viewContext{
		gc:       compGC,
		ctx:      c.ctx,
		registry: c.registry,
		buf:      &strings.Builder{},
		indent:   1,
	}

	var b strings.Builder
	fmt.Fprintf(&b, "func (m *Model) %s(%s) *C.GtkWidget {\n", methodName, strings.Join(params, ", "))
	vc.line("var result *C.GtkWidget")
	for _, stmt := range cc.Body {
		vc.renderStmt(stmt, "result")
	}
	b.WriteString(vc.buf.String())
	b.WriteString("\treturn result\n}\n\n")

	return b.String(), vc.fields, vc.updaters
}

func emitGTK4Func(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = p.Name + " " + golang.IRTypeToGo(p.Type)
	}
	retType := ""
	if fn.Return != nil && fn.Return.Kind != ir.TypeDyn {
		retType = golang.IRTypeToGo(fn.Return)
	}
	goName := golang.ExportName(fn.Name)
	localGC := gc
	for _, p := range fn.Params {
		localGC = localGC.WithLocal(p.Name)
	}
	if len(fn.Block) > 0 {
		fmt.Fprintf(b, "func (m *Model) %s(%s) %s {\n", goName, strings.Join(params, ", "), retType)
		for _, stmt := range fn.Block {
			for _, line := range localGC.EvalStmt(stmt) {
				fmt.Fprintf(b, "\t%s\n", line)
			}
		}
		b.WriteString("}\n\n")
	}
}

func emitGTK4Main(b *strings.Builder, cfg Config) {
	b.WriteString("//export snglActivate\n")
	b.WriteString("func snglActivate(app *C.GtkApplication, _ C.gpointer) {\n")
	b.WriteString("\tm := New()\n")
	b.WriteString("\twin := m.BuildUI(app)\n")
	b.WriteString("\tC.gtk_widget_show(win)\n")
	b.WriteString("}\n\n")

	b.WriteString("func main() {\n")
	b.WriteString("\truntime.LockOSThread()\n")
	fmt.Fprintf(b, "\tapp := C.gtk_application_new(C.CString(%q), C.G_APPLICATION_DEFAULT_FLAGS)\n", cfg.Package)
	b.WriteString("\tC.g_signal_connect_data((*C.GObject)(unsafe.Pointer(app)),\n")
	b.WriteString("\t\tC.CString(\"activate\"),\n")
	b.WriteString("\t\tC.GCallback(C.snglActivate), nil, nil, 0)\n")
	b.WriteString("\tC.g_application_run((*C.GApplication)(unsafe.Pointer(app)), 0, nil)\n")
	b.WriteString("\t_ = fmt.Sprint\n")
	b.WriteString("}\n")
}

func irVarInit(v *ir.Var) string {
	if v.Init == nil {
		return golang.ZeroValueGo(golang.IRTypeToGo(v.Type))
	}
	return golang.IRLiteralToGo(v.Init)
}

func irFuncReturnType(f *ir.Func) string {
	if f.Return != nil && f.Return.Kind != ir.TypeDyn {
		return golang.IRTypeToGo(f.Return)
	}
	return ""
}
```

- [ ] **Step 2: Check that key helper functions exist**

```bash
grep -n "func NewIRContext\|func IRTypeToGo\|func ZeroValueGo\|func IRLiteralToGo\|func ExportName\|func IsComputed" \
    /home/jonathan/src/git.duckfam.us/jonathan/sngl/codegen/lang/golang/*.go | head -20
```

Also verify `codegen.NewCodegenCtx`, `ctx.ExprCtx`, `ctx.MainComponent`, `ctx.Windows`, `ctx.NonMainComponents`, `codegen.RenderTemplates`:

```bash
grep -n "func NewCodegenCtx\|ExprCtx\b\|func.*MainComponent\|func.*Windows\|func.*NonMainComponents" \
    /home/jonathan/src/git.duckfam.us/jonathan/sngl/codegen/*.go | head -20
```

Fix any missing imports or wrong function names.

- [ ] **Step 3: Build**

```bash
go build ./codegen/platform/gtk4/... 2>&1
```

Fix compile errors. Common issues:
- `gc.ForComponent` — verify signature in golang package
- `c.ctx.ExprCtx.ForComponent` — check if `ExprCtx` is a field or method
- `cc.Body` vs `cc.Component.Body` — check `ComponentCtx` struct fields

```bash
grep -n "type ComponentCtx\|ComponentCtx struct\|\.Body\b\|\.Props\b" \
    /home/jonathan/src/git.duckfam.us/jonathan/sngl/codegen/codegenctx.go | head -20
```

- [ ] **Step 4: Run full test suite**

```bash
go tool verify 2>&1 | tail -10
```

Expected: tests pass. If `go/format` complains about generated code, add debug logging to see the raw source before formatting.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/gtk4/compiler_ir.go
git commit -m "feat: gtk4 MutationModel emitter"
```

---

## Task 7: Integration test

**Files:**
- Create: `cmd/sngl/testdata/compile_gtk4_button.txt`

- [ ] **Step 1: Write the txtar test**

The test uses `--opt gir=./Gtk-4.0-test.gir` pointing at a minimal GIR file embedded in the archive. This avoids needing GTK4 installed on CI.

```
# Verify that gtk4 platform compiles a button+label to GTK4 cgo output.
# Uses --opt gir= to point at a minimal inline GIR fixture.

sngl compile --platform=gtk4 --lang=go --opt=main=true --opt=gir=./Gtk-4.0-test.gir --out=out main.sngl
exists out/model.go

# cgo preamble must be present
grep '#include <gtk/gtk.h>' out/model.go

# import "C" must be present  
grep 'import "C"' out/model.go

# GTK button constructor must appear
grep 'gtk_button_new_with_label' out/model.go

# GTK application entrypoint must appear
grep 'snglActivate' out/model.go

-- Gtk-4.0-test.gir --
<?xml version="1.0"?>
<repository version="1.2"
  xmlns="http://www.gtk.org/introspection/core/1.0"
  xmlns:c="http://www.gtk.org/introspection/c/1.0"
  xmlns:glib="http://www.gtk.org/introspection/glib/1.0">
  <namespace name="Gtk" version="4.0">
    <class name="Button" c:type="GtkButton">
      <constructor name="new_with_label" c:identifier="gtk_button_new_with_label">
        <parameters>
          <parameter name="label" transfer-ownership="none" nullable="1">
            <type name="utf8" c:type="const gchar*"/>
          </parameter>
        </parameters>
      </constructor>
      <property name="label" writable="1">
        <type name="utf8" c:type="gchar*"/>
      </property>
      <glib:signal name="clicked">
        <return-value transfer-ownership="none">
          <type name="none" c:type="void"/>
        </return-value>
      </glib:signal>
    </class>
    <class name="Label" c:type="GtkLabel">
      <constructor name="new" c:identifier="gtk_label_new">
        <parameters>
          <parameter name="str" transfer-ownership="none" nullable="1">
            <type name="utf8" c:type="const gchar*"/>
          </parameter>
        </parameters>
      </constructor>
      <property name="label" writable="1">
        <type name="utf8" c:type="gchar*"/>
      </property>
    </class>
    <class name="Box" c:type="GtkBox">
      <constructor name="new" c:identifier="gtk_box_new">
        <parameters>
          <parameter name="orientation" transfer-ownership="none">
            <type name="gint" c:type="GtkOrientation"/>
          </parameter>
          <parameter name="spacing" transfer-ownership="none">
            <type name="gint" c:type="gint"/>
          </parameter>
        </parameters>
      </constructor>
    </class>
    <class name="ApplicationWindow" c:type="GtkApplicationWindow">
      <constructor name="new" c:identifier="gtk_application_window_new">
        <parameters>
          <parameter name="application" transfer-ownership="none">
            <type name="GtkApplication" c:type="GtkApplication*"/>
          </parameter>
        </parameters>
      </constructor>
      <property name="title" writable="1">
        <type name="utf8" c:type="gchar*"/>
      </property>
    </class>
  </namespace>
</repository>

-- main.sngl --
component main {
  var count int
  window(title="GTK4 Test") {
    vbox {
      label(text="Count: {count}")
      button(label="Increment", onClick={ count = count + 1 })
    }
  }
}
```

- [ ] **Step 2: Run the test**

```bash
go test ./cmd/sngl/... -run TestScript/compile_gtk4_button -v 2>&1
```

Expected: PASS. If it fails, read the output carefully.

Common failure: `sngl compile` returns an error. Check what the error is:
- If "GIR file not found": the `--opt gir=` path is not being resolved relative to the test working dir. Check how `compile.go` resolves the `gir` option path (it may need to be resolved like `projectDir`).
- If "cannot parse GIR": the minimal GIR XML has a syntax issue.
- If "formatting error": the generated Go code has a syntax error — add `t.Log(output)` to see it.

- [ ] **Step 3: Debug if needed**

If the generated model.go has syntax errors, run the compile command with a real output dir and inspect:

```bash
cd /tmp && mkdir gtk4test && cat > /tmp/gtk4test/main.sngl << 'EOF'
component main {
  var count int
  window(title="Test") {
    vbox {
      label(text="hello")
      button(label="click", onClick={ count = count + 1 })
    }
  }
}
EOF
sngl compile --platform=gtk4 --lang=go --opt=main=true --opt=gir=/usr/share/gir-1.0/Gtk-4.0.gir --out=/tmp/gtk4test/out /tmp/gtk4test/main.sngl
cat /tmp/gtk4test/out/model.go
```

Fix any issues in the generator code.

- [ ] **Step 4: Run full test suite**

```bash
go tool verify 2>&1 | tail -5
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/sngl/testdata/compile_gtk4_button.txt
git commit -m "test: gtk4 integration test (txtar)"
```

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Task |
|-----------------|------|
| `gtk4` platform identifier | Task 2 |
| GIR file autodetect from standard paths | Task 2 |
| `--opt gir=` override | Task 3 (Config.GIRPath) |
| Hard error on GIR not found | Task 2 (resolveGIRPath) |
| `Resolve("GtkButton")` → `ir.Component` | Task 2 |
| GIR type mapping | Task 1 (girTypeToIR) |
| gtk4.sngl stdlib: button, label, entry, vbox, hbox, checkbox, image, scroll, window | Task 4 |
| Widget constructor emission | Task 5 (renderGtkWidget) |
| Signal callback registration | Task 5 (sngl_connect + snglCallbacks) |
| Reactive updaters (setter table) | Task 5 (gtkSetterTable) |
| Container children (gtk_box_append) | Task 5 (gtkChildAddTable) |
| MutationModel emitter | Task 6 |
| Cgo preamble with `#cgo pkg-config: gtk4` | Task 3 (template) |
| `main()` with `runtime.LockOSThread()` | Task 6 (emitGTK4Main) |
| `gtkPost` async helper | Task 3 (template) |
| `//export` callbacks in model.go | Task 3 (template) |
| Hard error for non-go lang | Task 6 (BuildMutationModel) |
| Hard error for missing CCompiler | Not applicable — gtk4 generates its own preamble, not via CCompiler |
| `platforms.go` blank import | Task 2 |
| Integration test with inline GIR | Task 7 |
| GIR class unknown → nil → checker error | Task 2 (Resolve returns nil) |
| GIR type unmappable → TypeDyn | Task 1 (girTypeToIR default) |
| Non-go lang error | Task 6 |

**Key risk: `viewContext.renderNode` dispatch** — The GIR widget check (`strings.HasPrefix(n.Name, "Gtk")`) must occur BEFORE the user-component check. GIR-resolved components won't be in `ctx.Pkg.Components` but could accidentally fall through to `renderStdlibInline` if they have an empty body. Since `Resolve` returns a `*ir.Component{Body: nil}`, the `len(n.Component.Body) > 0` guard in `renderStdlibInline` will prevent this.

**Key risk: `//export` in file with cgo preamble** — cgo forbids `//export` in a file that has a cgo preamble comment. The template puts both the preamble and `//export` in `model.go`. This is a real cgo limitation. Fix: split into two files:
- `model.go.tmpl` — has cgo preamble, NO `//export`
- `callbacks.go.tmpl` — has `import "C"` (no preamble), has `//export snglGoDispatch`, `//export snglIdleCallback`

Update Task 3 to create `templates/callbacks.go.tmpl` separately and update `compiler_ir.go` to emit both files.

The `callbacks.go.tmpl`:
```
package {{.Package}}

/*
#include <gtk/gtk.h>
*/
import "C"

import "unsafe"

// snglCallbacks holds registered Go signal handlers indexed by int.
var snglCallbacks []func()

//export snglGoDispatch
func snglGoDispatch(idx C.int) {
	if int(idx) < len(snglCallbacks) {
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
}
```

And `model.go.tmpl` removes those functions.

The `EmitFromMutation` must then return two `*codegen.OutputFile` entries: `model.go` and `callbacks.go`.

Implement this split in Task 3 and Task 6.
