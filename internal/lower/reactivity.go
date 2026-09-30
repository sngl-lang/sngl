package lower

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passReactivity = pass{
	name:    "NoReactivity",
	enabled: func(c Features) bool { return !c.Reactivity },
	apply:   lowerReactivity,
}

// reactiveProp is one (node, prop) pair affected by a reactive var.
type reactiveProp struct {
	NodeID string
	Key    string
	KeyPos ast.Pos
	Expr   ir.Expr
	// Comp is the declaration the node targets, kept so updaterStmts can ask
	// whether the instance carries a setter for Key. Nil for a widget.
	Comp *ir.Component
	// Node is the instantiation itself, kept for the prop that cannot be
	// written: rebuilding the instance means building it from every prop the
	// site declared, not only the one that changed.
	Node *ir.NodeInst
	// Instance says the node is a component instance rather than a widget, so
	// the prop is written through the instance's setter instead of assigned to
	// the node. Assigning it was the old behaviour and it reached nothing: on
	// html it emitted `__n1.setAttribute("label", ...)` against a node that is
	// not the instance, and the page did not change.
	Instance bool
}

// reactivityState carries the analysis built up before mutation injection.
type reactivityState struct {
	pkg          *ir.Package
	reactiveVars map[*ir.Var]bool
	reverseDeps  map[*ir.Var][]reactiveProp
	reverseSlots map[*ir.Var][]reactiveSlot
	// undriven are the slots no var re-fires: a loop over a const whose body
	// holds a component built at run time. Rendered once, but a slot all the
	// same, and reverseSlots has no key to list one under.
	undriven map[string]bool
	// caps is the target's shape, which the slot lowering needs: whether a
	// component instance is a record decides what the slot attaches and
	// retains.
	caps        Features
	intrinsics  map[string]*ir.Func // CreateNode, AppendChild, RemoveChild, AttachHandler
	idCounter   int
	slotCounter int
	// instCounter names the state each component instantiation inside a slot
	// keeps, so two occurrences in one package never share a list.
	instCounter int
	// placement is the slot render currently being built, when the target can
	// place a child. Held on the state because the body's nodes place
	// themselves as they are emitted, and the func is assembled around them.
	placement *slotPlacement
	// platform is the target's identifier, for a diagnostic that has to name
	// it: whether an instance can be rebuilt where it stands is the platform's
	// answer, so the refusal says whose.
	platform string
	// slotDeclSt is the shared declarative state used to lower every
	// reactive-slot body. Sharing keeps the __nN counter monotonic
	// across slots so two slot Funcs in the same package don't
	// collide on `m.__n0` widget fields.
	slotDeclSt *declarativeState
	// slot synthesis owner: the *ir.Component or *ir.Window whose stmt body
	// we're currently walking, so synthesized slot Vars/Funcs get attached
	// to the right scope.
	owner reactivityOwner
	// held records the instance registries each component owns, in the order
	// they were opened, so its teardown can destroy what it holds. Keyed by
	// component because a window's registries live as long as the page does.
	held map[*ir.Component][]*slotInstance
	// err holds the first fatal lowering diagnostic (e.g. an unsupported
	// nested reactive structure). Recorded rather than panicked so the
	// build fails with a positioned compile error; the partially-built IR
	// is discarded once lowerReactivity returns this.
	err error
}

// failf records the first fatal reactivity-lowering diagnostic, prefixed
// with the source position when available. Subsequent calls are ignored so
// the earliest, most-relevant error wins. Callers keep walking after this
// (building otherwise-harmless IR); lowerReactivity returns st.err and the
// malformed IR is never used.
func (st *reactivityState) failf(pos ast.Pos, format string, args ...any) {
	if st.err != nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	if pos.IsValid() {
		msg = pos.String() + ": " + msg
	}
	st.err = fmt.Errorf("%s", msg)
}

// reactiveSlot records a per-If/per-For reactive dep. SlotID names the
// synthetic __slot<N>; GenFunc is the synthesized __renderSlot<N> Func
// (populated in a later task).
type reactiveSlot struct {
	SlotID    string
	GenFunc   *ir.Func
	ParentRef ir.Expr // ident the source-position CallStmt uses; same ref re-used for splice updaters
}

// cloneIdent shallow-copies an *ir.Ident so two splices do not alias the
// same pointer. Non-Ident expressions pass through unchanged (parent refs
// are always Idents today).
func cloneIdent(e ir.Expr) ir.Expr {
	id, ok := e.(*ir.Ident)
	if !ok {
		return e
	}
	cp := *id
	return &cp
}

// reactivityOwner is the closest enclosing scope that owns synthesized
// Vars/Funcs. Either a *ir.Component or *ir.Window.
type reactivityOwner interface {
	addVar(v *ir.Var)
	addFunc(f *ir.Func)
}

type compOwner struct{ c *ir.Component }

func (o compOwner) addVar(v *ir.Var)   { o.c.Vars = append(o.c.Vars, v) }
func (o compOwner) addFunc(f *ir.Func) { o.c.Funcs = append(o.c.Funcs, f) }

// A window owns nothing: what a pass synthesizes while walking one belongs to
// the window's container, which for a root-only construct is the package.
type windowOwner struct {
	w   *ir.Window
	pkg *ir.Package
	// root is the window's own ir.WindowRootName.
	root string
}

func (o windowOwner) addVar(v *ir.Var)   { o.pkg.Vars = append(o.pkg.Vars, v) }
func (o windowOwner) addFunc(f *ir.Func) { o.pkg.Funcs = append(o.pkg.Funcs, f) }

func (st *reactivityState) freshNodeID() string {
	id := "__n" + strconv.Itoa(st.idCounter)
	st.idCounter++
	return id
}

// lowerReactivity runs two passes over the package: first collects reverse
// deps and assigns synthetic IDs, then walks every Stmt slice splicing
// updater Assigns after every mutation that touches a tracked Var.
func lowerReactivity(pkg *ir.Package, caps Features, opts Options) error {
	if pkg == nil {
		return nil
	}
	st := &reactivityState{
		pkg:          pkg,
		caps:         caps,
		platform:     opts.Platform,
		reactiveVars: collectReactiveVars(pkg),
		reverseDeps:  make(map[*ir.Var][]reactiveProp),
		reverseSlots: make(map[*ir.Var][]reactiveSlot),
		undriven:     map[string]bool{},
		intrinsics:   make(map[string]*ir.Func),
		held:         make(map[*ir.Component][]*slotInstance),
	}
	for _, op := range ir.NodeOps {
		st.intrinsics[op] = nodeOpFunc(op)
	}
	if !caps.InlineSlots {
		for _, comp := range pkg.Components {
			comp.Body = st.slotFlows(comp.Body)
		}
		for _, w := range ir.AllWindows(pkg) {
			w.Children = st.slotFlows(w.Children)
		}
	}
	// Pass 1: collect reverse deps per owner scope.
	for _, comp := range pkg.Components {
		st.owner = compOwner{comp}
		st.collectFromStmts(comp.Body)
	}
	for _, w := range ir.AllWindows(pkg) {
		st.owner = windowOwner{w: w, pkg: pkg, root: ir.WindowRootName(pkg, w)}
		st.collectFromStmts(w.Children)
	}
	// Pass 2: rewrite + inject. Delegates to existing injectIntoStmts;
	// future tasks add slot synthesis here.

	// One injection per func, whoever reaches it first. A func mounted on
	// several windows is one body, and the updaters a walk adds are the whole
	// package's rather than that owner's -- so a second pass over it appends a
	// second copy of the same patches.
	injected := map[*ir.Func]bool{}
	for _, v := range pkg.Vars {
		for _, h := range v.Handlers {
			if h.Func != nil {
				h.Func.Block = st.injectIntoStmts(h.Func.Block)
			}
		}
	}
	// After the injection walks, so every slot's GenFunc is wired and the
	// props are the ones the build path settled on.
	defer func() {
		for _, comp := range pkg.Components {
			st.owner = compOwner{comp}
			st.synthesizeRemoteSettle()
		}
		for _, w := range ir.AllWindows(pkg) {
			st.owner = windowOwner{w: w, pkg: pkg, root: ir.WindowRootName(pkg, w)}
			st.synthesizeRemoteSettle()
		}
	}()

	for _, comp := range pkg.Components {
		st.owner = compOwner{comp}
		comp.Body = st.rewriteAndInject(comp.Body)
		for _, f := range comp.Funcs {
			if injected[f] {
				continue
			}
			injected[f] = true
			f.Block = st.rewriteAndInject(f.Block)
		}
		for _, v := range comp.Vars {
			for _, h := range v.Handlers {
				if h.Func != nil {
					h.Func.Block = st.rewriteAndInject(h.Func.Block)
				}
			}
		}
	}
	for _, w := range ir.AllWindows(pkg) {
		st.owner = windowOwner{w: w, pkg: pkg, root: ir.WindowRootName(pkg, w)}
		w.Children = st.rewriteAndInject(w.Children)
		for _, h := range ir.WindowHandlers(w) {
			if h.Func != nil {
				h.Func.Block = st.rewriteAndInject(h.Func.Block)
			}
		}
	}
	// Package-level funcs last, because the walks above are what create most
	// of them. A reactive `if` or `for` in a window body is lifted into a
	// synthesized `__renderSlotN` whose Block is the *uninjected* statements,
	// and that func is added to its owner while the body is being walked -- so
	// injecting into pkg.Funcs before the walks reaches a list the slot
	// renders are not in yet, and every updater inside one is dropped.
	//
	// `picked = it` in a `@click` inside a `for` lost its `__n0.text =
	// picked` on fyne, gtk4 and html, and lost it silently: the handler still
	// compiles and still writes the var, and nothing redraws.
	//
	// It used to be reached by the loop over a window's own Funcs, which ran
	// after that window's body and so saw what the body had just added. A
	// window owns nothing now, so the same funcs are the package's and the
	// package's loop is where they have to be met.
	//
	// rewriteAndInject rather than injectIntoStmts, which is what the window
	// loop did for the same reason: a lifted block may itself hold a reactive
	// structure, and only the first of the two rewrites one. A genuinely
	// top-level func is unaffected -- an `if` in an imperative body carries no
	// LoweredSlotID, so there is nothing for the rewrite half to match.
	//
	// An owned func is skipped: the checker registers a component-body func in
	// pkg.Funcs *as well*, and its owner's loop above has already injected.
	owned := ownedFuncs(pkg)
	// A package func belongs to no one window, so it is not handed the last
	// window's root to synthesize: the first window's `__root` is what it has
	// always been given.
	if o, ok := st.owner.(windowOwner); ok {
		o.root = ""
		st.owner = o
	}
	for _, f := range pkg.Funcs {
		if owned[f] || injected[f] {
			continue
		}
		injected[f] = true
		f.Block = st.rewriteAndInject(f.Block)
	}
	// After the walks, because a registry only exists once the slot render
	// that opened it has been built.
	st.destroyHeld()
	return st.err
}

