package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// elementRef resolves `c.<id>` where c is an instance of comp: the component
// the node with that id instantiates, and how many of it the read names.
//
// The count is the node's position, read statically from the tree an instance
// of comp renders -- its own body, the bodies of the components it
// instantiates, and the content its call sites supply to their slots, followed
// to where each slot is inserted. A `for` anywhere on the way makes it a list,
// a conditional an option, and nothing at all one node.
//
// A node reached at more than one position -- two instantiations of the
// component holding it, a slot inserted twice, a recursive component -- is a
// list, since nothing else holds the nodes that renders.
//
// The walk is over the AST because a component's IR body may not be checked
// yet at this point in pass2.
func (c *checker) elementRef(comp *ir.Component, id string) (*ir.Component, ir.RefCount) {
	if comp == nil || comp.AST == nil || id == "" {
		return nil, ir.RefNone
	}
	s := &refSearch{c: c, id: id}
	s.enter(nil, comp, nil, ir.RefOne)
	if s.found == 0 {
		return nil, ir.RefNone
	}
	if s.found > 1 || s.repeated {
		return s.host, ir.RefList
	}
	return s.host, s.count
}

type refSearch struct {
	c     *checker
	id    string
	host  *ir.Component
	count ir.RefCount
	found int
	// repeated is set when the node is under a component that renders itself,
	// however the recursion is guarded.
	repeated bool
	stack    []*refFrame
}

// refFrame is one component being expanded, with what its call site supplied
// for each of its slots.
type refFrame struct {
	// site is the statement that instantiated comp. Meeting it again while it
	// is still being expanded is recursion; meeting comp from another site is
	// not, since each site supplies its own content.
	site      ast.Stmt
	comp      *ir.Component
	supplied  map[string][]slotSupply
	reentered bool
}

// slotSupply is content written at a call site, and the frame it was written
// in: a slot inserted inside it is that frame's.
type slotSupply struct {
	stmts []ast.Stmt
	frame *refFrame
}

func joinRef(a, b ir.RefCount) ir.RefCount { return max(a, b) }

func (s *refSearch) enter(site ast.Stmt, comp *ir.Component, supplied map[string][]slotSupply, k ir.RefCount) {
	for _, fr := range s.stack {
		if site != nil && fr.site == site {
			fr.reentered = true
			return
		}
	}
	fr := &refFrame{site: site, comp: comp, supplied: supplied}
	s.stack = append(s.stack, fr)
	before := s.found
	s.walkIn(fr, comp.AST.Body.Stmts, k)
	s.stack = s.stack[:len(s.stack)-1]
	if fr.reentered && s.found > before {
		s.repeated = true
	}
}

// walkIn walks stmts written in fr's component, resolving names through the
// imports of the file that component was declared in.
func (s *refSearch) walkIn(fr *refFrame, stmts []ast.Stmt, k ir.RefCount) {
	restore := s.c.fileOf(compDeclPos(fr.comp))
	defer restore()
	s.walk(fr, stmts, k)
}

func (s *refSearch) walk(fr *refFrame, stmts []ast.Stmt, k ir.RefCount) {
	for _, st := range stmts {
		switch n := st.(type) {
		case *ast.VisualNode:
			s.node(fr, n, n.ID, visualNodeTarget(n), &n.Block, k)
		case *ast.CallStmt:
			if target, id, isElem := elementRefCallInfo(n.Call); isElem {
				s.node(fr, n, id, target, nil, k)
			} else {
				s.node(fr, n, "", callTargetName(n.Call.Func), nil, k)
			}
		case *ast.IfStmt:
			s.walk(fr, n.Body.Stmts, joinRef(k, ir.RefOption))
			s.walk(fr, n.Else.Stmts, joinRef(k, ir.RefOption))
		case *ast.ForStmt:
			s.walk(fr, n.Body.Stmts, joinRef(k, ir.RefList))
			s.walk(fr, n.Else.Stmts, joinRef(k, ir.RefOption))
		}
	}
}

func (s *refSearch) node(fr *refFrame, site ast.Stmt, id, target string, block *ast.StmtBlock, k ir.RefCount) {
	content, pops := splitPopulations(block)
	if slot := ownerSlot(fr.comp, target); slot != nil {
		for _, sup := range fr.supplied[slot.Name] {
			s.walkIn(sup.frame, sup.stmts, k)
		}
		// The block at an insertion is its fallback, which renders only where
		// nothing was supplied.
		s.walk(fr, content, joinRef(k, ir.RefOption))
		// An entry population renders where the population inserts the entry,
		// which this walk does not follow; it is counted as the insertion is.
		for _, cd := range pops {
			s.walk(fr, cd.Body.Stmts, k)
		}
		return
	}
	comp := s.c.componentNamed(target)
	if id == s.id && comp != nil {
		if s.found == 0 {
			s.host = comp
		}
		s.found++
		s.count = joinRef(s.count, k)
	}
	switch {
	case comp != nil && comp.Builtin == ir.BuiltinErrorBoundary && populatesFallback(comp, pops):
		// A boundary with a fallback renders it in place of its content once
		// an error reaches it; without one the content stays.
		k = joinRef(k, ir.RefOption)
	case comp != nil && comp.Builtin == ir.BuiltinWindow:
		// A window may not be open.
		k = joinRef(k, ir.RefOption)
	case comp != nil && expandsForRefs(comp):
		supplied := map[string][]slotSupply{}
		if rest := comp.RestSlot(); rest != nil && len(content) > 0 {
			supplied[rest.Name] = append(supplied[rest.Name], slotSupply{content, fr})
		}
		for _, cd := range pops {
			supplied[cd.Name] = append(supplied[cd.Name], slotSupply{cd.Body.Stmts, fr})
		}
		s.enter(site, comp, supplied, k)
		return
	}
	s.walk(fr, content, k)
	for _, cd := range pops {
		s.walk(fr, cd.Body.Stmts, k)
	}
}

// expandsForRefs reports whether comp's body is followed: a program's own
// component with one. A library component's body may be replaced per target,
// so what it renders its slots into is not a single answer, and its children
// are counted as rendered once where it is.
func expandsForRefs(comp *ir.Component) bool {
	return comp.Pkg == "" && comp.AST != nil && comp.AST.Body.IsDefined() && comp.Builtin == ir.BuiltinNone
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
