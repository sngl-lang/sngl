package lower

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
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
		comp.Body = st.processStmts(comp.Body, &comp.Funcs)
		destroyBuiltInstances(comp, st)
	}
	// The package body's parent is the application: each node there is
	// attached to it (ir.AppParent), and what that means is the platform's.
	pkg.Body = st.processStmtsForParent(pkg.Body, &pkg.Funcs, ir.AppParent)
	return nil
}

type declarativeState struct {
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
}

func newDeclarativeState(pkg *ir.Package, caps Features) *declarativeState {
	st := &declarativeState{liftHandlers: !caps.Lambda, instanceRecords: hasInstanceRuntime(caps), intrinsics: map[string]*ir.Func{}}
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

// lowerNS is the namespace symbol the intrinsic-call receivers
// lowering emits resolve to. Lowering carries its own rather than reading the
// checker's: a pass emits an intrinsic call whether or not the source package
// ever named the namespace, so there is not always one to borrow.
var lowerNS = &ir.Namespace{Name: "lower"}

// lowerNSIdent returns a fresh Ident referring to the `lower` namespace.
// Used as Call.Receiver so ir.Convert emits SelectExpr{Operand: Ident("lower"), Field: name}.
func lowerNSIdent() *ir.Ident {
	return &ir.Ident{Name: "lower", Type: ir.TypDyn, Sym: lowerNS, Synthesized: true}
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
	return st.processStmtsForParent(stmts, funcs, "")
}

// processStmtsForParent is the general form of processStmts: when parentID is
// non-empty, NodeInsts encountered (directly or recursively in control-flow
// bodies) emit an AppendChild call back to parentID after their own subtree
// is emitted. This keeps if/for-nested children attached to the surrounding
// container rather than orphaned as top-level widgets.
func (st *declarativeState) processStmtsForParent(stmts []ir.Stmt, funcs *[]*ir.Func, parentID string) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			out = append(out, st.lowerNodeIntoStmts(n, funcs)...)
			if parentID != "" {
				out = append(out, &ir.CallStmt{
					Call: &ir.Call{
						Type:     ir.TypVoid,
						Receiver: lowerNSIdent(),
						Func:     st.intrinsics["AppendChild"],
						Args: []ir.CallArg{
							{Value: &ir.Ident{Name: parentID, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
							{Value: &ir.Ident{Name: st.attachName(n), Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
						},
					},
				})
			}
		case *ir.If:
			n.Body = st.processStmtsForParent(n.Body, funcs, parentID)
			n.Else = st.processStmtsForParent(n.Else, funcs, parentID)
			out = append(out, n)
		case *ir.For:
			n.Body = st.processStmtsForParent(n.Body, funcs, parentID)
			n.Else = st.processStmtsForParent(n.Else, funcs, parentID)
			out = append(out, n)
		case *ir.SlotInst:
			n.Children = st.processStmtsForParent(n.Children, funcs, parentID)
			out = append(out, n)
		case *ir.ErrorBoundary:
			n.Children = st.processStmtsForParent(n.Children, funcs, parentID)
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
//  1. var __nM dyn = lower.createNode("name")
//  2. #__nM.<key> = <propExpr>            (per prop)
//  3. lower.attachHandler(#__nM, "<evt>", __nM_<evt>_handler)  (per handler)
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

	varType := ir.TypDyn
	if n.Component != nil {
		varType = &ir.Type{Kind: ir.TypeComponent, Decl: n.Component}
	}

	// 1. createNode — a canvas rides across on the statement that replaces it,
	// so a widget-emitting platform can still build a raster-backed canvas.
	// Its shapes are a family of their own and were never flattened; without
	// the back-pointer the flattening is where the drawing is lost.
	lv := &ir.LocalVar{
		Name:    id,
		Type:    varType,
		NodeAST: n.AST,
		Init: &ir.Call{
			Type:     ir.TypDyn,
			Receiver: lowerNSIdent(),
			Func:     st.intrinsics["CreateNode"],
			Args: []ir.CallArg{
				{Value: &ir.Literal{Type: ir.TypString, Value: n.Name}},
			},
		},
	}
	if ir.IsShapeContainer(n) {
		lv.CanvasNode = n
	}
	stmts = append(stmts, lv)

	// 2. props
	for _, p := range n.Props {
		stmts = append(stmts, &ir.Assign{
			Target: &ir.Select{
				Type:    ir.TypDyn,
				Operand: &ir.Ident{Name: id, Type: ir.TypDyn, IsElementRef: true},
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
		h.Func.LoweredFromComponentEvent = h.ComponentEvent
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
			h.Func.LoweredFromTag = n.Name
			h.Func.LoweredFromEvent = h.Name
			h.Func.LoweredFromNode = id
			*funcs = append(*funcs, h.Func)
			handlerArg = &ir.Ident{Name: handlerName, Type: ir.TypDyn, Sym: h.Func}
		}

		stmts = append(stmts, &ir.CallStmt{
			Call: &ir.Call{
				Type:     ir.TypVoid,
				Receiver: lowerNSIdent(),
				Func:     st.intrinsics["AttachHandler"],
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: id, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
					{Value: &ir.Literal{Type: ir.TypString, Value: h.Name}},
					{Value: handlerArg},
				},
			},
		})
	}

	// 4. children — recurse, then appendChild parent → child. A canvas's are
	// shapes and are not flattened: they are not widgets, there is nothing to
	// create for one, and an id spent on one shifts every id after it.
	for _, c := range ir.WidgetChildren(n) {
		switch cn := c.(type) {
		case *ir.NodeInst:
			stmts = append(stmts, st.lowerNodeIntoStmts(cn, funcs)...)
			stmts = append(stmts, &ir.CallStmt{
				Call: &ir.Call{
					Type:     ir.TypVoid,
					Receiver: lowerNSIdent(),
					Func:     st.intrinsics["AppendChild"],
					Args: []ir.CallArg{
						{Value: &ir.Ident{Name: id, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
						{Value: &ir.Ident{Name: st.attachName(cn), Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
					},
				},
			})
		default:
			// Non-NodeInst child (If/For/etc.): recurse, but thread the
			// surrounding parent id so any NodeInsts inside the body get
			// appended back to this parent rather than orphaned.
			stmts = append(stmts, st.processStmtsForParent([]ir.Stmt{c}, funcs, id)...)
		}
	}

	return stmts
}

// hasRealComponentBody reports whether comp carries SNGL-defined behavior
// beyond a bare prop list. Empty-body wrappers (stdlib `text`, `button`, a
// platform's declared primitives) return false: those are leaf platform
// elements and are still lowered via CreateNode("name").
func hasRealComponentBody(comp *ir.Component) bool {
	if comp == nil {
		return false
	}
	return len(comp.Body) > 0 || len(comp.Vars) > 0 || len(comp.Funcs) > 0
}

// lowerComponentNodeIntoStmts emits the flat sequence for a NodeInst whose
// target is a user component left in place by passNoInlineComponents:
//
//	var __nM dyn = lower.CreateComponent(<componentIdent>, {props...})
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
		Type:     ir.TypDyn,
		Receiver: lowerNSIdent(),
		Func:     st.intrinsics[ir.NodeOpCreateComponent],
		Args: []ir.CallArg{
			{Value: compIdent},
			{Value: &ir.StructLit{Type: ir.TypDyn, Fields: fields}},
		},
	}
}

// componentRootBinding binds the node an instance renders as, beside the
// instance itself. Empty on a target where the two are one thing.
func (st *declarativeState) componentRootBinding(n *ir.NodeInst) []ir.Stmt {
	if !st.instanceRecords {
		return nil
	}
	return []ir.Stmt{&ir.LocalVar{
		Name: st.attachName(n),
		Type: ir.TypDyn,
		Init: &ir.Call{
			Type:     ir.TypDyn,
			Receiver: lowerNSIdent(),
			Func:     st.intrinsics[ir.NodeOpComponentRoot],
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: n.ID, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
			},
		},
	}}
}

// appendChildStmt attaches a node, named, to a parent expression.
func (st *declarativeState) appendChildStmt(parent ir.Expr, child string) ir.Stmt {
	return &ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: lowerNSIdent(),
		Func:     st.intrinsics[ir.NodeOpAppendChild],
		Args: []ir.CallArg{
			{Value: parent},
			{Value: &ir.Ident{Name: child, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
		},
	}}
}

func (st *declarativeState) lowerComponentNodeIntoStmts(n *ir.NodeInst, id string) []ir.Stmt {
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
	return []ir.Stmt{
		instance,
		&ir.LocalVar{
			Name: st.attachName(n),
			Type: ir.TypDyn,
			Init: &ir.Call{
				Type:     ir.TypDyn,
				Receiver: lowerNSIdent(),
				Func:     st.intrinsics[ir.NodeOpComponentRoot],
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: id, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
				},
			},
		},
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
		stmts = append(stmts, &ir.CallStmt{
			Call: &ir.Call{
				Type:     ir.TypVoid,
				Receiver: lowerNSIdent(),
				Func:     st.intrinsics["AppendChild"],
				Args: []ir.CallArg{
					{Value: parentRef},
					{Value: &ir.Ident{Name: st.attachName(n), Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
				},
			},
		})
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
	// the slot's.
	st := newDeclarativeState(pkg, Features{Lambda: true, Reactivity: caps.Reactivity, Declarative: caps.Declarative})
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
	for _, s := range top(comp.Body) {
		lv, ok := s.(*ir.LocalVar)
		if !ok {
			continue
		}
		call, ok := lv.Init.(*ir.Call)
		if !ok || call.Func == nil || call.Func.Intrinsic != ir.NodeOpCreateComponent {
			continue
		}
		destroys = append([]ir.Stmt{&ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: lowerNSIdent(),
			Func:     st.intrinsics[ir.NodeOpDestroyComponent],
			Args:     []ir.CallArg{{Value: &ir.Ident{Name: lv.Name, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}}},
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
