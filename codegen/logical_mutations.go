package codegen

import "duckfam.us/sngl/ir"

// LogicalMutations returns block with its DOM patches removed, leaving the
// state mutations and every other statement. A DOM patch is an ir.Assign whose
// target is a field Select on an element-ref ident (`__n0.value = ...`) -- the
// lowered client-side patch, which has no place in code a server runs.
//
// It is what a route-mode server is handed of a handler (HTTPAction) and of
// every function it carries: a function a handler calls was lowered for the
// page too, and its patches name nodes no server holds.
func LogicalMutations(block []ir.Stmt) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(block))
	for _, s := range block {
		if IsDOMPatchStmt(s) {
			continue
		}
		// A patch inside a block is the page's too -- the catch block the
		// window's boundary makes of a handler that may raise, a branch. The
		// block is copied, since the page's half of the handler keeps it.
		switch n := s.(type) {
		case *ir.If:
			cp := *n
			cp.Body, cp.Else = LogicalMutations(n.Body), LogicalMutations(n.Else)
			s = &cp
		case *ir.For:
			cp := *n
			cp.Body, cp.Else = LogicalMutations(n.Body), LogicalMutations(n.Else)
			s = &cp
		}
		out = append(out, s)
	}
	return out
}

// IsDOMPatchStmt reports whether s is a lowered DOM-patch assignment
// (assignment to a field on an element-ref ident).
func IsDOMPatchStmt(s ir.Stmt) bool {
	a, ok := s.(*ir.Assign)
	if !ok {
		return false
	}
	sel, ok := a.Target.(*ir.Select)
	if !ok {
		return false
	}
	id, ok := sel.Operand.(*ir.Ident)
	return ok && id.IsElementRef
}
