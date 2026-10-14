package ir

import (
	"fmt"
	"slices"

	"duckfam.us/sngl/ast"
)

// SlotSplicer projects what a call site supplied into the insertion points a
// component's body declares. The optimizer and the inliner both do this, over
// IR they clone and bind differently -- one keys a parameter by pointer, the
// other by name -- so the two operations they differ in are supplied and the
// walk itself is written once. The two copies had already drifted -- one
// recursed into a ContextProvider's children and the other treated it as a
// leaf -- which is the divergence this shape exists to prevent, whichever of
// the two a given program reaches first.
type SlotSplicer struct {
	// Clone copies a body before it is spliced. Two insertions of one slot
	// would otherwise alias the same IR nodes.
	Clone func([]Stmt) []Stmt
	// Bind substitutes a scoped slot's arguments for the parameters the
	// populator declared, over a body Clone has already copied.
	Bind func(body []Stmt, sc *SlotContent, si *SlotInst) []Stmt
}

// Substitute replaces every SlotInst in stmts with what callsite supplied,
// and answers every `.empty` the body asks of one of its slots.
func (sp SlotSplicer) Substitute(stmts []Stmt, callsite *NodeInst) []Stmt {
	sp.answerEmpty(stmts, callsite)
	return sp.substitute(stmts, callsite)
}

// answerEmpty replaces each SlotEmpty in a body about to be spliced for
// callsite with what callsite supplied says (RendersNothing), on a copy of the
// content of its own, since the content itself is spliced in elsewhere. It
// runs before any content is spliced, so every SlotEmpty it meets is the
// body's own and asks about one of callsite's slots: one in a population a
// caller forwards asks about the caller's, and is answered where the caller's
// body is spliced.
func (sp SlotSplicer) answerEmpty(stmts []Stmt, callsite *NodeInst) {
	_ = RewriteExprs(stmts, func(e Expr) (Expr, error) {
		se, ok := e.(*SlotEmpty)
		if !ok {
			return e, nil
		}
		body, _, supplied := SlotBody(&SlotInst{Name: se.Slot.Name, Rest: se.Slot.Rest}, callsite)
		if !supplied {
			return boolLit(true), nil
		}
		answer, ok := RendersNothing(sp.Clone(body))
		if !ok {
			// The checker refused this content at the call site
			// (checkSlotEmptyAnswers); IR it did not see says nothing.
			return boolLit(false), nil
		}
		return answer, nil
	})
}

func (sp SlotSplicer) substitute(stmts []Stmt, callsite *NodeInst) []Stmt {
	out := make([]Stmt, 0, len(stmts))
	for _, s := range stmts {
		if si, isSlot := s.(*SlotInst); isSlot && si.Entry == nil {
			out = append(out, sp.body(si, callsite)...)
			continue
		}
		switch n := s.(type) {
		case *If:
			n.Body = sp.substitute(n.Body, callsite)
			n.Else = sp.substitute(n.Else, callsite)
		case *For:
			n.Body = sp.substitute(n.Body, callsite)
			n.Else = sp.substitute(n.Else, callsite)
		case *NodeInst:
			n.Children = sp.substitute(n.Children, callsite)
			for _, name := range SlotNames(n.Slots) {
				n.Slots[name].Body = sp.substitute(n.Slots[name].Body, callsite)
			}
		case *ErrorBoundary:
			n.Children = sp.substitute(n.Children, callsite)
			n.Failed = sp.substitute(n.Failed, callsite)
		case *ContextProvider:
			n.Children = sp.substitute(n.Children, callsite)
		case *Assign, *LocalVar, *Return, *CallStmt, *Emit, *Toggle, *CanvasRedrawStmt,
			*Break, *Continue:
			// Leaf stmts -- no nested SlotInsts.
		case *SlotInst:
			// A population's entry, which entries substitutes when the
			// population is spliced for the insertion that supplied it.
		default:
			panic(fmt.Sprintf("SlotSplicer.Substitute: unhandled %T", n))
		}
		out = append(out, s)
	}
	return out
}

// body is what one insertion point renders, cloned and bound for splicing.
func (sp SlotSplicer) body(si *SlotInst, callsite *NodeInst) []Stmt {
	stmts, sc, supplied := SlotBody(si, callsite)
	fallback := func() []Stmt { return sp.substitute(sp.Clone(si.Children), callsite) }
	if !supplied {
		return fallback()
	}
	var out []Stmt
	switch {
	case sc == nil:
		out = sp.Clone(stmts)
	case si.Decl == nil || len(si.Decl.Slots) == 0:
		out = sp.Bind(sp.Clone(stmts), sc, si)
	default:
		out = sp.entries(sp.Bind(sp.Clone(stmts), sc, si), si, callsite)
	}
	if len(si.Children) == 0 {
		return out
	}
	return WithFallback(out, fallback)
}

