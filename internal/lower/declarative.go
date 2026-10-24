package lower

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

var passDeclarative = pass{
	name:    "NoDeclarative",
	enabled: func(c Features) bool { return !c.Declarative },
	apply:   lowerDeclarative,
}

// lowerDeclarative flattens every visual node tree into an explicit
// sequence of LocalVar (createNode) + Assign (props) + CallStmt (handlers,
// appendChild). Last pass because it destroys the tree shape earlier
// passes rely on.
//
// When NoLambda is also on and a promoted handler body references
// outer-scope vars (the common case for `@click { count = count + 1 }`),
// the handler is lifted into a state-carrying *ir.Closure via the shared
// lifter. With NoLambda off the body's free vars stay free — which is fine
// for closure-supporting targets (e.g. Go) that emit the handler as a
// nested closure under its owning component/window.
func lowerDeclarative(pkg *ir.Package, caps Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := newDeclarativeState(pkg, caps)
	st.seedCounter(pkg)
	for _, comp := range pkg.Components {
		st.hosted = comp.Family
		comp.Stmts = st.processStmts(comp.Stmts, &comp.Funcs)
		destroyBuiltInstances(comp, st)
	}
	// The package body's parent is the application: each node there is
	// attached to it (ir.AppParent), and what that means is the platform's.
	st.hosted = nil
	pkg.Body = st.processStmtsForParent(pkg.Body, &pkg.Funcs, appRef())
	if st.err != nil {
		return st.err
	}
	return spliceSets(pkg, caps)
}

type declarativeState struct {
	// hosted is the family the position being lowered accepts, which a
	// component taking the family it is placed in is a member of
	// (rendersAsType).
	hosted       *ir.Component
	intrinsics   map[string]*ir.Func
	nextID       int
	lifter       *lifter
	liftHandlers bool // true when NoLambda is on alongside NoDeclarative
	// inlineHandlers keeps event handlers as inline closures attached at the
	// node, rather than promoting them to top-level named funcs. Set for the
	// reactive-slot path: a slot body may live inside a `for` loop, so a
	// promoted top-level handler would reference per-iteration vars (the loop
	// index, element refs) that exist only inside the loop. An inline closure
	// captures them. Only meaningful for closure-supporting targets
	// (liftHandlers=false); when NoLambda is on, the lifter handles captures.
	inlineHandlers bool
	// instanceRecords says CreateComponent yields a record carrying the node,
	// so the tree attaches ComponentRoot rather than the instance itself.
	// Where it is false the target still emits an instance as a method
	// returning a widget, and asking that widget for a root reads a field it
	// has not got.
	instanceRecords bool
	// connect is the platform's #[gen.protocol(connect)], nil on a target
	// declaring none.
	connect *ir.Func
	// err is the first protocol function that could not be spliced.
	err error
	// nestedInstance, set while a render slot's body is lowered, builds an
	// instance written under a node of that body: the slot keeps it in a
	// registry, as it keeps one written at the body's top, so the next render
	// destroys it.
	nestedInstance func(n *ir.NodeInst) []ir.Stmt
}

func newDeclarativeState(pkg *ir.Package, caps Features) *declarativeState {
	st := &declarativeState{liftHandlers: !caps.Lambda, instanceRecords: hasInstanceRuntime(caps), intrinsics: map[string]*ir.Func{}, connect: caps.connect}
	for _, op := range ir.NodeOps {
		st.intrinsics[op] = nodeOpFunc(op)
	}
	if st.liftHandlers {
		st.lifter = newLifter(pkg)
	}
	return st
}

// capturesPerIteration reports whether a handler body reads a binding that
// exists only for one turn of an enclosing loop.
//
// Everything else a handler captures outlives the handler: a component's state
// is a field on the model, and a promoted top-level method reaches it by name.
// A loop variable has no such home -- the promoted method is written outside
// the loop -- so the reference dangles, and on a target with a compiler that
// is a build failure rather than a wrong answer.
//
// This used to be hidden by unrolling: a view loop over a constant list was
// unrolled for every target, so each handler was promoted with the element
// substituted into it as a literal and captured nothing. Once a target with a
// host language emitted the loop instead, the loop variable was live and the
// promotion broke -- examples/calculator stopped building on fyne, whose
// KeyCap reads its `entry` prop, bound at the call site to the enclosing
// loop's variable.
func capturesPerIteration(captures []capture) bool {
	for _, c := range captures {
		if _, ok := c.Sym.(*ir.LoopVar); ok {
			return true
		}
	}
	return false
}

