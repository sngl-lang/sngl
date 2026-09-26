package lower

import (
	"fmt"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passTernary = pass{
	name:    "NoTernary",
	enabled: func(c Features) bool { return !c.Ternary },
	apply:   lowerTernary,
}

// lowerTernary rewrites every Ternary expression to a synthetic LocalVar
// declaration plus an If statement that assigns into it, with the original
// expression position replaced by an Ident referring to that temp.
//
// Sub-ternaries inside Cond surface before the synthesized If; sub-ternaries
// inside Then/Else are hoisted into the matching branch body (preserving
// the short-circuit semantics of the source).
//
// Running after passReactivity (orderConstraints says it must) costs nothing:
// a ternary is intact through dep analysis, where gatherDeps has a case for
// it, and reactivity deep-copies each prop expression into its updater -- so
// the build-path prop and its updater no longer alias one Ternary node and
// each lowers independently here.
func lowerTernary(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &ternState{}
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return st.transformBlock(stmts) },
		// An initializer or a default has no statement list to hoist into,
		// so a ternary there becomes a func literal called in place
		// (immediateTernaries).
		//
		// A lambda *body* inside one is not in that position: it is a
		// statement list, so a ternary there hoists into it like any other.
		// Skipping the initializer wholesale skipped those too, and
		// `var ys = xs.map(func(x int) => c ? a : b)` panicked every Go
		// emitter with "ir.Ternary reached Go codegen".
		expr: func(e ir.Expr) ir.Expr {
			_ = ir.Walk(e, func(n ir.Node) error {
				if l, ok := n.(*ir.Lambda); ok && l.Func != nil {
					l.Func.Block = st.transformBlock(l.Func.Block)
					// Exactly once, and never into the slice just replaced:
					// the transform already recurses through a nested lambda,
					// and ir.Walk descends after the callback returns.
					return ir.SkipDir
				}
				return nil
			})
			return st.immediateTernaries(e)
		},
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
	case *ir.Toggle:
		pre, n.Target = st.transformExpr(n.Target)
	case *ir.ContextProvider:
		pre, n.Value = st.transformExpr(n.Value)
		n.Children = st.transformBlock(n.Children)
	case *ir.Break, *ir.Continue:
		// A loop escape holds no ternary.
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

// immediateTernaries rewrites each ternary left in an expression with no
// statement list to hoist into -- a var initializer, a prop default -- as a
// func literal called where it stands, whose body is the If a hoist would
// have written. Left alone, `var label = on ? "b" : "c"` reached every Go
// emitter as a Ternary and panicked it. A ternary inside a lambda is not
// reached here: the lambda's body is a statement list and was hoisted into.
func (st *ternState) immediateTernaries(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	tmp := []ir.Stmt{&ir.LocalVar{Init: e}}
	w := newExprWalker(func(x ir.Expr) ir.Expr {
		t, ok := x.(*ir.Ternary)
		if !ok {
			return x
		}
		fn := &ir.Func{
			Return: t.Type,
			Block: st.transformBlock([]ir.Stmt{&ir.If{
				Cond: t.Cond,
				Body: []ir.Stmt{&ir.Return{Value: t.Then}},
				Else: []ir.Stmt{&ir.Return{Value: t.Else}},
			}}),
		}
		return &ir.Call{Type: t.Type, Callee: &ir.Lambda{Type: ir.FuncOf(nil, t.Type), Func: fn}}
	})
	return w.stmts(tmp)[0].(*ir.LocalVar).Init
}