// WithFallback is content with an insertion's fallback standing wherever the
// content renders nothing: in place of no content, in a branch of an `if` that
// holds none, and as the fallback of a slot the content only forwards, which
// renders nothing when what was supplied for that slot does. Looked for
// through those three alone: a `for` that runs no times, or a component that
// renders nothing, still renders the content.
func WithFallback(stmts []Stmt, fallback func() []Stmt) []Stmt {
	if len(stmts) == 0 {
		return fallback()
	}
	if len(stmts) != 1 {
		return stmts
	}
	switch n := stmts[0].(type) {
	case *If:
		if n.Catch != nil || n.FromTernary {
			return stmts
		}
		n.Body = WithFallback(n.Body, fallback)
		n.Else = WithFallback(n.Else, fallback)
	case *SlotInst:
		if n.Entry == nil && len(n.Children) == 0 {
			n.Children = fallback()
		}
	}
	return stmts
}

// entries replaces, in a population spliced for ins, every insertion of one of
// ins's component entries with what ins populated it with. That content was
// written beside ins, in the callee's body, so the callee's own slots in it
// still answer to callsite.
func (sp SlotSplicer) entries(stmts []Stmt, ins *SlotInst, callsite *NodeInst) []Stmt {
	out := make([]Stmt, 0, len(stmts))
	for _, s := range stmts {
		if x, ok := s.(*SlotInst); ok && x.Entry != nil && slices.Contains(ins.Decl.Slots, x.Entry) {
			out = append(out, sp.entry(x, ins, callsite)...)
			continue
		}
		switch n := s.(type) {
		case *If:
			n.Body = sp.entries(n.Body, ins, callsite)
			n.Else = sp.entries(n.Else, ins, callsite)
		case *For:
			n.Body = sp.entries(n.Body, ins, callsite)
			n.Else = sp.entries(n.Else, ins, callsite)
		case *NodeInst:
			n.Children = sp.entries(n.Children, ins, callsite)
			for _, name := range SlotNames(n.Slots) {
				n.Slots[name].Body = sp.entries(n.Slots[name].Body, ins, callsite)
			}
		case *SlotInst:
			n.Children = sp.entries(n.Children, ins, callsite)
			for _, name := range SlotNames(n.Slots) {
				n.Slots[name].Body = sp.entries(n.Slots[name].Body, ins, callsite)
			}
		case *ErrorBoundary:
			n.Children = sp.entries(n.Children, ins, callsite)
			n.Failed = sp.entries(n.Failed, ins, callsite)
		case *ContextProvider:
			n.Children = sp.entries(n.Children, ins, callsite)
		}
		out = append(out, s)
	}
	return out
}

func (sp SlotSplicer) entry(x, ins *SlotInst, callsite *NodeInst) []Stmt {
	content := ins.Slots[x.Entry.Name]
	if content == nil {
		return sp.entries(x.Children, ins, callsite)
	}
	body := sp.Bind(sp.Clone(content.Body), content, x)
	return sp.Substitute(body, callsite)
}

// Supplied is what a call site hands one slot of its component: a population
// by name (Content non-nil) or the bare children for the rest slot.
type Supplied struct {
	Decl    *SlotDecl
	Content *SlotContent
	Body    []Stmt
}

// SuppliedContent is everything n hands its component, in the order the
// component declares its slots. A node whose component declares none has only
// its children, under no declaration.
func SuppliedContent(n *NodeInst) []Supplied {
	if n.Component == nil || len(n.Component.Slots) == 0 {
		if len(n.Children) == 0 {
			return nil
		}
		return []Supplied{{Body: n.Children}}
	}
	var out []Supplied
	for _, s := range n.Component.Slots {
		switch sc := n.Slots[s.Name]; {
		case sc != nil:
			out = append(out, Supplied{Decl: s, Content: sc, Body: sc.Body})
		case s.Rest && len(n.Children) > 0:
			out = append(out, Supplied{Decl: s, Body: n.Children})
		}
	}
	return out
}

// SlotBody reports what one insertion point renders: the content the call site
// supplied for it, or the insertion's own block as the fallback when it
// supplied none.
//
// supplied is what tells a renderer whose scope to evaluate the body in.
// Content written at the call site reads the caller's scope; an insertion's own
// fallback reads the component's. A splicer flattens both into one scope and so
// does not need the distinction, but an interpreter evaluating in place does --
// which is why the decision lives here and the two share it rather than each
// deciding for itself. The two copies of the walk above had already drifted
// once; this is the same hazard one level down.
//
// sc is non-nil only for content that arrived through the named-slot map, and
// carries the parameter names a scoped slot binds its arguments to. Children
// written bare have no binding site to have written names at, so they see no
// parameters at all -- a caller that wants them writes the population. A nil
// callsite is a component instantiated with nothing supplied at all.
func SlotBody(si *SlotInst, callsite *NodeInst) (body []Stmt, sc *SlotContent, supplied bool) {
	if callsite == nil {
		return si.Children, nil, false
	}
	if c := callsite.Slots[si.Name]; c != nil {
		return c.Body, c, true
	}
	// The rest slot's content arrives as ordinary children rather than through
	// the map.
	if si.Rest && len(callsite.Children) > 0 {
		return callsite.Children, nil, true
	}
	return si.Children, nil, false
}

