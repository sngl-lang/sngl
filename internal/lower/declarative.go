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
	enabled: func(c Caps) bool { return c.NoDeclarative },
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
func lowerDeclarative(pkg *ir.Package, caps Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := newDeclarativeState(pkg, caps)
	st.seedCounter(pkg)
	for _, comp := range pkg.Components {
		comp.Body = st.processStmts(comp.Body, &comp.Funcs)
	}
	for _, w := range pkg.Windows {
		w.Body = st.processStmts(w.Body, &w.Funcs)
	}
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
}

func newDeclarativeState(pkg *ir.Package, caps Caps) *declarativeState {
	st := &declarativeState{liftHandlers: caps.NoLambda, intrinsics: map[string]*ir.Func{}}
	for _, op := range ir.NodeOps {
		st.intrinsics[op] = nodeOpFunc(op)
	}
	if st.liftHandlers {
		st.lifter = &lifter{pkg: pkg}
	}
	return st
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
			st.scanStmts(n.Children)
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
		case *ir.PlatformFilter:
			st.scanStmts(n.Body)
		case *ir.SlotInst:
			st.scanStmts(n.Children)
		case *ir.ErrorBoundary:
			st.scanStmts(n.Children)
		case *ir.Window:
			st.scanStmts(n.Body)
		case *ir.Assign, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider:
			// Leaf/non-visual stmts — no NodeInst IDs to observe.
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
							{Value: &ir.Ident{Name: n.ID, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
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
		case *ir.PlatformFilter:
			n.Body = st.processStmtsForParent(n.Body, funcs, parentID)
			out = append(out, n)
		case *ir.SlotInst:
			n.Children = st.processStmtsForParent(n.Children, funcs, parentID)
			out = append(out, n)
		case *ir.ErrorBoundary:
			n.Children = st.processStmtsForParent(n.Children, funcs, parentID)
			out = append(out, n)
		case *ir.Window:
			n.Body = st.processStmts(n.Body, &n.Funcs)
			out = append(out, n)
		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider:
			// Leaf stmts — no NodeInsts to lower or nested blocks to recurse.
			out = append(out, s)
		default:
			panic(fmt.Sprintf("processStmtsForParent: unhandled %T", n))
		}
	}
	return out
}

// lowerNodeIntoStmts emits the flat sequence for a single NodeInst:
//
//  1. var __nM dyn = lower.createNode("name")
//  2. #__nM.<key> = <propExpr>            (per prop)
//  3. lower.attachHandler(#__nM, "<evt>", __nM_<evt>_handler)  (per handler)
//  4. for each child: emit child's full subtree, then
//     lower.appendChild(#__nM, #__nC)
//
// nodeIntProp extracts the integer value of a numeric/measurement prop
// (e.g. a canvas `width=400px`) from a NodeInst. Returns 0 when the prop
// is absent or not a numeric literal.
func nodeIntProp(n *ir.NodeInst, name string) int {
	for _, p := range n.Props {
		if p.Name != name {
			continue
		}
		if lit, ok := p.Value.(*ir.Literal); ok {
			raw := strings.TrimSuffix(lit.Raw, lit.Suffix)
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

	// 1. createNode — thread canvas draw func + dimensions through the
	// flattening so widget-emitting platforms can build a raster-backed
	// canvas widget (the NodeInst's CanvasDraw is otherwise discarded here).
	lv := &ir.LocalVar{
		Name: id,
		Type: varType,
		Init: &ir.Call{
			Type:     ir.TypDyn,
			Receiver: lowerNSIdent(),
			Func:     st.intrinsics["CreateNode"],
			Args: []ir.CallArg{
				{Value: &ir.Literal{Type: ir.TypString, Raw: n.Name}},
			},
		},
	}
	if n.CanvasDraw != nil {
		lv.CanvasDraw = n.CanvasDraw
		lv.CanvasWidth = nodeIntProp(n, "width")
		lv.CanvasHeight = nodeIntProp(n, "height")
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
		if st.liftHandlers && len(analyzeCaptures(h.Func.Block, h.Func.Params)) > 0 {
			// NoLambda is on and the body has free vars → lift the handler
			// into a top-level Func + state struct. lifter.Lift appends the
			// synthesized struct + Func to pkg, so we don't push h.Func into
			// *funcs ourselves.
			handlerArg = st.lifter.Lift(h.Func.Block, h.Func.Params, h.Func.Return, nil)
		} else if st.inlineHandlers {
			// Closure-supporting target inside a re-rendering slot: emit the
			// handler as an inline closure attached at the node, so it captures
			// any enclosing loop vars (index, element refs) rather than
			// referencing them from an out-of-scope top-level func.
			handlerArg = &ir.Lambda{Type: h.Func.SymType(), Func: h.Func}
		} else {
			// Either NoLambda is off (closure-supporting target — keep free
			// vars free) or the body has no captures (no lift needed).
			// Simple promote to a named top-level Func.
			handlerName := id + "_" + h.Name + "_handler"
			h.Func.Name = handlerName
			h.Func.LoweredFromTag = n.Name
			h.Func.LoweredFromEvent = h.Name
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
					{Value: &ir.Literal{Type: ir.TypString, Raw: h.Name}},
					{Value: handlerArg},
				},
			},
		})
	}

	// 4. children — recurse, then appendChild parent → child.
	for _, c := range n.Children {
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
						{Value: &ir.Ident{Name: cn.ID, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
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
// beyond a bare prop list. Native and empty-body wrappers (stdlib `text`,
// `button`, platform-resolved components) return false: those are leaf
// platform elements and are still lowered via CreateNode("name").
func hasRealComponentBody(comp *ir.Component) bool {
	if comp == nil || comp.Native != nil {
		return false
	}
	return len(comp.Body) > 0 || len(comp.Vars) > 0 || len(comp.Funcs) > 0 || len(comp.Timers) > 0
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
func (st *declarativeState) lowerComponentNodeIntoStmts(n *ir.NodeInst, id string) []ir.Stmt {
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
	propsLit := &ir.StructLit{
		Type:   ir.TypDyn,
		Fields: fields,
	}

	createCall := &ir.Call{
		Type:     ir.TypDyn,
		Receiver: lowerNSIdent(),
		Func:     st.intrinsics["CreateComponent"],
		Args: []ir.CallArg{
			{Value: compIdent},
			{Value: propsLit},
		},
	}

	return []ir.Stmt{
		&ir.LocalVar{
			Name: id,
			Type: &ir.Type{Kind: ir.TypeComponent, Decl: n.Component},
			Init: createCall,
		},
	}
}

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
					{Value: &ir.Ident{Name: n.ID, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
				},
			},
		})
	}
	return n.ID, stmts
}

// newDeclarativeStateForSlot constructs a declarativeState for use by
// passReactivity slot generators. liftHandlers=false because the slot
// re-render attaches handlers fresh each call; no separate closure
// capture state is needed.
func newDeclarativeStateForSlot(pkg *ir.Package) *declarativeState {
	st := newDeclarativeState(pkg, Caps{NoLambda: false})
	st.inlineHandlers = true
	// Seed the counter past every __nN already allocated package-wide
	// — including those inside sibling slot Funcs created by earlier
	// reactivity-pass invocations — so widget ids stay unique across
	// the slot's body, the enclosing window/component body, and every
	// other slot in the package.
	st.seedCounter(pkg)
	return st
}
