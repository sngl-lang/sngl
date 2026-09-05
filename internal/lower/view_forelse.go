package lower

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passViewForElse rewrites a `for … else` written in a view body into the
// structural conditional every platform already renders:
//
//	for var it = items { BODY } else { ELSE }
//	    =>  if <items is empty> { ELSE } else { for var it = items { BODY } }
//
// Before this pass the else was the platform emitter's to render, and only
// bubbletea did: fyne, gtk4, android and html on --lang none each emitted the
// loop and nothing at all for the else, so an empty list rendered an empty
// region instead of the empty-state message the tour documents. Silently --
// the interpreter walks the IR and honours Else itself, so `sngl test` passed
// on every target. One pass rather than four emitters, which is the argument
// passForElse already makes for the imperative half.
//
// It is the imperative pass's counterpart and not its extension, because the
// two cannot share a desugaring. passForElse states "the body never ran" as a
// flag, and a flag is a statement: a view body on a target with no host
// language has nowhere to put one -- the bound passCSE documents, and the
// reason imperativeBlocks exists. So the question is asked of the iterable
// instead (ir.EmptyTest) and asked again on every render, which needs no
// storage anywhere.
//
// Only the two iterable forms reach here. A condition loop and a headless loop
// are imperative-only and the checker refuses both in a view body with a
// positioned error (checkHeadlessFor), and `for { } else { }` is an error
// everywhere, so a view body's loop always has an iterable to measure. An
// iterable this cannot measure or cannot evaluate twice is likewise refused by
// the checker (checkViewForElse), which is why a nil EmptyTest here is a bug
// rather than a case to fall through.
//
// Always-on, like the other three: the else means the same thing on every
// target and no target expresses it, which is the same reason ForElse,
// IndexedIter and CSE are not capability-gated.
//
// Early, and well before passReactivity, for two reasons. The `if` this
// produces has to reach reactivity as an ordinary view conditional so that a
// reactive iterable makes it a render slot -- that is what re-asks the
// emptiness question when the list changes, and without it the empty state
// would be decided once at build time. And the emptiness expression names
// whatever the loop head names, so it has to be in place before the passes
// that rewrite a name: a computed indirection (NoComputed), a promoted
// component prop (ComponentProps), a context read (NoContext) all rewrite the
// synthesized reference along with the original.
var passViewForElse = pass{
	name:    "ViewForElse",
	enabled: func(Caps) bool { return true },
	apply:   lowerViewForElse,
}

func lowerViewForElse(pkg *ir.Package, _ Caps, _ Options) error {
	for _, block := range viewBlocks(pkg) {
		rewriteViewForElse(block)
	}
	return nil
}

// rewriteViewForElse rewrites one view body in place, descending through the
// conditionals and loops it holds. Everything else nested in it -- a node's
// children, a slot's content, a window's body -- viewBlocks hands back
// separately, and the two overlap harmlessly: a loop whose else has been moved
// no longer has one, so a second visit is a no-op.
//
// The descent into an else block is what the overlap does not cover. This pass
// moves that block into a fresh `if`, and viewBlocks captured the address it
// used to live at before the move -- so a for-else nested in a for-else would
// be visited at an address holding nothing.
func rewriteViewForElse(block *[]ir.Stmt) {
	for i, s := range *block {
		if x, ok := s.(*ir.If); ok {
			rewriteViewForElse(&x.Body)
			rewriteViewForElse(&x.Else)
			continue
		}
		n, ok := s.(*ir.For)
		if !ok {
			continue
		}
		rewriteViewForElse(&n.Body)
		rewriteViewForElse(&n.Else)
		if len(n.Else) == 0 {
			continue
		}
		empty := ir.EmptyTest(n, deepCloneExpr)
		if empty == nil {
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
