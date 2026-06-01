package lower

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passReactivity = pass{
	name:    "NoReactivity",
	enabled: func(c Caps) bool { return c.NoReactivity },
	apply:   lowerReactivity,
}

// reactiveProp is one (node, prop) pair affected by a reactive var.
type reactiveProp struct {
	NodeID string
	Key    string
	Expr   ir.Expr
}

// reactivityState carries the analysis built up before mutation injection.
type reactivityState struct {
	pkg          *ir.Package
	reactiveVars map[*ir.Var]bool
	reverseDeps  map[*ir.Var][]reactiveProp
	reverseSlots map[*ir.Var][]reactiveSlot
	intrinsics   map[string]*ir.Func // CreateNode, AppendChild, RemoveChild, AttachHandler
	idCounter    int
	slotCounter  int
	// slotDeclSt is the shared declarative state used to lower every
	// reactive-slot body. Sharing keeps the __nN counter monotonic
	// across slots so two slot Funcs in the same package don't
	// collide on `m.__n0` widget fields.
	slotDeclSt *declarativeState
	// slot synthesis owner: the *ir.Component or *ir.Window whose stmt body
	// we're currently walking, so synthesized slot Vars/Funcs get attached
	// to the right scope.
	owner reactivityOwner
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

type windowOwner struct{ w *ir.Window }

func (o windowOwner) addVar(v *ir.Var)   { o.w.Vars = append(o.w.Vars, v) }
func (o windowOwner) addFunc(f *ir.Func) { o.w.Funcs = append(o.w.Funcs, f) }

func (st *reactivityState) freshNodeID() string {
	id := "__n" + strconv.Itoa(st.idCounter)
	st.idCounter++
	return id
}

// lowerReactivity runs two passes over the package: first collects reverse
// deps and assigns synthetic IDs, then walks every Stmt slice splicing
// updater Assigns after every mutation that touches a tracked Var.
func lowerReactivity(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &reactivityState{
		pkg:          pkg,
		reactiveVars: collectReactiveVars(pkg),
		reverseDeps:  make(map[*ir.Var][]reactiveProp),
		reverseSlots: make(map[*ir.Var][]reactiveSlot),
		intrinsics:   make(map[string]*ir.Func),
	}
	for _, def := range ir.LowerIntrinsics {
		st.intrinsics[def.Name] = &ir.Func{
			Name:      def.Name,
			Intrinsic: def.Name,
			Params:    def.Params,
			Return:    def.Return,
		}
	}
	// Pass 1: collect reverse deps per owner scope.
	for _, comp := range pkg.Components {
		st.owner = compOwner{comp}
		st.collectFromStmts(comp.Body)
	}
	for _, w := range pkg.Windows {
		st.owner = windowOwner{w}
		st.collectFromStmts(w.Body)
	}
	// Pass 2: rewrite + inject. Delegates to existing injectIntoStmts;
	// future tasks add slot synthesis here.

	// Package-level funcs (e.g. lifted lambdas) are not owned by a
	// component or window; process them without an owner.
	for _, f := range pkg.Funcs {
		f.Block = st.injectIntoStmts(f.Block)
	}
	for _, v := range pkg.Vars {
		for _, h := range v.Handlers {
			if h.Func != nil {
				h.Func.Block = st.injectIntoStmts(h.Func.Block)
			}
		}
	}
	for _, comp := range pkg.Components {
		st.owner = compOwner{comp}
		comp.Body = st.rewriteAndInject(comp.Body)
		for _, f := range comp.Funcs {
			f.Block = st.rewriteAndInject(f.Block)
		}
		for _, v := range comp.Vars {
			for _, h := range v.Handlers {
				if h.Func != nil {
					h.Func.Block = st.rewriteAndInject(h.Func.Block)
				}
			}
		}
		for _, t := range comp.Timers {
			if t.Handler != nil {
				t.Handler.Block = st.rewriteAndInject(t.Handler.Block)
			}
		}
	}
	for _, w := range pkg.Windows {
		st.owner = windowOwner{w}
		w.Body = st.rewriteAndInject(w.Body)
		for _, f := range w.Funcs {
			f.Block = st.rewriteAndInject(f.Block)
		}
		for _, v := range w.Vars {
			for _, h := range v.Handlers {
				if h.Func != nil {
					h.Func.Block = st.rewriteAndInject(h.Func.Block)
				}
			}
		}
		if w.ErrorHandler != nil && w.ErrorHandler.Func != nil {
			w.ErrorHandler.Func.Block = st.rewriteAndInject(w.ErrorHandler.Func.Block)
		}
	}
	return nil
}

// rewriteAndInject is the unified pass-2 walk. Synthesizes slot Vars for
// every reactive If/For collected in pass-1, then delegates to
// injectIntoStmts for prop-updater injection. Future tasks add slot
// generator Funcs and structural rewrites.
func (st *reactivityState) rewriteAndInject(stmts []ir.Stmt) []ir.Stmt {
	// If any reactive slots exist on this owner, synthesize a __root Var
	// of type dyn. Platforms bind this to their root-container reference
	// at codegen time.
	if len(st.reverseSlots) > 0 {
		st.synthesizeRootVar()
	}
	// Collect unique slot IDs from reverseSlots so each Func is built once.
	uniqueSlots := map[string]bool{}
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
					rootVar := st.findSlotVar("__root")
					ref = &ir.Ident{Name: "__root", Type: ir.TypDyn, IsElementRef: true, Synthesized: true, Sym: rootVar}
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
					rootVar := st.findSlotVar("__root")
					ref = &ir.Ident{Name: "__root", Type: ir.TypDyn, IsElementRef: true, Synthesized: true, Sym: rootVar}
				}
				st.recordSlotParent(n.LoweredSlotID, ref)
				out = append(out, st.slotCall(n.LoweredSlotID, ref))
				continue
			}
			n.Body = st.rewriteReactiveStructures(n.Body, parentRef)
			n.Else = st.rewriteReactiveStructures(n.Else, parentRef)
		case *ir.NodeInst:
			// If any direct child is a reactive If/For we need a stable
			// element ref for it to thread through to the slot updater.
			if n.ID == "" && childrenContainReactiveSlot(n.Children) {
				n.ID = st.freshNodeID()
			}
			pref := &ir.Ident{Name: n.ID, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}
			n.Children = st.rewriteReactiveStructures(n.Children, pref)
			for _, h := range n.Handlers {
				if h.Func != nil {
					h.Func.Block = st.rewriteReactiveStructures(h.Func.Block, parentRef)
				}
			}
		case *ir.PlatformFilter:
			n.Body = st.rewriteReactiveStructures(n.Body, parentRef)
		case *ir.SlotInst:
			n.Children = st.rewriteReactiveStructures(n.Children, parentRef)
		case *ir.ErrorBoundary:
			n.Children = st.rewriteReactiveStructures(n.Children, parentRef)
		case *ir.Window:
			n.Body = st.rewriteReactiveStructures(n.Body, parentRef)
		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider:
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
		rootVar := st.findSlotVar("__root")
		parentRef = &ir.Ident{Name: "__root", Type: ir.TypDyn, IsElementRef: true, Synthesized: true, Sym: rootVar}
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
		for _, f := range o.w.Funcs {
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

// synthesizeRootVar creates the `__root dyn` Var on the current owner
// if it doesn't exist yet. Idempotent. Marked Synthesized so codegen
// can detect it.
func (st *reactivityState) synthesizeRootVar() {
	if existing := st.findSlotVar("__root"); existing != nil {
		return
	}
	v := &ir.Var{
		Name:        "__root",
		Type:        ir.TypDyn,
		Synthesized: true,
	}
	st.owner.addVar(v)
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
		for _, v := range o.w.Vars {
			if v.Name == name {
				return v
			}
		}
	}
	return nil
}

