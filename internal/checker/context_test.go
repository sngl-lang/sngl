package checker_test

import (
	"fmt"
	"os"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// parseFile parses and checks a .sngl file, returning the package and any
// error-severity diagnostics.
func parseFile(t *testing.T, path string) (*ir.Package, []ir.Diagnostic) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc, err := parser.Parse(path, src)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	var errs []ir.Diagnostic
	for _, d := range diags {
		if d.Severity == ir.Error {
			errs = append(errs, d)
		}
	}
	return pkg, errs
}

// TestContextBasic verifies that testdata/context_decl_basic.sngl checks
// without errors and registers a Context named "theme" of inferred string type.
// The stdlib contributes one built-in context (#locale), so we look up by name.
func TestContextBasic(t *testing.T) {
	pkg, errs := parseFile(t, "../../testdata/context_decl_basic.sngl")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	var ctx *ir.Context
	for _, c := range pkg.Contexts {
		if c.Name == "theme" {
			ctx = c
			break
		}
	}
	if ctx == nil {
		t.Fatalf("context \"theme\" not found; got %d contexts: %v", len(pkg.Contexts), contextNames(pkg.Contexts))
	}
	if ctx.Typ == nil {
		t.Fatalf("Typ is nil")
	}
	if ctx.Typ.Kind != ir.TypeString {
		t.Errorf("Typ.Kind = %v, want TypeString", ctx.Typ.Kind)
	}
}

// TestContextInferredType verifies that testdata/context_decl_inferred_type.sngl
// checks without errors and registers all five user contexts with correct types.
// The stdlib contributes one built-in context (#locale), so we look up by name.
func TestContextInferredType(t *testing.T) {
	pkg, errs := parseFile(t, "../../testdata/context_decl_inferred_type.sngl")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	// Build a name→context map for stable lookups independent of insertion order.
	byName := make(map[string]*ir.Context, len(pkg.Contexts))
	for _, c := range pkg.Contexts {
		byName[c.Name] = c
	}

	tests := []struct {
		name    string
		wantKnd ir.TypeKind
	}{
		{"s", ir.TypeString},
		{"n", ir.TypeInt},
		{"b", ir.TypeBool},
		{"theme", ir.TypeStruct},
		{"mode", ir.TypeEnum},
	}
	for _, tt := range tests {
		ctx, ok := byName[tt.name]
		if !ok {
			t.Errorf("context %q not found; have: %v", tt.name, contextNames(pkg.Contexts))
			continue
		}
		if ctx.Typ == nil {
			t.Errorf("context %q: Typ is nil", tt.name)
			continue
		}
		if ctx.Typ.Kind != tt.wantKnd {
			t.Errorf("context %q: Typ.Kind = %v, want %v", tt.name, ctx.Typ.Kind, tt.wantKnd)
		}
	}
}

// checkSrc parses inline source and returns error-severity diagnostics.
func checkSrc(t *testing.T, src string) []ir.Diagnostic {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	var errs []ir.Diagnostic
	for _, d := range diags {
		if d.Severity == ir.Error {
			errs = append(errs, d)
		}
	}
	return errs
}

// TestContextNonConstDefaultAccepted verifies that a non-constant default is
// allowed: a context default is an initializer expression evaluated at
// root/provider setup (like a var initializer), not a compile-time constant.
func TestContextNonConstDefaultAccepted(t *testing.T) {
	errs := checkSrc(t, `
var x = 1
context #foo(x)
window #home(title="t") { text(value="") }
`)
	if len(errs) != 0 {
		t.Fatalf("expected non-const default to be accepted, got: %v", errs)
	}
}

// TestContextNoArgsRejected verifies that `context #foo()` with zero args is rejected.
func TestContextNoArgsRejected(t *testing.T) {
	errs := checkSrc(t, `
context #foo()
window #home(title="t") { text(value="") }
`)
	if len(errs) == 0 {
		t.Fatal("expected error for zero-arg context, got none")
	}
}

// TestContextDuplicateNameRejected verifies that declaring two contexts with
// the same name produces a duplicate-declaration error.
func TestContextDuplicateNameRejected(t *testing.T) {
	errs := checkSrc(t, `
context #foo("a")
context #foo("b")
window #home(title="t") { text(value="") }
`)
	if len(errs) == 0 {
		t.Fatal("expected duplicate-declaration error, got none")
	}
}

// TestContextProviderBasic verifies that a context name used as a visual node
// inside a window body produces an ir.ContextProvider.
func TestContextProviderBasic(t *testing.T) {
	pkg, errs := parseFile(t, "../../testdata/context_provider_basic.sngl")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(pkg.Windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(pkg.Windows))
	}
	win := pkg.Windows[0]
	var prov *ir.ContextProvider
	for _, s := range win.Children {
		if p, ok := s.(*ir.ContextProvider); ok {
			prov = p
			break
		}
	}
	if prov == nil {
		t.Fatalf("no ContextProvider found in window body; got: %v", stmtTypes(win.Children))
	}
	if prov.Ref == nil || prov.Ref.Name != "theme" {
		t.Errorf("Ref = %+v, want context named \"theme\"", prov.Ref)
	}
	if prov.Value == nil {
		t.Errorf("Value is nil")
	}
	if len(prov.Children) != 1 {
		t.Errorf("Children len = %d, want 1", len(prov.Children))
	}
}

// TestContextConsumerBasic verifies that a bare ident referring to a
// context resolves to an ir.ContextRead with the correct type.
func TestContextConsumerBasic(t *testing.T) {
	pkg, errs := parseFile(t, "../../testdata/context_consumer_basic.sngl")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	var toolbar *ir.Component
	for _, c := range pkg.Components {
		if c.Name == "Toolbar" {
			toolbar = c
			break
		}
	}
	if toolbar == nil {
		t.Fatalf("Toolbar component not found")
	}
	// theme appears as text(value=theme) — look into the NodeInst's Props.
	var found *ir.ContextRead
	for _, s := range toolbar.Body {
		ni, ok := s.(*ir.NodeInst)
		if !ok {
			continue
		}
		for _, prop := range ni.Props {
			if r, ok := prop.Value.(*ir.ContextRead); ok {
				found = r
			}
		}
	}
	if found == nil {
		t.Fatalf("no ContextRead found in Toolbar.Body")
	}
	if found.Ref == nil || found.Ref.Name != "theme" {
		t.Errorf("Ref = %+v, want context theme", found.Ref)
	}
	if found.Typ == nil || found.Typ.Kind != ir.TypeString {
		t.Errorf("Typ = %v, want string", found.Typ)
	}
}

// TestContextAssignRejected verifies that assigning to a context name is rejected.
func TestContextAssignRejected(t *testing.T) {
	errs := checkSrc(t, `
context #theme("light")
window #home(title="t") {
    text(value="x", @click { theme = "dark" })
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for context assign, got none")
	}
}

// TestContextVarInitRejected verifies that capturing a context into a local var is rejected.
func TestContextVarInitRejected(t *testing.T) {
	errs := checkSrc(t, `
context #theme("light")
window #home(title="t") {
    var t = theme
    text(value=t)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for context var-init, got none")
	}
}

// contextNames returns the names of all contexts for diagnostic messages.
func contextNames(ctxs []*ir.Context) []string {
	out := make([]string, len(ctxs))
	for i, c := range ctxs {
		out[i] = c.Name
	}
	return out
}

// stmtTypes formats a slice of ir.Stmt for diagnostic output.
func stmtTypes(stmts []ir.Stmt) []string {
	out := make([]string, len(stmts))
	for i, s := range stmts {
		out[i] = fmt.Sprintf("%T", s)
	}
	return out
}
