package checker

import (
	"slices"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// elementRef resolves `c.<id>` where c is an instance of comp: the component
// the node with that id instantiates, and how many of it the read names.
//
// comp's refs are the ids written in its own body, each instance holding its
// own. They are not the ids in the bodies of the components it instantiates --
// those are reached through that instance's id, `c.inbox.lbl` -- nor the ids
// in a slot population, which is a declaration with a scope of its own. Bare
// children handed to a callee are comp's, and render exactly once where the
// callee inserts them (checkRestInsertion), so they count as written.
//
// The count is the node's position: a `for` on the way makes it a list, a
// conditional an option, and nothing at all one node.
//
// The walk is over the AST because a component's IR body may not be checked
// yet at this point in pass2.
func (c *checker) elementRef(comp *ir.Component, id string) (*ir.Component, ir.RefCount) {
	if comp == nil || comp.AST == nil || id == "" {
		return nil, ir.RefNone
	}
	// Another package's body names things through that package's scope, which
	// this checker does not hold; an imported package is checked before its
	// importer, so its IR is complete.
	if !slices.Contains(c.pkg.Components, comp) {
		s := &refSearch{id: id}
		s.walkIR(comp.Body, ir.RefOne)
		return s.host, s.count
	}
	s := &refSearch{c: c, comp: comp, id: id}
	restore := c.fileOf(compDeclPos(comp))
	s.walk(comp.AST.Body.Stmts, ir.RefOne)
	restore()
	return s.host, s.count
}

type refSearch struct {
	c     *checker
	comp  *ir.Component
	id    string
	host  *ir.Component
	count ir.RefCount
}

func joinRef(a, b ir.RefCount) ir.RefCount { return max(a, b) }

func (s *refSearch) walk(stmts []ast.Stmt, k ir.RefCount) {
	for _, st := range stmts {
		switch n := st.(type) {
		case *ast.VisualNode:
			s.node(n.ID, visualNodeTarget(n), &n.Block, k)
		case *ast.CallStmt:
			if target, id, isElem := elementRefCallInfo(n.Call); isElem {
				s.node(id, target, nil, k)
			}
		case *ast.IfStmt:
			s.walk(n.Body.Stmts, joinRef(k, ir.RefOption))
			s.walk(n.Else.Stmts, joinRef(k, ir.RefOption))
		case *ast.ForStmt:
			s.walk(n.Body.Stmts, joinRef(k, ir.RefList))
			s.walk(n.Else.Stmts, joinRef(k, ir.RefOption))
		}
	}
}

func (s *refSearch) node(id, target string, block *ast.StmtBlock, k ir.RefCount) {
	content, pops := splitPopulations(block)
	if ownerSlot(s.comp, target) != nil {
		// The block at an insertion is its fallback, which renders only where
		// nothing was supplied.
		s.walk(content, joinRef(k, ir.RefOption))
		return
	}
	comp := s.c.componentNamed(target)
	if id == s.id && comp != nil && s.host == nil {
		s.host, s.count = comp, k
	}
	if comp == nil {
		s.walk(content, k)
		return
	}
	switch comp.Builtin {
	case ir.BuiltinErrorBoundary:
		// The fallback is placed where the content was, in this body, so its
		// ids are this component's; either half may be the one showing.
		if populatesFallback(comp, pops) {
			k = joinRef(k, ir.RefOption)
		}
		s.walk(content, k)
		for _, cd := range pops {
			s.walk(cd.Body.Stmts, k)
		}
	case ir.BuiltinWindow:
		// A window may not be open. Its content population is its body.
		k = joinRef(k, ir.RefOption)
		s.walk(content, k)
		for _, cd := range pops {
			s.walk(cd.Body.Stmts, k)
		}
	default:
		s.walk(content, k)
	}
}

// populatesFallback reports whether a boundary's populations include its
// fallback, which is whichever slot is not the rest slot.
func populatesFallback(boundary *ir.Component, pops []*ast.ComponentDecl) bool {
	rest := boundary.RestSlot()
	for _, cd := range pops {
		if rest == nil || cd.Name != rest.Name {
			return true
		}
	}
	return false
}

// splitPopulations separates a node's block into its bare children and the
// slot populations written by name.
func splitPopulations(block *ast.StmtBlock) ([]ast.Stmt, []*ast.ComponentDecl) {
	if block == nil {
		return nil, nil
	}
	var content []ast.Stmt
	var pops []*ast.ComponentDecl
	for _, st := range block.Stmts {
		if cd, ok := st.(*ast.ComponentDecl); ok {
			pops = append(pops, cd)
			continue
		}
		content = append(content, st)
	}
	return content, pops
}

// isElementRefRead reports whether e reads nodes by `#id` off a component:
// the read itself, an element of it, or a narrowed option of one.
func isElementRefRead(e ir.Expr) bool {
	for {
		switch n := e.(type) {
		case *ir.Conversion:
			e = n.Operand
		case *ir.Index:
			e = n.Operand
		case *ir.Select:
			return n.Ref != ir.RefNone
		default:
			return false
		}
	}
}

// walkIR is walk over a checked body, which is what another package's
// component offers.
func (s *refSearch) walkIR(stmts []ir.Stmt, k ir.RefCount) {
	for _, st := range stmts {
		switch n := st.(type) {
		case *ir.NodeInst:
			if n.ID == s.id && n.Component != nil && s.host == nil {
				s.host, s.count = n.Component, k
			}
			inner := k
			if ir.IsWindowNode(n) {
				inner = joinRef(k, ir.RefOption)
			}
			s.walkIR(n.Children, inner)
		case *ir.SlotInst:
			s.walkIR(n.Children, joinRef(k, ir.RefOption))
		case *ir.If:
			s.walkIR(n.Body, joinRef(k, ir.RefOption))
			s.walkIR(n.Else, joinRef(k, ir.RefOption))
		case *ir.For:
			s.walkIR(n.Body, joinRef(k, ir.RefList))
			s.walkIR(n.Else, joinRef(k, ir.RefOption))
		case *ir.ErrorBoundary:
			inner := k
			if len(n.Failed) > 0 {
				inner = joinRef(k, ir.RefOption)
			}
			s.walkIR(n.Children, inner)
			s.walkIR(n.Failed, inner)
		case *ir.ContextProvider:
			s.walkIR(n.Children, k)
		}
	}
}
