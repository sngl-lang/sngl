package fyne

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// This platform's answer to "can this prop be written after construction" is
// the Spec: a prop the constructor takes as an Arg and no Setter names has no
// method behind it, so an assignment to it is dropped. #[construct] is the
// same fact said in the declaration, and the two must not drift apart --
// adding a Setter for a marked prop, or marking one that has a Setter, would
// have the declaration claim one thing and the emitter do another.
//
// The check is per widget rather than over the primitives' `options` prop,
// because a Spec is what decides: another widget could name a setter for the
// same vocabulary prop.
func TestSelectOptionsIsConstructOnlyInBothAnswers(t *testing.T) {
	src, err := snglsrc.ReadFile("fyne.sngl")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parser.Parse("fyne.sngl", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var sel *ast.ComponentDecl
	for _, st := range doc.Stmts {
		if d, ok := st.(*ast.ComponentDecl); ok && d.Name == "Select" {
			sel = d
		}
	}
	if sel == nil {
		t.Fatal("no Select component in fyne.sngl")
	}

	marked := false
	for _, m := range sel.Props.Props {
		p, ok := m.(ast.Param)
		if !ok || p.Name != "options" {
			continue
		}
		for _, a := range p.Attrs {
			if a.Name == "construct" {
				marked = true
			}
		}
	}
	if !marked {
		t.Error("Select.options is a constructor argument with no setter; it should carry #[construct]")
	}

	// The Spec's half of the same fact, read off the source the emitter
	// decodes: options is an Arg, and no Setter names it.
	body := string(src)
	start := strings.Index(body, "component Select(")
	if start < 0 {
		t.Fatal("no Select declaration text")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("no end of Select declaration")
	}
	decl := body[start : start+end]
	if !strings.Contains(decl, `Arg{prop="options"`) {
		t.Error("Select's Spec should pass options as a constructor argument")
	}
	if strings.Contains(decl, `Setter{prop="options"`) {
		t.Error("Select's Spec names a setter for options, so the prop is not construct-only and the mark is wrong")
	}
}
