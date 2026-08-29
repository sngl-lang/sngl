package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestDotImportDoesNotLiftLibraryImports keeps the internal/ tier gated. A
// library package's imports are not part of what it exports, so a dot import
// of sngl:std must not lift the `ir` namespace lib/std binds for
// sngl:internal/ir — which a program may not import at all, and which a
// dot import reached right through when a lib package assembled itself
// directly in its own export scope.
func TestDotImportDoesNotLiftLibraryImports(t *testing.T) {
	const src = `
import . "sngl:std"

struct Probe {
    m ir.Macro
}

component main {
    text(value="hi")
}
`
	doc, err := parser.Parse("main.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, "ir") {
			return
		}
	}
	t.Errorf("a dot import of sngl:std made the compiler's own sngl:internal/ir reachable as %q; got %v", "ir.Macro", diags)
}

// TestImportedPackageSharesStdlibIdentity pins one library instance per build:
// an imported package is checked by a nested Check, and a `text` from a second
// sngl:std would be a different *ir.Component than the program's, so a
// platform extension body attached to one would be invisible on the other.
func TestImportedPackageSharesStdlibIdentity(t *testing.T) {
	res := &mockResolver{pkgs: map[string]string{
		"sub": `
import . "sngl:std"

component Label() {
    text(value="x")
}
`,
	}}
	const src = `
import "sub"
import . "sngl:std"

component main {
    text(value="hi")
    sub.Label {}
}
`
	doc, err := parser.Parse("main.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Resolver: res})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("unexpected error: %s", d.Error())
		}
	}
	sym, _ := pkg.Symbols.LookupComponent("text")
	text, _ := sym.(*ir.Component)
	if text == nil {
		t.Fatal("stdlib text component missing from the symbol table")
	}
	var label *ir.Component
	for _, imp := range pkg.Imports {
		if imp.Path != "sub" || imp.Pkg == nil {
			continue
		}
		for _, comp := range imp.Pkg.Components {
			if comp.Name == "Label" {
				label = comp
			}
		}
	}
	if label == nil {
		t.Fatal("imported package contributed no Label component")
	}
	inst, ok := label.Body[0].(*ir.NodeInst)
	if !ok {
		t.Fatalf("Label body[0] = %T; want *ir.NodeInst", label.Body[0])
	}
	if inst.Component != text {
		t.Errorf("text in the imported package is %p; the program holds %p — two identities", inst.Component, text)
	}
}