// ownedFuncs is every func a component or window owns, by pointer. The same
// *ir.Func may be owned twice over -- the checker registers a component-body
// func in pkg.Funcs as well -- and is one entry either way.
func ownedFuncs(pkg *ir.Package) map[*ir.Func]bool {
	out := map[*ir.Func]bool{}
	for _, o := range ir.Owners(pkg) {
		if o.Comp == nil && o.Win == nil {
			continue
		}
		for _, f := range o.Funcs {
			out[f] = true
		}
	}
	return out
}

// rewriteAndInject is the unified pass-2 walk. Synthesizes slot Vars for
// every reactive If/For collected in pass-1, then delegates to
// injectIntoStmts for prop-updater injection. Future tasks add slot
// generator Funcs and structural rewrites.
func (st *reactivityState) rewriteAndInject(stmts []ir.Stmt) []ir.Stmt {
	// If any reactive slots exist on this owner, synthesize a __root Var
	// of type dyn. Platforms bind this to their root-container reference
	// at codegen time.
	if len(st.reverseSlots) > 0 || len(st.undriven) > 0 {
		st.synthesizeRootVar()
	}
	// Collect unique slot IDs from reverseSlots so each Func is built once.
	uniqueSlots := maps.Clone(st.undriven)
	for _, slots := range st.reverseSlots {
		for _, slot := range slots {
			uniqueSlots[slot.SlotID] = true
		}
	}
	// Build one slot var + Func per unique ID. Sort for deterministic
	// codegen output: slot vars enter comp.Vars in this order and
	// propagate downstream (e.g. JS state object field order).
	slotIDs := make([]string, 0, len(uniqueSlots))
	for id := range uniqueSlots {
		slotIDs = append(slotIDs, id)
	}
	slices.Sort(slotIDs)
	built := map[string]*ir.Func{}
	for _, slotID := range slotIDs {
		st.synthesizeSlotVar(slotID)
		fn := st.buildRenderSlotFor(slotID, stmts)
		if fn != nil {
			st.owner.addFunc(fn)
			built[slotID] = fn
		}
	}
	// Wire GenFunc on every reactiveSlot record so updatersFor can reach the
	// Func through the reverseSlots map. rewriteAndInject runs once per block
	// (component body, each func body, each timer handler); a slot's GenFunc is
	// built only by the block that actually contains its If/For, so only
	// overwrite when this block built it. Otherwise a later block without the
	// If/For (e.g. a timer @tick handler that merely mutates the slot's dep)
	// would clobber GenFunc back to nil, and updatersFor would skip splicing
	// the __renderSlotN re-fire there — leaving timer-driven reactive slots
	// (the carousel) un-updated.
	for v, slots := range st.reverseSlots {
		for i := range slots {
			if fn := built[slots[i].SlotID]; fn != nil {
				slots[i].GenFunc = fn
			}
		}
		st.reverseSlots[v] = slots
	}
	// Replace reactive If/For at source position with renderSlot CallStmt.
	stmts = st.rewriteReactiveStructures(stmts, nil)
	return st.injectIntoStmts(stmts)
}

// rewriteReactiveStructures replaces every *ir.If/*ir.For carrying a
// LoweredSlotID with a CallStmt to its slot generator. The parent
// reference is the synthetic ident for the enclosing NodeInst — passed
// in via the recursion stack.
func (st *reactivityState) rewriteReactiveStructures(stmts []ir.Stmt, parentRef ir.Expr) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.If:
			if n.LoweredSlotID != "" {
				ref := parentRef
				if ref == nil {
					rootVar := st.findSlotVar(st.rootName())
					ref = &ir.Ident{Name: st.rootName(), Type: ir.TypDyn, IsElementRef: true, Synthesized: true, Sym: rootVar}
				}
				st.recordSlotParent(n.LoweredSlotID, ref)
				out = append(out, st.slotCall(n.LoweredSlotID, ref))
				continue
			}
			n.Body = st.rewriteReactiveStructures(n.Body, parentRef)
			n.Else = st.rewriteReactiveStructures(n.Else, parentRef)
		case *ir.For:
			if n.LoweredSlotID != "" {
				ref := parentRef
				if ref == nil {
					rootVar := st.findSlotVar(st.rootName())
					ref = &ir.Ident{Name: st.rootName(), Type: ir.TypDyn, IsElementRef: true, Synthesized: true, Sym: rootVar}
				}
				st.recordSlotParent(n.LoweredSlotID, ref)
				out = append(out, st.slotCall(n.LoweredSlotID, ref))
				continue
			}
			n.Body = st.rewriteReactiveStructures(n.Body, parentRef)
			n.Else = st.rewriteReactiveStructures(n.Else, parentRef)
		case *ir.NodeInst:
			// A window is driven as its own owner by lowerReactivity, like a
			// top-level one: its slot vars and render funcs belong to it, so
			// reaching it from the body it was written in would build them
			// against the wrong owner.
			if ir.IsWindowNode(n) {
				continue
			}
			// If any direct child is a reactive If/For we need a stable
			// element ref for it to thread through to the slot updater.
			kids := ir.WidgetChildren(n)
			if n.ID == "" && childrenContainReactiveSlot(kids) {
				n.ID = st.freshNodeID()
			}
			pref := &ir.Ident{Name: n.ID, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}
			if kids != nil {
				n.Children = st.rewriteReactiveStructures(kids, pref)
			}
			for _, h := range n.Handlers {
				if h.Func != nil {
					h.Func.Block = st.rewriteReactiveStructures(h.Func.Block, parentRef)
				}
			}
		case *ir.SlotInst:
			n.Children = st.rewriteReactiveStructures(n.Children, parentRef)
		case *ir.ErrorBoundary:
			n.Children = st.rewriteReactiveStructures(n.Children, parentRef)
		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider,
			*ir.Break, *ir.Continue:
			// Leaf/non-structural stmts — no nested reactive If/For to rewrite.
			// (ContextProvider is gone by reactivity time if NoContext cap is
			// set; if not, codegen never sees a reactive lowering — kept here
			// for completeness.)
		default:
			panic(fmt.Sprintf("rewriteReactiveStructures: unhandled %T", n))
		}
		out = append(out, s)
	}
	return out
}

