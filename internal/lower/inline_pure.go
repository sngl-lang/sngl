package lower

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passInlinePure substitutes pure-component calls with their inlined
// bodies. Pure = no Vars, no Funcs, no Timers.
//
// Two modes share the substitution engine:
//   - Optimization (always-on): inlines user-defined pure components.
//   - Strict (Caps.NoStdlibWrappers): inlines platform-stdlib wrappers
//     and errors if any platform-stdlib component is impure.
//
// Runs between passReactivity and passTimer. After passReactivity so
// reactive deps wire against user-level props before inlining flattens
// them; before passDeclarative so the inlined native NodeInsts get
// flattened along with everything else.
var passInlinePure = pass{
	name:    "InlinePure",
	enabled: func(c Caps) bool { return true }, // always on (strict path gated internally)
	apply:   lowerInlinePure,
}

func lowerInlinePure(pkg *ir.Package, caps Caps) error {
	if pkg == nil {
		return nil
	}
	st := &inlinePureState{
		pkg:        pkg,
		strictMode: caps.NoStdlibWrappers,
		inFlight:   map[*ir.Component]bool{},
		stack:      nil,
	}
	for _, comp := range pkg.Components {
		body, err := st.inlineStmts(comp.Body)
		if err != nil {
			return err
		}
		comp.Body = body
	}
	for _, w := range pkg.Windows {
		body, err := st.inlineStmts(w.Body)
		if err != nil {
			return err
		}
		w.Body = body
	}
	return nil
}

type inlinePureState struct {
	pkg        *ir.Package
	strictMode bool
	inFlight   map[*ir.Component]bool
	stack      []*ir.Component // active inline chain, for cycle-error messages
}

// isPure reports whether a component is structurally pure (no internal
// state). nil component → false.
func (st *inlinePureState) isPure(c *ir.Component) bool {
	if c == nil {
		return false
	}
	// A component with no body is platform-resolved (e.g. stdlib widget
	// stubs with Native metadata). Nothing to inline.
	if len(c.Body) == 0 {
		return false
	}
	return len(c.Vars) == 0 && len(c.Funcs) == 0 && len(c.Timers) == 0
}

// inlineStmts walks a stmt slice, recursing into nested control-flow
// bodies and NodeInst children/handlers, and inlines eligible
// NodeInst → component calls in place.
func (st *inlinePureState) inlineStmts(stmts []ir.Stmt) ([]ir.Stmt, error) {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		replaced, err := st.inlineStmt(s)
		if err != nil {
			return nil, err
		}
		out = append(out, replaced...)
	}
	return out, nil
}

// inlineStmt processes one stmt. Returns the slice of replacement stmts
// (may be one or many).
func (st *inlinePureState) inlineStmt(s ir.Stmt) ([]ir.Stmt, error) {
	switch n := s.(type) {
	case *ir.NodeInst:
		return st.inlineNodeInst(n)
	case *ir.If:
		body, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, err
		}
		els, err := st.inlineStmts(n.Else)
		if err != nil {
			return nil, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, nil
	case *ir.For:
		body, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, err
		}
		els, err := st.inlineStmts(n.Else)
		if err != nil {
			return nil, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, nil
	case *ir.PlatformFilter:
		body, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, err
		}
		n.Body = body
		return []ir.Stmt{n}, nil
	case *ir.SlotInst:
		ch, err := st.inlineStmts(n.Children)
		if err != nil {
			return nil, err
		}
		n.Children = ch
		return []ir.Stmt{n}, nil
	}
	return []ir.Stmt{s}, nil
}

