package ir

import "testing"

func TestContextDeclSymName(t *testing.T) {
	c := &Context{Name: "theme"}
	if got := c.SymName(); got != "theme" {
		t.Errorf("SymName = %q, want %q", got, "theme")
	}
}

func TestContextReadIsExpr(t *testing.T) {
	var _ Expr = (*ContextRead)(nil)
}

func TestContextProviderIsStmt(t *testing.T) {
	var _ Stmt = (*ContextProvider)(nil)
}

func TestPackageContexts(t *testing.T) {
	p := &Package{}
	p.Contexts = append(p.Contexts, &Context{Name: "theme"})
	if len(p.Contexts) != 1 {
		t.Fatalf("len = %d", len(p.Contexts))
	}
}