// childrenContainReactiveSlot reports whether any direct child of a
// NodeInst is a reactive If/For (i.e. carries a LoweredSlotID). Used to
// force an enclosing element ref so slot updaters have a stable parent.
// A boundary's children are the node's children, as rewriteReactiveStructures
// passes the node's ref through one.
func childrenContainReactiveSlot(stmts []ir.Stmt) bool {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.If:
			if n.LoweredSlotID != "" {
				return true
			}
		case *ir.For:
			if n.LoweredSlotID != "" {
				return true
			}
		case *ir.ErrorBoundary:
			if childrenContainReactiveSlot(n.Children) {
				return true
			}
		}
	}
	return false
}

// recordSlotParent stamps the parent ref onto every reactiveSlot record
// with this SlotID. The same ref is reused by updatersFor when splicing
// re-renders so structurally-reactive children re-attach to the correct
// parent on mutation.
func (st *reactivityState) recordSlotParent(slotID string, parentRef ir.Expr) {
	for v, slots := range st.reverseSlots {
		for i := range slots {
			if slots[i].SlotID == slotID {
				slots[i].ParentRef = cloneIdent(parentRef)
			}
		}
		st.reverseSlots[v] = slots
	}
}

// slotCall emits CallStmt __renderSlotN(parentRef).
func (st *reactivityState) slotCall(slotID string, parentRef ir.Expr) *ir.CallStmt {
	if parentRef == nil {
		// Top-level reactive If/For: use the "__root" sentinel. Platforms
		// translate this to their root container reference.
		rootVar := st.findSlotVar(st.rootName())
		parentRef = &ir.Ident{Name: st.rootName(), Type: ir.TypDyn, IsElementRef: true, Synthesized: true, Sym: rootVar}
	}
	// Find the synthesized Func on the current owner.
	want := renderFuncName(slotID)
	var fn *ir.Func
	switch o := st.owner.(type) {
	case compOwner:
		for _, f := range o.c.Funcs {
			if f.Name == want {
				fn = f
				break
			}
		}
	case windowOwner:
		for _, f := range o.pkg.Funcs {
			if f.Name == want {
				fn = f
				break
			}
		}
	}
	return &ir.CallStmt{Call: &ir.Call{
		Type: ir.TypVoid,
		Func: fn,
		Args: []ir.CallArg{{Value: parentRef}},
	}}
}

// synthesizeSlotVar creates the per-slot `__slotN list<dyn>` Var and
// attaches it to the current owner. Idempotent — returns the existing
// Var if one was already created.
func (st *reactivityState) synthesizeSlotVar(slotID string) *ir.Var {
	if existing := st.findSlotVar(slotID); existing != nil {
		return existing
	}
	v := &ir.Var{
		Name:        slotID,
		Type:        ir.ListOf(ir.TypDyn),
		Init:        &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
		Synthesized: true,
	}
	st.owner.addVar(v)
	return v
}

// slotIdent references the per-slot `__slotN` Var, creating it on first use.
func (st *reactivityState) slotIdent(slotID string) *ir.Ident {
	return &ir.Ident{
		Name:        slotID,
		Type:        ir.ListOf(ir.TypDyn),
		Sym:         st.synthesizeSlotVar(slotID),
		Synthesized: true,
	}
}

// synthesizeRootVar creates the `__root dyn` Var on the current owner
// if it doesn't exist yet. Idempotent. Marked Synthesized so codegen
// can detect it.
func (st *reactivityState) synthesizeRootVar() {
	if existing := st.findSlotVar(st.rootName()); existing != nil {
		return
	}
	v := &ir.Var{
		Name:        st.rootName(),
		Type:        ir.TypDyn,
		Synthesized: true,
	}
	st.owner.addVar(v)
}

// rootName is the var the owner's top-level render slots are parented to: a
// component's own `__root`, or the window's ir.WindowRootName.
func (st *reactivityState) rootName() string {
	if o, ok := st.owner.(windowOwner); ok && o.root != "" {
		return o.root
	}
	return "__root"
}

func (st *reactivityState) findSlotVar(name string) *ir.Var {
	switch o := st.owner.(type) {
	case compOwner:
		for _, v := range o.c.Vars {
			if v.Name == name {
				return v
			}
		}
	case windowOwner:
		for _, v := range o.pkg.Vars {
			if v.Name == name {
				return v
			}
		}
	}
	return nil
}

func collectReactiveVars(pkg *ir.Package) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool)
	add := func(vars []*ir.Var) {
		for _, v := range vars {
			if v != nil && !v.IsConst {
				out[v] = true
			}
		}
	}
	add(pkg.Vars)
	for _, comp := range pkg.Components {
		add(comp.Vars)
	}
	return out
}

func (st *reactivityState) collectFromStmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		st.collectFromStmt(s)
	}
}

func (st *reactivityState) collectFromStmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.NodeInst:
		// Not walked from here: lowerReactivity walks every window with itself
		// as the owner, wherever it was written.
		if ir.IsWindowNode(n) {
			return
		}
		st.collectFromNode(n)
	case *ir.If:
		st.collectFromIf(n)
		if n.LoweredSlotID == "" {
			st.collectFromStmts(n.Body)
			st.collectFromStmts(n.Else)
		} else {
			// Reactive slot: the body is re-rendered wholesale on every
			// re-fire, so its bound props must not register standalone
			// prop-updaters (those would reference per-slot element ids that
			// only exist inside the slot func, leaking into outside handlers).
			// Instead make the slot re-fire for every reactive var the body
			// reads.
			st.registerSlotBodyDeps(n.Body, n.LoweredSlotID)
			st.registerSlotBodyDeps(n.Else, n.LoweredSlotID)
		}
	case *ir.For:
		st.collectFromFor(n)
		if n.LoweredSlotID == "" {
			st.collectFromStmts(n.Body)
			st.collectFromStmts(n.Else)
		} else {
			st.registerSlotBodyDeps(n.Body, n.LoweredSlotID)
			st.registerSlotBodyDeps(n.Else, n.LoweredSlotID)
		}
	case *ir.SlotInst:
		st.collectFromStmts(n.Children)
	case *ir.ErrorBoundary:
		st.collectFromStmts(n.Children)
	case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider,
		*ir.Break, *ir.Continue:
		// Non-visual stmts — no reactive props/slots to collect from.
		// Imperative-handler dataflow is tracked at injection time.
	default:
		panic(fmt.Sprintf("collectFromStmt: unhandled %T", n))
	}
}

func (st *reactivityState) collectFromNode(n *ir.NodeInst) {
	// The same question passDeclarative asks to choose CreateComponent over
	// CreateNode, asked here because the answer decides how the prop is
	// written. By this pass a node still targeting a component with a body is
	// one the inliner could not flatten -- a recursive cycle, or an
	// instantiation under a dynamic `for`.
	instance := n.Component != nil && hasRealComponentBody(n.Component)
	for _, prop := range n.Props {
		deps := st.exprDeps(prop.Value)
		if len(deps) == 0 {
			continue
		}
		if n.ID == "" {
			n.ID = st.freshNodeID()
		}
		for v := range deps {
			st.reverseDeps[v] = append(st.reverseDeps[v], reactiveProp{
				NodeID:   n.ID,
				Key:      prop.Name,
				KeyPos:   prop.NamePos,
				Expr:     prop.Value,
				Instance: instance,
				Comp:     n.Component,
				Node:     n,
			})
		}
	}
	st.collectFromStmts(ir.WidgetChildren(n))
}

func (st *reactivityState) collectFromIf(n *ir.If) {
	deps := st.exprDeps(n.Cond)
	if len(deps) == 0 {
		return
	}
	slot := reactiveSlot{SlotID: st.freshSlotID()}
	for v := range deps {
		st.reverseSlots[v] = append(st.reverseSlots[v], slot)
	}
	n.LoweredSlotID = slot.SlotID
}

func (st *reactivityState) collectFromFor(n *ir.For) {
	deps := st.exprDeps(n.Iter)
	// A loop over a const is a slot too when its body needs one, the way a loop
	// over state is. Left standing, each copy's bound prop got an updater of
	// its own under the one id the lowering gave the body, and a condition in it
	// became a render func reading a loop variable only the host loop binds.
	if len(deps) == 0 && !st.bodyNeedsSlot(n.Body) && !st.bodyNeedsSlot(n.Else) {
		return
	}
	slot := reactiveSlot{SlotID: st.freshSlotID()}
	for v := range deps {
		st.reverseSlots[v] = append(st.reverseSlots[v], slot)
	}
	if len(deps) == 0 {
		st.undriven[slot.SlotID] = true
	}
	n.LoweredSlotID = slot.SlotID
}