// inlineNodeInst decides whether to inline n. If yes, runs the
// substitution engine and returns the substituted stmts. If no,
// recurses into n's children/handlers and returns n unchanged.
func (st *inlinePureState) inlineNodeInst(n *ir.NodeInst) ([]ir.Stmt, error) {
	// Recurse first so nested calls inline bottom-up.
	children, err := st.inlineStmts(n.Children)
	if err != nil {
		return nil, err
	}
	n.Children = children
	for _, h := range n.Handlers {
		if h.Func == nil {
			continue
		}
		body, err := st.inlineStmts(h.Func.Block)
		if err != nil {
			return nil, err
		}
		h.Func.Block = body
	}

	comp := n.Component
	if comp == nil {
		return []ir.Stmt{n}, nil
	}

	// Decide eligibility.
	pure := st.isPure(comp)
	strictApplies := st.strictMode && isPlatformStdlibComponent(st.pkg, comp)
	if !pure && !strictApplies {
		return []ir.Stmt{n}, nil
	}
	if strictApplies && !pure {
		return nil, fmt.Errorf("platform stdlib wrapper %q must be pure (declares %s) at %s", comp.Name, impurityReason(comp), compPos(comp))
	}

	// Cycle check. A pure-component recursion has no terminating shape
	// (no Vars/Funcs/Timers means no base case), so we treat any cycle
	// as a hard error regardless of mode.
	if st.inFlight[comp] {
		return nil, fmt.Errorf("inline cycle: %s at %s", st.cycleChain(comp), posOf(n.AST))
	}
	st.inFlight[comp] = true
	st.stack = append(st.stack, comp)
	defer func() {
		delete(st.inFlight, comp)
		st.stack = st.stack[:len(st.stack)-1]
	}()

	// Substitute.
	body, err := st.substitute(comp, n)
	if err != nil {
		return nil, err
	}
	// Recurse on substituted body (the wrapper's body may itself contain
	// pure-component calls that need inlining).
	return st.inlineStmts(body)
}

// cycleChain renders the active inline stack joined with " → ", appending
// the offending re-entry component to close the loop. Example: "Foo → Bar → Foo".
func (st *inlinePureState) cycleChain(reentry *ir.Component) string {
	parts := make([]string, 0, len(st.stack)+1)
	for _, c := range st.stack {
		parts = append(parts, c.Name)
	}
	parts = append(parts, reentry.Name)
	return strings.Join(parts, " → ")
}

// posOf extracts a printable *ast.Pos from an ast.Stmt, or "<unknown>" if
// the stmt is nil or has no position.
func posOf(s ast.Stmt) string {
	if s == nil {
		return "<unknown>"
	}
	if p := s.StmtPos(); p != nil && p.IsValid() {
		return p.String()
	}
	return "<unknown>"
}

// compPos returns a printable position for a component's declaration.
func compPos(c *ir.Component) string {
	if c == nil || c.AST == nil {
		return "<unknown>"
	}
	if p := c.AST.StmtPos(); p != nil && p.IsValid() {
		return p.String()
	}
	return "<unknown>"
}

// impurityReason returns a short string describing why comp is impure.
// Caller has already established len(Vars|Funcs|Timers) > 0.
func impurityReason(comp *ir.Component) string {
	var parts []string
	if len(comp.Vars) > 0 {
		parts = append(parts, fmt.Sprintf("var %q", comp.Vars[0].Name))
	}
	if len(comp.Funcs) > 0 {
		parts = append(parts, fmt.Sprintf("func %q", comp.Funcs[0].Name))
	}
	if len(comp.Timers) > 0 {
		parts = append(parts, "timer")
	}
	return strings.Join(parts, ", ")
}

// isPlatformStdlibComponent reports whether comp came from one of the
// package's platform:// imports.
func isPlatformStdlibComponent(pkg *ir.Package, comp *ir.Component) bool {
	for _, imp := range pkg.Imports {
		if !strings.HasPrefix(imp.Path, "platform://") {
			continue
		}
		if imp.Pkg == nil {
			continue
		}
		for _, c := range imp.Pkg.Components {
			if c == comp {
				return true
			}
		}
	}
	return false
}

// substitute applies the three substitutions (params, slot, events) to
// the wrapper's body, returning a fresh stmt slice ready to splice into
// the caller's position.
func (st *inlinePureState) substitute(comp *ir.Component, callsite *ir.NodeInst) ([]ir.Stmt, error) {
	if len(comp.Body) == 0 {
		return nil, fmt.Errorf("component %q has no body to inline", comp.Name)
	}

	// Build param-binding map: paramName → user's bound arg expression.
	bindings := map[string]ir.Expr{}
	for _, p := range comp.Props {
		for _, prop := range callsite.Props {
			if prop.Name == p.Name {
				bindings[p.Name] = prop.Value
				break
			}
		}
	}

	// Deep-clone the wrapper body so substitution mutations don't leak
	// across call sites.
	body := deepCloneStmts(comp.Body)

	// Apply param substitution (Ident-with-Param-Sym matching by name).
	body = substituteParams(body, bindings)

	// Apply slot substitution: replace each *ir.SlotInst with the user's
	// children.
	body = substituteSlots(body, callsite.Children)

	// Apply event-invocation substitution: replace any *ir.Emit whose
	// Name matches a user-provided event handler with the handler body.
	body = substituteEvents(body, callsite.Handlers)

	// ID preservation: transfer callsite.ID to the first top-level
	// NodeInst of the substituted body.
	if callsite.ID != "" {
		for _, s := range body {
			if ni, ok := s.(*ir.NodeInst); ok {
				ni.ID = callsite.ID
				break
			}
		}
	}

	return body, nil
}

