package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A library package is a package. What a user package may declare, a `sngl:`
// one may declare too, and it means the same thing -- the tier a declaration
// lives in says where it is imported from, never which of the language it may
// use.
//
// The library used to load through a second set of registrars, so a form the
// user path handled and the library path did not was accepted by the parser,
// dropped by the loader, and reported by nobody.
func libParityConfig(t *testing.T, src string) (*ir.Package, []ir.Diagnostic) {
	t.Helper()
	lib, err := parser.Parse("stub.sngl", []byte(src))
	if err != nil {
		t.Fatalf("stub parse: %v", err)
	}
	doc, err := parser.Parse("main.sngl", []byte(`import . "sngl:ui"
import stub "sngl:libparitystub"

component main node {
    text(value="x")
}
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:     true,
		LibSources: map[string][]*ast.Document{"libparitystub": {lib}},
	})
	if pkg == nil {
		t.Fatal("nil package")
	}
	// The stub is reachable through the import that named it; LibPackage runs
	// its own checker and would not see this config's LibSources.
	for _, imp := range pkg.Imports {
		if imp.Path == "sngl:libparitystub" && imp.Pkg != nil {
			return imp.Pkg, diags
		}
	}
	t.Fatalf("stub package did not load; imports: %v", pkg.Imports)
	return nil, nil
}

func libParityErrors(t *testing.T, diags []ir.Diagnostic) []string {
	t.Helper()
	var out []string
	for _, d := range diags {
		if d.Severity == ir.Error {
			out = append(out, d.Msg)
		}
	}
	return out
}

// A method written inside the type's own braces is the same declaration as one
// written beside it. The library loader registered neither the nested form nor
// an error about it, so a stdlib type could silently lose half its API.
func TestLibPackageRegistersNestedMethods(t *testing.T) {
	stub, diags := libParityConfig(t, `
struct Box {
    n int = 0

    func doubled() int { return this.n * 2 }
}
`)
	if errs := libParityErrors(t, diags); len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	for _, sd := range stub.Structs {
		if sd.Name != "Box" {
			continue
		}
		if _, ok := sd.Methods["doubled"]; !ok {
			t.Fatalf("Box lost its nested method; has %d: %v", len(sd.Methods), sd.Methods)
		}
		return
	}
	t.Fatal("no Box struct registered")
}

// Two declarations of one name is an error in a library package for the same
// reason it is in a user one. The library binder reported only its own
// "stdlib declares X twice", and only for a scope collision.
func TestLibPackageRejectsADuplicateName(t *testing.T) {
	_, diags := libParityConfig(t, `
struct Dup {}
struct Dup {}
`)
	if errs := libParityErrors(t, diags); len(errs) == 0 {
		t.Fatal("a library package declared one name twice and nothing said so")
	}
}

// A function body is held to the same rules in either tier. The library used
// to be body-checked by a second checker that inferred a return type but
// verified nothing, so a library function could declare a return and not
// return on every path -- accepted, and reported to nobody.
func TestLibPackageChecksFunctionBodies(t *testing.T) {
	_, diags := libParityConfig(t, `
func broken(x int) int {
    if x > 0 {
        return 1
    }
}
`)
	errs := libParityErrors(t, diags)
	var found bool
	for _, e := range errs {
		if strings.Contains(e, "must return int on all paths") {
			found = true
		}
	}
	if !found {
		t.Errorf("a library function that does not return on all paths was accepted; diagnostics: %v", errs)
	}
}

// A package may mark its own declarations with a macro it declares itself.
// Nothing about a macro is special -- it is a func returning ir.Macro -- so the
// only thing standing in the way was registration order: pass1 registered
// struct shells, and applied their marks, long before it reached any func. A
// package that wrote its own mark on its own struct got "unknown macro", and
// the workaround was to reach for an equivalent mark from an imported tier.
//
// Asserted on sngl:language/go rather than a stub, because a mark's
// implementation is keyed by the package that declares it: the compiler
// implements the marks, so a stub package cannot invent one to be marked with.
// `native` is that package's own macro, and DrawContext its own struct.
func TestPackageMarksWithItsOwnMacro(t *testing.T) {
	pkg, diags := checker.CheckLibPackage("language/go")
	if pkg == nil {
		t.Fatal("sngl:language/go did not load")
	}
	if errs := libParityErrors(t, diags); len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	for _, sd := range pkg.Structs {
		if sd.Name != "DrawContext" {
			continue
		}
		if sd.Foreign.Name != "*snglcanvas.Context" {
			t.Fatalf("DrawContext was not marked by the package's own `native`: Foreign = %+v", sd.Foreign)
		}
		return
	}
	t.Fatal("sngl:language/go declares no DrawContext")
}
