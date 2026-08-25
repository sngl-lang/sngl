package tree

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func expandOne(t *testing.T, src string) (ast.Stmt, []ir.Diagnostic) {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(doc.Stmts) == 0 {
		t.Fatalf("no statements after expand")
	}
	return doc.Stmts[len(doc.Stmts)-1], diags
}

func hasDiag(diags []ir.Diagnostic, want string) bool {
	for _, d := range diags {
		if strings.Contains(d.Msg, want) {
			return true
		}
	}
	return false
}

const header = "import tree \"sngl://internal/tree\"\n\n"

func TestKindAndChildrenStampTheComponent(t *testing.T) {
	decl, diags := expandOne(t, header+`#[tree.kind("block")]
#[tree.children("inline")]
component para() {}`)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	comp, ok := decl.(*ast.ComponentDecl)
	if !ok {
		t.Fatalf("got %T, want *ast.ComponentDecl", decl)
	}
	if comp.Tree.Kind != "block" || comp.Tree.Children != "inline" {
		t.Errorf("Tree = %+v, want {block inline}", comp.Tree)
	}
}

// The alias is an ordinary file-scope binding, so the mark follows it.
func TestMarksFollowTheImportAlias(t *testing.T) {
	decl, diags := expandOne(t, "import tr \"sngl://internal/tree\"\n\n"+`#[tr.kind("shape")]
component rect() {}`)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got := decl.(*ast.ComponentDecl).Tree.Kind; got != "shape" {
		t.Errorf("Tree.Kind = %q, want shape", got)
	}
}

func TestOnlyAComponentIsATreeNode(t *testing.T) {
	_, diags := expandOne(t, header+`#[tree.kind("block")]
struct S { x int = 0 }`)
	if !hasDiag(diags, "cannot mark") {
		t.Errorf("want cannot-mark diagnostic, got %v", diags)
	}
}

func TestKindRejectsAnEmptyName(t *testing.T) {
	_, diags := expandOne(t, header+`#[tree.kind("")]
component c() {}`)
	if !hasDiag(diags, "non-empty tree name") {
		t.Errorf("want empty-name diagnostic, got %v", diags)
	}
}

// A node belongs to one tree. A second mark would silently overwrite the
// first, which is the kind of thing a duplicated mark is never meant to do.
func TestKindRefusesASecondMark(t *testing.T) {
	_, diags := expandOne(t, header+`#[tree.kind("block")]
#[tree.kind("inline")]
component c() {}`)
	if !hasDiag(diags, `already a "block" node`) {
		t.Errorf("want duplicate-mark diagnostic, got %v", diags)
	}
}

func TestChildrenRefusesASecondMark(t *testing.T) {
	_, diags := expandOne(t, header+`#[tree.children("block")]
#[tree.children("inline")]
component c() {}`)
	if !hasDiag(diags, `already restricted to "block"`) {
		t.Errorf("want duplicate-mark diagnostic, got %v", diags)
	}
}
