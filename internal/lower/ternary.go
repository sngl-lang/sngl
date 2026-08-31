package lower

import (
	"fmt"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passTernary = pass{
	name:    "NoTernary",
	enabled: func(c Caps) bool { return c.NoTernary },
	apply:   lowerTernary,
}

// lowerTernary rewrites every Ternary expression to a synthetic LocalVar
// declaration plus an If statement that assigns into it, with the original
// expression position replaced by an Ident referring to that temp.
//
// Sub-ternaries inside Cond surface before the synthesized If; sub-ternaries
// inside Then/Else are hoisted into the matching branch body (preserving
// the short-circuit semantics of the source).
func lowerTernary(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &ternState{}
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return st.transformBlock(stmts) },
		// Const-context expressions (initializers, defaults) are folded by
		// the optimizer before lower runs in the production pipeline. If a
		// dynamic ternary reaches us in such a position there is no
		// statement to hoist before — leave it alone.
		expr: func(e ir.Expr) ir.Expr { return e },
	})
	return nil
}

type ternState struct {
	counter int
}

func (st *ternState) freshName() string {
	n := st.counter
	st.counter++
	return "__lt" + strconv.Itoa(n)
}

func (st *ternState) transformBlock(block []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range block {
		pre, rewritten := st.transformStmt(s)
		out = append(out, pre...)
		if rewritten != nil {
			out = append(out, rewritten)
		}
	}
	return out
}

func (st *ternState) transformStmt(s ir.Stmt) ([]ir.Stmt, ir.Stmt) {
	var pre []ir.Stmt
	switch n := s.(type) {
	case *ir.Assign:
		pre, n.Value = st.transformExpr(n.Value)
	case *ir.LocalVar:
		if n.Init != nil {
			pre, n.Init = st.transformExpr(n.Init)
		}
	case *ir.Return:
		if n.Value != nil {
			pre, n.Value = st.transformExpr(n.Value)
		}
	case *ir.If:
		pre, n.Cond = st.transformExpr(n.Cond)
		n.Body = st.transformBlock(n.Body)
		n.Else = st.transformBlock(n.Else)
	case *ir.For:
		pre, n.Iter = st.transformExpr(n.Iter)
		n.Body = st.transformBlock(n.Body)
		n.Else = st.transformBlock(n.Else)
	case *ir.NodeInst:
		for i := range n.Props {
			if n.Props[i].Value != nil {
				p, v := st.transformExpr(n.Props[i].Value)
				pre = append(pre, p...)
				n.Props[i].Value = v
			}
		}
		if n.Key != nil {
			p, v := st.transformExpr(n.Key)
			pre = append(pre, p...)
			n.Key = v
		}
		if n.Ref != nil {
			p, v := st.transformExpr(n.Ref)
			pre = append(pre, p...)
			n.Ref = v
		}
		n.Children = st.transformBlock(n.Children)
		for i := range n.Handlers {
			if n.Handlers[i].Func != nil {
				n.Handlers[i].Func.Block = st.transformBlock(n.Handlers[i].Func.Block)
			}
		}
	case *ir.SlotInst:
		n.Children = st.transformBlock(n.Children)
	case *ir.ErrorBoundary:
		n.Children = st.transformBlock(n.Children)
		if n.Handler != nil && n.Handler.Func != nil {
			n.Handler.Func.Block = st.transformBlock(n.Handler.Func.Block)
		}
	case *ir.Emit:
		for i := range n.Args {
			p, v := st.transformExpr(n.Args[i].Value)
			pre = append(pre, p...)
			n.Args[i].Value = v
		}
	case *ir.CallStmt:
		if n.Call != nil {
			if n.Call.Receiver != nil {
				p, v := st.transformExpr(n.Call.Receiver)
				pre = append(pre, p...)
				n.Call.Receiver = v
			}
			for i := range n.Call.Args {
				p, v := st.transformExpr(n.Call.Args[i].Value)
				pre = append(pre, p...)
				n.Call.Args[i].Value = v
			}
		}
	case *ir.Window:
		if n.Href != nil {
			p, v := st.transformExpr(n.Href)
			pre = append(pre, p...)
			n.Href = v
		}
		if n.Title != nil {
			p, v := st.transformExpr(n.Title)
			pre = append(pre, p...)
			n.Title = v
		}
		if n.Favicon != nil {
			p, v := st.transformExpr(n.Favicon)
			pre = append(pre, p...)
			n.Favicon = v
		}
		n.Body = st.transformBlock(n.Body)
		// A window declared inside a component is a statement here rather
		// than an entry in pkg.Windows, so walkPackage never reaches its
		// funcs. The canvas lowering puts a draw function there, and its
		// ternaries went to Go codegen unlowered.
		for _, f := range n.Funcs {
			if f != nil {
				f.Block = st.transformBlock(f.Block)
			}
		}
	case *ir.Toggle:
		pre, n.Target = st.transformExpr(n.Target)
	case *ir.ContextProvider:
		pre, n.Value = st.transformExpr(n.Value)
		n.Children = st.transformBlock(n.Children)
	default:
		panic(fmt.Sprintf("ternState.transformStmt: unhandled %T", n))
	}
	return pre, s
}

