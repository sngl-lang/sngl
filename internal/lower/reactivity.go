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
	id, ok := a.Target.(*ir.Ident)
	if !ok {
		return nil
	}
	v, ok := id.Sym.(*ir.Var)
	if !ok {
		return nil
	}
	props, ok := st.reverseDeps[v]
	if !ok {
		return nil
	}
	var out []ir.Stmt
	for _, p := range props {
		out = append(out, &ir.Assign{
			Target: &ir.Select{
				Type:    ir.TypDyn,
				Operand: &ir.Ident{Name: p.NodeID, Type: ir.TypDyn, IsElementRef: true},
				Field:   p.Key,
			},
			Op:    ast.AssignSet,
			Value: p.Expr,
		})
	}
	return out
}
