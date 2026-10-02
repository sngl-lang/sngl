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
//   - Strict: inlines platform-stdlib wrappers
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
	enabled: func(c Features) bool { return true }, // always on (strict path gated internally)
	apply:   lowerInlinePure,
}

func lowerInlinePure(pkg *ir.Package, caps Features, opts Options) error {
	if pkg == nil {
		return nil
	}
	st := &inlinePureState{
		pkg:      pkg,
		instSeq:  seqOrOwn(opts.instSeq),
		platform: opts.Platform,
		inFlight: map[*ir.Component]bool{},
		stack:    nil,
	}
	for _, comp := range pkg.Components {
		st.hoist = &comp.Vars
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
	// The package's own body, and then the windows: both hoist into the
	// package, a window being a rendering root that owns nothing.
	st.hoist = &pkg.Vars
	body, err := st.inlineStmts(pkg.Body)
	if err != nil {
		return err
	}
	pkg.Body = body
	return nil
}

type inlinePureState struct {
	pkg *ir.Package
	// platform is the target being lowered for, empty for a platform-agnostic
	// caller (LSP, format), which leaves every library component abstract.
	platform string
	inFlight map[*ir.Component]bool
	stack    []*ir.Component // active inline chain, for cycle-error messages
	// hoist is where a substituted callee's vars land: the owner whose body is
	// being walked. A var hoisted onto a component that is itself a runtime
	// instance becomes a local of that instance's factory, which is what makes
	// one per instance.
	hoist *[]*ir.Var
	// instSeq names each substitution's copy of the callee's state. Shared with
	// passNoInlineComponents through Options: the two substitute into one
	// emitted namespace and a counter each made them collide.
	instSeq *int
	// loopDepth counts the `for`s the walk is inside. A call site under one
	// holds many copies of the body and a substitution makes one, so a callee
	// with state of its own may not be substituted there -- it stays a runtime
	// instance and the platform gives each row its own record.
	loopDepth int
}

func (st *inlinePureState) freshSuffix() string { return freshInstSuffix(st.instSeq) }

// viewReadVars is every var of comp's that its rendered tree reads.
//
// A `var` is what makes a component impure to substitute, and the reason is
// about the *rendered* tree: a var a node prop, a view condition or an
// interpolation reads needs an updater when it changes and a setter when a
// reconcile pushes it, and a substituted body has neither. A var only a
// handler writes and only another handler reads needs neither -- html's
// `timer` override holds the setInterval handle in one, and nothing renders it.
//
// Conservative in the direction rendersNothing is: an expression not
// classified here counts as a read, so an unfamiliar body costs a substitution
// rather than a wrong one. A component with funcs is not asked at all, since
// isPure disqualifies on those already.
func viewReadVars(comp *ir.Component) map[*ir.Var]bool {
	out := map[*ir.Var]bool{}
	if comp == nil {
		return out
	}
	own := make(map[*ir.Var]bool, len(comp.Vars))
	for _, v := range comp.Vars {
		own[v] = true
	}
	read := func(root any) {
		_ = ir.WalkExprs(root, func(e ir.Expr) error {
			if id, ok := e.(*ir.Ident); ok {
				if v, ok := id.Sym.(*ir.Var); ok && own[v] {
					out[v] = true
				}
			}
			return nil
		})
	}
	var visit func(stmts []ir.Stmt)
	visit = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				// The props and bindings are the rendered reads. Handlers are
				// deliberately not walked: that is the whole distinction.
				for _, a := range n.Props {
					read(a.Value)
				}
				for _, b := range n.Bindings {
					read(b.Target)
				}
				visit(n.Children)
				for _, name := range ir.SlotNames(n.Slots) {
					if sc := n.Slots[name]; sc != nil {
						visit(sc.Body)
					}
				}
			case *ir.If:
				read(n.Cond)
				visit(n.Body)
				visit(n.Else)
			case *ir.For:
				read(n.Iter)
				visit(n.Body)
				visit(n.Else)
			case *ir.SlotInst:
				visit(n.Children)
				for _, name := range ir.SlotNames(n.Slots) {
					visit(n.Slots[name].Body)
				}
			case *ir.ErrorBoundary:
				visit(n.Children)
			case *ir.ContextProvider:
				visit(n.Children)
			default:
				// Anything else in a view body is not classified, so every
				// var it mentions counts -- handlers included, since this
				// walk cannot tell which part of it is rendered.
				read(s)
			}
		}
	}
	visit(comp.Body)
	return out
}

