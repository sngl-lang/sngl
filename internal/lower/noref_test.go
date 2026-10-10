package lower

import (
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

func TestSeedAddressedVarsFromUnaryAddr(t *testing.T) {
	pkg := &ir.Package{
		AddressedVars: map[*ir.Var]bool{},
	}
	n := &ir.Var{Name: "n", Type: ir.TypInt}
	pkg.Vars = []*ir.Var{n}

	handler := &ir.Func{
		Block: []ir.Stmt{
			&ir.LocalVar{
				Name: "p",
				Type: ir.RefOf(ir.TypInt),
				Init: &ir.Unary{
					Op:      ast.UnaryAddr,
					Operand: &ir.Ident{Name: "n", Sym: n, Type: ir.TypInt},
					Type:    ir.RefOf(ir.TypInt),
				},
			},
		},
	}
	pkg.Funcs = []*ir.Func{handler}

	seedAddressedVars(pkg)

	if !pkg.AddressedVars[n] {
		t.Errorf("n should be marked addressed")
	}
}

func TestBoxRegistryDedupesByElemType(t *testing.T) {
	pkg := &ir.Package{}
	r := newBoxRegistry(pkg)

	a := r.boxFor(ir.TypInt)
	b := r.boxFor(ir.TypInt)
	if a != b {
		t.Errorf("same elem type should share box def")
	}

	c := r.boxFor(ir.TypString)
	if c == a {
		t.Errorf("different elem types should not share box def")
	}

	if a.Name != "__ref_int" || c.Name != "__ref_string" {
		t.Errorf("wrong canonical names: %q %q", a.Name, c.Name)
	}

	if len(pkg.Structs) != 2 {
		t.Errorf("expected 2 box structs in pkg, got %d", len(pkg.Structs))
	}
}
