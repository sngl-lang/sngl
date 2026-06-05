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
// window holding a button whose @click handler calls a go:// native func —
// i.e. a Backend-placed handler.
func backendHandlerPkg() *ir.Package {
	imp := &ir.Import{
		Alias:  "api",
		AST:    &ast.Import{Path: "go://example.com/api"},
		Native: &ir.NativeImport{ImportPath: "example.com/api"},
	}
	handler := EventHandlerValue(&ir.Func{Block: []ir.Stmt{
		&ir.Assign{
			Target: &ir.Ident{Name: "count"},
			Value:  &ir.Call{Func: &ir.Func{NativePkg: "example.com/api", Name: "Persist"}},
		},
	}})
	btn := &ir.NodeInst{Name: "button", Handlers: []ir.EventHandler{handler}}
	win := &ir.Window{
		Name: "app",
		Href: &ir.Literal{Raw: `"/"`, Type: ir.TypString},
		Body: []ir.Stmt{btn},
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
