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
func lowerDeclarative(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	st := newDeclarativeState()
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
}

func newDeclarativeState() *declarativeState { return &declarativeState{} }

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

	// 1. createNode
	stmts = append(stmts, &ir.LocalVar{
		Name: id,
		Type: ir.TypDyn,
		Init: &ir.Call{
			Type: ir.TypDyn,
			Func: st.createFunc(),
			Args: []ir.CallArg{
				{Value: &ir.Literal{Type: ir.TypString, Raw: strconv.Quote(n.Name)}},
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

	// 3. handlers — lift each into a named Func owned by the surrounding
	// component/window, then emit attachHandler.
	for i := range n.Handlers {
		h := &n.Handlers[i]
		if h.Func == nil {
			continue
		}
		handlerName := id + "_" + h.Name + "_handler"
		h.Func.Name = handlerName
		*funcs = append(*funcs, h.Func)
		stmts = append(stmts, &ir.CallStmt{
			Call: &ir.Call{
				Type: ir.TypVoid,
				Func: st.attachHandlerFunc(),
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: id, Type: ir.TypDyn, IsElementRef: true}},
					{Value: &ir.Literal{Type: ir.TypString, Raw: strconv.Quote(h.Name)}},
					{Value: &ir.Ident{Name: handlerName, Type: ir.TypDyn, Sym: h.Func}},
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
