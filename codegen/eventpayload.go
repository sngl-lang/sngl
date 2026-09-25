package codegen

import "git.duckfam.us/jonathan/sngl/ir"

// SubstituteEventPayload rewrites, in stmts, each read of an event parameter's
// first field to what the host handed the callback. A toolkit reports one
// positional value and an event is a declared struct, and one value fills the
// first field -- the rule interp.coerceEventArg applies -- so this is the same
// adaptation for a target whose callback cannot receive the struct. value
// answers nil to leave a field of that type alone.
func SubstituteEventPayload(stmts []ir.Stmt, params []*ir.Param, value func(field *ir.StructField) ir.Expr) {
	for _, p := range params {
		if p == nil || p.Type == nil || p.Type.Kind != ir.TypeStruct {
			continue
		}
		def, _ := p.Type.Decl.(*ir.StructDef)
		if def == nil || len(def.Fields) == 0 {
			continue
		}
		field := def.Fields[0]
		repl := value(field)
		if repl == nil {
			continue
		}
		_ = ir.RewriteExprs(stmts, func(e ir.Expr) (ir.Expr, error) {
			sel, ok := e.(*ir.Select)
			if !ok || sel.Field != field.Name {
				return e, nil
			}
			id, ok := sel.Operand.(*ir.Ident)
			if !ok || !(id.Sym == ir.Symbol(p) || (id.Sym == nil && id.Name == p.Name)) {
				return e, nil
			}
			return repl, nil
		})
	}
}