// nodeOpFunc is the callee a lowering pass hangs a node operation on. Only the
// id travels: codegen.WalkLowered matches on it and reads the operands off the
// call, so a signature here would be describing nobody's contract.
func nodeOpFunc(op string) *ir.Func {
	return &ir.Func{Name: op, Intrinsic: op}
}

// seedCounter scans every NodeInst.ID matching __n<digits> and starts the
// counter past the max. Lets NoDeclarative coexist with NoReactivity's
// pre-assigned IDs without clashing.
func (st *declarativeState) seedCounter(pkg *ir.Package) {
	// No `expr` hook, unlike the other walkPackage users: this scans for node
	// ids and for the `__nN` locals a lowering pass synthesizes, and neither
	// can occur in the one place an expr hook reaches that stmts does not -- a
	// lambda body. A visual node in an imperative body is a checker error
	// (rejectNodeInFuncBody) and no pass synthesizes a `__nN` there.
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt {
			st.scanStmts(stmts)
			return stmts
		},
	})
}

func (st *declarativeState) scanStmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			st.observeID(n.ID)
			st.scanStmts(ir.WidgetChildren(n))
		case *ir.LocalVar:
			// LocalVar __nN = lower.CreateNode(...) — already-lowered
			// node from a sibling pass (e.g. reactivity's slot synth).
			// Counted so a later declarative pass on comp/window bodies
			// doesn't restart the counter and collide on field names.
			if strings.HasPrefix(n.Name, "__n") {
				st.observeID(n.Name)
			}
		case *ir.If:
			st.scanStmts(n.Body)
			st.scanStmts(n.Else)
		case *ir.Match:
			for _, arm := range n.Arms {
				st.scanStmts(arm.Body)
			}
		case *ir.For:
			st.scanStmts(n.Body)
			st.scanStmts(n.Else)
		case *ir.SlotInst:
			st.scanStmts(n.Children)
		case *ir.ErrorBoundary:
			st.scanStmts(n.Children)
		case *ir.Assign, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider,
			*ir.Break, *ir.Continue, *ir.CanvasRedrawStmt:
			// Leaf/non-visual stmts — no NodeInst IDs to observe. A redraw
			// names a canvas declared elsewhere, so its id is already counted;
			// it reaches this scan at all only now that a redraw can land in
			// an effect's body rather than only in a handler's.
		default:
			panic(fmt.Sprintf("declarativeState.scanStmts: unhandled %T", n))
		}
	}
}

func (st *declarativeState) observeID(id string) {
	if !strings.HasPrefix(id, "__n") {
		return
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, "__n"))
	if err != nil {
		return
	}
	if n >= st.nextID {
		st.nextID = n + 1
	}
}

func (st *declarativeState) freshID() string {
	id := "__n" + strconv.Itoa(st.nextID)
	st.nextID++
	return id
}

// processStmts walks stmts, replacing each *ir.NodeInst with its flat
// emission and recursing into nested control-flow / handler bodies.
// funcs is the owning Funcs slice for handler lifting.
func (st *declarativeState) processStmts(stmts []ir.Stmt, funcs *[]*ir.Func) []ir.Stmt {
	return st.processStmtsForParent(stmts, funcs, nil)
}

// processStmtsForParent is the general form of processStmts: when parent is
// non-nil, NodeInsts encountered (directly or recursively in control-flow
// bodies) emit an AppendChild call back to parentID after their own subtree
// is emitted. This keeps if/for-nested children attached to the surrounding
// container rather than orphaned as top-level widgets.
func (st *declarativeState) processStmtsForParent(stmts []ir.Stmt, funcs *[]*ir.Func, parent *ir.Ident) []ir.Stmt {
	if parent != nil {
		defer st.within(hostedBy(parent.Type))()
	}
	var out []ir.Stmt
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			out = append(out, st.lowerNodeIntoStmts(n, funcs)...)
			if parent != nil {
				out = append(out, st.appendChildStmt(cloneIdent(parent), n))
			}
		case *ir.If:
			n.Body = st.processStmtsForParent(n.Body, funcs, parent)
			n.Else = st.processStmtsForParent(n.Else, funcs, parent)
			out = append(out, n)
		case *ir.Match:
			for _, arm := range n.Arms {
				arm.Body = st.processStmtsForParent(arm.Body, funcs, parent)
			}
			out = append(out, n)
		case *ir.For:
			n.Body = st.processStmtsForParent(n.Body, funcs, parent)
			n.Else = st.processStmtsForParent(n.Else, funcs, parent)
			out = append(out, n)
		case *ir.SlotInst:
			n.Children = st.processStmtsForParent(n.Children, funcs, parent)
			out = append(out, n)
		case *ir.ErrorBoundary:
			n.Children = st.processStmtsForParent(n.Children, funcs, parent)
			// Kept: what a boundary holds is flat now, and the boundary itself
			// stays until passBoundaryPassthrough, because its handler is
			// reached through it by every pass that still reads one.
			out = append(out, n)
		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider,
			*ir.Break, *ir.Continue:
			// Leaf stmts — no NodeInsts to lower or nested blocks to recurse.
			out = append(out, s)
		default:
			panic(fmt.Sprintf("processStmtsForParent: unhandled %T", n))
		}
	}
	return out
}