func collectReactiveVars(pkg *ir.Package) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool)
	for _, v := range pkg.Vars {
		if !v.IsConst {
			out[v] = true
		}
	}
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			if !v.IsConst {
				out[v] = true
			}
		}
	}
	for _, w := range pkg.Windows {
		for _, v := range w.Vars {
			if !v.IsConst {
				out[v] = true
			}
		}
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
		st.collectFromNode(n)
	case *ir.If:
		st.collectFromIf(n)
		st.collectFromStmts(n.Body)
		st.collectFromStmts(n.Else)
	case *ir.For:
		st.collectFromFor(n)
		st.collectFromStmts(n.Body)
		st.collectFromStmts(n.Else)
	case *ir.PlatformFilter:
		st.collectFromStmts(n.Body)
	case *ir.SlotInst:
		st.collectFromStmts(n.Children)
	case *ir.ErrorBoundary:
		st.collectFromStmts(n.Children)
	case *ir.Window:
		// Top-level windows live in pkg.Windows and are walked by the loop in
		// lowerReactivity. Windows declared inside a component body
		// (`component main { window { ... } }`) are *ir.Window statements here
		// instead, and pass 2 already recurses into them — so pass 1 must too,
		// or reactive If/For inside such a window never get a slot collected.
		st.collectFromStmts(n.Body)
	case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider:
		// Non-visual stmts — no reactive props/slots to collect from.
		// Imperative-handler dataflow is tracked at injection time.
	default:
		panic(fmt.Sprintf("collectFromStmt: unhandled %T", n))
	}
}

