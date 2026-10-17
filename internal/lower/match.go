package lower

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passMatch writes a match as the if/else-if chain or the ternaries it means
// wherever the target cannot emit it as one: everywhere for a language that
// does not declare `matches` (a statement) or `matchValues` (an expression),
// in a view body for a platform that does not declare `viewMatches`, and
// anywhere for a match the host's switch would compare differently from `==`
// (ir.MatchArms.Switchable, which an option subject is only where the
// language declares `optionMatches`).
//
// A kept match whose arm breaks out of a loop around it (ir.Match.BreaksOut)
// labels the loop and names it in each such break (labelLoops), since Go's
// and JavaScript's switch take a bare break as their own. The late run does
// that, once every inliner has copied what it copies, so no function holds
// two loops of one label.
//
// A capability not written is not held, so a target that has not heard of
// match gets the chain, which is what the checker used to write for every
// target.
//
// A test function's match is the chain on every target too: a test body is
// written out by each language's test lowering rather than its emitter.
//
// It runs first, so every pass after it sees a match only where the target
// will emit one, and again before NoTernary (passMatchLate), for a match a
// pass between has carried out of an imperative body.
var passMatch = pass{
	name:    "Match",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, f Features, _ Options) error {
		return lowerMatches(pkg, f, false)
	},
}

var passMatchLate = pass{
	name:    "MatchLate",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, f Features, _ Options) error {
		return lowerMatches(pkg, f, true)
	},
}

func lowerMatches(pkg *ir.Package, f Features, late bool) error {
	if pkg == nil {
		return nil
	}
	// A view's own statements and the expressions they evaluate themselves --
	// a node's props, an `if`'s condition -- and not its handlers or lambdas,
	// which are a function's.
	view := map[ir.Node]bool{}
	if !f.ViewMatch {
		for _, b := range viewBlocks(pkg) {
			for _, s := range *b {
				_ = ir.Walk(s, func(n ir.Node) error {
					if st, isStmt := n.(ir.Stmt); isStmt && st != s {
						return ir.SkipDir
					}
					switch n.(type) {
					case *ir.Lambda:
						return ir.SkipDir
					case *ir.Match, *ir.MatchExpr:
						view[n] = true
					}
					return nil
				})
			}
		}
	}
	// A test's body is written out by each language's own test lowering,
	// which reaches into what an `if` holds and no further.
	test := map[ir.Node]bool{}
	for _, fn := range pkg.Funcs {
		if fn.IsTest {
			_ = ir.Walk(fn.Block, func(n ir.Node) error {
				test[n] = true
				return nil
			})
		}
	}
	held := func(n ir.Node, m *ir.MatchArms, can bool) bool {
		return can && m.Switchable(f.OptionMatch) && !test[n] && !view[n]
	}
	err := ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
		switch x := n.(type) {
		case *ir.Match:
			if held(x, &x.MatchArms, f.Match) {
				return x, nil
			}
			chain := x.Chain()
			if len(chain) == 1 {
				return chain[0], nil
			}
		case *ir.MatchExpr:
			if !held(x, &x.MatchArms, f.MatchValue) {
				return x.TernaryChain(), nil
			}
		}
		return n, nil
	})
	if err != nil || !(f.Match || f.MatchValue) {
		return err
	}
	for _, b := range imperativeBlocks(pkg) {
		unbindSubjects(b)
	}
	if late {
		labelLoops(pkg)
	}
	return nil
}

// labelLoops gives each loop a match in it breaks out of a label, and names
// it in every break that leaves the loop from inside such a match. Only a
// match passMatch kept is left by then, so each one found is a switch.
func labelLoops(pkg *ir.Package) {
	n := 0
	_ = ir.Walk(pkg, func(node ir.Node) error {
		loop, ok := node.(*ir.For)
		if !ok {
			return nil
		}
		var visit func([]ir.Stmt)
		visit = func(stmts []ir.Stmt) {
			for _, s := range stmts {
				if _, inner := s.(*ir.For); inner {
					// Its breaks are its own, and the walk reaches it.
					continue
				}
				if m, ok := s.(*ir.Match); ok && m.BreaksOut() {
					if loop.Label == "" {
						loop.Label = fmt.Sprintf("__loop%d", n)
						n++
					}
					nameBreaks(m.ArmBlocks(), loop.Label)
					continue
				}
				for _, b := range ir.TransparentBlocks(s) {
					visit(*b)
				}
			}
		}
		visit(loop.Body)
		return nil
	})
}

// nameBreaks labels every break in blocks that is not an inner loop's.
func nameBreaks(blocks []*[]ir.Stmt, label string) {
	for _, b := range blocks {
		for _, s := range *b {
			switch x := s.(type) {
			case *ir.Break:
				x.Label = label
			case *ir.For:
			default:
				nameBreaks(ir.TransparentBlocks(x), label)
			}
		}
	}
}

// unbindSubjects puts back the call the checker bound to a `__matchN` ahead
// of a statement, where the match it was bound for is one the target
// switches on: a switch evaluates its subject once, which is all the binding
// was for. Only where the statement evaluates the subject first -- the match
// itself, or a match a return, a declaration or a write to a name is the
// whole value of -- so that nothing the statement does moves before or after
// the call.
func unbindSubjects(block *[]ir.Stmt) {
	stmts := *block
	out := stmts[:0:0]
	for i := range stmts {
		s := stmts[i]
		for _, b := range ir.TransparentBlocks(s) {
			unbindSubjects(b)
		}
		lv, ok := s.(*ir.LocalVar)
		if ok && i+1 < len(stmts) && lv.Sym != nil && lv.Sym.Synthesized && strings.HasPrefix(lv.Name, "__match") {
			if m := leadingMatch(stmts[i+1]); m != nil && readsOnly(m.Subject, lv.Sym) && reads(stmts, lv.Sym) == 1 {
				m.Subject = lv.Init
				continue
			}
		}
		out = append(out, s)
	}
	*block = out
}

// leadingMatch is the match s evaluates before anything else, or nil.
func leadingMatch(s ir.Stmt) *ir.MatchArms {
	var e ir.Expr
	switch x := s.(type) {
	case *ir.Match:
		return &x.MatchArms
	case *ir.Return:
		e = x.Value
	case *ir.LocalVar:
		e = x.Init
	case *ir.Assign:
		if _, plain := x.Target.(*ir.Ident); plain && x.Op == ast.AssignSet {
			e = x.Value
		}
	}
	if m, ok := e.(*ir.MatchExpr); ok {
		return &m.MatchArms
	}
	return nil
}

func readsOnly(e ir.Expr, sym *ir.Var) bool {
	id, ok := e.(*ir.Ident)
	return ok && id.Sym == sym
}

// reads counts the reads of sym in stmts.
func reads(stmts []ir.Stmt, sym *ir.Var) int {
	n := 0
	_ = ir.Walk(stmts, func(node ir.Node) error {
		if id, ok := node.(*ir.Ident); ok && id.Sym == sym {
			n++
		}
		return nil
	})
	return n
}