// registerSlotBodyDeps makes the slot named slotID re-fire whenever any
// reactive var read by a bound prop / nested cond / nested iter in stmts
// mutates. Used for the body of a reactive If/For: the body re-renders
// wholesale, so individual props don't get standalone updaters — instead the
// whole slot re-fires for any var the body depends on. Deduped per var.
func (st *reactivityState) registerSlotBodyDeps(stmts []ir.Stmt, slotID string) {
	slotBodyExprs(stmts, func(e ir.Expr) {
		for v := range st.exprDeps(e) {
			st.addSlotDep(v, slotID)
		}
	})
}

// bodyNeedsSlot reports whether a loop body renders anything a slot has to
// reconcile: an expression reading a reactive var -- what registerSlotBodyDeps
// would subscribe the slot to -- or a component built at run time, whose copies
// only a slot keeps a list of. Outside one, each copy was assigned to the one
// field the node's id named and only the last reached the widget tree.
func (st *reactivityState) bodyNeedsSlot(stmts []ir.Stmt) bool {
	found := false
	slotBodyExprs(stmts, func(e ir.Expr) {
		found = found || len(st.exprDeps(e)) > 0
	})
	_ = ir.WalkStmts(stmts, func(s ir.Stmt) error {
		n, ok := s.(*ir.NodeInst)
		switch {
		case found:
			return ir.SkipAll
		case !ok:
			return nil
		case ir.IsWindowNode(n):
			return ir.SkipDir
		case n.Component != nil && n.Component.RuntimeInstance && hasRealComponentBody(n.Component):
			found = true
		}
		return nil
	})
	return found
}

// slotBodyExprs visits the expressions a slot's body renders from: bound
// props, nested conditions and nested iterables, and not handlers.
func slotBodyExprs(stmts []ir.Stmt, addDep func(ir.Expr)) {
	var walk func([]ir.Stmt)
	walk = func(ss []ir.Stmt) {
		for _, s := range ss {
			switch n := s.(type) {
			case *ir.NodeInst:
				// A window renders a document of its own, so a loop over pages is
				// not a slot for what a page reads.
				if ir.IsWindowNode(n) {
					continue
				}
				for _, p := range n.Props {
					addDep(p.Value)
				}
				walk(ir.WidgetChildren(n))
			case *ir.If:
				addDep(n.Cond)
				walk(n.Body)
				walk(n.Else)
			case *ir.For:
				addDep(n.Iter)
				walk(n.Body)
				walk(n.Else)
			case *ir.SlotInst:
				walk(n.Children)
			case *ir.ErrorBoundary:
				walk(n.Children)
			default:
				// Imperative/leaf stmts carry no rendered props.
			}
		}
	}
	walk(stmts)
}

// addSlotDep records that the slot named slotID must re-fire when v mutates,
// without duplicating an existing (v, slotID) entry.
func (st *reactivityState) addSlotDep(v *ir.Var, slotID string) {
	for _, s := range st.reverseSlots[v] {
		if s.SlotID == slotID {
			return
		}
	}
	st.reverseSlots[v] = append(st.reverseSlots[v], reactiveSlot{SlotID: slotID})
}

func (st *reactivityState) freshSlotID() string {
	id := ir.SlotVarPrefix + strconv.Itoa(st.slotCounter)
	st.slotCounter++
	return id
}

func (st *reactivityState) exprDeps(e ir.Expr) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool)
	st.gatherDeps(e, out)
	return out
}

// gatherFuncReads unions the reactive vars that a called derived/computed
// func transitively reads into out. fn.Reads gives the vars the body reads
// resolved against the func's own scope (so `this.field` is captured here even
// though a raw body walk would only see a Select); recursing through every
// func the body itself calls extends this across chains of derived funcs.
// visited guards recursive call graphs. nil fn (dynamic/native call) is a
// no-op.
func (st *reactivityState) gatherFuncReads(fn *ir.Func, out map[*ir.Var]bool, visited map[*ir.Func]bool) {
	gatherFuncReads(fn, st.reactiveVars, out, visited)
}

// gatherFuncReads is that without a reactivityState, so passEffect can ask it
// too: an `on` written as a call reads whatever the callee reads, and a bracket
// keyed on one settled on nothing while the pass looked only at the identifiers
// the expression itself spelled.
func gatherFuncReads(fn *ir.Func, reactive map[*ir.Var]bool, out map[*ir.Var]bool, visited map[*ir.Func]bool) {
	if fn == nil {
		return
	}
	if visited == nil {
		visited = make(map[*ir.Func]bool)
	}
	if visited[fn] {
		return
	}
	visited[fn] = true
	for _, v := range fn.Reads {
		if reactive[v] {
			out[v] = true
		}
	}
	for _, s := range fn.Block {
		eachCallInStmt(s, func(c *ir.Call) {
			gatherFuncReads(c.Func, reactive, out, visited)
		})
	}
}

// eachCallInStmt invokes fn for every *ir.Call reachable from s (and nested
// statements). Used to follow the call graph for transitive reactive-dep
// gathering; only the call nodes matter, so non-call exprs are descended
// without other side effects.
func eachCallInStmt(s ir.Stmt, fn func(*ir.Call)) {
	switch n := s.(type) {
	case *ir.Return:
		eachCallInExpr(n.Value, fn)
	case *ir.LocalVar:
		eachCallInExpr(n.Init, fn)
	case *ir.Assign:
		eachCallInExpr(n.Value, fn)
	case *ir.CallStmt:
		eachCallInExpr(n.Call, fn)
	case *ir.Emit:
		for _, a := range n.Args {
			eachCallInExpr(a.Value, fn)
		}
	case *ir.If:
		eachCallInExpr(n.Cond, fn)
		for _, c := range n.Body {
			eachCallInStmt(c, fn)
		}
		for _, c := range n.Else {
			eachCallInStmt(c, fn)
		}
	case *ir.For:
		eachCallInExpr(n.Iter, fn)
		for _, c := range n.Body {
			eachCallInStmt(c, fn)
		}
		for _, c := range n.Else {
			eachCallInStmt(c, fn)
		}
	}
}

// eachCallInExpr invokes fn for every *ir.Call reachable from e.
func eachCallInExpr(e ir.Expr, fn func(*ir.Call)) {
	switch x := e.(type) {
	case nil:
		return
	case *ir.Call:
		fn(x)
		eachCallInExpr(x.Receiver, fn)
		for _, a := range x.Args {
			eachCallInExpr(a.Value, fn)
		}
	case *ir.Binary:
		eachCallInExpr(x.Left, fn)
		eachCallInExpr(x.Right, fn)
	case *ir.Unary:
		eachCallInExpr(x.Operand, fn)
	case *ir.Ternary:
		eachCallInExpr(x.Cond, fn)
		eachCallInExpr(x.Then, fn)
		eachCallInExpr(x.Else, fn)
	case *ir.Conversion:
		eachCallInExpr(x.Operand, fn)
	case *ir.Select:
		eachCallInExpr(x.Operand, fn)
	case *ir.Index:
		eachCallInExpr(x.Operand, fn)
		eachCallInExpr(x.Idx, fn)
	case *ir.ListLit:
		for _, el := range x.Elems {
			eachCallInExpr(el, fn)
		}
	case *ir.MapLitIR:
		for _, en := range x.Entries {
			eachCallInExpr(en.Key, fn)
			eachCallInExpr(en.Value, fn)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			eachCallInExpr(f.Value, fn)
		}
	case *ir.Spread:
		eachCallInExpr(x.Operand, fn)
	}
}