func (st *reactivityState) collectFromNode(n *ir.NodeInst) {
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
				NodeID: n.ID,
				Key:    prop.Name,
				Expr:   prop.Value,
			})
		}
	}
	st.collectFromStmts(n.Children)
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
	if len(deps) == 0 {
		return
	}
	slot := reactiveSlot{SlotID: st.freshSlotID()}
	for v := range deps {
		st.reverseSlots[v] = append(st.reverseSlots[v], slot)
	}
	n.LoweredSlotID = slot.SlotID
}

func (st *reactivityState) freshSlotID() string {
	id := "__slot" + strconv.Itoa(st.slotCounter)
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
		if st.reactiveVars[v] {
			out[v] = true
		}
	}
	for _, s := range fn.Block {
		st.eachCallInStmt(s, func(c *ir.Call) {
			st.gatherFuncReads(c.Func, out, visited)
		})
	}
}

// eachCallInStmt invokes fn for every *ir.Call reachable from s (and nested
// statements). Used to follow the call graph for transitive reactive-dep
// gathering; only the call nodes matter, so non-call exprs are descended
// without other side effects.
func (st *reactivityState) eachCallInStmt(s ir.Stmt, fn func(*ir.Call)) {
	switch n := s.(type) {
	case *ir.Return:
		st.eachCallInExpr(n.Value, fn)
	case *ir.LocalVar:
		st.eachCallInExpr(n.Init, fn)
	case *ir.Assign:
		st.eachCallInExpr(n.Value, fn)
	case *ir.CallStmt:
		st.eachCallInExpr(n.Call, fn)
	case *ir.Emit:
		for _, a := range n.Args {
			st.eachCallInExpr(a.Value, fn)
		}
	case *ir.If:
		st.eachCallInExpr(n.Cond, fn)
		for _, c := range n.Body {
			st.eachCallInStmt(c, fn)
		}
		for _, c := range n.Else {
			st.eachCallInStmt(c, fn)
		}
	case *ir.For:
		st.eachCallInExpr(n.Iter, fn)
		for _, c := range n.Body {
			st.eachCallInStmt(c, fn)
		}
		for _, c := range n.Else {
			st.eachCallInStmt(c, fn)
		}
	}
}

