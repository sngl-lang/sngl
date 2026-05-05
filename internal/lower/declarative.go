package lower

import (
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
func lowerDeclarative(pkg *ir.Package, caps Caps) error {
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
	create        *ir.Func
	appendChild   *ir.Func
	attachHandler *ir.Func
	nextID        int
	lifter        *lifter
	liftHandlers  bool // true when NoLambda is on alongside NoDeclarative
}

func newDeclarativeState(pkg *ir.Package, caps Caps) *declarativeState {
	st := &declarativeState{liftHandlers: caps.NoLambda}
	if st.liftHandlers {
		st.lifter = &lifter{pkg: pkg}
	}
	return st
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

func (st *declarativeState) createFunc() *ir.Func {
	if st.create == nil {
		st.create = &ir.Func{
			Name:      "lower.createNode",
			Intrinsic: "LowerCreateNode",
			Params:    []*ir.Param{{Name: "tag", Type: ir.TypString}},
			Return:    ir.TypDyn,
		}
	}
	return st.create
}

func (st *declarativeState) appendChildFunc() *ir.Func {
	if st.appendChild == nil {
		st.appendChild = &ir.Func{
			Name:      "lower.appendChild",
			Intrinsic: "LowerAppendChild",
			Params: []*ir.Param{
				{Name: "parent", Type: ir.TypDyn},
				{Name: "child", Type: ir.TypDyn},
			},
			Return: ir.TypVoid,
		}
	}
	return st.appendChild
}

func (st *declarativeState) attachHandlerFunc() *ir.Func {
	if st.attachHandler == nil {
		st.attachHandler = &ir.Func{
			Name:      "lower.attachHandler",
			Intrinsic: "LowerAttachHandler",
			Params: []*ir.Param{
				{Name: "node", Type: ir.TypDyn},
				{Name: "event", Type: ir.TypString},
				{Name: "handler", Type: ir.TypDyn},
			},
			Return: ir.TypVoid,
		}
	}
	return st.attachHandler
}

// processStmts walks stmts, replacing each *ir.NodeInst with its flat
// emission and recursing into nested control-flow / handler bodies.
// funcs is the owning Funcs slice for handler lifting.
func (st *declarativeState) processStmts(stmts []ir.Stmt, funcs *[]*ir.Func) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			out = append(out, st.lowerNode(n, funcs)...)
		case *ir.If:
			n.Body = st.processStmts(n.Body, funcs)
			n.Else = st.processStmts(n.Else, funcs)
			out = append(out, n)
		case *ir.For:
			n.Body = st.processStmts(n.Body, funcs)
			n.Else = st.processStmts(n.Else, funcs)
			out = append(out, n)
		case *ir.PlatformFilter:
			n.Body = st.processStmts(n.Body, funcs)
			out = append(out, n)
		case *ir.SlotInst:
			n.Children = st.processStmts(n.Children, funcs)
			out = append(out, n)
		case *ir.ErrorBoundary:
			n.Children = st.processStmts(n.Children, funcs)
			out = append(out, n)
		case *ir.Window:
			n.Body = st.processStmts(n.Body, &n.Funcs)
			out = append(out, n)
		default:
			out = append(out, s)
		}
	}
	return out
}

// lowerNode emits the flat sequence for a single NodeInst:
//
//  1. var __nM dyn = lower.createNode("name")
//  2. #__nM.<key> = <propExpr>            (per prop)
//  3. lower.attachHandler(#__nM, "<evt>", __nM_<evt>_handler)  (per handler)
//  4. for each child: emit child's full subtree, then
//     lower.appendChild(#__nM, #__nC)
func (st *declarativeState) lowerNode(n *ir.NodeInst, funcs *[]*ir.Func) []ir.Stmt {
	id := n.ID
	if id == "" {
		id = st.freshID()
		n.ID = id
	}

	var stmts []ir.Stmt

	varType := ir.TypDyn
	if n.Component != nil {
		varType = &ir.Type{Kind: ir.TypeComponent, Decl: n.Component}
	}

	// 1. createNode
	stmts = append(stmts, &ir.LocalVar{
		Name: id,
		Type: varType,
		Init: &ir.Call{
			Type: ir.TypDyn,
			Func: st.createFunc(),
			Args: []ir.CallArg{
				{Value: &ir.Literal{Type: ir.TypString, Raw: n.Name}},
			},
		},
	})

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
		} else {
			// Either NoLambda is off (closure-supporting target — keep free
			// vars free) or the body has no captures (no lift needed).
			// Simple promote to a named top-level Func.
			handlerName := id + "_" + h.Name + "_handler"
			h.Func.Name = handlerName
			*funcs = append(*funcs, h.Func)
			handlerArg = &ir.Ident{Name: handlerName, Type: ir.TypDyn, Sym: h.Func}
		}

		stmts = append(stmts, &ir.CallStmt{
			Call: &ir.Call{
				Type: ir.TypVoid,
				Func: st.attachHandlerFunc(),
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: id, Type: ir.TypDyn, IsElementRef: true}},
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
			stmts = append(stmts, st.lowerNode(cn, funcs)...)
			stmts = append(stmts, &ir.CallStmt{
				Call: &ir.Call{
					Type: ir.TypVoid,
					Func: st.appendChildFunc(),
					Args: []ir.CallArg{
						{Value: &ir.Ident{Name: id, Type: ir.TypDyn, IsElementRef: true}},
						{Value: &ir.Ident{Name: cn.ID, Type: ir.TypDyn, IsElementRef: true}},
					},
				},
			})
		default:
			// Non-NodeInst child (If/For/etc.): recurse via processStmts on
			// a one-element slice. Result inherits parent's child position.
			stmts = append(stmts, st.processStmts([]ir.Stmt{c}, funcs)...)
		}
	}

	return stmts
}