// SlotEmpty is `src.empty` in a component's body: whether what a call site
// supplied for one of the component's slots renders nothing. It asks about a
// call site, so it has no value of its own: the splicer answers it from the
// population where the body is spliced (SlotSplicer.Substitute, with
// RendersNothing), an instance built at run time is handed the answer as a
// prop, and the interpreter asks the content it mounted.
type SlotEmpty struct {
	AST  *ast.SelectExpr
	Slot *SlotDecl
}

// SlotEmptyMember is the member a slot's name is read through to ask it.
const SlotEmptyMember = "empty"

func (x *SlotEmpty) exprNode()       {}
func (x *SlotEmpty) ExprType() *Type { return TypBool }

// RendersNothing is whether stmts, a population a call site supplied, renders
// nothing: true for none, false where a node stands, and decided where it is
// written for the rest -- an `if` by its condition, a `for` by its iterable, a
// forward of another slot by that slot's own answer and its fallback's. So a
// population that renders under `if hasPhoto` is empty exactly when
// `!hasPhoto`, and stays reactive. ok is false for content whose answer turns
// on what one copy of a loop renders, which no expression at the position
// can state, or on a loop whose head cannot be measured.
//
// The answer is built from stmts' own expressions, so stmts must be a copy
// nothing else holds.
func RendersNothing(stmts []Stmt) (e Expr, ok bool) {
	var parts []Expr
	for _, s := range stmts {
		p, ok := stmtRendersNothing(s)
		if !ok {
			return nil, false
		}
		if lit, isLit := p.(*Literal); isLit {
			if lit.Value == "false" {
				return boolLit(false), true
			}
			continue // a true part says nothing
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return boolLit(true), true
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out = &Binary{Type: TypBool, Op: ast.BinAnd, Left: out, Right: p}
	}
	return out, true
}

func stmtRendersNothing(s Stmt) (Expr, bool) {
	switch n := s.(type) {
	case *NodeInst, *CallStmt:
		// A node, or a component written as a call.
		return boolLit(false), true
	case *If:
		if n.Catch != nil {
			return RendersNothing(n.Body)
		}
		then, ok := RendersNothing(n.Body)
		if !ok {
			return nil, false
		}
		els, ok := RendersNothing(n.Else)
		if !ok {
			return nil, false
		}
		return foldTernary(n.Cond, then, els), true
	case *For:
		body, ok := RendersNothing(n.Body)
		if !ok {
			return nil, false
		}
		els, ok := RendersNothing(n.Else)
		if !ok {
			return nil, false
		}
		lit, isLit := body.(*Literal)
		if !isLit {
			return nil, false
		}
		if lit.Value == "true" {
			return els, true
		}
		// Each copy renders something, so the loop does exactly when it runs.
		empty := EmptyTest(n, nil)
		if empty == nil {
			return nil, false
		}
		return foldTernary(empty, els, boolLit(false)), true
	case *SlotInst:
		var own Expr = &SlotEmpty{Slot: n.Decl}
		if n.Entry != nil {
			own = &SlotEmpty{Slot: n.Entry}
		}
		if len(n.Children) == 0 {
			return own, true
		}
		fb, ok := RendersNothing(n.Children)
		if !ok {
			return nil, false
		}
		return foldAnd(own, fb), true
	case *ErrorBoundary:
		return RendersNothing(n.Children)
	case *ContextProvider:
		return RendersNothing(n.Children)
	}
	// A declaration or an imperative statement renders nothing itself.
	return boolLit(true), true
}

func boolLit(v bool) *Literal {
	if v {
		return &Literal{Type: TypBool, Value: "true"}
	}
	return &Literal{Type: TypBool, Value: "false"}
}

func foldAnd(a, b Expr) Expr {
	if l, ok := a.(*Literal); ok {
		if l.Value == "false" {
			return a
		}
		return b
	}
	if l, ok := b.(*Literal); ok {
		if l.Value == "false" {
			return b
		}
		return a
	}
	return &Binary{Type: TypBool, Op: ast.BinAnd, Left: a, Right: b}
}

func foldTernary(cond, then, els Expr) Expr {
	tl, tok := then.(*Literal)
	el, eok := els.(*Literal)
	switch {
	case tok && eok && tl.Value == el.Value:
		return then
	case tok && eok && tl.Value == "true":
		return cond
	case tok && eok:
		return &Unary{Type: TypBool, Op: ast.UnaryNot, Operand: cond}
	}
	return &Ternary{Type: TypBool, Cond: cond, Then: then, Else: els}
}