func (st *reactivityState) gatherDeps(e ir.Expr, out map[*ir.Var]bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Ident:
		if v, ok := x.Sym.(*ir.Var); ok && st.reactiveVars[v] {
			out[v] = true
		}
	case *ir.Binary:
		st.gatherDeps(x.Left, out)
		st.gatherDeps(x.Right, out)
	case *ir.Unary:
		st.gatherDeps(x.Operand, out)
	case *ir.Ternary:
		st.gatherDeps(x.Cond, out)
		st.gatherDeps(x.Then, out)
		st.gatherDeps(x.Else, out)
	case *ir.Call:
		if x.Receiver != nil {
			st.gatherDeps(x.Receiver, out)
		}
		for _, a := range x.Args {
			st.gatherDeps(a.Value, out)
		}
		// A call to a derived/computed func transitively reads that func's
		// dep set: a reactive `if isLong` where `isLong() => name.length > 3`
		// depends on `name`, so the slot must re-fire when `name` mutates.
		// Func.Reads is the var set the body reads (resolved against the
		// owning scope, covering `this.field` access too); union the
		// reactive ones. Without this, the slot's only "dep" is the func
		// reference itself, which no mutation matches — so it never re-fires.
		st.gatherFuncReads(x.Func, out, nil)
	case *ir.Conversion:
		st.gatherDeps(x.Operand, out)
	case *ir.Select:
		st.gatherDeps(x.Operand, out)
	case *ir.Index:
		st.gatherDeps(x.Operand, out)
		st.gatherDeps(x.Idx, out)
	case *ir.ListLit:
		for _, el := range x.Elems {
			st.gatherDeps(el, out)
		}
	case *ir.MapLitIR:
		for _, en := range x.Entries {
			st.gatherDeps(en.Key, out)
			st.gatherDeps(en.Value, out)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			st.gatherDeps(f.Value, out)
		}
	case *ir.Spread:
		st.gatherDeps(x.Operand, out)
	case *ir.Literal, *ir.ContextRead:
		// Terminal — no reactive reads.
	case *ir.Lambda, *ir.Closure:
		// Lambda/Closure: existing reactivity model treats reactive deps
		// as the lexical reads in the enclosing prop expression. Reads
		// inside a lambda body fire when the lambda is invoked, not when
		// the surrounding view re-renders, so they are intentionally not
		// counted as deps of the construction site.
	default:
		panic(fmt.Sprintf("gatherDeps: unhandled %T", x))
	}
}

// handlerBodiesIn reports the callback bodies one statement holds in its own
// expressions -- a call's bare argument, a field of the props struct an
// argument holds, a node prop, the right-hand side of an assignment. It stops
// at the first nested statement, so it never enters a body injectIntoStmts is
// about to walk itself, and it takes the outermost callback only, so one
// written inside another is reached by recursing through it rather than
// injected into twice.
//
// This used to be two hand-written descents, reachable only from a CallStmt
// and a LocalVar. A mutation inside a callback held anywhere else got no
// updater, and the widget reading that state never changed -- html's timer,
// whose schedule is armed by `handle = setInterval(func() { ... }, d)`, is an
// assignment and was the case that found it.
func handlerBodiesIn(s ir.Stmt) []*ir.Func {
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
		switch x := n.(type) {
		case *ir.Lambda:
			if x.Func != nil {
				out = append(out, x.Func)
			}
			return ir.SkipDir
		case *ir.Closure:
			if x.Func != nil {
				out = append(out, x.Func)
			}
			return ir.SkipDir
		case *ir.Call:
			// A per-call @error is a body the call carries; the walk goes on
			// into the arguments, and stops at the handler's statements.
			if x.ErrorHandler != nil && x.ErrorHandler.Func != nil {
				out = append(out, x.ErrorHandler.Func)
			}
		}
		return nil
	})
	return out
}

// injectIntoStmts walks stmts, splicing updater Assigns after every Assign
// that mutates a tracked Var. Recurses into nested blocks.
func (st *reactivityState) injectIntoStmts(stmts []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		out = append(out, s)
		switch n := s.(type) {
		case *ir.If:
			n.Body = st.injectIntoStmts(n.Body)
			n.Else = st.injectIntoStmts(n.Else)
		case *ir.For:
			n.Body = st.injectIntoStmts(n.Body)
			n.Else = st.injectIntoStmts(n.Else)
		case *ir.NodeInst:
			// See rewriteReactiveStructures: driven as its own owner.
			if ir.IsWindowNode(n) {
				continue
			}
			if kids := ir.WidgetChildren(n); kids != nil {
				n.Children = st.injectIntoStmts(kids)
			}
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = st.injectIntoStmts(n.Handlers[i].Func.Block)
				}
			}
		case *ir.SlotInst:
			n.Children = st.injectIntoStmts(n.Children)
		case *ir.ErrorBoundary:
			n.Children = st.injectIntoStmts(n.Children)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = st.injectIntoStmts(n.Handler.Func.Block)
			}
		case *ir.CallStmt, *ir.LocalVar, *ir.Assign, *ir.Return, *ir.Emit, *ir.Toggle,
			*ir.ContextProvider, *ir.Break, *ir.Continue:
			// No child statement list of their own. Any callback they hold --
			// a statement-level call's inline handler, the props struct a
			// reconcile builds an instance from -- is reached by the walk
			// below, and updatersFor handles the mutation the statement is.
		default:
			panic(fmt.Sprintf("injectIntoStmts: unhandled %T", n))
		}
		// A callback is a body too, and every statement can carry one in its
		// expressions. Its mutations need the same updaters spliced after them
		// as any other body's, since the state they write is read by the same
		// widgets.
		for _, f := range handlerBodiesIn(s) {
			f.Block = st.injectIntoStmts(f.Block)
		}
		if updaters := st.updatersFor(s); len(updaters) > 0 {
			out = append(out, updaters...)
		}
	}
	return out
}

// updatersFor returns the list of updaters to splice after s. Empty for stmts
// that don't mutate a tracked Var. Three statement shapes mutate state: a
// plain assignment `x = ...` (*ir.Assign), a toggle `x!!` (*ir.Toggle), and a
// statement-level call to an in-place list method like `tasks.push(x)`
// (*ir.CallStmt) — see mutatingCallReceiver. Each must trigger the dependent
// prop and slot updaters.
func (st *reactivityState) updatersFor(s ir.Stmt) []ir.Stmt {
	var target ir.Expr
	switch n := s.(type) {
	case *ir.Assign:
		target = n.Target
	case *ir.Toggle:
		target = n.Target
	case *ir.CallStmt:
		target = mutatingCallReceiver(n.Call)
		if target == nil {
			return nil
		}
	default:
		return nil
	}
	v, fieldRewrite := st.assignTargetVar(target)
	if v == nil {
		return nil
	}
	return st.updaterStmts(st.reverseDeps[v], st.reverseSlots[v], fieldRewrite)
}

// updaterStmts is the body of an update: reassign every dependent prop from
// its own expression, then re-fire every dependent slot. What made the update
// necessary is the caller's business -- an assignment the program wrote, or a
// fetch that answered long after the render that started it.
func (st *reactivityState) updaterStmts(props []reactiveProp, slots []reactiveSlot, fieldRewrite map[ir.Symbol]ir.Expr) []ir.Stmt {
	if len(props) == 0 && len(slots) == 0 {
		return nil
	}
	// Which instances this update rebuilds rather than patches, decided before
	// anything is emitted: a prop update on an instance about to be destroyed
	// writes into a setter whose repaint nobody will see, and one of the two
	// answers has to win per node rather than per prop.
	rebuild, rebuilt := st.rebuildsFor(props)

	// fieldRewrite (non-nil only inside a lifted body) rewrites reads of
	// captured Syms to go through `*state.fieldName`. Loop-invariant: it is
	// the whole updater's context, not one prop's.
	rewrite := fieldRewrite
	if rewrite == nil {
		rewrite = map[ir.Symbol]ir.Expr{}
	}

	var out []ir.Stmt
	for _, p := range props {
		// Deep-copy the prop expression into the updater so the updater never
		// aliases the build-path prop's sub-nodes. This matters for a later
		// NoTernary pass: a shared *ir.Ternary would be lowered in place once
		// (decl + if hoisted into whichever site is walked first), leaving the
		// other site referencing an undeclared temp. rewriteIdentsToCaptures
		// reconstructs every interior node (Binary/Ternary/Call/...) while
		// passing leaf Idents/Literals through unchanged, which is safe since
		// NoTernary only ever replaces Ternary nodes, never leaves.
		//
		value := rewriteIdentsToCaptures(p.Expr, rewrite)
		nodeRef := func() ir.Expr {
			return &ir.Ident{Name: p.NodeID, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}
		}
		if p.Instance {
			// A prop the instance cannot absorb is not written through a
			// setter: rebuildsFor has already decided whether this position
			// rebuilds for it or the write is reported.
			if !componentAbsorbs(p.Comp, p.Key) {
				continue
			}
			// An instance this update replaces is handed nothing first. The
			// setter would repaint a node that is about to be removed, and the
			// fresh instance is built from every prop of the site anyway.
			if rebuilt[p.NodeID] {
				continue
			}
			out = append(out, &ir.CallStmt{Call: &ir.Call{
				Type:     ir.TypVoid,
				Receiver: lowerNSIdent(),
				Func:     st.intrinsics[ir.NodeOpUpdateComponent],
				Args: []ir.CallArg{
					{Value: nodeRef()},
					{Value: &ir.Literal{Type: ir.TypString, Value: p.Key}},
					{Value: value},
				},
			}})
			continue
		}
		out = append(out, &ir.Assign{
			Target: &ir.Select{
				Type:    ir.TypDyn,
				Operand: nodeRef(),
				Field:   p.Key,
			},
			Op:    ast.AssignSet,
			Value: value,
		})
	}
	// After every prop the live instances could absorb, so a rebuild that
	// reads a sibling node's state reads the state this update settled on.
	for _, n := range rebuild {
		out = append(out, st.recreateStatic(n, st.declState(), rewrite)...)
	}
	// Structural updaters: re-fire __renderSlotN for every reactive If/For
	// dependent on this Var.
	for _, slot := range slots {
		if slot.GenFunc == nil {
			continue
		}
		parentRef := slot.ParentRef
		if parentRef == nil {
			rootVar := st.findSlotVar(st.rootName())
			parentRef = &ir.Ident{Name: st.rootName(), Type: ir.TypDyn, IsElementRef: true, Synthesized: true, Sym: rootVar}
		}
		out = append(out, &ir.CallStmt{Call: &ir.Call{
			Type: ir.TypVoid,
			Func: slot.GenFunc,
			Args: []ir.CallArg{{Value: cloneIdent(parentRef)}},
		}})
	}
	return out
}

