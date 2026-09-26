package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// checkRestInsertion holds a body to inserting its rest slot exactly once,
// where it always renders. The children a caller writes bare are the caller's
// -- their `#id`s are the caller's refs -- so where they render has to be a
// fact of the caller's body alone: a callee that inserted them twice, or under
// a condition, would make one written node many or none.
//
// A signature has no insertion to hold, so the rule is asked of a body only.
// A library declaration's `{}` is the same: its render is a platform's
// primitive or override, which is asked instead, and so is a declaration the
// compiler or an emitter renders from its marks.
func (c *checker) checkRestInsertion(comp *ir.Component) {
	rest := comp.RestSlot()
	if rest == nil || comp.AST == nil || !comp.AST.Body.IsDefined() {
		return
	}
	if comp.Builtin != ir.BuiltinNone || comp.Intrinsic != "" || comp.Wildcard != "" {
		return
	}
	if comp.Pkg != "" && len(comp.AST.Body.Stmts) == 0 {
		return
	}
	w := &restWalk{rest: rest.Name}
	w.stmts(comp.Body, "")
	if len(w.sites) == 0 {
		c.error(restSlotPos(comp, rest), "rest slot %q of %s is never inserted: the children a caller writes bare render where the body inserts %s, exactly once", rest.Name, comp.Name, rest.Name)
		return
	}
	first := w.sites[0]
	for _, s := range w.sites {
		if s.under != "" {
			c.error(s.pos, "rest slot %q is inserted under %s: the children a caller writes bare render exactly once, so insert %s where it always renders", rest.Name, s.under, rest.Name)
		}
	}
	for _, s := range w.sites[1:] {
		c.error(s.pos, "rest slot %q is inserted more than once: the children a caller writes bare render exactly once (first inserted at %s)", rest.Name, first.pos)
	}
}

func restSlotPos(comp *ir.Component, rest *ir.SlotDecl) ast.Pos {
	// A library body is usually a target's override, which the declaration's
	// own position would not name.
	if comp.Pkg != "" {
		return comp.AST.Body.Pos
	}
	for _, p := range comp.AST.Props.Props {
		if pd, ok := p.(ast.Param); ok && pd.Name == rest.Name {
			return pd.Pos
		}
	}
	return compDeclPos(comp)
}

type restSite struct {
	pos ast.Pos
	// under names the construct that makes the insertion conditional or
	// repeated, or is empty where it always renders once.
	under string
}

type restWalk struct {
	rest  string
	sites []restSite
}

func (w *restWalk) stmts(stmts []ir.Stmt, under string) {
	for _, st := range stmts {
		w.stmt(st, under)
	}
}

func orUnder(under, s string) string {
	if under != "" {
		return under
	}
	return s
}

func (w *restWalk) stmt(st ir.Stmt, under string) {
	switch n := st.(type) {
	case *ir.SlotInst:
		if n.Rest && n.Entry == nil && n.Name == w.rest {
			var pos ast.Pos
			if n.AST != nil {
				pos = n.AST.Pos
			}
			w.sites = append(w.sites, restSite{pos: pos, under: under})
		}
		w.stmts(n.Children, orUnder(under, "a slot fallback"))
		for _, sc := range n.Slots {
			w.stmts(sc.Body, orUnder(under, "a slot population"))
		}
	case *ir.NodeInst:
		inner := under
		if ir.IsWindowNode(n) {
			inner = orUnder(under, "a window")
		}
		w.stmts(n.Children, inner)
		for _, sc := range n.Slots {
			w.stmts(sc.Body, orUnder(inner, "a slot population"))
		}
	case *ir.If:
		w.stmts(n.Body, orUnder(under, "an if"))
		w.stmts(n.Else, orUnder(under, "an if"))
	case *ir.For:
		w.stmts(n.Body, orUnder(under, "a for"))
		w.stmts(n.Else, orUnder(under, "a for-else"))
	case *ir.ErrorBoundary:
		inner := under
		if len(n.Failed) > 0 {
			inner = orUnder(under, "a boundary with a fallback")
		}
		w.stmts(n.Children, inner)
		w.stmts(n.Failed, inner)
	case *ir.ContextProvider:
		w.stmts(n.Children, under)
	}
}
