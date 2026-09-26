package codegen

import "git.duckfam.us/jonathan/sngl/ir"

// SubstituteEventPayload returns a copy of stmts with each read of an event
// parameter's first field replaced by the host's one positional value (the
// interp.coerceEventArg rule); value answers nil to leave a field alone. A
// handler body is shared IR that an emitter may render twice, hence the copy.
func SubstituteEventPayload(stmts []ir.Stmt, params []*ir.Param, value func(field *ir.StructField) ir.Expr) []ir.Stmt {
	copied := false
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
		if !copied {
			stmts = ir.CloneStmtsSharingDecls(stmts)
			copied = true
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
			return ir.CloneExprSharingDecls(repl), nil
		})
	}
	return stmts
}