// rebuildsFor decides, for one update, which instances at a fixed position it
// rebuilds instead of patching, and reports every prop it can do neither for.
// The order is the order the props were collected in, so the emission is
// deterministic; the set is the same answer keyed by node, because two
// #[construct] props of one site are one rebuild.
//
// This is the question reuseOrCreate asks of a prop inside a reactive slot,
// and for the same reason: a prop with no setter has nothing for
// UpdateComponent to call, and the emitted call names a method no backend
// declared -- `m.__n1.SetStart(m.k)` against a record declaring only SetTail,
// `__n1.__set_start(...)` on an object exporting only __set_tail.
//
// #[construct] is how a declaration says the prop is read once while the
// instance is built, and the answer is the same at either position: destroy
// what the position holds and build a fresh one. What differs is that a slot
// re-renders and a fixed position does not, so the rebuild has to put the new
// root back among the old one's siblings itself -- InsertBefore, which is
// optional. A platform without it cannot express the operation at all, and the
// prop is refused there rather than compiled into a program that drops the
// write.
//
// A prop that is unwritable for any other reason is a routing nothing in the
// compiler answers, and is reported. Asked here rather than where the prop was
// collected, because a prop reading a var nothing ever writes needs no update
// and is not a problem -- testdata/test_components.sngl is that shape.
func (st *reactivityState) rebuildsFor(props []reactiveProp) ([]*ir.NodeInst, map[string]bool) {
	var rebuild []*ir.NodeInst
	rebuilt := map[string]bool{}
	for _, p := range props {
		if !p.Instance || componentAbsorbs(p.Comp, p.Key) || propIsConst(p.Comp, p.Key) {
			continue
		}
		if !propIsConstruct(p.Comp, p.Key) {
			st.failf(p.KeyPos, "prop %q of component %s can neither be written after construction nor rebuild the instance; mark it #[construct] if it is read only while the instance is built", p.Key, p.Comp.Name)
			continue
		}
		if !st.caps.InsertBefore {
			st.failf(p.KeyPos, "#[construct] prop %q of component %s is written from state, which rebuilds the instance where it stands -- and %s cannot put a child back at a position, so there is nowhere to put the new one; move the instantiation inside an `if` or a `for`, which rebuilds by re-rendering", p.Key, p.Comp.Name, st.platformName())
			continue
		}
		if p.Node == nil || rebuilt[p.NodeID] {
			continue
		}
		rebuilt[p.NodeID] = true
		rebuild = append(rebuild, p.Node)
	}
	return rebuild, rebuilt
}

// platformName is the target the refusal names, or a description of the
// capability when the pass was run without one (a unit test, the LSP).
func (st *reactivityState) platformName() string {
	if st.platform == "" {
		return "this platform"
	}
	return st.platform
}

// declState is the declarative state an updater borrows to build a
// CreateComponent call, shared with the reactive-slot lowering so the two
// cannot disagree about the shape of one.
func (st *reactivityState) declState() *declarativeState {
	if st.slotDeclSt == nil {
		st.slotDeclSt = newDeclarativeStateForSlot(st.pkg, st.caps)
	}
	return st.slotDeclSt
}

// mutatingCallReceiver returns the receiver expression of a statement-level
// call that mutates its receiver in place, or nil otherwise. A method like
// list.push returns nothing and is backed by an intrinsic that mutates the
// receiver (see ir.IntrinsicDef.MutatesReceiver), so `tasks.push(x)` -- the
// only form it has -- mutates `tasks` and must fire its reactive updaters,
// exactly as `tasks = ...` would. The mutation semantics
// come from the intrinsic metadata — keyed by the func's intrinsic ID, not by
// method name. For a type-method call the receiver value is Args[0].
func mutatingCallReceiver(c *ir.Call) ir.Expr {
	if c == nil || c.Func == nil || c.Func.Intrinsic == "" || len(c.Args) == 0 {
		return nil
	}
	if def, ok := ir.IntrinsicByName(c.Func.Intrinsic); ok && def.MutatesReceiver {
		return c.Args[0].Value
	}
	return nil
}

// assignTargetVar resolves an Assign target to the underlying reactive Var.
// Returns (nil, nil) when the target does not denote a tracked reactive Var.
//
// Two shapes are recognized:
//
//  1. Plain `Ident{Var}` — the original pre-NoLambda form.
//  2. `Unary{Deref, Select{Ident{stateParam}, fieldName}}` — a mutation
//     inside a lifted closure body whose state struct aliases the
//     captured Var as `fieldName`.
//
// For shape (2), the second return value is a rewrite map keyed by every
// captured Symbol of the lifted Func; values are the access expressions
// (`*state.fieldName`) that read the underlying value inside the lifted
// body. Callers use this map to rewrite injected updater expressions —
// reads of captured Vars must route through state.
func (st *reactivityState) assignTargetVar(target ir.Expr) (*ir.Var, map[ir.Symbol]ir.Expr) {
	return mutatedVar(st.pkg, st.reactiveVars, target)
}

// mutatedVar is assignTargetVar without a reactivityState, so a pass running
// before passReactivity can ask the same question of the same shapes. passEffect
// is the other caller: which cell a statement wrote is what says whether a
// bracket's key list may have moved, and answering it its own way is how
// `obj.f = x` and `xs[i] = x` ended no lifetimes.
func mutatedVar(pkg *ir.Package, reactive map[*ir.Var]bool, target ir.Expr) (*ir.Var, map[ir.Symbol]ir.Expr) {
	// A write to a struct field or collection element (`u.score += 10`,
	// `items[i] = x`) mutates the root reactive var; peel the Select/Index
	// chain to that root so the whole-var dep fires its updaters. Reactivity
	// tracks deps at whole-var granularity, so any field/element write must
	// re-render everything reading the var. The lifted-closure access form
	// `(*state.u)` is a Unary, so peeling stops there and the deref branch
	// below resolves it (covering `(*state.u).score` field writes too).
peel:
	for {
		switch n := target.(type) {
		case *ir.Select:
			target = n.Operand
		case *ir.Index:
			target = n.Operand
		default:
			break peel
		}
	}
	if id, ok := target.(*ir.Ident); ok {
		if v, ok := id.Sym.(*ir.Var); ok && reactive[v] {
			return v, nil
		}
		return nil, nil
	}
	deref, ok := target.(*ir.Unary)
	if !ok || deref.Op != ast.UnaryDeref {
		return nil, nil
	}
	sel, ok := deref.Operand.(*ir.Select)
	if !ok {
		return nil, nil
	}
	ident, ok := sel.Operand.(*ir.Ident)
	if !ok {
		return nil, nil
	}
	stateParam, ok := ident.Sym.(*ir.Param)
	if !ok {
		return nil, nil
	}
	if pkg == nil {
		return nil, nil
	}
	for liftedFunc, capMap := range pkg.LiftedCaptures {
		if len(liftedFunc.Params) == 0 || liftedFunc.Params[0] != stateParam {
			continue
		}
		var resolved *ir.Var
		for sym, name := range capMap {
			if name == sel.Field {
				if v, ok := sym.(*ir.Var); ok && reactive[v] {
					resolved = v
				}
				break
			}
		}
		if resolved == nil {
			return nil, nil
		}
		// Build a rewrite map from every captured Sym to the access expr
		// that reads it inside the lifted body. The injected updater's
		// value expression originally references the reactive Var
		// directly; rewrite those Idents to route through state.
		rewrite := make(map[ir.Symbol]ir.Expr, len(capMap))
		for sym, name := range capMap {
			rewrite[sym] = &ir.Unary{
				Op: ast.UnaryDeref,
				Operand: &ir.Select{
					Operand: &ir.Ident{
						Name: stateParam.Name,
						Sym:  stateParam,
						Type: stateParam.Type,
					},
					Field: name,
					Type:  ir.RefOf(sym.SymType()),
				},
				Type: sym.SymType(),
			}
		}
		return resolved, rewrite
	}
	return nil, nil
}

