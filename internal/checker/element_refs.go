package checker

import (
	"slices"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// elementRef resolves `c.<id>` where c is an instance of comp: the component
// the node with that id instantiates, and how many of it the read names.
//
// comp's refs are the ids written in its own body, each instance holding its
// own. They are not the ids in the bodies of the components it instantiates --
// those are reached through that instance's id, `c.inbox.lbl` -- nor the ids
// in a slot population, which is a declaration with a scope of its own. Bare
// children handed to a callee are comp's, and render exactly once where the
// callee inserts them (checkSlotInsertions), so they count as written.
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
		s.walkIR(comp.Stmts, ir.RefOne)
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
			s.walk(n.Body.Stmts, k.Inside(ir.RefOption))
			s.walk(n.Else.Stmts, k.Inside(ir.RefOption))
		case *ast.ForStmt:
			s.walk(n.Body.Stmts, k.Inside(ir.RefList))
			s.walk(n.Else.Stmts, k.Inside(ir.RefOption))
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				s.walk(arm.Body.Stmts, k.Inside(ir.RefOption))
			}
		}
	}
}

func (s *refSearch) node(id, target string, block *ast.StmtBlock, k ir.RefCount) {
	content, pops := splitPopulations(block)
	if ownerSlot(s.comp, target) != nil {
		// The block at an insertion is its fallback, which renders only where
		// nothing was supplied.
		s.walk(content, k.Inside(ir.RefOption))
		return
	}
	comp := s.c.componentNamed(target)
	if id == s.id && comp != nil && s.host == nil {
		s.host, s.count = comp, k
	}
	// A population's ids are its own, reached through the node it populates
	// (populationRef). Content beside a boundary's fallback may be what the
	// fallback replaced.
	if comp != nil && comp.Builtin == ir.BuiltinErrorBoundary && populatesFallback(comp, pops) {
		k = k.Inside(ir.RefOption)
	}
	if comp != nil {
		if rest := comp.RestSlot(); rest != nil {
			k = insideSlot(k, comp, rest)
		}
	}
	s.walk(content, k)
}

// insideSlot is k for what fills slot of host, which renders as often as host
// inserts it.
func insideSlot(k ir.RefCount, host *ir.Component, slot *ir.SlotDecl) ir.RefCount {
	switch host.InsertsOf(slot) {
	case ir.InsertOptional:
		return k.Inside(ir.RefOption)
	case ir.InsertMany:
		return k.Inside(ir.RefList)
	}
	return k
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
func isElementRefRead(e ir.Expr) bool { return refSelect(e) != nil }

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
			if n.Component != nil {
				if rest := n.Component.RestSlot(); rest != nil {
					inner = insideSlot(k, n.Component, rest)
				}
			}
			s.walkIR(n.Children, inner)
		case *ir.SlotInst:
			s.walkIR(n.Children, k.Inside(ir.RefOption))
		case *ir.If:
			s.walkIR(n.Body, k.Inside(ir.RefOption))
			s.walkIR(n.Else, k.Inside(ir.RefOption))
		case *ir.For:
			s.walkIR(n.Body, k.Inside(ir.RefList))
			s.walkIR(n.Else, k.Inside(ir.RefOption))
		case *ir.ErrorBoundary:
			inner := k
			if len(n.Failed) > 0 {
				inner = k.Inside(ir.RefOption)
			}
			s.walkIR(n.Children, inner)
		case *ir.ContextProvider:
			s.walkIR(n.Children, k)
		}
	}
}

// populationRef resolves `<node>.<id>` where id is written in a population of
// node's slots, which owner's body wrote: the component that node instantiates
// and how many of it one instance of node renders. That is how often node
// inserts the slot, then where the node sits inside the population.
func (c *checker) populationRef(owner *ir.Component, node *ast.VisualNode, id string) (*ir.Component, ir.RefCount) {
	if node == nil {
		return nil, ir.RefNone
	}
	host := c.componentNamed(visualNodeTarget(node))
	for _, st := range node.Block.Stmts {
		cd, ok := st.(*ast.ComponentDecl)
		if !ok {
			continue
		}
		k := ir.RefOne
		if host != nil {
			for _, slot := range host.Slots {
				if slot.Name == cd.Name {
					k = insideSlot(k, host, slot)
				}
			}
		}
		s := &refSearch{c: c, comp: owner, id: id}
		s.walk(cd.Body.Stmts, k)
		if s.host != nil {
			return s.host, s.count
		}
	}
	return nil, ir.RefNone
}

// refSelect is the ref read e is, under the index or narrowing that picked one
// node of it, or nil.
func refSelect(e ir.Expr) *ir.Select {
	for {
		switch n := e.(type) {
		case *ir.Conversion:
			e = n.Operand
		case *ir.Index:
			e = n.Operand
		case *ir.Select:
			if n.Ref == ir.RefNone {
				return nil
			}
			return n
		default:
			return nil
		}
	}
}
