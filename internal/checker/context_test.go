package checker_test

import (
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
// without errors and registers one Context named "theme" of inferred string type.
func TestContextBasic(t *testing.T) {
	pkg, errs := parseFile(t, "../../testdata/context_decl_basic.sngl")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(pkg.Contexts) != 1 {
		t.Fatalf("expected 1 context, got %d", len(pkg.Contexts))
	}
	ctx := pkg.Contexts[0]
	if ctx.Name != "theme" {
		t.Errorf("Name = %q, want \"theme\"", ctx.Name)
	}
	if ctx.Typ == nil {
		t.Fatalf("Typ is nil")
	}
	if ctx.Typ.Kind != ir.TypeString {
		t.Errorf("Typ.Kind = %v, want TypeString", ctx.Typ.Kind)
	}
}

// TestContextInferredType verifies that testdata/context_decl_inferred_type.sngl
// checks without errors and registers all five contexts with correct types.
func TestContextInferredType(t *testing.T) {
	pkg, errs := parseFile(t, "../../testdata/context_decl_inferred_type.sngl")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(pkg.Contexts) != 5 {
		t.Fatalf("expected 5 contexts, got %d", len(pkg.Contexts))
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
	for i, tt := range tests {
		ctx := pkg.Contexts[i]
		if ctx.Name != tt.name {
			t.Errorf("context[%d].Name = %q, want %q", i, ctx.Name, tt.name)
		}
		if ctx.Typ == nil {
			t.Errorf("context[%d].Typ is nil", i)
			continue
		}
		if ctx.Typ.Kind != tt.wantKnd {
			t.Errorf("context[%d].Typ.Kind = %v, want %v", i, ctx.Typ.Kind, tt.wantKnd)
		}
	}
}

// checkSrc parses inline source and returns error-severity diagnostics.
func checkSrc(t *testing.T, src string) []ir.Diagnostic {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(src))
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

// TestContextNonConstDefaultRejected verifies that a non-constant default is rejected.
func TestContextNonConstDefaultRejected(t *testing.T) {
	errs := checkSrc(t, `
var x = 1
context #foo(x)
window #home(title="t", href="/") { text(value="") }
`)
	if len(errs) == 0 {
		t.Fatal("expected error for non-const default, got none")
	}
}

// TestContextNoArgsRejected verifies that `context #foo()` with zero args is rejected.
func TestContextNoArgsRejected(t *testing.T) {
	errs := checkSrc(t, `
context #foo()
window #home(title="t", href="/") { text(value="") }
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
window #home(title="t", href="/") { text(value="") }
`)
	if len(errs) == 0 {
		t.Fatal("expected duplicate-declaration error, got none")
	}
}
