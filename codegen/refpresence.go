package codegen

import (
	"maps"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// PresenceMethod is the check a target generates beside its trigger invokers
// for each `#id` a test null-tests: whether the node is rendered now. The
// invokers are named `<id><Event>`, and this is named to sit beside them.
func PresenceMethod(id string) string { return id + "Present" }

// PresenceAtMethod is PresenceMethod for one copy of a node loops rendered,
// `c.row[1] == null`: whether the copy with the key it is handed
// (ir.CopyKey) is rendered now.
func PresenceAtMethod(id string) string { return InvokerAt(PresenceMethod(id)) }

// RefNullTest is ir.NullTestedRef, naming the instance variable and the id.
func RefNullTest(e ir.Expr) (recv, id string, path []ir.Expr, present, ok bool) {
	sel, path, present, ok := ir.NullTestedRef(e)
	if !ok {
		return "", "", nil, false, false
	}
	return sel.Operand.(*ir.Ident).Name, sel.Field, path, present, true
}

// NullTestedRefs is every `#id` a test in pkg null-tests, which is what a
// target generates a presence check for, and keyed those of them it
// null-tests one copy of, which get the PresenceAtMethod check.
func NullTestedRefs(pkg *ir.Package) (ids, keyed map[string]bool) {
	ids, keyed = map[string]bool{}, map[string]bool{}
	fns, _, _ := CollectTestFuncs(pkg)
	for _, fn := range fns {
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			if e, ok := n.(ir.Expr); ok {
				if _, id, path, _, ok := RefNullTest(e); ok {
					if len(path) > 0 {
						keyed[id] = true
					} else {
						ids[id] = true
					}
				}
			}
			return nil
		})
	}
	return ids, keyed
}

// LowerRefNullTests is a test body with each null test of a ref replaced by a
// read of the presence check, negated for `== null`, for a target whose check
// is a method on the instance a test holds -- a call handed the copy's key for
// one copy. methodFields gains the checks, so the read is spelled as the call
// it is. The body is copied: the test's own IR is shared by every target built
// from the package.
func LowerRefNullTests(stmts []ir.Stmt, methodFields map[string]bool) ([]ir.Stmt, map[string]bool) {
	fields := maps.Clone(methodFields)
	if fields == nil {
		fields = map[string]bool{}
	}
	out := ir.CloneStmtsSharingDecls(stmts)
	_ = ir.RewriteExprs(out, func(e ir.Expr) (ir.Expr, error) {
		sel, path, present, ok := ir.NullTestedRef(e)
		if !ok {
			return e, nil
		}
		var check ir.Expr
		if len(path) == 0 {
			name := PresenceMethod(sel.Field)
			fields[name] = true
			check = &ir.Select{Type: ir.TypBool, Operand: sel.Operand, Field: name}
		} else {
			check = &ir.Call{
				Type:     ir.TypBool,
				Receiver: sel.Operand,
				Func:     &ir.Func{Name: PresenceAtMethod(sel.Field), Return: ir.TypBool},
				Args:     []ir.CallArg{{Value: ir.CopyKey(path)}},
			}
		}
		if !present {
			check = &ir.Unary{Type: ir.TypBool, Op: ast.UnaryNot, Operand: check}
		}
		return check, nil
	})
	return out, fields
}
