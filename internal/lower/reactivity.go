package lower

import (
	"strconv"

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
	idCounter    int
}

func (st *reactivityState) freshNodeID() string {
	id := "__n" + strconv.Itoa(st.idCounter)
	st.idCounter++
	return id
}

// lowerReactivity runs two passes over the package: first collects reverse
// deps and assigns synthetic IDs, then walks every Stmt slice splicing
// updater Assigns after every mutation that touches a tracked Var.
func lowerReactivity(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	st := &reactivityState{
		pkg:          pkg,
		reactiveVars: collectReactiveVars(pkg),
		reverseDeps:  make(map[*ir.Var][]reactiveProp),
	}
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt {
			st.collectFromStmts(stmts)
			return stmts
		},
	})
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt {
			return st.injectIntoStmts(stmts)
		},
	})
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
		st.collectFromStmts(n.Body)
		st.collectFromStmts(n.Else)
	case *ir.For:
		st.collectFromStmts(n.Body)
		st.collectFromStmts(n.Else)
	case *ir.PlatformFilter:
		st.collectFromStmts(n.Body)
	case *ir.SlotInst:
		st.collectFromStmts(n.Children)
	case *ir.ErrorBoundary:
		st.collectFromStmts(n.Children)
	case *ir.Window:
		st.collectFromStmts(n.Body)
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
