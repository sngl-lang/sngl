package html

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
)

// noneLang returns the registered "none" (static-site) language translator.
func noneLang(t *testing.T) codegen.LangTranslator {
	t.Helper()
	l := codegen.LookupLang("none")
	if l == nil {
		t.Fatal("none language translator not registered")
	}
	return l
}

// backendHandlerPkg builds a minimal package whose main component has one
// window holding a button whose @click handler calls a go: native func —
// i.e. a Backend-placed handler.
func backendHandlerPkg() *ir.Package {
	imp := &ir.Import{
		Alias:  "api",
		AST:    &ast.Import{Path: "go:example.com/api"},
		Native: &ir.NativeImport{ImportPath: "example.com/api"},
	}
	handler := EventHandlerValue(&ir.Func{Block: []ir.Stmt{
		&ir.Assign{
			Target: &ir.Ident{Name: "count"},
			Value:  &ir.Call{Func: &ir.Func{Foreign: ir.Foreign{Path: "example.com/api"}, Name: "Persist"}},
		},
	}})
	btn := &ir.NodeInst{Name: "button", Handlers: []ir.EventHandler{handler}}
	win := &ir.Window{
		Name:      "window",
		ID:        "app",
		Component: &ir.Component{Name: "window", Builtin: ir.BuiltinWindow},
		Props:     []ir.Arg{{Name: ir.WindowHref, Value: &ir.Literal{Value: `"/"`, Type: ir.TypString}}},
		Children:  []ir.Stmt{btn},
	}
	return &ir.Package{
		Imports:    []*ir.Import{imp},
		Components: []*ir.Component{{Name: "main", Body: []ir.Stmt{win}}},
	}
}

// EventHandlerValue is a tiny constructor so tests can build a value (not
// pointer) EventHandler inline.
func EventHandlerValue(fn *ir.Func) ir.EventHandler { return ir.EventHandler{Func: fn} }

// TestStaticBackendHandlerErrors pins Task 6.1: a backend handler in a
// serverless (lang=none) build is a clear, named error.
func TestStaticBackendHandlerErrors(t *testing.T) {
	gen := &Generator{}
	mem := codegen.NewMemSink()
	err := gen.Generate(&codegen.Request{
		Pkg:  backendHandlerPkg(),
		Lang: noneLang(t),
	}, mem)
	if err == nil {
		t.Fatal("expected error for backend handler in static build, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "app") || !strings.Contains(msg, "server") {
		t.Fatalf("error must name the window and mention a server, got: %v", msg)
	}
}

// directivePkg wraps a single expression in a text binding inside main, so
// checkPlacementDirectives sees it during the package walk.
func directivePkg(binding ir.Expr) *ir.Package {
	text := &ir.NodeInst{
		Name:  "text",
		Props: []ir.Arg{{Value: binding}},
	}
	return &ir.Package{
		Components: []*ir.Component{{Name: "main", Body: []ir.Stmt{text}}},
	}
}

// TestBackendConstExprErrors pins Task 6.2: html.backend wrapping a constant
// (non-func) expression is a clear not-yet-implemented error.
func TestBackendConstExprErrors(t *testing.T) {
	binding := intrinsicCall("html.backend", &ir.Literal{Value: "42", Type: ir.TypInt})
	err := checkPlacementDirectives(directivePkg(binding))
	if err == nil {
		t.Fatal("expected error for html.backend(const), got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not yet implemented") || !strings.Contains(msg, "html.backend") {
		t.Fatalf("error must say html.backend const→file not yet implemented, got: %v", msg)
	}
}

// TestFrontendOnBackendValueErrors pins Task 6.3: html.frontend wrapping a
// value explicitly pinned server-side (nested html.backend) is a clear error.
func TestFrontendOnBackendValueErrors(t *testing.T) {
	inner := intrinsicCall("html.backend", &ir.Lambda{Type: &ir.Type{Kind: ir.TypeFunc}})
	binding := intrinsicCall("html.frontend", inner)
	err := checkPlacementDirectives(directivePkg(binding))
	if err == nil {
		t.Fatal("expected error for html.frontend(html.backend(...)), got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "html.frontend") || !strings.Contains(msg, "server") {
		t.Fatalf("error must explain client/server contradiction, got: %v", msg)
	}
}

// TestFrontendGoCallAllowed guards the Phase 5 contract against the 6.3 rule:
// a bare go: call under html.frontend is the WASM opt-in and must NOT error.
func TestFrontendGoCallAllowed(t *testing.T) {
	pkg := pkgWithImport("go", "example.com/api")
	pkg.Components = []*ir.Component{{Name: "main", Body: []ir.Stmt{
		&ir.NodeInst{Name: "text", Props: []ir.Arg{{
			Value: intrinsicCall("html.frontend", goCall("example.com/api")),
		}}},
	}}}
	if err := checkPlacementDirectives(pkg); err != nil {
		t.Fatalf("html.frontend(go_call) is the WASM opt-in and must not error, got: %v", err)
	}
}
