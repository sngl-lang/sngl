package lower

import (
	"fmt"
	"slices"
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
// Runs between passToggle and passReactivity. Must run BEFORE
// passReactivity because passReactivity eagerly invokes the declarative
// lowering for renderSlot bodies — once a NodeInst has been flattened
// into LocalVar(CreateNode("<tag>")) + Assigns + CallStmts, this pass
// can no longer recognise the wrapper call and substitute its body.
// Runs after passToggle/passLambda/etc. so the wrapper body the pass
// sees has already had high-level shapes (toggles, ternaries) lowered.
var passInlinePure = pass{
	name:    "InlinePure",
	enabled: func(c Caps) bool { return true }, // always on (strict path gated internally)
	apply:   lowerInlinePure,
}

func lowerInlinePure(pkg *ir.Package, caps Caps, _ Options) error {
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
		// passReactivity synthesizes __renderSlotN Funcs whose bodies still
		// contain NodeInsts referencing stdlib wrapper components; inline
		// those too so the platform translator only ever sees native tags.
		for _, fn := range comp.Funcs {
			if fn == nil {
				continue
			}
			fnBody, err := st.inlineStmts(fn.Block)
			if err != nil {
				return err
			}
			fn.Block = fnBody
		}
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
	case *ir.ErrorBoundary:
		ch, err := st.inlineStmts(n.Children)
		if err != nil {
			return nil, err
		}
		n.Children = ch
		return []ir.Stmt{n}, nil
	case *ir.Window:
		body, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, err
		}
		n.Body = body
		return []ir.Stmt{n}, nil
	case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider:
		// Leaf/imperative stmts — no NodeInsts to inline.
		return []ir.Stmt{s}, nil
	default:
		panic(fmt.Sprintf("inlineStmt: unhandled %T", n))
	}
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

	// Recursive pure components (self-call directly or transitively) can't
	// be inlined to a finite body. In strict mode that's fatal; in
	// optimization mode the user's recursion is legitimate, leave as-is.
	if st.inFlight[comp] || containsSelfRef(comp) {
		if strictApplies {
			return nil, fmt.Errorf("inline cycle: %s at %s", st.cycleChain(comp), posOf(n.AST))
		}
		return []ir.Stmt{n}, nil
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

// containsSelfRef reports whether comp's body invokes comp anywhere
// (direct self-reference). Mutual recursion isn't detected here — the
// in-flight check catches that during substitution.
func containsSelfRef(comp *ir.Component) bool {
	var visit func(stmts []ir.Stmt) bool
	visit = func(stmts []ir.Stmt) bool {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				if n.Component == comp {
					return true
				}
				if visit(n.Children) {
					return true
				}
				for _, h := range n.Handlers {
					if h.Func != nil && visit(h.Func.Block) {
						return true
					}
				}
			case *ir.If:
				if visit(n.Body) || visit(n.Else) {
					return true
				}
			case *ir.For:
				if visit(n.Body) || visit(n.Else) {
					return true
				}
			case *ir.PlatformFilter:
				if visit(n.Body) {
					return true
				}
			case *ir.SlotInst:
				if visit(n.Children) {
					return true
				}
			case *ir.ErrorBoundary:
				if visit(n.Children) {
					return true
				}
			case *ir.Window:
				if visit(n.Body) {
					return true
				}
			case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider:
				// Leaf stmts can't host a NodeInst self-reference.
			default:
				panic(fmt.Sprintf("containsSelfRef.visit: unhandled %T", n))
			}
		}
		return false
	}
	return visit(comp.Body)
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
		if slices.Contains(imp.Pkg.Components, comp) {
			return true
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

	// Build param-binding map. Bind every prop, falling back from the
	// call-site arg to the prop's default to a typed zero-value, so the
	// body never keeps a bare param identifier (mirrors expandCall in
	// inline_components.go; ZeroExpr covers props with no default, e.g.
	// `disabled bool`).
	bindings := map[string]ir.Expr{}
	for _, p := range comp.Props {
		var val ir.Expr
		for _, prop := range callsite.Props {
			if prop.Name == p.Name {
				val = prop.Value
				break
			}
		}
		if val == nil {
			val = p.Default
		}
		if val == nil {
			val = ir.ZeroExpr(p.Type)
		}
		if val != nil {
			bindings[p.Name] = val
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

	// Event-handler transfer: the wrapper body may declare its events
	// purely as metadata (e.g. the bubbletea `Styled(events=[Event{...}])`
	// primitive) without emitting them via @name(). Those wrappers never
	// match in substituteEvents, so the user's call-site handlers (@click,
	// @change, ...) would be lost. Carry any call-site handler not already
	// consumed by an emit onto the first top-level primitive NodeInst of
	// the body so platform codegen can find it. Handlers already wired
	// through an explicit @name() emit in the body have been substituted in
	// place above and are skipped here to avoid double-emission.
	if len(callsite.Handlers) > 0 {
		emitted := emittedHandlerNames(comp.Body)
		var pending []ir.EventHandler
		for _, h := range callsite.Handlers {
			if _, ok := emitted[h.Name]; ok {
				continue
			}
			pending = append(pending, h)
		}
		if len(pending) > 0 {
			for _, s := range body {
				if ni, ok := s.(*ir.NodeInst); ok {
					ni.Handlers = append(ni.Handlers, pending...)
					break
				}
			}
		}
	}

	return body, nil
}

// emittedHandlerNames returns the set of event names the component body
// invokes via an `@name()` emit (either *ir.Emit or a *ir.CallStmt over an
// *ast.EventRefExpr). Handlers with these names are wired through
// substituteEvents and must not also be transferred onto the root node.
func emittedHandlerNames(stmts []ir.Stmt) map[string]struct{} {
	out := map[string]struct{}{}
	var visit func(stmts []ir.Stmt)
	visit = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.Emit:
				out[n.Name] = struct{}{}
			case *ir.CallStmt:
				if n.Call != nil && n.Call.AST != nil {
					if ev, ok := n.Call.AST.Func.(*ast.EventRefExpr); ok {
						out[ev.Name] = struct{}{}
					}
				}
			case *ir.NodeInst:
				visit(n.Children)
				for _, h := range n.Handlers {
					if h.Func != nil {
						visit(h.Func.Block)
					}
				}
			case *ir.If:
				visit(n.Body)
				visit(n.Else)
			case *ir.For:
				visit(n.Body)
				visit(n.Else)
			case *ir.PlatformFilter:
				visit(n.Body)
			case *ir.SlotInst:
				visit(n.Children)
			case *ir.ErrorBoundary:
				visit(n.Children)
			case *ir.Window:
				visit(n.Body)
			case *ir.ContextProvider:
				visit(n.Children)
			}
		}
	}
	visit(stmts)
	return out
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
		case *ir.ErrorBoundary:
			n.Children = substituteSlots(n.Children, children)
		case *ir.Window:
			n.Body = substituteSlots(n.Body, children)
		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider:
			// Leaf stmts — no nested SlotInsts.
		default:
			panic(fmt.Sprintf("substituteSlots: unhandled %T", n))
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
				out = append(out, bindEventParams(deepCloneStmts(h.Func.Block), h.Func.Params, emit.Args)...)
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
					out = append(out, bindEventParams(deepCloneStmts(h.Func.Block), h.Func.Params, cs.Call.Args)...)
					continue
				}
				// No matching user handler — the caller never bound @<name>.
				// Drop the @<name>() invocation entirely so codegen doesn't
				// emit a callee-less `()` expression. The wrapping platform
				// handler may end up with an empty body; that's fine — the
				// caller never wanted an event handler installed.
				continue
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
		case *ir.SlotInst:
			n.Children = substituteEvents(n.Children, handlers)
		case *ir.ErrorBoundary:
			n.Children = substituteEvents(n.Children, handlers)
		case *ir.Window:
			n.Body = substituteEvents(n.Body, handlers)
		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Toggle, *ir.ContextProvider:
			// Leaf/imperative — no nested Emit/EventRefExpr that this pass
			// would substitute. (CallStmt with EventRefExpr handled above.)
		case *ir.Emit:
			// Emit already handled at top of loop; reaching here means
			// no matching handler — pass-through.
		default:
			panic(fmt.Sprintf("substituteEvents: unhandled %T", n))
		}
		out = append(out, s)
	}
	return out
}