// eachCallInExpr invokes fn for every *ir.Call reachable from e.
func (st *reactivityState) eachCallInExpr(e ir.Expr, fn func(*ir.Call)) {
	switch x := e.(type) {
	case nil:
		return
	case *ir.Call:
		fn(x)
		st.eachCallInExpr(x.Receiver, fn)
		for _, a := range x.Args {
			st.eachCallInExpr(a.Value, fn)
		}
	case *ir.Binary:
		st.eachCallInExpr(x.Left, fn)
		st.eachCallInExpr(x.Right, fn)
	case *ir.Unary:
		st.eachCallInExpr(x.Operand, fn)
	case *ir.Ternary:
		st.eachCallInExpr(x.Cond, fn)
		st.eachCallInExpr(x.Then, fn)
		st.eachCallInExpr(x.Else, fn)
	case *ir.Conversion:
		st.eachCallInExpr(x.Operand, fn)
	case *ir.Select:
		st.eachCallInExpr(x.Operand, fn)
	case *ir.Index:
		st.eachCallInExpr(x.Operand, fn)
		st.eachCallInExpr(x.Idx, fn)
	case *ir.ListLit:
		for _, el := range x.Elems {
			st.eachCallInExpr(el, fn)
		}
	case *ir.MapLitIR:
		for _, en := range x.Entries {
			st.eachCallInExpr(en.Key, fn)
			st.eachCallInExpr(en.Value, fn)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			st.eachCallInExpr(f.Value, fn)
		}
	case *ir.Spread:
		st.eachCallInExpr(x.Operand, fn)
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
		case *ir.PlatformFilter:
			n.Body = st.injectIntoStmts(n.Body)
		case *ir.NodeInst:
			n.Children = st.injectIntoStmts(n.Children)
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
		case *ir.Window:
			n.Body = st.injectIntoStmts(n.Body)
			for _, fn := range n.Funcs {
				fn.Block = st.injectIntoStmts(fn.Block)
			}
			for _, v := range n.Vars {
				for _, h := range v.Handlers {
					if h.Func != nil {
						h.Func.Block = st.injectIntoStmts(h.Func.Block)
					}
				}
			}
		case *ir.CallStmt:
			// A statement-level call may carry an inline-closure handler
			// (e.g. lower.attachHandler(elem, "change", () => { … })), produced
			// when a reactive slot body is lowered with inline handlers. Recurse
			// into the closure body so mutations inside it get their dependent
			// prop/slot updaters spliced — otherwise a list-item handler that
			// mutates the list would never re-fire the slot.
			if n.Call != nil {
				for i := range n.Call.Args {
					switch lam := n.Call.Args[i].Value.(type) {
					case *ir.Lambda:
						if lam.Func != nil {
							lam.Func.Block = st.injectIntoStmts(lam.Func.Block)
						}
					case *ir.Closure:
						if lam.Func != nil {
							lam.Func.Block = st.injectIntoStmts(lam.Func.Block)
						}
					}
				}
			}
		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.ContextProvider:
			// Leaf stmts — no nested blocks to recurse into. updatersFor
			// below handles Assign-driven updater injection.
		default:
			panic(fmt.Sprintf("injectIntoStmts: unhandled %T", n))
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
	props := st.reverseDeps[v]
	slots := st.reverseSlots[v]
	if len(props) == 0 && len(slots) == 0 {
		return nil
	}
	var out []ir.Stmt
	for _, p := range props {
		// If we're inside a lifted body, the prop expression references the
		// reactive Var directly; rewrite reads of any captured Sym to go
		// through `*state.fieldName` so the updater compiles in the lifted
		// scope.
		value := p.Expr
		if fieldRewrite != nil {
			value = rewriteIdentsToCaptures(value, fieldRewrite)
		}
		out = append(out, &ir.Assign{
			Target: &ir.Select{
				Type:    ir.TypDyn,
				Operand: &ir.Ident{Name: p.NodeID, Type: ir.TypDyn, IsElementRef: true, Synthesized: true},
				Field:   p.Key,
			},
			Op:    ast.AssignSet,
			Value: value,
		})
	}
	// Structural updaters: re-fire __renderSlotN for every reactive If/For
	// dependent on this Var.
	for _, slot := range slots {
		if slot.GenFunc == nil {
			continue
		}
		parentRef := slot.ParentRef
		if parentRef == nil {
			rootVar := st.findSlotVar("__root")
			parentRef = &ir.Ident{Name: "__root", Type: ir.TypDyn, IsElementRef: true, Synthesized: true, Sym: rootVar}
		}
		out = append(out, &ir.CallStmt{Call: &ir.Call{
			Type: ir.TypVoid,
			Func: slot.GenFunc,
			Args: []ir.CallArg{{Value: cloneIdent(parentRef)}},
		}})
	}
	return out
}

// mutatingCallReceiver returns the receiver expression of a statement-level
// call that mutates its receiver in place, or nil otherwise. A method like
// list.push is declared returning a new list but backed by an intrinsic that
// mutates the receiver (see ir.IntrinsicDef.MutatesReceiver); a bare
// `tasks.push(x)` statement therefore mutates `tasks` and must fire its
// reactive updaters, exactly as `tasks = ...` would. The mutation semantics
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
		if v, ok := id.Sym.(*ir.Var); ok && st.reactiveVars[v] {
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
	if st.pkg == nil {
		return nil, nil
	}
	for liftedFunc, capMap := range st.pkg.LiftedCaptures {
		if len(liftedFunc.Params) == 0 || liftedFunc.Params[0] != stateParam {
			continue
		}
		var resolved *ir.Var
		for sym, name := range capMap {
			if name == sel.Field {
				if v, ok := sym.(*ir.Var); ok && st.reactiveVars[v] {
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
	n := strings.TrimPrefix(slotID, "__slot")
	return "__renderSlot" + n
}

// synthesizeRenderSlotFunc generates the __renderSlotN(parent dyn) Func.
// origBody/origElse are the original If.Body/Else or For.Body/Else.
// Exactly one of (cond) or (iter) should be non-nil.
func (st *reactivityState) synthesizeRenderSlotFunc(slotID string, cond ir.Expr, iter ir.Expr, key, value string, refElem bool, indexVar string, elemType *ir.Type, origBody, origElse []ir.Stmt) *ir.Func {
	parentParam := &ir.Param{Name: "parent", Type: ir.TypDyn}
	fn := &ir.Func{
		Name:        renderFuncName(slotID),
		Params:      []*ir.Param{parentParam},
		Return:      ir.TypVoid,
		Synthesized: true,
	}

	// 1. Teardown: for __entry = __slotN { lower.RemoveChild(parent, __entry) }
	entryVar := "__entry"
	teardown := &ir.For{
		Key:  entryVar,
		Iter: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn), Synthesized: true},
		Body: []ir.Stmt{
			&ir.CallStmt{Call: &ir.Call{
				Type:     ir.TypVoid,
				Receiver: lowerNSIdent(),
				Func:     st.intrinsics["RemoveChild"],
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: parentParam.Name, Type: ir.TypDyn, Sym: parentParam, IsElementRef: true}},
					{Value: &ir.Ident{Name: entryVar, Type: ir.TypDyn}},
				},
			}},
		},
	}

	// 2. Reset: __slotN = []
	reset := &ir.Assign{
		Target: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn), Synthesized: true},
		Op:     ast.AssignSet,
		Value:  &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
	}

	// 3. Re-evaluate and re-render. Reuse one declarative state across
	// every reactive slot in the package so `__nN` widget ids stay
	// monotonic and don't collide between sibling slot Funcs.
	if st.slotDeclSt == nil {
		st.slotDeclSt = newDeclarativeStateForSlot(st.pkg)
	}
	// Seed slot's counter past any IDs reactivity has assigned so far
	// (collectFromNode in pass-1 may have set NodeInst.IDs that the
	// initial seedCounter didn't see if they came from later passes).
	if st.idCounter > st.slotDeclSt.nextID {
		st.slotDeclSt.nextID = st.idCounter
	}
	body := st.renderSlotBody(st.slotDeclSt, parentParam.Name, slotID, cond, iter, key, value, refElem, indexVar, elemType, origBody, origElse)
	// Propagate the slot's advanced counter back so subsequent
	// reactivity freshNodeID calls (line 253, line 446) don't reuse
	// __nN values the slot just claimed.
	if st.slotDeclSt.nextID > st.idCounter {
		st.idCounter = st.slotDeclSt.nextID
	}

	fn.Block = append([]ir.Stmt{teardown, reset}, body...)
	return fn
}