// rewriteIdentsToCaptures returns a copy of e where every Ident whose Sym
// appears in rewrite is replaced by the corresponding access expression.
// Other nodes are reconstructed structurally so the rewrite does not alias
// the original prop expression (the original is still reachable via the
// reactiveProp record and may be used by sibling sites).
func rewriteIdentsToCaptures(e ir.Expr, rewrite map[ir.Symbol]ir.Expr) ir.Expr {
	switch x := e.(type) {
	case nil:
		return nil
	case *ir.Ident:
		if x.Sym != nil {
			if access, ok := rewrite[x.Sym]; ok {
				return cloneExpr(access)
			}
		}
		return x
	case *ir.Binary:
		cp := *x
		cp.Left = rewriteIdentsToCaptures(x.Left, rewrite)
		cp.Right = rewriteIdentsToCaptures(x.Right, rewrite)
		return &cp
	case *ir.Unary:
		cp := *x
		cp.Operand = rewriteIdentsToCaptures(x.Operand, rewrite)
		return &cp
	case *ir.Ternary:
		cp := *x
		cp.Cond = rewriteIdentsToCaptures(x.Cond, rewrite)
		cp.Then = rewriteIdentsToCaptures(x.Then, rewrite)
		cp.Else = rewriteIdentsToCaptures(x.Else, rewrite)
		return &cp
	case *ir.Call:
		cp := *x
		cp.Receiver = rewriteIdentsToCaptures(x.Receiver, rewrite)
		cp.Args = make([]ir.CallArg, len(x.Args))
		for i, a := range x.Args {
			cp.Args[i] = a
			cp.Args[i].Value = rewriteIdentsToCaptures(a.Value, rewrite)
		}
		return &cp
	case *ir.Conversion:
		cp := *x
		cp.Operand = rewriteIdentsToCaptures(x.Operand, rewrite)
		return &cp
	case *ir.Select:
		cp := *x
		cp.Operand = rewriteIdentsToCaptures(x.Operand, rewrite)
		return &cp
	case *ir.Index:
		cp := *x
		cp.Operand = rewriteIdentsToCaptures(x.Operand, rewrite)
		cp.Idx = rewriteIdentsToCaptures(x.Idx, rewrite)
		return &cp
	case *ir.ListLit:
		cp := *x
		cp.Elems = make([]ir.Expr, len(x.Elems))
		for i, el := range x.Elems {
			cp.Elems[i] = rewriteIdentsToCaptures(el, rewrite)
		}
		return &cp
	case *ir.MapLitIR:
		cp := *x
		cp.Entries = make([]ir.MapEntry, len(x.Entries))
		for i, en := range x.Entries {
			cp.Entries[i] = ir.MapEntry{
				Key:   rewriteIdentsToCaptures(en.Key, rewrite),
				Value: rewriteIdentsToCaptures(en.Value, rewrite),
			}
		}
		return &cp
	case *ir.StructLit:
		cp := *x
		cp.Fields = make([]ir.FieldInit, len(x.Fields))
		for i, f := range x.Fields {
			cp.Fields[i] = f
			cp.Fields[i].Value = rewriteIdentsToCaptures(f.Value, rewrite)
		}
		return &cp
	case *ir.Spread:
		cp := *x
		cp.Operand = rewriteIdentsToCaptures(x.Operand, rewrite)
		return &cp
	case *ir.Literal, *ir.ContextRead:
		// Terminal — no Idents to rewrite.
		return e
	case *ir.Lambda, *ir.Closure:
		// Lambda/Closure bodies aren't traversed here: this rewriter is
		// called only on reactive prop expressions, which by construction
		// are not lambda-bodied. Pass-through.
		return e
	default:
		panic(fmt.Sprintf("rewriteIdentsToCaptures: unhandled %T", x))
	}
}

// renderFuncName: "__slot<N>" → "__renderSlot<N>".
func renderFuncName(slotID string) string {
	n := strings.TrimPrefix(slotID, ir.SlotVarPrefix)
	return "__renderSlot" + n
}

// synthesizeRenderSlotFunc generates the __renderSlotN(parent dyn) Func.
// origBody/origElse are the original If.Body/Else or For.Body/Else.
// Exactly one of (cond) or (iter) should be non-nil.
func (st *reactivityState) synthesizeRenderSlotFunc(slotID string, cond ir.Expr, iter ir.Expr, key, value string, origBody, origElse []ir.Stmt) *ir.Func {
	parentParam := &ir.Param{Name: "parent", Type: ir.TypDyn}
	fn := &ir.Func{
		Name:        renderFuncName(slotID),
		Params:      []*ir.Param{parentParam},
		Return:      ir.TypVoid,
		Synthesized: true,
		SlotRender:  true,
	}

	// 1. Teardown: for var __entry = __slotN { lower.RemoveChild(parent, __entry) }
	entryVar := "__entry"
	entrySym := &ir.LoopVar{Name: entryVar, Type: ir.TypDyn}
	teardown := &ir.For{
		Key:    entryVar,
		KeySym: entrySym,
		Iter:   st.slotIdent(slotID),
		Body: []ir.Stmt{
			&ir.CallStmt{Call: &ir.Call{
				Type:     ir.TypVoid,
				Receiver: lowerNSIdent(),
				Func:     st.intrinsics["RemoveChild"],
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: parentParam.Name, Type: ir.TypDyn, Sym: parentParam, IsElementRef: true}},
					{Value: &ir.Ident{Name: entryVar, Type: ir.TypDyn, Sym: entrySym, Synthesized: true}},
				},
			}},
		},
	}

	// 2. Reset: __slotN = []
	reset := &ir.Assign{
		Target: st.slotIdent(slotID),
		Op:     ast.AssignSet,
		Value:  &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
	}

	// 3. Re-evaluate and re-render. Reuse one declarative state across
	// every reactive slot in the package so `__nN` widget ids stay
	// monotonic and don't collide between sibling slot Funcs.
	st.declState()
	// Seed slot's counter past any IDs reactivity has assigned so far
	// (collectFromNode in pass-1 may have set NodeInst.IDs that the
	// initial seedCounter didn't see if they came from later passes).
	if st.idCounter > st.slotDeclSt.nextID {
		st.slotDeclSt.nextID = st.idCounter
	}
	// Created before the body, because each node it emits places itself.
	placement := st.newSlotPlacement(slotID, st.synthesizeSlotVar(slotID),
		&ir.Ident{Name: parentParam.Name, Type: ir.TypDyn, Sym: parentParam, IsElementRef: true})
	st.placement = placement
	body := st.renderSlotBody(st.slotDeclSt, parentParam, slotID, cond, iter, key, value, origBody, origElse)
	st.placement = nil
	// Propagate the slot's advanced counter back so subsequent
	// reactivity freshNodeID calls (line 253, line 446) don't reuse
	// __nN values the slot just claimed.
	if st.slotDeclSt.nextID > st.idCounter {
		st.idCounter = st.slotDeclSt.nextID
	}

	// A target that can place a child keeps what it already has and moves only
	// what moved; the teardown-and-rebuild above is what the rest still do.
	if sp := placement; sp != nil {
		fn.Block = append(sp.open(), body...)
		fn.Block = append(fn.Block, st.closeSlotPlacement(sp)...)
		return fn
	}
	fn.Block = append([]ir.Stmt{teardown, reset}, body...)
	return fn
}

