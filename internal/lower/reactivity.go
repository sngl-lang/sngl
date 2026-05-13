package lower

import (
	"fmt"
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
	// slot synthesis owner: the *ir.Component or *ir.Window whose stmt body
	// we're currently walking, so synthesized slot Vars/Funcs get attached
	// to the right scope.
	owner reactivityOwner
}

// reactiveSlot records a per-If/per-For reactive dep. SlotID names the
// synthetic __slot<N>; GenFunc is the synthesized __renderSlot<N> Func
// (populated in a later task).
type reactiveSlot struct {
	SlotID  string
	GenFunc *ir.Func
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
func lowerReactivity(pkg *ir.Package, _ Caps) error {
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
	// Collect unique slot IDs from reverseSlots so each Func is built once.
	uniqueSlots := map[string]bool{}
	for _, slots := range st.reverseSlots {
		for _, slot := range slots {
			uniqueSlots[slot.SlotID] = true
		}
	}
	// Build one slot var + Func per unique ID.
	built := map[string]*ir.Func{}
	for slotID := range uniqueSlots {
		st.synthesizeSlotVar(slotID)
		fn := st.buildRenderSlotFor(slotID, stmts)
		if fn != nil {
			st.owner.addFunc(fn)
			built[slotID] = fn
		}
	}
	// Wire GenFunc on every reactiveSlot record so downstream Task 11 can
	// reach the Func through the reverseSlots map.
	for v, slots := range st.reverseSlots {
		for i := range slots {
			slots[i].GenFunc = built[slots[i].SlotID]
		}
		st.reverseSlots[v] = slots
	}
	return st.injectIntoStmts(stmts)
}

// synthesizeSlotVar creates the per-slot `__slotN list<dyn>` Var and
// attaches it to the current owner. Idempotent — returns the existing
// Var if one was already created.
func (st *reactivityState) synthesizeSlotVar(slotID string) *ir.Var {
	if existing := st.findSlotVar(slotID); existing != nil {
		return existing
	}
	v := &ir.Var{
		Name: slotID,
		Type: ir.ListOf(ir.TypDyn),
		Init: &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
	}
	st.owner.addVar(v)
	return v
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
		// handled by top-level loop in lowerReactivity
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
		}
		if updaters := st.updatersFor(s); len(updaters) > 0 {
			out = append(out, updaters...)
		}
	}
	return out
}

// updatersFor returns the list of *ir.Assign updaters to splice after s.
// Empty for stmts that don't mutate a tracked Var.
func (st *reactivityState) updatersFor(s ir.Stmt) []ir.Stmt {
	a, ok := s.(*ir.Assign)
	if !ok {
		return nil
	}
	v, fieldRewrite := st.assignTargetVar(a.Target)
	if v == nil {
		return nil
	}
	props, ok := st.reverseDeps[v]
	if !ok {
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
				Operand: &ir.Ident{Name: p.NodeID, Type: ir.TypDyn, IsElementRef: true},
				Field:   p.Key,
			},
			Op:    ast.AssignSet,
			Value: value,
		})
	}
	return out
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
	}
	return e
}

// renderFuncName: "__slot<N>" → "__renderSlot<N>".
func renderFuncName(slotID string) string {
	n := strings.TrimPrefix(slotID, "__slot")
	return "__renderSlot" + n
}

// synthesizeRenderSlotFunc generates the __renderSlotN(parent dyn) Func.
// origBody/origElse are the original If.Body/Else or For.Body/Else.
// Exactly one of (cond) or (iter) should be non-nil.
func (st *reactivityState) synthesizeRenderSlotFunc(slotID string, cond ir.Expr, iter ir.Expr, key, value string, origBody, origElse []ir.Stmt) *ir.Func {
	parentParam := &ir.Param{Name: "parent", Type: ir.TypDyn}
	fn := &ir.Func{
		Name:   renderFuncName(slotID),
		Params: []*ir.Param{parentParam},
		Return: ir.TypVoid,
	}

	// 1. Teardown: for __entry = __slotN { lower.RemoveChild(parent, __entry) }
	entryVar := "__entry"
	teardown := &ir.For{
		Key:  entryVar,
		Iter: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn)},
		Body: []ir.Stmt{
			&ir.CallStmt{Call: &ir.Call{
				Type:     ir.TypVoid,
				Receiver: lowerNSIdent(),
				Func:     st.intrinsics["RemoveChild"],
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: parentParam.Name, Type: ir.TypDyn, Sym: parentParam}},
					{Value: &ir.Ident{Name: entryVar, Type: ir.TypDyn}},
				},
			}},
		},
	}

	// 2. Reset: __slotN = []
	reset := &ir.Assign{
		Target: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn)},
		Op:     ast.AssignSet,
		Value:  &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
	}

	// 3. Re-evaluate and re-render.
	declSt := newDeclarativeStateForSlot(st.pkg)
	body := st.renderSlotBody(declSt, parentParam.Name, slotID, cond, iter, key, value, origBody, origElse)

	fn.Block = append([]ir.Stmt{teardown, reset}, body...)
	return fn
}

// renderSlotBody emits the cond/iter-gated create+append sequence for the
// slot's children, with each created top-level NodeInst's ref pushed onto
// __slotN via ListPush.
func (st *reactivityState) renderSlotBody(declSt *declarativeState, parentName, slotID string, cond, iter ir.Expr, key, value string, origBody, origElse []ir.Stmt) []ir.Stmt {
	listPushDef := ir.LookupIntrinsic("ListPush")
	listPushFn := &ir.Func{
		Name:      "ListPush",
		Intrinsic: "ListPush",
		Params:    listPushDef.Params,
		Return:    listPushDef.Return,
	}
	pushToSlot := func(nodeID string) ir.Stmt {
		return &ir.Assign{
			Target: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn)},
			Op:     ast.AssignSet,
			Value: &ir.Call{
				Type:     ir.ListOf(ir.TypDyn),
				Receiver: &ir.Ident{Name: "stdlib"},
				Func:     listPushFn,
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn)}},
					{Value: &ir.Ident{Name: nodeID, Type: ir.TypDyn, IsElementRef: true}},
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
	emitStmts := func(stmts []ir.Stmt) []ir.Stmt {
		var out []ir.Stmt
		for _, s := range stmts {
			nodeInst, ok := s.(*ir.NodeInst)
			if !ok {
				panic(fmt.Sprintf("lower(reactivity): non-NodeInst stmt %T in reactive slot body — nested reactive structures are not yet supported (see docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md Next steps)", s))
			}
			out = append(out, emitNodeAt(nodeInst)...)
		}
		return out
	}
	if iter != nil {
		return []ir.Stmt{&ir.For{
			Key:   key,
			Value: value,
			Iter:  iter,
			Body:  emitStmts(origBody),
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
			}
		}
	}
	walk(stmts)
	return fn
}

