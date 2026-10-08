package fyne

import (
	"duckfam.us/sngl/lib"
	"strings"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/parser"
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
	src, err := lib.FS.ReadFile("platform/fyne/fyne.sngl")
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

// SetSelected ignores a value that is not already among Options, so the choices
// have to be assigned first. Nothing in the Spec enforces that -- the order is
// the order Select's body writes the two props in, and tidying those lines the
// other way round silently drops the initial selection.
// TestSelectOptionsUpdateInPlace is the runtime half of this; here so a reorder
// says which half it broke.
func TestSelectAssignsOptionsBeforeSelected(t *testing.T) {
	model := generateFyneModelBuilt(t, selectOptionsSrc)
	// The build, where the two are first written. The change handler above
	// it re-syncs the selection a binding wrote, which is no initial one.
	build := strings.Index(model, "func (m *Model) BuildUI(")
	if build < 0 {
		t.Fatal("generated model has no BuildUI")
	}
	model = model[build:]
	opts := strings.Index(model, ".SetOptions(")
	sel := strings.Index(model, ".SetSelected(")
	if opts < 0 || sel < 0 {
		t.Fatalf("generated model names SetOptions at %d and SetSelected at %d; want both", opts, sel)
	}
	if opts > sel {
		t.Error("SetSelected is emitted before SetOptions; the initial selection is not " +
			"among the widget's choices yet and Fyne drops it")
	}
}