func (st *ternState) transformExpr(e ir.Expr) ([]ir.Stmt, ir.Expr) {
	if e == nil {
		return nil, nil
	}
	switch x := e.(type) {
	case *ir.Ternary:
		return st.liftTernary(x)
	case *ir.Binary:
		var pre []ir.Stmt
		var p []ir.Stmt
		p, x.Left = st.transformExpr(x.Left)
		pre = append(pre, p...)
		p, x.Right = st.transformExpr(x.Right)
		pre = append(pre, p...)
		return pre, x
	case *ir.Unary:
		p, op := st.transformExpr(x.Operand)
		x.Operand = op
		return p, x
	case *ir.Call:
		var pre []ir.Stmt
		if x.Receiver != nil {
			p, v := st.transformExpr(x.Receiver)
			pre = append(pre, p...)
			x.Receiver = v
		}
		for i := range x.Args {
			p, v := st.transformExpr(x.Args[i].Value)
			pre = append(pre, p...)
			x.Args[i].Value = v
		}
		return pre, x
	case *ir.Conversion:
		p, op := st.transformExpr(x.Operand)
		x.Operand = op
		return p, x
	case *ir.Select:
		p, op := st.transformExpr(x.Operand)
		x.Operand = op
		return p, x
	case *ir.Index:
		var pre []ir.Stmt
		p, op := st.transformExpr(x.Operand)
		pre = append(pre, p...)
		x.Operand = op
		p, idx := st.transformExpr(x.Idx)
		pre = append(pre, p...)
		x.Idx = idx
		return pre, x
	case *ir.ListLit:
		var pre []ir.Stmt
		for i := range x.Elems {
			p, v := st.transformExpr(x.Elems[i])
			pre = append(pre, p...)
			x.Elems[i] = v
		}
		return pre, x
	case *ir.StructLit:
		var pre []ir.Stmt
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				p, v := st.transformExpr(x.Fields[i].Value)
				pre = append(pre, p...)
				x.Fields[i].Value = v
			}
		}
		return pre, x
	case *ir.Spread:
		p, op := st.transformExpr(x.Operand)
		x.Operand = op
		return p, x
	case *ir.Closure:
		var pre []ir.Stmt
		if x.State != nil {
			for i := range x.State.Fields {
				if x.State.Fields[i].Value != nil {
					p, v := st.transformExpr(x.State.Fields[i].Value)
					pre = append(pre, p...)
					x.State.Fields[i].Value = v
				}
			}
		}
		if x.Func != nil {
			x.Func.Block = st.transformBlock(x.Func.Block)
		}
		return pre, x
	case *ir.Lambda:
		if x.Func != nil {
			x.Func.Block = st.transformBlock(x.Func.Block)
		}
		return nil, x
	case *ir.MapLitIR:
		var pre []ir.Stmt
		for i := range x.Entries {
			p, k := st.transformExpr(x.Entries[i].Key)
			pre = append(pre, p...)
			x.Entries[i].Key = k
			p, v := st.transformExpr(x.Entries[i].Value)
			pre = append(pre, p...)
			x.Entries[i].Value = v
		}
		return pre, x
	case *ir.Literal, *ir.Ident, *ir.ContextRead:
		// Terminal — no Ternary to lift.
		return nil, e
	default:
		panic(fmt.Sprintf("ternState.transformExpr: unhandled %T", x))
	}
}

func (st *ternState) liftTernary(t *ir.Ternary) ([]ir.Stmt, ir.Expr) {
	var pre []ir.Stmt

	condPre, condExpr := st.transformExpr(t.Cond)
	pre = append(pre, condPre...)

	thenPre, thenExpr := st.transformExpr(t.Then)
	elsePre, elseExpr := st.transformExpr(t.Else)

	name := st.freshName()
	tmpSym := &ir.Var{Name: name, Type: t.Type, Synthesized: true}
	tmpDecl := &ir.LocalVar{
		Name: name,
		Type: t.Type,
		Sym:  tmpSym,
	}
	tmpIdent := func() *ir.Ident {
		return &ir.Ident{Name: name, Type: t.Type, Sym: tmpSym, Synthesized: true}
	}
	tmpRef := tmpIdent()

	body := append(thenPre, &ir.Assign{
		Target: tmpIdent(),
		Op:     ast.AssignSet,
		Value:  thenExpr,
	})
	elseBlock := append(elsePre, &ir.Assign{
		Target: tmpIdent(),
		Op:     ast.AssignSet,
		Value:  elseExpr,
	})

	pre = append(pre, tmpDecl, &ir.If{
		Cond:        condExpr,
		Body:        body,
		Else:        elseBlock,
		FromTernary: true,
	})
	return pre, tmpRef
}
