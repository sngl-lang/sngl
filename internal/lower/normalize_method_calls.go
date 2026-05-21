package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passNoImplicitRecv normalizes method calls so every *ir.Call whose
// Func has a non-empty Receiver carries the receiver value in Args[0].
//
// Two source patterns currently reach IR:
//
//	Pattern A (implicit recv):
//	  ir.Call{Func: T.method (with receiver param), Args: []}
//	Pattern B (explicit recv):
//	  ir.Call{Func: T.method, Args: [recv, ...]}
//
// Pattern A arises from bare-name component-method refs (T8 elision
// followed by implicitCall) where the source has no receiver expression
// to write. After this pass, every Call is Pattern B; codegen drops its
// "is Args empty?" branching.
//
// For component receivers, the synthesized Args[0] is an *ir.Ident
// whose Sym is the *ir.Component. Each codegen target translates that
// ident to its per-instance state expression (`state` for JS, `m` for Go).
var passNoImplicitRecv = pass{
	name:    "NoImplicitRecv",
	enabled: func(c Caps) bool { return c.NoImplicitRecv },
	apply:   lowerNoImplicitRecv,
}

func lowerNoImplicitRecv(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &noImplicitRecvState{pkg: pkg}
	for _, comp := range pkg.Components {
		st.currentComp = comp
		if err := st.walkStmts(comp.Body); err != nil {
			return err
		}
		for _, fn := range comp.Funcs {
			if err := st.walkStmts(fn.Block); err != nil {
				return err
			}
		}
		for _, t := range comp.Timers {
			if t.Handler != nil {
				if err := st.walkStmts(t.Handler.Block); err != nil {
					return err
				}
			}
		}
	}
	st.currentComp = nil
	for _, fn := range pkg.Funcs {
		if err := st.walkStmts(fn.Block); err != nil {
			return err
		}
	}
	for _, w := range pkg.Windows {
		if err := st.walkStmts(w.Body); err != nil {
			return err
		}
		for _, fn := range w.Funcs {
			if err := st.walkStmts(fn.Block); err != nil {
				return err
			}
		}
	}
	return nil
}

type noImplicitRecvState struct {
	pkg         *ir.Package
	currentComp *ir.Component
}

func (st *noImplicitRecvState) walkStmts(stmts []ir.Stmt) error {
	for _, s := range stmts {
		if err := st.walkStmt(s); err != nil {
			return err
		}
	}
	return nil
}

func (st *noImplicitRecvState) walkStmt(s ir.Stmt) error {
	switch n := s.(type) {
	case *ir.Assign:
		return st.walkExpr(n.Value)
	case *ir.Toggle:
		return st.walkExpr(n.Target)
	case *ir.Return:
		return st.walkExpr(n.Value)
	case *ir.LocalVar:
		return st.walkExpr(n.Init)
	case *ir.If:
		if err := st.walkExpr(n.Cond); err != nil {
			return err
		}
		if err := st.walkStmts(n.Body); err != nil {
			return err
		}
		return st.walkStmts(n.Else)
	case *ir.For:
		if err := st.walkExpr(n.Iter); err != nil {
			return err
		}
		if err := st.walkStmts(n.Body); err != nil {
			return err
		}
		return st.walkStmts(n.Else)
	case *ir.Emit:
		for _, a := range n.Args {
			if err := st.walkExpr(a.Value); err != nil {
				return err
			}
		}
	case *ir.CallStmt:
		return st.walkCall(n.Call)
	case *ir.NodeInst:
		for _, p := range n.Props {
			if err := st.walkExpr(p.Value); err != nil {
				return err
			}
		}
		for _, h := range n.Handlers {
			if h.Func != nil {
				if err := st.walkStmts(h.Func.Block); err != nil {
					return err
				}
			}
		}
		return st.walkStmts(n.Children)
	case *ir.SlotInst:
		return st.walkStmts(n.Children)
	case *ir.ErrorBoundary:
		if err := st.walkStmts(n.Children); err != nil {
			return err
		}
		if n.Handler != nil && n.Handler.Func != nil {
			return st.walkStmts(n.Handler.Func.Block)
		}
	case *ir.PlatformFilter:
		return st.walkStmts(n.Body)
	case *ir.Window:
		if err := st.walkExpr(n.Href); err != nil {
			return err
		}
		if err := st.walkExpr(n.Title); err != nil {
			return err
		}
		if err := st.walkExpr(n.Favicon); err != nil {
			return err
		}
		return st.walkStmts(n.Body)
	case *ir.ContextProvider:
		if err := st.walkExpr(n.Value); err != nil {
			return err
		}
		return st.walkStmts(n.Children)
	}
	return nil
}