// substituteParams walks stmts replacing every *ir.Ident whose Sym is a
// *ir.Param matched by name in bindings with a clone of the bound
// argument expression.
func substituteParams(stmts []ir.Stmt, bindings map[string]ir.Expr) []ir.Stmt {
	w := newExprWalker(func(e ir.Expr) ir.Expr {
		id, ok := e.(*ir.Ident)
		if !ok {
			return e
		}
		if _, isParam := id.Sym.(*ir.Param); !isParam {
			return e
		}
		if bound, ok := bindings[id.Name]; ok {
			return deepCloneExpr(bound)
		}
		return e
	})
	return w.stmts(stmts)
}

// substituteSlots replaces every *ir.SlotInst with the children slice.
func substituteSlots(stmts []ir.Stmt, children []ir.Stmt) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		if _, isSlot := s.(*ir.SlotInst); isSlot {
			out = append(out, children...)
			continue
		}
		switch n := s.(type) {
		case *ir.If:
			n.Body = substituteSlots(n.Body, children)
			n.Else = substituteSlots(n.Else, children)
		case *ir.For:
			n.Body = substituteSlots(n.Body, children)
			n.Else = substituteSlots(n.Else, children)
		case *ir.PlatformFilter:
			n.Body = substituteSlots(n.Body, children)
		case *ir.NodeInst:
			n.Children = substituteSlots(n.Children, children)
		}
		out = append(out, s)
	}
	return out
}