// bindEventParams rebinds references to a user event handler's declared
// params inside `stmts` to the arg expressions the wrapper passed. When
// the wrapper passes zero args but the user handler declared params (the
// common `@input { @input() }` shape in platform .sngl wrappers), the
// params are renamed to "event" so the JS emitter's EventVar swap maps
// `myEvent.value` → `e.target.value` the same way it did before the
// wrapper was inlined.
func bindEventParams(stmts []ir.Stmt, params []*ir.Param, args []ir.CallArg) []ir.Stmt {
	if len(params) == 0 {
		return stmts
	}
	bindings := map[string]ir.Expr{}
	for i, p := range params {
		if p == nil || p.Name == "" {
			continue
		}
		if i < len(args) {
			bindings[p.Name] = args[i].Value
			continue
		}
		// No matching arg — wrapper invoked @event() with fewer args
		// than the user handler declared. Rename the param to the
		// canonical `event` ident so JS EventVar swap picks it up.
		bindings[p.Name] = &ir.Ident{Name: "event", Type: p.Type, Sym: p}
	}
	if len(bindings) == 0 {
		return stmts
	}
	walker := newExprWalker(func(e ir.Expr) ir.Expr {
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
	return walker.stmts(stmts)
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
	case *ir.Toggle:
		clone := *n
		clone.Target = deepCloneExpr(n.Target)
		return &clone
	case *ir.ErrorBoundary:
		clone := *n
		clone.Children = deepCloneStmts(n.Children)
		return &clone
	case *ir.Window:
		clone := *n
		clone.Body = deepCloneStmts(n.Body)
		return &clone
	case *ir.ContextProvider:
		clone := *n
		clone.Value = deepCloneExpr(n.Value)
		clone.Children = deepCloneStmts(n.Children)
		return &clone
	}
	panic(fmt.Sprintf("deepCloneStmt: unhandled %T", s))
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
	case *ir.ContextRead:
		clone := *n
		return &clone
	case *ir.Lambda:
		clone := *n
		if n.Func != nil {
			fc := *n.Func
			fc.Block = deepCloneStmts(n.Func.Block)
			clone.Func = &fc
		}
		return &clone
	case *ir.Closure:
		clone := *n
		if n.State != nil {
			s := deepCloneExpr(n.State).(*ir.StructLit)
			clone.State = s
		}
		return &clone
	}
	panic(fmt.Sprintf("deepCloneExpr: unhandled %T", e))
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
	case *ir.Lambda:
		if n.Func != nil {
			w.stmts(n.Func.Block)
		}
	case *ir.Closure:
		if n.State != nil {
			for i := range n.State.Fields {
				n.State.Fields[i].Value = w.expr(n.State.Fields[i].Value)
			}
		}
	case *ir.Literal, *ir.Ident, *ir.ContextRead:
		// Terminal — no nested exprs. (Ident.Sym is rewritten by callers'
		// transform; this walker only handles structural recursion.)
	default:
		panic(fmt.Sprintf("exprWalker.expr: unhandled %T", n))
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
	case *ir.Toggle:
		n.Target = w.expr(n.Target)
	case *ir.ErrorBoundary:
		w.stmts(n.Children)
	case *ir.Window:
		n.Href = w.expr(n.Href)
		n.Title = w.expr(n.Title)
		n.Favicon = w.expr(n.Favicon)
		w.stmts(n.Body)
	case *ir.ContextProvider:
		n.Value = w.expr(n.Value)
		w.stmts(n.Children)
	default:
		panic(fmt.Sprintf("exprWalker.stmt: unhandled %T", n))
	}
}
