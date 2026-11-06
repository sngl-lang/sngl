package lower

import (
	"duckfam.us/sngl/ir"
)

// passUnreadLocals removes a local nothing reads, for a target that does not
// claim `unreadLocals`: Go refuses to declare one. A program may bind a value
// only for what computing it does, `var r = fetch(k)`, so a local bound to a
// call of a declared function becomes the call, and one bound to a value
// whose evaluation changes nothing goes. Any other is left as written.
//
// Before ValueCopy, which wraps an initializer in the copy a store makes and
// so hides the call it was.
var passUnreadLocals = pass{
	name:    "UnreadLocals",
	enabled: func(f Features) bool { return !f.UnreadLocals },
	apply:   lowerUnreadLocals,
}

func lowerUnreadLocals(pkg *ir.Package, _ Features, _ Options) error {
	ir.RewriteBlocks(pkg, func(stmts []ir.Stmt) []ir.Stmt {
		out := stmts[:0:0]
		for i, s := range stmts {
			lv, ok := s.(*ir.LocalVar)
			if !ok || lv.Sym == nil || lv.Init == nil || readsSym(stmts[i+1:], lv.Sym) {
				out = append(out, s)
				continue
			}
			switch call, isCall := lv.Init.(*ir.Call); {
			case ir.Reevaluable(lv.Init):
			case isCall && call.Func != nil && call.Func.Intrinsic == "":
				out = append(out, &ir.CallStmt{Call: call})
			default:
				out = append(out, s)
			}
		}
		return out
	})
	return nil
}

// readsSym reports whether stmts name sym.
func readsSym(stmts []ir.Stmt, sym ir.Symbol) bool {
	found := false
	_ = ir.Walk(stmts, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok && id.Sym == sym {
			found = true
		}
		return nil
	})
	return found
}
