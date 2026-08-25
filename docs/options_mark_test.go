package docs

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"

	_ "git.duckfam.us/jonathan/sngl/internal/macros/platforms"
)

// The reference page documents the struct the #[options] mark names, not one
// named Options: the mark is what every options lookup keys on, so the docs
// have to agree with the checker about which struct that is.
func TestOptionsFromPackageKeysOnMark(t *testing.T) {
	const src = `import . "sngl://platforms"

#[options]
struct Knobs {
    // What the gadget does.
    gadget string = ""
}

struct Options {
    decoy string = ""
}
`
	doc, err := parser.Parse("stub.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, d := range expand.ExpandPre([]*ast.Document{doc}) {
		if d.Severity == ir.Error {
			t.Fatalf("expand: %s", d.Msg)
		}
	}
	opts := optionsFromPackage([]*ast.Document{doc})
	if len(opts) != 1 || opts[0].Name != "gadget" {
		t.Fatalf("options = %+v, want the marked struct's single field", opts)
	}
	if opts[0].Doc == "" {
		t.Error("field doc lost")
	}
}