// nodeIntProp extracts the integer value of a numeric/measurement prop
// (e.g. a canvas `width=400px`) from a NodeInst. Returns 0 when the prop
// is absent or not a numeric literal.
func nodeIntProp(n *ir.NodeInst, name string) int {
	for _, p := range n.Props {
		if p.Name != name {
			continue
		}
		if lit, ok := p.Value.(*ir.Literal); ok {
			raw := strings.TrimSuffix(lit.Value, lit.Suffix)
			if v, err := strconv.Atoi(raw); err == nil {
				return v
			}
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				return int(f)
			}
		}
	}
	return 0
}

// lowerNodeIntoStmts emits the flat sequence for a single NodeInst:
//
//  1. var __nM <component> = lower.createNode("name")
//  2. #__nM.<key> = <propExpr>            (per prop)
//  3. lower.attachHandler(#__nM, "<evt>", __nM_<evt>_handler[, connect])  (per handler)
//  4. for each child: emit child's full subtree, then
//     lower.appendChild(#__nM, #__nC)
func (st *declarativeState) lowerNodeIntoStmts(n *ir.NodeInst, funcs *[]*ir.Func) []ir.Stmt {
	id := n.ID
	if id == "" {
		id = st.freshID()
		n.ID = id
	}

	// Component-targeted NodeInst with a real user component body —
	// left in place by passNoInlineComponents because the call site sits
	// inside a reactive context or a recursive cycle. Lower to
	// lower.CreateComponent(comp, propsLit) so codegen can wire it to a
	// real factory. Native/platform components and empty stdlib wrappers
	// (`text`, `button`, etc.) keep flowing through the CreateNode path:
	// they ARE the platform elements at the leaves of the tree.
	if n.Component != nil && hasRealComponentBody(n.Component) {
		return st.lowerComponentNodeIntoStmts(n, id)
	}

	var stmts []ir.Stmt

	varType := handleType(n)
	ref := func() *ir.Ident { return handleRef(id, varType) }

	// 1. createNode — a canvas rides across on the statement that replaces it,
	// so a widget-emitting platform can still build a raster-backed canvas.
	// Its shapes are a family of their own and were never flattened; without
	// the back-pointer the flattening is where the drawing is lost.
	create := &ir.Call{
		Type: varType,
		Func: st.intrinsics["CreateNode"],
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: ir.TypString, Value: n.Name}},
		},
	}
	if made := st.made(n); made != nil {
		create.Args = append(create.Args, ir.CallArg{Value: made})
	}
	lv := &ir.LocalVar{
		Name:    id,
		Type:    varType,
		NodeAST: n.AST,
		Init:    create,
	}
	if ir.IsShapeContainer(n) {
		lv.CanvasNode = n
	}
	stmts = append(stmts, lv)

	// 2. props
	for _, p := range n.Props {
		stmts = append(stmts, &ir.Assign{
			Target: &ir.Select{
				Type:    propType(n, p.Name, p.Value),
				Operand: &ir.Ident{Name: id, Type: varType, IsElementRef: true},
				Field:   p.Name,
			},
			Op:    ast.AssignSet,
			Value: p.Value,
		})
	}

	// 3. handlers — promote each into a named Func owned by the surrounding
	// component/window, or (when the body captures outer-scope vars) lift it
	// into a state-carrying *ir.Closure via the shared lifter. Emit
	// attachHandler with either a Func ident or the Closure as the third arg.
	for i := range n.Handlers {
		h := &n.Handlers[i]
		if h.Func == nil {
			continue
		}

		var handlerArg ir.Expr
		// Whichever form it takes: a closure is what a handler inside a
		// component instance becomes, and a test names it by this too.
		h.Func.Lowered.ComponentEvent = h.ComponentEvent
		captures := analyzeCaptures(h.Func.Block, h.Func.Params)
		switch {
		case st.liftHandlers && len(captures) > 0:
			// NoLambda is on and the body has free vars → lift the handler
			// into a top-level Func + state struct. lifter.Lift appends the
			// synthesized struct + Func to pkg, so we don't push h.Func into
			// *funcs ourselves.
			handlerArg = st.lifter.Lift(h.Func.Block, h.Func.Params, h.Func.Return, nil)
		case st.inlineHandlers || capturesPerIteration(captures):
			// Closure-supporting target whose handler body reads a
			// per-iteration binding: emit the handler as an inline closure
			// attached at the node, so it captures the loop's variable rather
			// than referencing it from an out-of-scope top-level func.
			handlerArg = &ir.Lambda{Type: h.Func.SymType(), Func: h.Func}
		default:
			// Nothing the body captures needs a closure to reach: a
			// component's own state is a field on the model, which a method
			// on it reads by name. Promote to a named top-level Func.
			handlerName := id + "_" + h.Name + "_handler"
			h.Func.Name = handlerName
			h.Func.Lowered.Tag = n.Name
			h.Func.Lowered.Event = h.Name
			h.Func.Lowered.Node = id
			*funcs = append(*funcs, h.Func)
			handlerArg = &ir.Ident{Name: handlerName, Type: h.Func.SymType(), Sym: h.Func}
		}

		args := []ir.CallArg{
			{Value: ref()},
			{Value: &ir.Literal{Type: ir.TypString, Value: h.Name}},
			{Value: handlerArg},
		}
		if connect := st.connected(n, h.Name, ref()); connect != nil {
			args = append(args, ir.CallArg{Value: connect})
		}
		stmts = append(stmts, &ir.CallStmt{
			Call: &ir.Call{Type: ir.TypVoid, Func: st.intrinsics["AttachHandler"], Args: args},
		})
	}

	// 4. children — recurse, then appendChild parent → child. A canvas's are
	// shapes and are not flattened: they are not widgets, there is nothing to
	// create for one, and an id spent on one shifts every id after it.
	defer st.within(hostedBy(varType))()
	for _, c := range ir.WidgetChildren(n) {
		switch cn := c.(type) {
		case *ir.NodeInst:
			stmts = append(stmts, st.lowerNodeIntoStmts(cn, funcs)...)
			stmts = append(stmts, st.appendChildStmt(ref(), cn))
		default:
			// Non-NodeInst child (If/For/etc.): recurse, but thread the
			// surrounding parent id so any NodeInsts inside the body get
			// appended back to this parent rather than orphaned.
			stmts = append(stmts, st.processStmtsForParent([]ir.Stmt{c}, funcs, ref())...)
		}
	}

	return stmts
}

