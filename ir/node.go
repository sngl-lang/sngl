package ir

// Node is the common interface of every IR statement and expression. Both the
// Stmt and Expr interfaces embed it, so any statement or expression is a Node.
// It exists so the IR visitor (see walkexprs.go) can present a single
// callback — func(Node) — over both kinds, mirroring go/ast's ast.Node.
type Node interface{ irNode() }

// irNode markers. Every concrete Stmt and Expr type implements Node.

// Expressions.
func (*Literal) irNode()     {}
func (*Ident) irNode()       {}
func (*Binary) irNode()      {}
func (*Unary) irNode()       {}
func (*Ternary) irNode()     {}
func (*Call) irNode()        {}
func (*Conversion) irNode()  {}
func (*Select) irNode()      {}
func (*Index) irNode()       {}
func (*StructLit) irNode()   {}
func (*ListLit) irNode()     {}
func (*MapLitIR) irNode()    {}
func (*Spread) irNode()      {}
func (*Lambda) irNode()      {}
func (*Closure) irNode()     {}
func (*ContextRead) irNode() {}

// Statements.
func (*NodeInst) irNode()         {}
func (*CallStmt) irNode()         {}
func (*SlotInst) irNode()         {}
func (*ErrorBoundary) irNode()    {}
func (*Assign) irNode()           {}
func (*Toggle) irNode()           {}
func (*Emit) irNode()             {}
func (*LocalVar) irNode()         {}
func (*Return) irNode()           {}
func (*Break) irNode()            {}
func (*Continue) irNode()         {}
func (*If) irNode()               {}
func (*For) irNode()              {}
func (*ContextProvider) irNode()  {}
func (*CanvasRedrawStmt) irNode() {}

// TransparentBlocks are the statement lists a construct carries a question
// into, for the four that are *how* the nodes under them got there rather than
// nodes in their own right: an `If` and a `For`, which say when and how many;
// an `ErrorBoundary`, which says what happens when one raises; and a
// `ContextProvider`, which sets a value for what is under it.
//
// Nil for anything else, which is what "not transparent" means.
//
// CLAUDE.md records this walk being written five times with a different member
// missing from each. The checker's treeTransparent is deliberately not this: it
// returns a second list as well, the blocks that may *supply* a family rather
// than merely be held to one, and that distinction belongs where the rule it
// serves is written.
//
// Each is a pointer to the field, so a pass rewriting the blocks assigns
// through it; one only reading them dereferences.
func TransparentBlocks(st Stmt) []*[]Stmt {
	switch s := st.(type) {
	case *If:
		return []*[]Stmt{&s.Body, &s.Else}
	case *For:
		return []*[]Stmt{&s.Body, &s.Else}
	case *ErrorBoundary:
		return []*[]Stmt{&s.Children, &s.Failed}
	case *ContextProvider:
		return []*[]Stmt{&s.Children}
	}
	return nil
}

// AttachNodeID gives a call site's `#id` and its handle to the first node the
// substituted body renders, and reports whether it found one.
//
// Both inliners need it and neither had it right. The optimizer's
// inlineComponentCall transferred nothing at all, so a `#id` on a component it
// composed away was simply gone -- and a read of it then reached a backend as a
// field nothing declares. lower's passInlinePure transferred to the first
// *top-level* NodeInst, so a body that opens with an `if` lost it the same way.
//
// The handle travels with the name because it is the name's other half: every
// read of the id resolves to that binding, and separating them leaves a rename
// with nothing to repoint and uniqueNodeIDs with nothing to key on.
func AttachNodeID(body []Stmt, id string, handle *Var) bool {
	return attachNode(body, func(ni *NodeInst) { ni.ID, ni.Handle = id, handle })
}

// AttachNodeSite records on the node AttachNodeID picks that it stands for
// callsite, keeping the outermost site through nested substitutions. The
// callsite's Record goes with it, being what the node a program wrote is as a
// value: a target that overrides nav.page with a primitive of its own finds
// which page the primitive is by it, and the params cell the page's content
// reads (Params) beside it.
func AttachNodeSite(body []Stmt, callsite *NodeInst) bool {
	site := callsite.Site
	if site == nil {
		site = callsite.AST
	}
	if site == nil && callsite.Record == nil && !callsite.Document {
		return false
	}
	return attachNode(body, func(ni *NodeInst) {
		if site != nil {
			ni.Site = site
		}
		if callsite.Document {
			ni.Document = true
		}
		if callsite.Record != nil && ni.Record == nil {
			ni.Record, ni.Params = callsite.Record, callsite.Params
		}
	})
}

func attachNode(body []Stmt, set func(*NodeInst)) bool {
	for _, s := range body {
		if ni, ok := s.(*NodeInst); ok {
			set(ni)
			return true
		}
		// Through the four, because a component whose body opens with a
		// conditional still renders whatever is inside it.
		for _, block := range TransparentBlocks(s) {
			if attachNode(*block, set) {
				return true
			}
		}
	}
	return false
}

// RepointHandler makes every alias of old under root name repl instead: a
// catch block (If.Catch) and a raise resolved to it (Call.ResolvedHandler).
// Both are pointers to a handler a boundary or a window owns, so a clone of
// the boundary that copies its handler has to carry the aliases in what it
// cloned to the copy, or the clone's content catches into the original.
func RepointHandler(root any, old, repl *EventHandler) {
	if old == nil || old == repl {
		return
	}
	_ = Walk(root, func(n Node) error {
		switch x := n.(type) {
		case *If:
			if x.Catch == old {
				x.Catch = repl
			}
		case *Call:
			if x.ResolvedHandler == old {
				x.ResolvedHandler = repl
			}
		}
		return nil
	})
}
