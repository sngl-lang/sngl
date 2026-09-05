package fyne

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// This platform's answer to "can this prop be written after construction" is
// the Spec: a prop the constructor takes as an Arg and no Setter names has no
// method behind it, so an assignment to it is dropped. #[construct] is the same
// fact said in the declaration, and the two must not drift apart.
//
// No widget here needs the mark any more: every prop the vocabulary offers has
// a Fyne method behind it. That matters beyond this file -- #[construct] now
// demands a type every target compares alike, and a `list` is not one, so a
// mark reappearing on a list-valued prop would not be a drift to notice later
// but a package that stops checking.
func TestNoWidgetPropIsConstructOnly(t *testing.T) {
	src, err := snglsrc.ReadFile("fyne.sngl")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parser.Parse("fyne.sngl", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, st := range doc.Stmts {
		d, ok := st.(*ast.ComponentDecl)
		if !ok {
			continue
		}
		for _, m := range d.Props.Props {
			p, ok := m.(ast.Param)
			if !ok {
				continue
			}
			for _, a := range p.Attrs {
				if a.Name == "construct" {
					t.Errorf("%s.%s carries #[construct]; give it a Setter instead, "+
						"and check its type is one every target compares alike", d.Name, p.Name)
				}
			}
		}
	}
}

// The choices reach the widget twice, and both are load-bearing: SetOptions is
// what a later change to the bound list travels through, and the Arg is what
// puts the choices in place before SetSelected runs -- Fyne ignores a selection
// that is not already among Options. TestSelectOptionsUpdateInPlace is the
// runtime half of this; here so a Spec edit says which half it broke.
func TestSelectPassesOptionsAsBothArgAndSetter(t *testing.T) {
	src, err := snglsrc.ReadFile("fyne.sngl")
	if err != nil {
		t.Fatal(err)
	}
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
		t.Error("Select's Spec should pass options as a constructor argument, " +
			"or the initial selection lands on an empty Options list and is dropped")
	}
	if !strings.Contains(decl, `Setter{prop="options"`) {
		t.Error("Select's Spec should name SetOptions, or a change to the bound list never reaches the widget")
	}
}