// made is what n's #[gen.protocol(create)] function returns, spliced as the
// createNode op's second operand, or nil for a node whose platform makes it
// itself.
func (st *declarativeState) made(n *ir.NodeInst) ir.Expr {
	if n.Component == nil || n.Component.Protocol == nil || n.Component.Protocol.Create == nil {
		return nil
	}
	made, err := protocolSplice(n.Component.Protocol.Create, nil)
	if err != nil && st.err == nil {
		st.err = fmt.Errorf("%s: %w", n.Name, err)
	}
	return made
}

// connected is what the component's #[gen.protocol(on)] for event, or else
// the platform's #[gen.protocol(connect)], does with event on node, spliced
// as the attachHandler op's fourth operand, or nil where the node's component
// declares no protocol and its platform connects it itself. The platform's
// connect is handed the event under the name its host knows it by.
func (st *declarativeState) connected(n *ir.NodeInst, event string, node ir.Expr) ir.Expr {
	if n.Component == nil || n.Component.Protocol == nil {
		return nil
	}
	fn, args := n.Component.Protocol.Handler(event), []ir.Expr{node}
	if fn == nil {
		if st.connect == nil {
			return nil
		}
		name := event
		for _, e := range n.Component.Events {
			if e.Name == event && e.Connects != "" {
				name = e.Connects
			}
		}
		fn, args = st.connect, append(args, &ir.Literal{Type: ir.TypString, Value: name})
	}
	connect, err := protocolSplice(fn, args)
	if err != nil && st.err == nil {
		st.err = fmt.Errorf("%s: %w", n.Name, err)
	}
	return connect
}

