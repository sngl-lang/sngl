package codegen

import "duckfam.us/sngl/ir"

// KeepLocalsRead is what a translator writes for a prop it renders nothing
// for -- a platform's own `style`, which an override forwards rather than a
// widget taking -- where the prop's value reads a local a lowering pass
// synthesized for it: the temp passTernary hoists a `c ? a : b` into is
// declared ahead of the node, and with the one statement reading it dropped,
// Go refuses a declared and unused variable. Each such local is read into the
// blank identifier instead, once.
func KeepLocalsRead(value ir.Expr) []ir.Stmt {
	var out []ir.Stmt
	seen := map[*ir.Var]bool{}
	_ = ir.WalkExprs(value, func(e ir.Expr) error {
		id, ok := e.(*ir.Ident)
		if !ok || !id.Synthesized {
			return nil
		}
		v, ok := id.Sym.(*ir.Var)
		if !ok || !v.Synthesized || seen[v] {
			return nil
		}
		seen[v] = true
		out = append(out, &ir.Assign{
			Target: &ir.Ident{Name: "_", Type: id.Type, Synthesized: true},
			Value:  &ir.Ident{Name: id.Name, Type: id.Type, Sym: v, Synthesized: true},
		})
		return nil
	})
	return out
}
