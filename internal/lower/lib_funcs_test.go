package lower

import (
	"testing"

	"duckfam.us/sngl/ir"
)

// A spread in a library helper is lowered as one in the program is: LibFuncs
// promotes the helper into the package, and a NoStructSpread that ran before
// it left the helper's `{...c, a=1}` standing for a backend that has no
// spread to write it with.
func TestLibFuncSpreadIsLowered(t *testing.T) {
	st := structType("Cfg")
	sd := st.Decl.(*ir.StructDef)
	c := &ir.Param{Name: "c", Type: st}
	padded := &ir.Func{
		Name: "padded", Pkg: &ir.Package{Path: "sngl:fake"},
		Params: []*ir.Param{c},
		Return: st,
		Block: []ir.Stmt{&ir.Return{Value: &ir.StructLit{Type: st, Def: sd, Fields: []ir.FieldInit{
			{Spread: true, Value: &ir.Ident{Name: "c", Type: st, Sym: c}},
			{Name: "a", Value: intLit("1")},
		}}}},
	}
	op := &ir.Param{Name: "op", Type: st}
	main := &ir.Func{
		Name:   "main",
		Params: []*ir.Param{op},
		Return: st,
		Block: []ir.Stmt{&ir.Return{Value: &ir.Call{Type: st, Func: padded, Args: []ir.CallArg{
			{Value: &ir.Ident{Name: "op", Type: st, Sym: op}},
		}}}},
	}
	pkg := &ir.Package{
		Funcs:          []*ir.Func{main},
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	if err := Lower(pkg, without("structSpread"), Options{}); err != nil {
		t.Fatalf("Lower: %v", err)
	}
	promoted := false
	for _, f := range pkg.Funcs {
		promoted = promoted || f == padded
	}
	if !promoted {
		t.Fatal("LibFuncs did not promote the library helper")
	}
	_ = ir.WalkExprs(padded.Block, func(e ir.Expr) error {
		if sl, ok := e.(*ir.StructLit); ok {
			for _, f := range sl.Fields {
				if f.Spread {
					t.Errorf("the library helper still spreads %v", f.Value)
				}
			}
		}
		return nil
	})
}
