package codegen

import (
	"maps"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// PresenceMethod is the check a target generates beside its trigger invokers
// for each `#id` a test null-tests: whether the node is rendered now. The
// invokers are named `<id><Event>`, and this is named to sit beside them.
func PresenceMethod(id string) string { return id + "Present" }

// RefNullTest reads a test's `c.x == null` or `c.x != null`, either way round,
// where `c.x` is a ref to a node that may not be rendered. present is true for
// `!=`.
func RefNullTest(e ir.Expr) (recv, id string, present, ok bool) {
	sel, present, ok := refNullTest(e)
	if !ok {
		return "", "", false, false
	}
	return sel.Operand.(*ir.Ident).Name, sel.Field, present, true
}

func refNullTest(e ir.Expr) (*ir.Select, bool, bool) {
	bin, isBin := e.(*ir.Binary)
	if !isBin || (bin.Op != ast.BinEq && bin.Op != ast.BinNeq) {
		return nil, false, false
	}
	ref, other := bin.Left, bin.Right
	if isNull(ref) {
		ref, other = other, ref
	}
	if !isNull(other) {
		return nil, false, false
	}
	sel, isSel := stripConversions(ref).(*ir.Select)
	if !isSel || len(sel.Ref) < 2 || sel.Ref[0] != ir.RefOption {
		return nil, false, false
	}
	if _, isIdent := sel.Operand.(*ir.Ident); !isIdent {
		return nil, false, false
	}
	return sel, bin.Op == ast.BinNeq, true
}

func isNull(e ir.Expr) bool {
	lit, ok := stripConversions(e).(*ir.Literal)
	return ok && lit.Type != nil && lit.Type.Kind == ir.TypeNull
}

func stripConversions(e ir.Expr) ir.Expr {
	for {
		conv, ok := e.(*ir.Conversion)
		if !ok {
			return e
		}
		e = conv.Operand
	}
}

// NullTestedRefs is every `#id` a test in pkg null-tests, which is what a
// target generates a presence check for.
func NullTestedRefs(pkg *ir.Package) map[string]bool {
	out := map[string]bool{}
	fns, _, _ := CollectTestFuncs(pkg)
	for _, fn := range fns {
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			if e, ok := n.(ir.Expr); ok {
				if _, id, _, ok := RefNullTest(e); ok {
					out[id] = true
				}
			}
			return nil
		})
	}
	return out
}

// LowerRefNullTests is a test body with each null test of a ref replaced by a
// read of the presence check, negated for `== null`, for a target whose check
// is a method on the instance a test holds. methodFields gains the checks, so
// the read is spelled as the call it is. The body is copied: the test's own IR
// is shared by every target built from the package.
func LowerRefNullTests(stmts []ir.Stmt, methodFields map[string]bool) ([]ir.Stmt, map[string]bool) {
	fields := maps.Clone(methodFields)
	if fields == nil {
		fields = map[string]bool{}
	}
	out := ir.CloneStmtsSharingDecls(stmts)
	_ = ir.RewriteExprs(out, func(e ir.Expr) (ir.Expr, error) {
		sel, present, ok := refNullTest(e)
		if !ok {
			return e, nil
		}
		name := PresenceMethod(sel.Field)
		fields[name] = true
		var check ir.Expr = &ir.Select{Type: ir.TypBool, Operand: sel.Operand, Field: name}
		if !present {
			check = &ir.Unary{Type: ir.TypBool, Op: ast.UnaryNot, Operand: check}
		}
		return check, nil
	})
	return out, fields
}