// renderSlotBody emits the cond/iter-gated create+append sequence for the
// slot's children, with each created top-level NodeInst's ref pushed onto
// __slotN via ListPush.
func (st *reactivityState) renderSlotBody(declSt *declarativeState, parentParam *ir.Param, slotID string, cond, iter ir.Expr, key, value string, origBody, origElse []ir.Stmt) []ir.Stmt {
	// dyn is the element type here, not a fallback: a slot holds node handles,
	// which are opaque. The lookup misses for IR built without a check, and an
	// empty signature is what the pass used to synthesize anyway.
	var pushParams []*ir.Param
	var pushReturn *ir.Type
	if def := ir.LookupIntrinsic("list.push"); def != nil {
		pushParams, pushReturn = def.Instantiate(ir.TypDyn)
	}
	listPushFn := &ir.Func{
		Name:      "push",
		Receiver:  "list",
		Intrinsic: "list.push",
		Params:    pushParams,
		Return:    pushReturn,
	}
	// A call, not an assignment to the slot. push mutates its receiver and
	// returns nothing, so `__slotN = list.push(__slotN, n)` says the append
	// twice -- and every Go backend renders the intrinsic as the assignment
	// itself, so the wrapper emitted `x = x = append(...)`. passListLambdas
	// has always built the bare form for exactly that reason; this one had
	// not, and only html's OnSlotAppend hook hid it.
	pushToSlot := func(nodeID string) ir.Stmt {
		return &ir.CallStmt{
			Call: &ir.Call{
				Type: ir.TypVoid,
				Func: listPushFn,
				Args: []ir.CallArg{
					{Value: st.slotIdent(slotID)},
					{Value: &ir.Ident{Name: nodeID, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
				},
			},
		}
	}
	var ownerFuncs *[]*ir.Func
	switch o := st.owner.(type) {
	case compOwner:
		ownerFuncs = &o.c.Funcs
	case windowOwner:
		ownerFuncs = &o.pkg.Funcs
	}
	// The append target is the slot func's `parent` param; carry its Sym so
	// codegen resolves it as the local parameter rather than a Model field.
	parentRef := &ir.Ident{Name: parentParam.Name, Type: ir.TypDyn, Sym: parentParam, IsElementRef: true}
	// The instantiation sites met while emitting this body, in the order they
	// were reached: each keeps a list of the instances it holds, opened before
	// the body and closed after it.
	var instances []*slotInstance
	// attach is how a node reaches the parent: placed where the desired order
	// says, on a target that can place one, and appended on every other.
	attach := func(name string) []ir.Stmt {
		if st.placement != nil {
			return st.place(st.placement, name)
		}
		return []ir.Stmt{declSt.appendChildStmt(parentRef, name)}
	}
	emitNodeAt := func(n *ir.NodeInst) []ir.Stmt {
		var sub []ir.Stmt
		if declSt.instanceRecords && isInstanceNode(n) {
			if n.ID == "" {
				n.ID = declSt.freshID()
			}
			si := st.newSlotInstance(n)
			instances = append(instances, si)
			sub = st.reuseOrCreate(si, n, declSt)
			sub = append(sub, declSt.componentRootBinding(n)...)
		} else {
			// nil parent: the attachment is this function's business now, so
			// the node lowering must not append one of its own.
			_, sub = lowerNodeForSlot(declSt, n, nil, ownerFuncs)
		}
		name := declSt.attachName(n)
		sub = append(sub, attach(name)...)
		// What the slot retains is what it later removes from the parent, and
		// RemoveChild takes a node -- so for an instance that is its root, not
		// the instance itself.
		return append(sub, pushToSlot(name))
	}
	// Recursively process a body: NodeInsts are realized + pushed onto the
	// slot list. Nested If/For without their own slot (i.e. depending only
	// on loop variables / non-reactive state) are kept structurally; their
	// bodies are walked the same way. Nested If/For that DO carry a slot
	// (nested *reactive* structure on top-level reactive state) still
	// panic — that case needs the design work in
	// docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md.
	var emitStmts func(stmts []ir.Stmt) []ir.Stmt
	emitStmts = func(stmts []ir.Stmt) []ir.Stmt {
		var out []ir.Stmt
		for _, s := range stmts {
			switch sx := s.(type) {
			case *ir.NodeInst:
				out = append(out, emitNodeAt(sx)...)
			case *ir.If:
				if sx.LoweredSlotID != "" {
					var pos ast.Pos
					if sx.AST != nil {
						pos = sx.AST.Pos
					}
					st.failf(pos, "a reactive `if` nested inside another reactive `if`/`for` is not yet supported; move it out of the enclosing reactive block or gate it on a non-reactive condition")
				}
				inner := &ir.If{Cond: sx.Cond, Body: emitStmts(sx.Body)}
				if len(sx.Else) > 0 {
					inner.Else = emitStmts(sx.Else)
				}
				out = append(out, inner)
			case *ir.For:
				if sx.LoweredSlotID != "" {
					var pos ast.Pos
					if sx.AST != nil {
						pos = sx.AST.Pos
					}
					st.failf(pos, "a reactive `for` nested inside another reactive `if`/`for` is not yet supported; move it out of the enclosing reactive block or iterate a non-reactive collection")
				}
				inner := &ir.For{
					Key:      sx.Key,
					Value:    sx.Value,
					KeySym:   sx.KeySym,
					ValueSym: sx.ValueSym,
					Iter:     sx.Iter,
					ElemType: sx.ElemType,
					AST:      sx.AST,
					Body:     emitStmts(sx.Body),
				}
				if len(sx.Else) > 0 {
					inner.Else = emitStmts(sx.Else)
				}
				out = append(out, inner)
			case *ir.ErrorBoundary:
				cp := *sx
				cp.Children = emitStmts(sx.Children)
				out = append(out, &cp)
			default:
				// Non-structural stmt (LocalVar/Assign/CallStmt/etc.) — pass through.
				_ = sx
				out = append(out, s)
			}
		}
		return out
	}
	var structure []ir.Stmt
	if iter != nil {
		structure = []ir.Stmt{&ir.For{
			Key:   key,
			Value: value,
			Iter:  iter,
			Body:  emitStmts(origBody),
		}}
	} else {
		ifStmt := &ir.If{Cond: cond, Body: emitStmts(origBody)}
		if len(origElse) > 0 {
			ifStmt.Else = emitStmts(origElse)
		}
		structure = []ir.Stmt{ifStmt}
	}
	// Wrapped after the walk, because which instantiation sites the body holds
	// is only known once it has been walked.
	var out []ir.Stmt
	for _, si := range instances {
		out = append(out, si.open()...)
	}
	out = append(out, structure...)
	for _, si := range instances {
		out = append(out, st.closeSlotInstance(si)...)
	}
	return out
}

// buildRenderSlotFor walks stmts to find the If/For carrying slotID, then
// constructs the corresponding __renderSlotN Func. Returns nil if no
// matching node is present in stmts.
func (st *reactivityState) buildRenderSlotFor(slotID string, stmts []ir.Stmt) *ir.Func {
	var fn *ir.Func
	var walk func([]ir.Stmt)
	walk = func(ss []ir.Stmt) {
		for _, s := range ss {
			if fn != nil {
				return
			}
			switch n := s.(type) {
			case *ir.If:
				if n.LoweredSlotID == slotID {
					fn = st.synthesizeRenderSlotFunc(slotID, n.Cond, nil, "", "", n.Body, n.Else)
					return
				}
				walk(n.Body)
				walk(n.Else)
			case *ir.For:
				if n.LoweredSlotID == slotID {
					fn = st.synthesizeRenderSlotFunc(slotID, nil, n.Iter, n.Key, n.Value, n.Body, n.Else)
					return
				}
				walk(n.Body)
				walk(n.Else)
			case *ir.NodeInst:
				// A window is its own block; lowerReactivity builds that
				// slot's Func with the window as the owner.
				if ir.IsWindowNode(n) {
					return
				}
				walk(ir.WidgetChildren(n))
				for _, h := range n.Handlers {
					if h.Func != nil {
						walk(h.Func.Block)
					}
				}
			case *ir.SlotInst:
				walk(n.Children)
			case *ir.ErrorBoundary:
				walk(n.Children)
			case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider,
				*ir.Break, *ir.Continue:
				// Leaf/non-structural stmts cannot host an If/For with a
				// LoweredSlotID.
			default:
				panic(fmt.Sprintf("buildRenderSlotFor.walk: unhandled %T", n))
			}
		}
	}
	walk(stmts)
	return fn
}
