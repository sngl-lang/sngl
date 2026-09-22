package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passViewForElse rewrites a `for … else` written in a view body:
//
//	for var it = items { BODY } else { ELSE }
//	    =>  if <items is empty> { ELSE } else { for var it = items { BODY } }
//
// passForElse's flag is a statement, and a view body on --lang none has
// nowhere to put one -- so the emptiness is re-asked rather than stored.
//
// A condition or headless loop cannot appear here (checkHeadlessFor refuses
// both in a view body), so the head is always an iterable. checkViewForElse
// refuses every head ir.EmptyTest cannot measure, which is what surviving
// below holds it to.
var passViewForElse = pass{
	name:    "ViewForElse",
	enabled: func(Features) bool { return true },
	apply:   lowerViewForElse,
}

func lowerViewForElse(pkg *ir.Package, _ Features, _ Options) error {
	var unmeasured *ir.For
	for _, block := range viewBlocks(pkg) {
		rewriteViewForElse(block, &unmeasured)
	}
	if unmeasured == nil {
		return nil
	}
	// Left alone, this reaches codegen as a loop whose else no emitter reads
	// -- the silent drop the pass exists to remove. The checker refuses every
	// head that could cause one, so failing here is a compiler bug rather than
	// a program error, and saying so loudly is what keeps the class from
	// coming back.
	pos := ""
	if unmeasured.AST != nil {
		pos = unmeasured.AST.Pos.String() + ": "
	}
	return fmt.Errorf("%sview for-else survived lowering: its iterable (%s) has no emptiness test, and the else would be dropped", pos, unmeasured.Iter.ExprType())
}

// rewriteViewForElse rewrites one view body in place, descending through the
// conditionals and loops it holds, and records a loop it could not measure.
//
// The descent into an else block is what viewBlocks does not cover: this pass
// moves that block into a fresh `if`, and viewBlocks captured the address it
// used to live at before the move -- so a for-else nested in a for-else would
// be visited at an address holding nothing.
func rewriteViewForElse(block *[]ir.Stmt, unmeasured **ir.For) {
	for i, s := range *block {
		if x, ok := s.(*ir.If); ok {
			rewriteViewForElse(&x.Body, unmeasured)
			rewriteViewForElse(&x.Else, unmeasured)
			continue
		}
		n, ok := s.(*ir.For)
		if !ok {
			continue
		}
		rewriteViewForElse(&n.Body, unmeasured)
		rewriteViewForElse(&n.Else, unmeasured)
		if len(n.Else) == 0 {
			continue
		}
		empty := ir.EmptyTest(n, deepCloneExpr)
		if empty == nil {
			if *unmeasured == nil {
				*unmeasured = n
			}
			continue
		}
		pos := ast.Pos{}
		if n.AST != nil {
			pos = n.AST.Pos
		}
		els := n.Else
		n.Else = nil
		(*block)[i] = &ir.If{
			AST:  &ast.IfStmt{Pos: pos},
			Cond: empty,
			Body: els,
			Else: []ir.Stmt{n},
		}
	}
}