// substituteEvents replaces every *ir.Emit whose Name matches a
// user-provided event handler with the handler's body.
func substituteEvents(stmts []ir.Stmt, handlers []ir.EventHandler) []ir.Stmt {
	byName := map[string]*ir.EventHandler{}
	for i := range handlers {
		h := &handlers[i]
		byName[h.Name] = h
	}
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		if emit, isEmit := s.(*ir.Emit); isEmit {
			if h, ok := byName[emit.Name]; ok && h != nil && h.Func != nil {
				out = append(out, deepCloneStmts(h.Func.Block)...)
				continue
			}
			out = append(out, s)
			continue
		}
		// CallStmt whose callee is an EventRefExpr is the parser's
		// other shape for `@name(...)` invocations inside event
		// handler blocks.
		if cs, ok := s.(*ir.CallStmt); ok && cs.Call != nil && cs.Call.AST != nil {
			if evRef, ok := cs.Call.AST.Func.(*ast.EventRefExpr); ok {
				if h, ok := byName[evRef.Name]; ok && h != nil && h.Func != nil {
					out = append(out, deepCloneStmts(h.Func.Block)...)
					continue
				}
			}
		}
		switch n := s.(type) {
		case *ir.If:
			n.Body = substituteEvents(n.Body, handlers)
			n.Else = substituteEvents(n.Else, handlers)
		case *ir.For:
			n.Body = substituteEvents(n.Body, handlers)
			n.Else = substituteEvents(n.Else, handlers)
		case *ir.PlatformFilter:
			n.Body = substituteEvents(n.Body, handlers)
		case *ir.NodeInst:
			n.Children = substituteEvents(n.Children, handlers)
			for _, h := range n.Handlers {
				if h.Func != nil {
					h.Func.Block = substituteEvents(h.Func.Block, handlers)
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// deepCloneStmts produces a deep copy of stmts so substitution mutations
// don't leak across multiple call sites of the same component.
func deepCloneStmts(stmts []ir.Stmt) []ir.Stmt {
	out := make([]ir.Stmt, len(stmts))
	for i, s := range stmts {
		out[i] = deepCloneStmt(s)
	}
	return out
}

func deepCloneStmt(s ir.Stmt) ir.Stmt {
	switch n := s.(type) {
	case *ir.NodeInst:
		clone := *n
		clone.Children = deepCloneStmts(n.Children)
		clone.Handlers = make([]ir.EventHandler, len(n.Handlers))
		for i, h := range n.Handlers {
			hc := h
			if h.Func != nil {
				fc := *h.Func
				fc.Block = deepCloneStmts(h.Func.Block)
				hc.Func = &fc
			}
			clone.Handlers[i] = hc
		}
		clone.Props = make([]ir.Arg, len(n.Props))
		for i, p := range n.Props {
			pc := p
			pc.Value = deepCloneExpr(p.Value)
			clone.Props[i] = pc
		}
		clone.Key = deepCloneExpr(n.Key)
		clone.Ref = deepCloneExpr(n.Ref)
		return &clone
	case *ir.If:
		clone := *n
		clone.Cond = deepCloneExpr(n.Cond)
		clone.Body = deepCloneStmts(n.Body)
		clone.Else = deepCloneStmts(n.Else)
		return &clone
	case *ir.For:
		clone := *n
		clone.Iter = deepCloneExpr(n.Iter)
		clone.Body = deepCloneStmts(n.Body)
		clone.Else = deepCloneStmts(n.Else)
		return &clone
	case *ir.PlatformFilter:
		clone := *n
		clone.Body = deepCloneStmts(n.Body)
		return &clone
	case *ir.SlotInst:
		clone := *n
		clone.Children = deepCloneStmts(n.Children)
		return &clone
	case *ir.Emit:
		clone := *n
		clone.Args = append([]ir.CallArg{}, n.Args...)
		for i := range clone.Args {
			clone.Args[i].Value = deepCloneExpr(clone.Args[i].Value)
		}
		return &clone
	case *ir.Assign:
		clone := *n
		clone.Target = deepCloneExpr(n.Target)
		clone.Value = deepCloneExpr(n.Value)
		return &clone
	case *ir.CallStmt:
		clone := *n
		if n.Call != nil {
			cc, ok := deepCloneExpr(n.Call).(*ir.Call)
			if ok {
				clone.Call = cc
			}
		}
		return &clone
	case *ir.LocalVar:
		clone := *n
		clone.Init = deepCloneExpr(n.Init)
		return &clone
	case *ir.Return:
		clone := *n
		clone.Value = deepCloneExpr(n.Value)
		return &clone
	}
	return s
}

// deepCloneExpr is the expression analog of deepCloneStmt.
func deepCloneExpr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *ir.Ident:
		clone := *n
		return &clone
	case *ir.Literal:
		clone := *n
		return &clone
	case *ir.Binary:
		clone := *n
		clone.Left = deepCloneExpr(n.Left)
		clone.Right = deepCloneExpr(n.Right)
		return &clone
	case *ir.Unary:
		clone := *n
		clone.Operand = deepCloneExpr(n.Operand)
		return &clone
	case *ir.Ternary:
		clone := *n
		clone.Cond = deepCloneExpr(n.Cond)
		clone.Then = deepCloneExpr(n.Then)
		clone.Else = deepCloneExpr(n.Else)
		return &clone
	case *ir.Select:
		clone := *n
		clone.Operand = deepCloneExpr(n.Operand)
		return &clone
	case *ir.Index:
		clone := *n
		clone.Operand = deepCloneExpr(n.Operand)
		clone.Idx = deepCloneExpr(n.Idx)
		return &clone
	case *ir.Call:
		clone := *n
		clone.Receiver = deepCloneExpr(n.Receiver)
		clone.Callee = deepCloneExpr(n.Callee)
		clone.Args = append([]ir.CallArg{}, n.Args...)
		for i := range clone.Args {
			clone.Args[i].Value = deepCloneExpr(clone.Args[i].Value)
		}
		return &clone
	case *ir.Conversion:
		clone := *n
		clone.Operand = deepCloneExpr(n.Operand)
		return &clone
	case *ir.StructLit:
		clone := *n
		clone.Fields = append([]ir.FieldInit{}, n.Fields...)
		for i := range clone.Fields {
			clone.Fields[i].Value = deepCloneExpr(clone.Fields[i].Value)
		}
		return &clone
	case *ir.ListLit:
		clone := *n
		clone.Elems = append([]ir.Expr{}, n.Elems...)
		for i := range clone.Elems {
			clone.Elems[i] = deepCloneExpr(clone.Elems[i])
		}
		return &clone
	case *ir.MapLitIR:
		clone := *n
		clone.Entries = append([]ir.MapEntry{}, n.Entries...)
		for i := range clone.Entries {
			clone.Entries[i].Key = deepCloneExpr(clone.Entries[i].Key)
			clone.Entries[i].Value = deepCloneExpr(clone.Entries[i].Value)
		}
		return &clone
	case *ir.Spread:
		clone := *n
		clone.Operand = deepCloneExpr(n.Operand)
		return &clone
	}
	return e
}

// exprWalker applies a transform to every reachable expression in a
// statement tree. Used for param substitution.
type exprWalker struct {
	transform func(ir.Expr) ir.Expr
}

func newExprWalker(transform func(ir.Expr) ir.Expr) *exprWalker {
	return &exprWalker{transform: transform}
}

func (w *exprWalker) expr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	if out := w.transform(e); out != e {
		// Transform replaced this node. Do not recurse into the
		// replacement (the bound argument is already-checked user code
		// and may itself contain refs to other params/scopes we should
		// not re-transform).
		return out
	}
	switch n := e.(type) {
	case *ir.Binary:
		n.Left = w.expr(n.Left)
		n.Right = w.expr(n.Right)
	case *ir.Unary:
		n.Operand = w.expr(n.Operand)
	case *ir.Ternary:
		n.Cond = w.expr(n.Cond)
		n.Then = w.expr(n.Then)
		n.Else = w.expr(n.Else)
	case *ir.Select:
		n.Operand = w.expr(n.Operand)
	case *ir.Index:
		n.Operand = w.expr(n.Operand)
		n.Idx = w.expr(n.Idx)
	case *ir.Call:
		n.Receiver = w.expr(n.Receiver)
		n.Callee = w.expr(n.Callee)
		for i := range n.Args {
			n.Args[i].Value = w.expr(n.Args[i].Value)
		}
	case *ir.Conversion:
		n.Operand = w.expr(n.Operand)
	case *ir.StructLit:
		for i := range n.Fields {
			n.Fields[i].Value = w.expr(n.Fields[i].Value)
		}
	case *ir.ListLit:
		for i := range n.Elems {
			n.Elems[i] = w.expr(n.Elems[i])
		}
	case *ir.MapLitIR:
		for i := range n.Entries {
			n.Entries[i].Key = w.expr(n.Entries[i].Key)
			n.Entries[i].Value = w.expr(n.Entries[i].Value)
		}
	case *ir.Spread:
		n.Operand = w.expr(n.Operand)
	}
	return e
}

func (w *exprWalker) stmts(stmts []ir.Stmt) []ir.Stmt {
	for _, s := range stmts {
		w.stmt(s)
	}
	return stmts
}

func (w *exprWalker) stmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.NodeInst:
		for i := range n.Props {
			n.Props[i].Value = w.expr(n.Props[i].Value)
		}
		n.Key = w.expr(n.Key)
		n.Ref = w.expr(n.Ref)
		w.stmts(n.Children)
		for _, h := range n.Handlers {
			if h.Func != nil {
				w.stmts(h.Func.Block)
			}
		}
	case *ir.If:
		n.Cond = w.expr(n.Cond)
		w.stmts(n.Body)
		w.stmts(n.Else)
	case *ir.For:
		n.Iter = w.expr(n.Iter)
		w.stmts(n.Body)
		w.stmts(n.Else)
	case *ir.PlatformFilter:
		w.stmts(n.Body)
	case *ir.SlotInst:
		w.stmts(n.Children)
	case *ir.Assign:
		n.Target = w.expr(n.Target)
		n.Value = w.expr(n.Value)
	case *ir.Emit:
		for i := range n.Args {
			n.Args[i].Value = w.expr(n.Args[i].Value)
		}
	case *ir.LocalVar:
		n.Init = w.expr(n.Init)
	case *ir.Return:
		n.Value = w.expr(n.Value)
	case *ir.CallStmt:
		if n.Call != nil {
			n.Call.Receiver = w.expr(n.Call.Receiver)
			n.Call.Callee = w.expr(n.Call.Callee)
			for i := range n.Call.Args {
				n.Call.Args[i].Value = w.expr(n.Call.Args[i].Value)
			}
		}
	}
}