// protocolSplice is what fn, a #[gen.protocol] function, does with its
// parameters bound to args: the expression it returns, or the call that is
// its one statement. Marshaled here: passCMarshal ran before the op it is
// spliced into existed. A parameter past args is left as it is written: the
// connect's handler, which the op carries beside the splice, is put in place
// by the platform, since whether it is a method or a closure is the shape of
// the code it writes, and a copy here would be a second body every pass
// after this one rewrites.
func protocolSplice(fn *ir.Func, args []ir.Expr) (ir.Expr, error) {
	body := exprBody(fn)
	if body == nil && len(fn.Block) == 1 {
		if cs, ok := fn.Block[0].(*ir.CallStmt); ok {
			body = cs.Call
		}
	}
	if body == nil {
		return nil, fmt.Errorf("#[gen.protocol(%s)] %s is spliced where its op is made, so its body is one return or one call", fn.Protocol, fn.Name)
	}
	bound := map[ir.Symbol]ir.Expr{}
	for i, arg := range args {
		bound[fn.Params[i]] = arg
	}
	spliced := ir.CloneExprSharingDecls(body)
	// The optimizer folds a program's `null` and never saw this body: a
	// library function nothing calls is not one of the program's.
	_ = ir.Rewrite(spliced, func(n ir.Node) (ir.Node, error) {
		if id, ok := n.(*ir.Ident); ok {
			if arg, ok := bound[id.Sym]; ok {
				return ir.CloneExprSharingDecls(arg), nil
			}
			if v, ok := id.Sym.(*ir.Var); ok && v.Builtin == ir.BuiltinNull {
				return &ir.Literal{Type: ir.TypNull}, nil
			}
		}
		return n, nil
	})
	if err := marshalSpliced(spliced); err != nil {
		return nil, err
	}
	return spliced, nil
}

// childOp is the node op intrinsic (AppendChild, RemoveChild) of child under
// parent. Where the parent's component declares its protocol, what its
// #[gen.protocol(op)] function does with the two rides along as the last
// operand; a component made through a create and declaring no append takes
// no child.
func childOp(intrinsic *ir.Func, op ir.ProtocolOp, parent, child ir.Expr) (*ir.CallStmt, error) {
	call := &ir.Call{
		Type: ir.TypVoid,
		Func: intrinsic,
		Args: []ir.CallArg{{Value: parent}, {Value: child}},
	}
	stmt := &ir.CallStmt{Call: call}
	t := parent.ExprType()
	if t == nil {
		return stmt, nil
	}
	comp, _ := t.Decl.(*ir.Component)
	if comp == nil || comp.Protocol == nil {
		return stmt, nil
	}
	fn := comp.Protocol.Op(op)
	if fn == nil {
		if op == ir.ProtocolAppend {
			return stmt, fmt.Errorf("%s takes no child: its package declares how it is made, and no #[gen.protocol(append)] to put one into it", comp.Name)
		}
		return stmt, nil
	}
	spliced, err := protocolSplice(fn, []ir.Expr{parent, child})
	if err != nil {
		return stmt, fmt.Errorf("%s: %w", comp.Name, err)
	}
	call.Args = append(call.Args, ir.CallArg{Value: spliced})
	return stmt, nil
}

// hasRealComponentBody reports whether comp carries SNGL-defined behavior
// beyond a bare prop list. Empty-body wrappers (stdlib `text`, `button`, a
// platform's declared primitives) return false: those are leaf platform
// elements and are still lowered via CreateNode("name").
func hasRealComponentBody(comp *ir.Component) bool {
	if comp == nil {
		return false
	}
	return len(comp.Stmts) > 0 || len(comp.Vars) > 0 || len(comp.Funcs) > 0
}