func (st *noImplicitRecvState) walkExpr(e ir.Expr) error {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *ir.Call:
		return st.walkCall(n)
	case *ir.Binary:
		if err := st.walkExpr(n.Left); err != nil {
			return err
		}
		return st.walkExpr(n.Right)
	case *ir.Unary:
		return st.walkExpr(n.Operand)
	case *ir.Ternary:
		if err := st.walkExpr(n.Cond); err != nil {
			return err
		}
		if err := st.walkExpr(n.Then); err != nil {
			return err
		}
		return st.walkExpr(n.Else)
	case *ir.Conversion:
		return st.walkExpr(n.Operand)
	case *ir.Select:
		return st.walkExpr(n.Operand)
	case *ir.Index:
		if err := st.walkExpr(n.Operand); err != nil {
			return err
		}
		return st.walkExpr(n.Idx)
	case *ir.ListLit:
		for _, el := range n.Elems {
			if err := st.walkExpr(el); err != nil {
				return err
			}
		}
	case *ir.StructLit:
		for _, f := range n.Fields {
			if err := st.walkExpr(f.Value); err != nil {
				return err
			}
		}
	case *ir.MapLitIR:
		for _, en := range n.Entries {
			if err := st.walkExpr(en.Key); err != nil {
				return err
			}
			if err := st.walkExpr(en.Value); err != nil {
				return err
			}
		}
	case *ir.Spread:
		return st.walkExpr(n.Operand)
	case *ir.Lambda:
		if n.Func != nil {
			return st.walkStmts(n.Func.Block)
		}
	case *ir.Closure:
		if n.Func != nil {
			return st.walkStmts(n.Func.Block)
		}
	}
	return nil
}

func (st *noImplicitRecvState) walkCall(c *ir.Call) error {
	for _, a := range c.Args {
		if err := st.walkExpr(a.Value); err != nil {
			return err
		}
	}
	if c.Receiver != nil {
		if err := st.walkExpr(c.Receiver); err != nil {
			return err
		}
	}
	if c.Func == nil || c.Func.Receiver == "" {
		return nil
	}
	if len(c.Args) >= len(c.Func.Params) {
		return nil
	}
	if len(c.Func.Params) == 0 {
		return nil
	}
	recvType := c.Func.Params[0].Type
	if recvType == nil {
		return nil
	}
	recv, err := st.synthRecv(recvType, c.Func.Receiver)
	if err != nil {
		return err
	}
	c.Args = append([]ir.CallArg{{Value: recv}}, c.Args...)
	return nil
}

// synthRecv returns an expression representing the implicit receiver
// for a method call whose Args[0] was elided by the checker. For
// component receivers, returns an *ir.Ident whose Sym is the
// *ir.Component (codegen translates this to per-target state).
//
// Struct and enum receivers should never reach this path.
func (st *noImplicitRecvState) synthRecv(recvType *ir.Type, receiverName string) (ir.Expr, error) {
	switch d := recvType.Decl.(type) {
	case *ir.Component:
		return &ir.Ident{
			Name: d.Name,
			Type: recvType,
			Sym:  d,
		}, nil
	case *ir.StructDef:
		return nil, fmt.Errorf("noImplicitRecv: struct method %s.%s called without explicit receiver", d.Name, receiverName)
	case *ir.EnumDef:
		return nil, fmt.Errorf("noImplicitRecv: enum method %s.%s called without explicit receiver", d.Name, receiverName)
	}
	return nil, fmt.Errorf("noImplicitRecv: unsupported receiver type kind %v for %s", recvType.Kind, receiverName)
}
