package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
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