// lowerComponentNodeIntoStmts emits the flat sequence for a NodeInst whose
// target is a user component left in place by passNoInlineComponents:
//
//	var __nM <component> = lower.CreateComponent(<componentIdent>, {props...})
//
// AppendChild back to the surrounding parent is emitted by the caller
// (processStmtsForParent / lowerNodeForSlot), mirroring how DOM-node
// instances are attached. Children/handlers on a component-target
// NodeInst are not lowered here — by design, a component call expresses
// itself entirely through its prop set; children are encoded as a
// `children` prop earlier in the pipeline.
func (st *declarativeState) componentCreateCall(n *ir.NodeInst) *ir.Call {
	compIdent := &ir.Ident{
		Name: n.Component.Name,
		Sym:  n.Component,
		Type: &ir.Type{Kind: ir.TypeComponent, Decl: n.Component},
	}
	fields := make([]ir.FieldInit, 0, len(n.Props))
	for _, p := range n.Props {
		if p.Name == "" {
			continue
		}
		fields = append(fields, ir.FieldInit{
			Name:    p.Name,
			NamePos: p.NamePos,
			Value:   p.Value,
		})
	}
	return &ir.Call{
		Type: handleType(n),
		Func: st.intrinsics[ir.NodeOpCreateComponent],
		Args: []ir.CallArg{
			{Value: compIdent},
			{Value: &ir.StructLit{Type: propsRecordType(n.Component, fields), Fields: fields}},
		},
	}
}

// componentRootBinding binds the node an instance renders as, beside the
// instance itself. Empty on a target where the two are one thing.
func (st *declarativeState) componentRootBinding(n *ir.NodeInst) []ir.Stmt {
	if !st.instanceRecords {
		return nil
	}
	return []ir.Stmt{st.componentRootLocal(n, n.ID)}
}

// appendChildStmt attaches a node to a parent expression: what the tree
// attaches for it (attachRef).
func (st *declarativeState) appendChildStmt(parent ir.Expr, child *ir.NodeInst) ir.Stmt {
	stmt, err := childOp(st.intrinsics[ir.NodeOpAppendChild], ir.ProtocolAppend, parent, st.attachRef(child))
	if err != nil && st.err == nil {
		st.err = err
	}
	return stmt
}

// attachRef is a reference to what the tree attaches for n (attachName),
// typed as what it is: the node, or the node an instance renders as.
func (st *declarativeState) attachRef(n *ir.NodeInst) *ir.Ident {
	if st.attachName(n) != n.ID {
		return handleRef(st.attachName(n), rendersAsType(n.Component, st.hosted))
	}
	return handleRef(n.ID, handleType(n))
}

func (st *declarativeState) lowerComponentNodeIntoStmts(n *ir.NodeInst, id string) []ir.Stmt {
	if st.instanceRecords && st.nestedInstance != nil && n.Key == nil {
		return st.nestedInstance(n)
	}
	createCall := st.componentCreateCall(n)

	instance := &ir.LocalVar{
		Name: id,
		Type: &ir.Type{Kind: ir.TypeComponent, Decl: n.Component},
		Init: createCall,
	}
	// On a target whose instance is still a method returning a widget, the one
	// binding is both things and there is no root to ask for.
	if !st.instanceRecords {
		return []ir.Stmt{instance}
	}
	// Otherwise two bindings, because an instance and the node it renders as
	// are two things. The id keeps the instance -- it is what a prop update is
	// addressed to, and what the reactivity injection already named -- while
	// the tree attaches the root beside it.
	return []ir.Stmt{instance, st.componentRootLocal(n, id)}
}

// componentRootLocal binds the node the instance named id renders as.
func (st *declarativeState) componentRootLocal(n *ir.NodeInst, id string) *ir.LocalVar {
	t := rendersAsType(n.Component, st.hosted)
	args := []ir.CallArg{{Value: handleRef(id, handleType(n))}}
	// The copy an instance stands in is what a test finds one copy of it by,
	// and a reused instance is handed its new one before its root is bound.
	if key := n.Prop(ir.CopyProp); key != nil && n.Component.KeyedCopy {
		args = append(args, ir.CallArg{Value: deepCloneExpr(key)})
	}
	return &ir.LocalVar{
		Name: st.attachName(n),
		Type: t,
		Init: &ir.Call{Type: t, Func: st.intrinsics[ir.NodeOpComponentRoot], Args: args},
	}
}

// attachName is what the tree attaches for a node: the node itself, or the
// root an instance renders as. Appending the instance is what the lowering
// used to do, and every platform then handed its container an object that is
// not a node.
func (st *declarativeState) attachName(n *ir.NodeInst) string {
	if st.instanceRecords && n != nil && n.Component != nil && hasRealComponentBody(n.Component) {
		return n.ID + instanceRootSuffix
	}
	return n.ID
}

