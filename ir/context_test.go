package ir

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

func TestContextDeclSymName(t *testing.T) {
	c := &Context{Name: "theme"}
	if got := c.SymName(); got != "theme" {
		t.Errorf("SymName = %q, want %q", got, "theme")
	}
}

func TestContextDeclSymType(t *testing.T) {
	typ := &Type{}
	c := &Context{Typ: typ}
	if got := c.SymType(); got != typ {
		t.Errorf("SymType pointer mismatch")
	}
}

func TestConvertContextDecl(t *testing.T) {
	pkg := &Package{Contexts: []*Context{
		{
			Name:    "theme",
			Typ:     &Type{Kind: TypeString},
			Default: &Literal{Type: &Type{Kind: TypeString}, Raw: "light"},
		},
	}}
	doc := Convert(pkg)
	var cs *ast.CallStmt
	for _, s := range doc.Stmts {
		if c, ok := s.(*ast.CallStmt); ok {
			cs = c
			break
		}
	}
	if cs == nil {
		t.Fatalf("Convert did not emit a CallStmt for the context decl")
	}
	sel, ok := cs.Call.Func.(*ast.SelectExpr)
	if !ok || sel.Kind != ast.SelectElemRef {
		t.Fatalf("Convert emitted wrong Func shape: %T %+v", cs.Call.Func, sel)
	}
	if sel.Field != "theme" {
		t.Errorf("Field = %q, want \"theme\"", sel.Field)
	}
	ident, ok := sel.Operand.(*ast.IdentExpr)
	if !ok || ident.Name != "context" {
		t.Errorf("Operand wrong: %T %+v", sel.Operand, ident)
	}
}

func TestConvertContextProvider(t *testing.T) {
	ctx := &Context{Name: "theme", Typ: &Type{Kind: TypeString}}
	prov := &ContextProvider{
		Ref:   ctx,
		Value: &Literal{Type: &Type{Kind: TypeString}, Raw: "dark"},
	}
	s := ConvertStmt(prov)
	vn, ok := s.(*ast.VisualNode)
	if !ok {
		t.Fatalf("ConvertStmt(provider) = %T, want *ast.VisualNode", s)
	}
	ident, ok := vn.Target.(*ast.IdentExpr)
	if !ok || ident.Name != "theme" {
		t.Errorf("Target wrong: %T %+v", vn.Target, ident)
	}
}

func TestConvertContextRead(t *testing.T) {
	ctx := &Context{Name: "theme", Typ: &Type{Kind: TypeString}}
	r := &ContextRead{Ref: ctx, Typ: ctx.Typ}
	e := ConvertExpr(r)
	ident, ok := e.(*ast.IdentExpr)
	if !ok || ident.Name != "theme" {
		t.Fatalf("ConvertExpr(read) = %T %+v, want IdentExpr{Name:theme}", e, ident)
	}
}