// isPure reports whether a component carries no state of its own, which is
// what makes substituting its body for a call to it sound. nil → false.
//
// An empty body is pure: it declares nothing, so nothing about substituting it
// can go wrong. Whether there is anything worth substituting is a separate
// question, and the caller's — for a primitive nothing is expected, for a
// library component it means the target implemented nothing, and for a user
// component it means the node renders nothing.
//
// "No state" is not the same as "no var". A var nothing in the rendered tree
// reads (viewReadVars) is hoisted onto the caller and renamed per call site,
// exactly as passNoInlineComponents does it -- what a substitution cannot
// supply is an updater and a setter, and such a var needs neither. That is what
// lets html declare `timer` as an `effect` holding the setInterval handle.
func (st *inlinePureState) isPure(c *ir.Component) bool {
	if c == nil {
		return false
	}
	if len(c.Funcs) > 0 {
		return false
	}
	if len(c.Vars) == 0 {
		return true
	}
	// A var is allowed only for this build's platform override, and only
	// where the position holds one copy of the body.
	//
	// The override is the case that needs it: every platform-package
	// component must inline or the build fails, so an override that holds
	// state has nowhere else to go -- html's `timer` keeps the setInterval
	// handle in one. A *user* component with unrendered state is left alone
	// deliberately: it becomes a runtime instance today, its platform gives
	// each one a record, and turning that into a substitution would change
	// how every component with a private counter compiles for the sake of a
	// stdlib override.
	//
	// Under a `for` neither is substituted, because the position holds a copy
	// of the body per element and a substitution makes one. Two gauges with a
	// private hit counter shared it, which is what the fyne instance-canvas
	// fixture says.
	if !st.overriddenHere(c) || st.loopDepth > 0 {
		return false
	}
	return len(viewReadVars(c)) == 0
}

// constSubstitutable reports whether c says it is const and nothing about the
// position keeps a substitution from being sound. A const component's render
// reads only its props -- the checker holds it to that -- so it inlines the
// way a stateless one does, and isPure stays the opportunistic path for a
// component that says nothing.
//
// Two things a substitution still cannot supply. A func is a method on the
// instance, and nothing hoists one. And a var only handlers touch is hoisted
// onto the caller once per call site, which under a `for` is one cell for
// every copy -- so there the component stays a runtime instance, which gives
// each copy its own.
func (st *inlinePureState) constSubstitutable(c *ir.Component) bool {
	if c == nil || !c.Const || len(c.Funcs) > 0 {
		return false
	}
	return len(c.Vars) == 0 || st.loopDepth == 0
}