// renderSlotBody emits the cond/iter-gated create+append sequence for the
// slot's children, with each created top-level NodeInst's ref pushed onto
// __slotN via ListPush.
func (st *reactivityState) renderSlotBody(declSt *declarativeState, parentName, slotID string, cond, iter ir.Expr, key, value string, refElem bool, indexVar string, elemType *ir.Type, origBody, origElse []ir.Stmt) []ir.Stmt {
	listPushDef := ir.LookupIntrinsic("ListPush")
	listPushFn := &ir.Func{
		Name:      "ListPush",
		Intrinsic: "ListPush",
		Params:    listPushDef.Params,
		Return:    listPushDef.Return,
	}
	pushToSlot := func(nodeID string) ir.Stmt {
		return &ir.Assign{
			Target: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn), Synthesized: true},
			Op:     ast.AssignSet,
			Value: &ir.Call{
				Type:     ir.ListOf(ir.TypDyn),
				Receiver: &ir.Ident{Name: "stdlib"},
				Func:     listPushFn,
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn), Synthesized: true}},
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
		ownerFuncs = &o.w.Funcs
	}
	emitNodeAt := func(n *ir.NodeInst) []ir.Stmt {
		_, sub := lowerNodeForSlot(declSt, n, parentName, ownerFuncs)
		sub = append(sub, pushToSlot(n.ID))
		return sub
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
					panic("lower(reactivity): nested *ir.If with its own slot — nested reactive structures are not yet supported (see docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md Next steps)")
				}
				inner := &ir.If{Cond: sx.Cond, Body: emitStmts(sx.Body)}
				if len(sx.Else) > 0 {
					inner.Else = emitStmts(sx.Else)
				}
				out = append(out, inner)
			case *ir.For:
				if sx.LoweredSlotID != "" {
					panic("lower(reactivity): nested *ir.For with its own slot — nested reactive structures are not yet supported (see docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md Next steps)")
				}
				inner := &ir.For{
					Key:      sx.Key,
					Value:    sx.Value,
					Iter:     sx.Iter,
					ElemType: sx.ElemType,
					RefElem:  sx.RefElem,
					IndexVar: sx.IndexVar,
					AST:      sx.AST,
					Body:     emitStmts(sx.Body),
				}
				if len(sx.Else) > 0 {
					inner.Else = emitStmts(sx.Else)
				}
				out = append(out, inner)
			default:
				// Non-structural stmt (LocalVar/Assign/CallStmt/etc.) — pass through.
				_ = sx
				out = append(out, s)
			}
		}
		return out
	}
	if iter != nil {
		return []ir.Stmt{&ir.For{
			Key:      key,
			Value:    value,
			Iter:     iter,
			ElemType: elemType,
			RefElem:  refElem,
			IndexVar: indexVar,
			Body:     emitStmts(origBody),
		}}
	}
	ifStmt := &ir.If{Cond: cond, Body: emitStmts(origBody)}
	if len(origElse) > 0 {
		ifStmt.Else = emitStmts(origElse)
	}
	return []ir.Stmt{ifStmt}
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
					fn = st.synthesizeRenderSlotFunc(slotID, n.Cond, nil, "", "", false, "", nil, n.Body, n.Else)
					return
				}
				walk(n.Body)
				walk(n.Else)
			case *ir.For:
				if n.LoweredSlotID == slotID {
					fn = st.synthesizeRenderSlotFunc(slotID, nil, n.Iter, n.Key, n.Value, n.RefElem, n.IndexVar, n.ElemType, n.Body, n.Else)
					return
				}
				walk(n.Body)
				walk(n.Else)
			case *ir.NodeInst:
				walk(n.Children)
				for _, h := range n.Handlers {
					if h.Func != nil {
						walk(h.Func.Block)
					}
				}
			case *ir.PlatformFilter:
				walk(n.Body)
			case *ir.SlotInst:
				walk(n.Children)
			case *ir.ErrorBoundary:
				walk(n.Children)
			case *ir.Window:
				walk(n.Body)
			case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider:
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
