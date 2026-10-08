package bubbletea

import (
	"strings"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/checker"
)

// btPrimitives is every primitive the view renderer dispatches on. Each must
// carry the #[intrinsic] id btIntrinsic reads off the declaration. Without the
// mark the renderer stops recognising the node and emits it as an ordinary
// component reference — which changes generated output without failing
// anything else, so this is the assertion that catches a dropped mark.
var btPrimitives = []string{"Layout", "Styled", "Widget", "Overlay"}

func TestPrimitivesCarryTheirIntrinsicID(t *testing.T) {
	// The package is lib/platform/bubbletea; PackageSource is what the
	// checker reads it from.
	docs := checker.PackageSource("platform/bubbletea")
	if len(docs) == 0 {
		t.Fatal("no sngl:platform/bubbletea source; nothing below would be checked")
	}

	marks := map[string][]string{}
	declared := map[string]bool{}
	for _, doc := range docs {
		for _, d := range doc.Stmts {
			cd, ok := d.(*ast.ComponentDecl)
			if !ok {
				continue
			}
			declared[cd.Name] = true
			for _, a := range cd.Attrs {
				if a.Name != "intrinsic" || len(a.Args) != 1 {
					continue
				}
				lit, ok := a.Args[0].(*ast.LiteralExpr)
				if !ok {
					continue
				}
				marks[cd.Name] = append(marks[cd.Name], strings.Trim(lit.Raw, `"`))
			}
		}
	}

	for _, name := range btPrimitives {
		if !declared[name] {
			t.Errorf("%s: not declared in sngl:platform/bubbletea", name)
			continue
		}
		got := marks[name]
		want := "bubbletea:" + name
		switch {
		case len(got) == 0:
			t.Errorf("%s: no #[intrinsic] mark; btIntrinsic will not recognise it", name)
		case len(got) != 1:
			t.Errorf("%s: %d #[intrinsic] marks, want 1: %v", name, len(got), got)
		case got[0] != want:
			t.Errorf("%s: #[intrinsic(%q)], want %q", name, got[0], want)
		}
	}
}