// overriddenHere reports whether c carries a platform extension body for the
// platform being lowered for. The same question inline_components.go asks as
// specializedHere; with no platform nothing is specialized.
func (st *inlinePureState) overriddenHere(c *ir.Component) bool {
	if st.platform == "" || c == nil || c.PlatformOverrides == nil {
		return false
	}
	_, ok := c.PlatformOverrides[st.platform]
	return ok
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
		st.loopDepth++
		body, err := st.inlineStmts(n.Body)
		if err != nil {
			st.loopDepth--
			return nil, err
		}
		els, err := st.inlineStmts(n.Else)
		st.loopDepth--
		if err != nil {
			return nil, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, nil
	case *ir.SlotInst:
		ch, err := st.inlineStmts(n.Children)
		if err != nil {
			return nil, err
		}
		n.Children = ch
		for _, name := range ir.SlotNames(n.Slots) {
			body, err := st.inlineStmts(n.Slots[name].Body)
			if err != nil {
				return nil, err
			}
			n.Slots[name].Body = body
		}
		return []ir.Stmt{n}, nil
	case *ir.ErrorBoundary:
		ch, err := st.inlineStmts(n.Children)
		if err != nil {
			return nil, err
		}
		n.Children = ch
		return []ir.Stmt{n}, nil
	case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider,
		*ir.Break, *ir.Continue:
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
	// A named slot's content is a body like any other, and this walk used to
	// reach every body but that one. Missed, the content arrived at
	// passReactivity still spelled as the wrapper the caller wrote -- a
	// platform override, which has a real body -- so a reactive `if` around the
	// insertion classified it as an instance to reconcile and demanded a setter
	// no platform primitive has. Sorted, for the reason ir.Walk gives over the
	// same map: a pass that numbers what it finds must find it in one order
	// twice.
	for _, name := range ir.SlotNames(n.Slots) {
		sc := n.Slots[name]
		if sc == nil {
			continue
		}
		body, err := st.inlineStmts(sc.Body)
		if err != nil {
			return nil, err
		}
		sc.Body = body
	}
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

	// A primitive is what every wrapper lowers *to* and has no body by design:
	// #[intrinsic] for a platform widget, #[builtin] for a node kind the
	// checker dispatches, a wildcard for a raw element, a slot hosting a tree.
	if isPrimitiveComponent(comp) {
		return []ir.Stmt{n}, nil
	}

	pure := st.isPure(comp) || st.constSubstitutable(comp)
	strictApplies := isPlatformStdlibComponent(st.pkg, comp)
	if strictApplies && !pure {
		return nil, fmt.Errorf("platform stdlib wrapper %q must be pure (declares %s) at %s", comp.Name, impurityReason(comp), compPos(comp))
	}

	// Renders nothing *and* holds nothing: a component with state or a function
	// is not empty even with no visual body, and dropping it takes that state
	// with it. canInline asks the same questions (inline_components.go), and
	// asking only about Body here is how a timer-only component vanished from
	// every platform with no diagnostic -- a timer is a node in the body now,
	// so Body is what answers for one.
	if len(comp.Body) == 0 && len(comp.Vars) == 0 && len(comp.Funcs) == 0 {
		// A user component declaring nothing at all renders nothing, so the
		// node goes rather than reaching a codegen that has to guess what an
		// empty component means — each platform guessed differently, and two
		// grew a local workaround for it. A library component with no body is
		// one the target implemented nothing for; that is a diagnostic waiting
		// on the last platform to state its implementations declaratively, so
		// for now it is left alone. Only when lowering for a target: with none,
		// every library component is still abstract.
		switch {
		case strictApplies:
			// A platform package's own component reaches here only unmarked:
			// isPrimitiveComponent returned above for every marked one. So it
			// is a primitive missing its mark, and nothing downstream can tell
			// that from a wrapper that implements nothing.
			return nil, fmt.Errorf("platform stdlib wrapper %q has no body to inline; mark it #[intrinsic] if it is a platform primitive (at %s)", comp.Name, compPos(comp))
		case st.platform != "" && !comp.Stdlib:
			return nil, nil
		}
		return []ir.Stmt{n}, nil
	}

	// No body means nothing to substitute, whatever the state says. The branch
	// above answered the component that declares nothing at all; this is the
	// one that holds a var and renders nothing, which passNoInlineComponents
	// keeps so the state survives. Reached only since a var the rendered tree
	// does not read stopped making a component impure.
	if len(comp.Body) == 0 {
		return []ir.Stmt{n}, nil
	}

	if !pure && !strictApplies {
		return []ir.Stmt{n}, nil
	}

	// Recursive pure components (self-call directly or transitively) can't
	// be inlined to a finite body. In strict mode that's fatal; in
	// optimization mode the user's recursion is legitimate, leave as-is.
	if st.inFlight[comp] || ir.ComponentSelfRefs(comp) {
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
	for _, v := range comp.Vars {
		if viewReadVars(comp)[v] {
			parts = append(parts, fmt.Sprintf("var %q, which the rendered tree reads", v.Name))
			break
		}
	}
	if len(comp.Funcs) > 0 {
		parts = append(parts, fmt.Sprintf("func %q", comp.Funcs[0].Name))
	}
	if len(parts) == 0 {
		// Unreachable: the caller asks only when isPure said no, and isPure
		// says no only for one of the two above.
		return "state"
	}
	return strings.Join(parts, ", ")
}

// isPrimitiveComponent reports whether a component is something a codegen
// renders directly rather than a wrapper to be composed away. Each marker says
// so in its own vocabulary, and none of them implies a body.
func isPrimitiveComponent(comp *ir.Component) bool {
	if comp == nil {
		return false
	}
	// A tree kind is deliberately not on this list. Belonging to a segmented
	// tree says which family a declaration joins, not that a codegen renders
	// it: a shape composed out of other shapes is a wrapper like any other,
	// and the canvas emitter draws whatever reaches it, composed away or not.
	//
	// Hosting a family used to be on this list, and only passCanvas wanted it
	// there: that pass looked for the node the shapes hang off, so a canvas
	// whose override had been composed away was an `html.canvas` with shape
	// children and no draw function. Nothing lifts them out now, so hosting a
	// family is the ordinary thing it reads as -- `richText` hosts the inline
	// family the way `vbox` hosts widgets -- and a platform may implement such
	// a component in its own package like any other.
	return comp.Intrinsic != "" || comp.Wildcard != "" || comp.Builtin != ""
}

// isPlatformStdlibComponent reports whether comp came from one of the
// package's sngl:platform/… imports.
func isPlatformStdlibComponent(pkg *ir.Package, comp *ir.Component) bool {
	// An intrinsic is the primitive the wrappers lower *to* — a raw element,
	// a declared native widget — not a wrapper over one. It has no body to
	// inline, so the strict check would read it as an impure wrapper and fail
	// the build. A wildcard component is one by construction.
	if comp != nil && (comp.Intrinsic != "" || comp.Wildcard != "") {
		return false
	}
	for _, imp := range pkg.Imports {
		if !strings.HasPrefix(imp.Path, "sngl:platform/") {
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
	// call-site arg to the prop's default to the type's declared default, so
	// the body never keeps a bare param identifier.
	//
	// DeclaredDefault, not ZeroExpr: shapeBody in pass_canvas.go answers the
	// same question the same way, and the two have to agree because a shape
	// reaches whichever of them composes it away. A struct's declared default
	// is its fields' own, which is how a body reading `style.fontSize` off a
	// prop the call site left out gets 16 rather than nothing.
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
			val = ir.DeclaredDefault(p.Type)
		}
		if val != nil {
			bindings[p.Name] = val
		}
	}

	// Deep-clone the wrapper body so substitution mutations don't leak
	// across call sites.
	provided := providedContextVars(comp, callsite.Props)
	body := substituteVars(deepCloneStmts(comp.Body), provided)

	// Hoist the callee's own vars onto the owner being walked, one copy per
	// call site. isPure let them through because nothing rendered reads them,
	// so they need no updater and no setter -- but they still need somewhere
	// to live that outlasts the handler that writes them.
	if len(comp.Vars) > len(provided) {
		if st.hoist == nil {
			return nil, fmt.Errorf("component %q declares state and there is no owner to hoist it onto at %s", comp.Name, compPos(comp))
		}
		suffix := st.freshSuffix()
		renames := map[ir.Symbol]string{}
		symRenames := map[ir.Symbol]ir.Symbol{}
		start := len(*st.hoist)
		for _, v := range comp.Vars {
			if _, ok := provided[v]; ok {
				continue
			}
			clone := cloneVarShallow(v)
			clone.Name = v.Name + suffix
			clone.Init = deepCloneExpr(v.Init)
			renames[v] = clone.Name
			symRenames[v] = clone
			*st.hoist = append(*st.hoist, clone)
		}
		for i := start; i < len(*st.hoist); i++ {
			if (*st.hoist)[i].Init == nil {
				continue
			}
			// The props too, not just the renames: an init may name a prop
			// (`var handle = interval`), and after substitution the param it
			// named does not exist.
			(*st.hoist)[i].Init = substituteVarsExpr((*st.hoist)[i].Init, provided)
			(*st.hoist)[i].Init = substituteParamsExpr((*st.hoist)[i].Init, bindings)
			(*st.hoist)[i].Init = renameInExpr((*st.hoist)[i].Init, renames, symRenames)
		}
		body = renameIdents(body, renames, symRenames)
	}

	// Apply param substitution (Ident-with-Param-Sym matching by name).
	body = substituteParams(body, bindings)
	body = dropConstantWrites(body)

	// Apply event-invocation substitution: replace any *ir.Emit whose
	// Name matches a user-provided event handler with the handler body.
	// Before the slots are spliced, since an emit in the content the call
	// site supplied is the caller's own and names none of these events.
	body = substituteEvents(body, callsite.Handlers)

	// Apply slot substitution: replace each *ir.SlotInst with what the call
	// site supplied for it -- its ordinary children for the anonymous slot,
	// the matching `slot name { ... }` block for a named one.
	body = substituteSlots(body, callsite)

	// ID preservation: transfer callsite.ID to the first top-level
	// NodeInst of the substituted body.
	//
	// The handle travels with it. It is the id's other half -- the binding
	// every read of the id resolves to -- and leaving it behind severed the
	// two for every node whose component inlines, which is every stdlib
	// component on every platform with an override. uniqueNodeIDs then had a
	// nil Handle to key on, so its refusal of a read that cannot say which
	// copy it meant never fired for one, and a rename had nothing to repoint
	// the reads through.
	if callsite.ID != "" {
		ir.AttachNodeID(body, callsite.ID, callsite.Handle)
	}
	// Where the program wrote it travels to the same node, for a platform's
	// diagnostic about the primitive it became.
	ir.AttachNodeSite(body, callsite)

	// Event-handler transfer (platform-independent rule): any pure wrapper
	// that declares its events purely as metadata — rather than emitting
	// them via an explicit @name() — gets its call-site handlers carried
	// onto the first inlined node. For example the bubbletea
	// `Styled(events=[Event{...}])` primitive describes activation as data;
	// an html wrapper might do the same. Those wrappers never match in
	// substituteEvents, so the user's call-site handlers (@click, @change,
	// ...) would otherwise be lost. Carry any call-site handler not already
	// consumed by an emit onto the first top-level primitive NodeInst of the
	// body so platform codegen can find it. Handlers already wired through an
	// explicit @name() emit in the body have been substituted in place above
	// and are skipped here to avoid double-emission.
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

// emittedHandlerNames returns the set of event names the component body emits.
// Handlers with these names are wired through substituteEvents and must not
// also be transferred onto the root node.
func emittedHandlerNames(stmts []ir.Stmt) map[string]struct{} {
	out := map[string]struct{}{}
	var visit func(stmts []ir.Stmt)
	visit = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.Emit:
				out[n.Name] = struct{}{}
			case *ir.NodeInst:
				visit(n.Children)
				for _, h := range n.Handlers {
					if h.Func != nil {
						visit(h.Func.Block)
					}
				}
				for _, f := range propLambdas(n) {
					visit(f.Block)
				}
			case *ir.If:
				visit(n.Body)
				visit(n.Else)
			case *ir.For:
				visit(n.Body)
				visit(n.Else)
			case *ir.SlotInst:
				visit(n.Children)
			case *ir.ErrorBoundary:
				visit(n.Children)
				visit(n.Failed)
				// A window's `boundary(@error(e) { error(e) })` emits from
				// the boundary's own handler.
				if n.Handler != nil && n.Handler.Func != nil {
					visit(n.Handler.Func.Block)
				}
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

// substituteSlots replaces every *ir.SlotInst with what the call site supplied.
// The inliner binds a scoped slot's arguments by parameter name, since the body
// it splices has been deep-cloned away from the *ir.Param the populator wrote.
func substituteSlots(stmts []ir.Stmt, callsite *ir.NodeInst) []ir.Stmt {
	return substituteSlotsCloning(stmts, callsite, deepCloneStmts)
}

func substituteSlotsCloning(stmts []ir.Stmt, callsite *ir.NodeInst, clone func([]ir.Stmt) []ir.Stmt) []ir.Stmt {
	sp := ir.SlotSplicer{
		Clone: clone,
		Bind: func(body []ir.Stmt, sc *ir.SlotContent, si *ir.SlotInst) []ir.Stmt {
			bindings := make(map[string]ir.Expr, len(sc.Params))
			for i, p := range sc.Params {
				if i < len(si.Args) {
					bindings[p.Name] = si.Args[i]
				}
			}
			return substituteParams(body, bindings)
		},
	}
	return sp.Substitute(stmts, callsite)
}

// substituteEvents replaces every *ir.Emit whose Name matches a
// user-provided event handler with the handler's body.
func substituteEvents(stmts []ir.Stmt, handlers []ir.EventHandler) []ir.Stmt {
	return substituteEventsIn(stmts, handlers, nil)
}

// substituteEventsIn carries the event parameter of the handler whose body is
// being walked. A user handler that declared a parameter the wrapper passes no
// argument for is naming that same event, so its references are bound to the
// enclosing parameter rather than left pointing at a parameter that is about
// to be inlined away.
func substituteEventsIn(stmts []ir.Stmt, handlers []ir.EventHandler, enclosing *ir.Func) []ir.Stmt {
	return substituteEventsUnder(stmts, handlers, enclosing, nil)
}

// substituteEventsUnder is substituteEventsIn carrying the override's own
// event handler whose body is being walked, so a substitution made beneath it
// can record which program-written event it serves. That pairing exists only
// here: after this pass the override's `click()` emit is gone, replaced by the
// program's block, and the handler is left named for the host widget's event.
func substituteEventsUnder(stmts []ir.Stmt, handlers []ir.EventHandler, enclosing *ir.Func, under *ir.EventHandler) []ir.Stmt {
	byName := map[string]*ir.EventHandler{}
	for i := range handlers {
		h := &handlers[i]
		byName[h.Name] = h
	}
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		if emit, isEmit := s.(*ir.Emit); isEmit {
			h, ok := byName[emit.Name]
			// A binding's write-back is emitted ahead of the event it rides on,
			// and is not an event a test can name.
			if under != nil && under.ComponentEvent == "" && !(ok && h != nil && h.Func != nil && h.Func.Synthesized) {
				under.ComponentEvent = emit.Name
			}
			if ok && h != nil && h.Func != nil {
				out = append(out, bindEventParams(deepCloneStmts(h.Func.Block), h.Func.Params, emit.Args, enclosing)...)
				continue
			}
			// No matching handler — the caller never subscribed to this
			// event, so the emit goes nowhere. Drop it rather than leaving a
			// dangling `emit(...)` for codegen to choke on.
			continue
		}
		switch n := s.(type) {
		case *ir.If:
			n.Body = substituteEventsUnder(n.Body, handlers, enclosing, under)
			n.Else = substituteEventsUnder(n.Else, handlers, enclosing, under)
		case *ir.For:
			n.Body = substituteEventsUnder(n.Body, handlers, enclosing, under)
			n.Else = substituteEventsUnder(n.Else, handlers, enclosing, under)
		case *ir.NodeInst:
			n.Children = substituteEventsUnder(n.Children, handlers, enclosing, under)
			substituteEventsInPopulations(n.Slots, handlers, enclosing, under)
			for i := range n.Handlers {
				h := &n.Handlers[i]
				if h.Func == nil {
					continue
				}
				// This handler is the override's subscription to its host
				// widget's event; anything substituted inside it is the
				// program's, so it is what `under` names.
				h.Func.Block = substituteEventsUnder(h.Func.Block, handlers, h.Func, h)
			}
		case *ir.SlotInst:
			n.Children = substituteEventsUnder(n.Children, handlers, enclosing, under)
			substituteEventsInPopulations(n.Slots, handlers, enclosing, under)
		case *ir.ErrorBoundary:
			n.Children = substituteEventsUnder(n.Children, handlers, enclosing, under)
			n.Failed = substituteEventsUnder(n.Failed, handlers, enclosing, under)
			// The boundary's own handler is the override's code as much as its
			// children are: a window's `boundary(@error(e) { error(e) })`
			// forwards to the window's @error, and the caller's handler for it
			// is what runs -- or nothing, where the caller wrote none.
			if h := n.Handler; h != nil && h.Func != nil {
				h.Func.Block = substituteEventsUnder(h.Func.Block, handlers, h.Func, under)
			}
		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Toggle, *ir.ContextProvider,
			*ir.Break, *ir.Continue:
			// No child statement list of their own; the lambda walk below is
			// what reaches an emit written inside one of their expressions.
		case *ir.Emit:
			// Emit already handled at top of loop; reaching here means
			// no matching handler — pass-through.
		default:
			panic(fmt.Sprintf("substituteEvents: unhandled %T", n))
		}
		// A lambda is a body too, and every statement can carry one in its
		// expressions -- `h = setInterval(func() { tick() }, d)` is an Assign,
		// which the switch above treats as a leaf. A lambda is also the
		// enclosing scope of its own block: a parameter a user handler names
		// but the emit passes no argument for is the one this lambda receives.
		for _, f := range lambdaBodiesIn(s) {
			f.Block = substituteEventsUnder(f.Block, handlers, f, under)
		}
		out = append(out, s)
	}
	return out
}

// substituteEventsInPopulations reaches the populations a body writes for a
// node's or an insertion's slots. They are that body's own code, and the
// events run before the slots are spliced, so no later walk meets them.
func substituteEventsInPopulations(slots map[string]*ir.SlotContent, handlers []ir.EventHandler, enclosing *ir.Func, under *ir.EventHandler) {
	for _, sc := range slots {
		if sc != nil {
			sc.Body = substituteEventsUnder(sc.Body, handlers, enclosing, under)
		}
	}
}

// bindEventParams rebinds references to a user event handler's declared
// params inside `stmts` to the arg expressions the wrapper passed. A param
// the wrapper passed no arg for (the common `@input { @input() }` shape in
// platform .sngl wrappers) keeps referring to itself, and the backend maps
// it to its own event variable by declaration.
func bindEventParams(stmts []ir.Stmt, params []*ir.Param, args []ir.CallArg, enclosing *ir.Func) []ir.Stmt {
	if len(params) == 0 {
		return stmts
	}
	bindings := map[string]ir.Expr{}
	// A wrapper that has the value but no event carries it as the argument:
	// a platform callback reporting a change it is passed nothing for
	// (Compose's RadioButton onClick, whose value is the option the override
	// looped to) can only name the value. The payloads this applies to hold
	// exactly that one field, so a read of it is the argument itself.
	payloadValues := map[string]ir.Expr{}
	var held []heldField
	for i, p := range params {
		if p == nil || p.Name == "" {
			continue
		}
		if i < len(args) {
			bindings[p.Name] = args[i].Value
			if f, ok := soleFieldGiven(p.Type, args[i].Value); ok {
				payloadValues[p.Name+"."+f] = args[i].Value
			}
			// A payload the wrapper builds in place is read field by field,
			// so no target has to declare the struct to take one apart. It is
			// evaluated where it was emitted, before the handler runs: a field
			// computed from state is held in a temp, or `{checked = !checked}`
			// read after the handler wrote `checked` reads the new state
			// negated.
			if lit, ok := args[i].Value.(*ir.StructLit); ok {
				for _, f := range lit.Fields {
					if f.Spread || f.Name == "" || f.Value == nil {
						continue
					}
					v := f.Value
					if !payloadFieldStable(v) {
						t := v.ExprType()
						sym := &ir.Var{Name: "__" + p.Name + "_" + f.Name, Type: t, Synthesized: true}
						held = append(held, heldField{decl: &ir.LocalVar{Name: sym.Name, Type: t, Init: v, Sym: sym}})
						v = &ir.Ident{Name: sym.Name, Type: t, Sym: sym}
						held[len(held)-1].ref = sym
					}
					payloadValues[p.Name+"."+f.Name] = v
				}
			}
			continue
		}
		// No matching arg — the wrapper invoked @event() with fewer args
		// than the user handler declared. The parameter names the event the
		// enclosing handler receives, so it becomes that handler's parameter:
		// the wrapper is the one the platform installs, and a reference has
		// to name something the surviving handler declares.
		// By position, as the handler bound it: the enclosing handler's
		// parameter at the same index, or this one appended when it has
		// none there yet.
		target := p
		if enclosing != nil {
			if i < len(enclosing.Params) {
				target = enclosing.Params[i]
			} else if i == len(enclosing.Params) {
				enclosing.Params = append(enclosing.Params, p)
			}
		}
		bindings[p.Name] = &ir.Ident{Name: target.Name, Type: target.Type, Sym: target}
	}
	if len(bindings) == 0 {
		return stmts
	}
	read := map[*ir.Var]bool{}
	walker := newExprWalker(func(e ir.Expr) ir.Expr {
		if sel, ok := e.(*ir.Select); ok && len(payloadValues) > 0 {
			if id, ok := sel.Operand.(*ir.Ident); ok {
				if _, isParam := id.Sym.(*ir.Param); isParam {
					if v, ok := payloadValues[id.Name+"."+sel.Field]; ok {
						if ref, ok := v.(*ir.Ident); ok {
							if sym, ok := ref.Sym.(*ir.Var); ok {
								read[sym] = true
							}
						}
						return deepCloneExpr(v)
					}
				}
			}
		}
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
	out := walker.stmts(stmts)
	var pre []ir.Stmt
	for _, h := range held {
		if read[h.ref] {
			pre = append(pre, h.decl)
		}
	}
	return append(pre, out...)
}

// heldField is a payload field evaluated once into a temp, declared only if
// the handler reads it.
type heldField struct {
	decl *ir.LocalVar
	ref  *ir.Var
}

// payloadFieldStable reports whether a payload field reads the same wherever
// the handler reads it: a literal, a const, or a name no statement assigns --
// a parameter, a loop variable, or the temp injectBind already held it in. A
// source payload cannot name a synthesized var, so the only one to reach here
// is that temp.
func payloadFieldStable(e ir.Expr) bool {
	switch v := e.(type) {
	case *ir.Literal:
		return true
	case *ir.Ident:
		sym, ok := v.Sym.(*ir.Var)
		return !ok || sym.IsConst || sym.Synthesized
	}
	return false
}

// soleFieldGiven reports the one field of the event payload p declares, when
// arg is that field's value rather than the payload. A payload of one field
// and a value of that field's type are the same information, and a wrapper
// with no event to pass has only the second — so the handler's read of the
// field resolves to it. Anything else (a payload passed as itself, a payload
// of more than one field) is left to the ordinary parameter binding.
func soleFieldGiven(payload *ir.Type, arg ir.Expr) (string, bool) {
	if payload == nil || payload.Kind != ir.TypeStruct || arg == nil {
		return "", false
	}
	sd, ok := payload.Decl.(*ir.StructDef)
	if !ok || len(sd.Fields) != 1 {
		return "", false
	}
	at := arg.ExprType()
	if at == nil || at.Kind == ir.TypeStruct {
		return "", false
	}
	return sd.Fields[0].Name, true
}

// propLambdas are the lambdas a node's arguments carry. A prop declared with a
// func type takes one, and its body is code written in the component the call
// site belongs to — so a walk over that component's statements has to reach it
// or the body is invisible to every rewrite the walk performs.
func propLambdas(n *ir.NodeInst) []*ir.Func {
	var out []*ir.Func
	for _, p := range n.Props {
		if lam, ok := p.Value.(*ir.Lambda); ok && lam.Func != nil {
			out = append(out, lam.Func)
		}
	}
	return out
}

// lambdaBodiesIn reports the lambdas one statement holds in its own
// expressions -- a prop's value, a call's argument, an assignment's right-hand
// side -- and stops at the first nested statement, so it never enters a body
// the caller's own descent is about to walk.
//
// Outermost only, for the same reason: a lambda written inside another is
// reached by recursing through the outer one's block, and visiting it here as
// well would substitute into it twice.
func lambdaBodiesIn(s ir.Stmt) []*ir.Func {
	var out []*ir.Func
	root := true
	_ = ir.Walk(s, func(n ir.Node) error {
		if root {
			root = false
			return nil
		}
		if _, isStmt := n.(ir.Stmt); isStmt {
			return ir.SkipDir
		}
		if lam, ok := n.(*ir.Lambda); ok {
			if lam.Func != nil {
				out = append(out, lam.Func)
			}
			return ir.SkipDir
		}
		return nil
	})
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
		clone.Slots = deepCloneSlots(n.Slots)
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
	case *ir.SlotInst:
		clone := *n
		clone.Args = make([]ir.Expr, len(n.Args))
		for i, a := range n.Args {
			clone.Args[i] = deepCloneExpr(a)
		}
		clone.Children = deepCloneStmts(n.Children)
		clone.Slots = deepCloneSlots(n.Slots)
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
	case *ir.ContextProvider:
		clone := *n
		clone.Value = deepCloneExpr(n.Value)
		clone.Children = deepCloneStmts(n.Children)
		return &clone
	case *ir.Break:
		clone := *n
		return &clone
	case *ir.Continue:
		clone := *n
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

// exprWalker applies a transform to every expression reachable from a
// statement tree. Used for parameter and context substitution.
//
// It is ir.Rewrite with one rule of its own: an expression the transform
// replaces is not descended into. What is substituted in is already-checked
// code from another scope -- a bound argument, a hidden state read -- and
// walking it would apply the same substitution to names that were never the
// callee's to bind.
//
// It used to be a second copy of the traversal, and drifted from the first in
// both directions: it reached NodeInst.Slots, which the context pass's copy
// did not, and it stopped at ErrorBoundary.Handler, which nothing did.
type exprWalker struct {
	transform func(ir.Expr) ir.Expr
}

func newExprWalker(transform func(ir.Expr) ir.Expr) *exprWalker {
	return &exprWalker{transform: transform}
}

func (w *exprWalker) visit(n ir.Node) (ir.Node, error) {
	e, isExpr := n.(ir.Expr)
	if !isExpr {
		return n, nil
	}
	if out := w.transform(e); out != e {
		return out, ir.SkipDir
	}
	return n, nil
}

// expr transforms e and its subexpressions, returning the replacement. A bare
// expression is its own root, and ir.Rewrite cannot write a replacement back
// into a root it was handed by value -- so the root is transformed here and
// the walk only descends.
func (w *exprWalker) expr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	if out := w.transform(e); out != e {
		return out
	}
	root := true
	_ = ir.Rewrite(e, func(n ir.Node) (ir.Node, error) {
		if root {
			root = false // already transformed above; do not run it twice
			return n, nil
		}
		return w.visit(n)
	})
	return e
}

func (w *exprWalker) stmts(stmts []ir.Stmt) []ir.Stmt {
	_ = ir.Rewrite(stmts, w.visit)
	return stmts
}

// deepCloneSlots copies each population. Shallow-copying the map would alias
// each SlotContent across call sites, so the first instance's renames would
// land on all of them.
func deepCloneSlots(slots map[string]*ir.SlotContent) map[string]*ir.SlotContent {
	if slots == nil {
		return nil
	}
	out := make(map[string]*ir.SlotContent, len(slots))
	for name, sc := range slots {
		out[name] = &ir.SlotContent{
			Params: slices.Clone(sc.Params),
			Body:   deepCloneStmts(sc.Body),
		}
	}
	return out
}

// dropConstantWrites removes a write whose target the substitution made a
// constant: a two-way prop nothing holds, which a call site's value can only
// have started. Every such prop is a cell by now (passImplicitState) but one --
// the document a surface target writes its page from, which cannot leave the
// screen and keeps no `visible` where nothing reads it -- so this is where the
// write its host would report goes nowhere, rather than `true = __visible`.
func dropConstantWrites(stmts []ir.Stmt) []ir.Stmt {
	constant := func(e ir.Expr) bool { _, ok := e.(*ir.Literal); return ok }
	out := stmts[:0:0]
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Assign:
			if constant(n.Target) {
				continue
			}
		case *ir.Toggle:
			if constant(n.Target) {
				continue
			}
		case *ir.If:
			n.Body, n.Else = dropConstantWrites(n.Body), dropConstantWrites(n.Else)
		case *ir.For:
			n.Body, n.Else = dropConstantWrites(n.Body), dropConstantWrites(n.Else)
		case *ir.NodeInst:
			n.Children = dropConstantWrites(n.Children)
			for i := range n.Handlers {
				if f := n.Handlers[i].Func; f != nil {
					f.Block = dropConstantWrites(f.Block)
				}
			}
		case *ir.ErrorBoundary:
			n.Children = dropConstantWrites(n.Children)
		}
		out = append(out, s)
	}
	return out
}
