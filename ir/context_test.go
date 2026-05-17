package ir

import "testing"

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