// instanceRootSuffix names the root binding beside an instance's own.
const instanceRootSuffix = "__el"

// lowerNodeForSlot emits the same create/setProp/attachHandler/appendChild
// sequence passDeclarative produces for one NodeInst's subtree, then
// appends the resulting top-level node to `parentID` (rather than the
// source-position parent). funcs is the owning Funcs slice for handler
// promotion. Returns the LocalVar name bound to the new top-level node ref.
func lowerNodeForSlot(st *declarativeState, n *ir.NodeInst, parentRef ir.Expr, funcs *[]*ir.Func) (string, []ir.Stmt) {
	stmts := st.lowerNodeIntoStmts(n, funcs)
	if parentRef != nil {
		stmts = append(stmts, st.appendChildStmt(parentRef, n))
	}
	return st.attachName(n), stmts
}

// newDeclarativeStateForSlot constructs a declarativeState for use by
// passReactivity slot generators. liftHandlers=false because the slot
// re-render attaches handlers fresh each call; no separate closure
// capture state is needed.
func newDeclarativeStateForSlot(pkg *ir.Package, caps Features) *declarativeState {
	// Only the capabilities that decide shape travel: the slot path always
	// keeps handlers as closures -- which is Lambda held, not withheld --
	// and whether an instance is a record is the target's answer rather than
	// the slot's. The connect is the platform's wherever a node is built.
	st := newDeclarativeState(pkg, Features{Lambda: true, Reactivity: caps.Reactivity, Declarative: caps.Declarative, connect: caps.connect})
	st.inlineHandlers = true
	// Seed the counter past every __nN already allocated package-wide
	// — including those inside sibling slot Funcs created by earlier
	// reactivity-pass invocations — so widget ids stay unique across
	// the slot's body, the enclosing window/component body, and every
	// other slot in the package.
	st.seedCounter(pkg)
	return st
}

// destroyBuiltInstances gives an instance built at run time the teardown of
// the instances its own build creates: a component rendered in its body is
// an instance of its own, and ends when the one around it does. Without it a
// window under `if details` was destroyed while an effect in a component it
// rendered stayed mounted, since nothing but the slot that built the window
// held the window, and nothing held what the window's build made.
//
// Only the build's own top level: an instance a render slot creates is held
// by that slot's registry, which destroyHeld already empties.
func destroyBuiltInstances(comp *ir.Component, st *declarativeState) {
	if comp == nil || !comp.RuntimeInstance {
		return
	}
	var destroys []ir.Stmt
	// Through a boundary, which is no level of its own: a window's content
	// is under the one its @error is.
	var top func(stmts []ir.Stmt) []ir.Stmt
	top = func(stmts []ir.Stmt) []ir.Stmt {
		var out []ir.Stmt
		for _, s := range stmts {
			if b, ok := s.(*ir.ErrorBoundary); ok {
				out = append(out, top(b.Children)...)
				continue
			}
			out = append(out, s)
		}
		return out
	}
	for _, s := range top(comp.Stmts) {
		lv, ok := s.(*ir.LocalVar)
		if !ok {
			continue
		}
		call, ok := lv.Init.(*ir.Call)
		if !ok || call.Func == nil || call.Func.Intrinsic != ir.NodeOpCreateComponent {
			continue
		}
		destroys = append([]ir.Stmt{&ir.CallStmt{Call: &ir.Call{
			Type: ir.TypVoid,
			Func: st.intrinsics[ir.NodeOpDestroyComponent],
			Args: []ir.CallArg{{Value: handleRef(lv.Name, lv.Type)}},
		}}}, destroys...)
	}
	if len(destroys) == 0 {
		return
	}
	if fn := funcNamed(comp.Funcs, TeardownFunc); fn != nil {
		fn.Block = append(fn.Block, destroys...)
		return
	}
	comp.Funcs = append(comp.Funcs, &ir.Func{
		Name:   TeardownFunc,
		Return: ir.TypVoid,
		Purity: ir.PurityMutates,
		Block:  destroys,
	})
}

// within makes hosted the family the position being lowered accepts, and
// returns what restores the one before.
func (st *declarativeState) within(hosted *ir.Component) func() {
	prev := st.hosted
	if hosted != nil {
		st.hosted = hosted
	}
	return func() { st.hosted = prev }
}
