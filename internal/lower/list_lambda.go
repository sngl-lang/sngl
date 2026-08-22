package lower

import (
	"fmt"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passNoListLambdas expands `xs.filter(f)` and `xs.map(f)` into an
// explicit accumulator + for-loop that calls the lambda directly,
// removing the generic `list.filter` / `list.map` builtin from the
// codegen surface. Targets that can't represent `func(any) any`-style
// type-erased lambdas (today: Go) opt in via Caps.NoListLambdas; other
// languages with native filter/map (JS Array, Kotlin Iterable) leave
// the builtin alone.
//
// Each lifted call hoists three statements before the using statement:
//
//	var __list<N> list<T> = []
//	for item<N> = xs {
//	    // filter: if (f)(item<N>) { __list<N> = ListPush(__list<N>, item<N>) }
//	    // map:    __list<N> = ListPush(__list<N>, (f)(item<N>))
//	}
//
// The original call expression is replaced with an Ident referring to
// the temp. Subsequent expression positions (assignments, returns,
// passed args, etc.) read from that fully-built list.
var passNoListLambdas = pass{
	name:    "NoListLambdas",
	enabled: func(c Caps) bool { return c.NoListLambdas },
	apply:   lowerListLambdas,
}

func lowerListLambdas(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &listLambdaState{}
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return st.transformBlock(stmts) },
		expr: func(e ir.Expr) ir.Expr {
			// Const-context exprs (initializers, defaults) get folded by
			// the optimizer before lower runs; if a filter/map slips
			// through here there's no statement to hoist before, so
			// leave it for codegen (which will fall back to the generic
			// path).
			return e
		},
	})
	return nil
}

type listLambdaState struct {
	listCounter int
	itemCounter int
}

func (st *listLambdaState) freshList() string {
	n := st.listCounter
	st.listCounter++
	return "__list" + strconv.Itoa(n)
}

func (st *listLambdaState) freshItem() string {
	n := st.itemCounter
	st.itemCounter++
	return "__item" + strconv.Itoa(n)
}

func (st *listLambdaState) transformBlock(block []ir.Stmt) []ir.Stmt {
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

func (st *listLambdaState) transformStmt(s ir.Stmt) ([]ir.Stmt, ir.Stmt) {
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
	case *ir.PlatformFilter:
		n.Body = st.transformBlock(n.Body)
	case *ir.NodeInst:
		for i := range n.Props {
			if n.Props[i].Value != nil {
				p, v := st.transformExpr(n.Props[i].Value)
				pre = append(pre, p...)
				n.Props[i].Value = v
			}
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
	case *ir.ContextProvider:
		pre, n.Value = st.transformExpr(n.Value)
		n.Children = st.transformBlock(n.Children)
	default:
		panic(fmt.Sprintf("listLambdaState.transformStmt: unhandled %T", n))
	}
	return pre, s
}

func (st *listLambdaState) transformExpr(e ir.Expr) ([]ir.Stmt, ir.Expr) {
	if e == nil {
		return nil, nil
	}
	switch x := e.(type) {
	case *ir.Call:
		// Bottom-up: recurse first so nested filter/map chains lift in
		// inner-to-outer order.
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
		// Now check whether THIS Call matches list.filter / list.map.
		if isListLambdaCall(x) {
			liftPre, replacement := st.liftListLambda(x)
			pre = append(pre, liftPre...)
			return pre, replacement
		}
		return pre, x
	case *ir.Binary:
		var pre []ir.Stmt
		p, l := st.transformExpr(x.Left)
		pre = append(pre, p...)
		x.Left = l
		p, r := st.transformExpr(x.Right)
		pre = append(pre, p...)
		x.Right = r
		return pre, x
	case *ir.Unary:
		p, op := st.transformExpr(x.Operand)
		x.Operand = op
		return p, x
	case *ir.Ternary:
		var pre []ir.Stmt
		p, c := st.transformExpr(x.Cond)
		pre = append(pre, p...)
		x.Cond = c
		p, t := st.transformExpr(x.Then)
		pre = append(pre, p...)
		x.Then = t
		p, el := st.transformExpr(x.Else)
		pre = append(pre, p...)
		x.Else = el
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
	case *ir.Spread:
		p, op := st.transformExpr(x.Operand)
		x.Operand = op
		return p, x
	case *ir.Lambda:
		if x.Func != nil {
			x.Func.Block = st.transformBlock(x.Func.Block)
		}
		return nil, x
	case *ir.Closure:
		if x.State != nil {
			var pre []ir.Stmt
			for i := range x.State.Fields {
				if x.State.Fields[i].Value != nil {
					p, v := st.transformExpr(x.State.Fields[i].Value)
					pre = append(pre, p...)
					x.State.Fields[i].Value = v
				}
			}
			if x.Func != nil {
				x.Func.Block = st.transformBlock(x.Func.Block)
			}
			return pre, x
		}
		if x.Func != nil {
			x.Func.Block = st.transformBlock(x.Func.Block)
		}
		return nil, x
	case *ir.Literal, *ir.Ident, *ir.ContextRead:
		// Terminal — no list-lambda call to lift.
		return nil, e
	default:
		panic(fmt.Sprintf("listLambdaState.transformExpr: unhandled %T", x))
	}
}

// isListLambdaCall reports whether n is a `xs.filter(f)` / `xs.map(f)`
// method call. The checker normalizes these as Func.Receiver == "list"
// (or "*" for cross-type generic methods) with Args[0] holding the
// receiver value.
func isListLambdaCall(n *ir.Call) bool {
	if n == nil || n.Func == nil {
		return false
	}
	if n.Func.Receiver != "list" && n.Func.Receiver != "*" {
		return false
	}
	if n.Func.Name != "filter" && n.Func.Name != "map" {
		return false
	}
	if len(n.Args) < 2 {
		return false
	}
	// Receiver must be a list with a known element type — otherwise we
	// can't declare the temp. Fall back to the codegen path.
	rt := n.Args[0].Value.ExprType()
	if rt == nil || rt.Kind != ir.TypeList || len(rt.Elems) == 0 {
		return false
	}
	return true
}

// liftListLambda emits the temp-list declaration, the for-loop that
// populates it, and returns an Ident referencing the temp. The
// returned pre-stmts must be placed before the using statement.
func (st *listLambdaState) liftListLambda(n *ir.Call) ([]ir.Stmt, ir.Expr) {
	xs := n.Args[0].Value
	fn := n.Args[1].Value
	elemT := xs.ExprType().Elems[0]

	var outElemT *ir.Type
	switch n.Func.Name {
	case "filter":
		outElemT = elemT
	case "map":
		// map's result type is list<U>; pick U from n.Type.
		if n.Type != nil && n.Type.Kind == ir.TypeList && len(n.Type.Elems) > 0 {
			outElemT = n.Type.Elems[0]
		} else {
			outElemT = ir.TypDyn
		}
	}
	outListT := ir.ListOf(outElemT)

	tempName := st.freshList()
	itemName := st.freshItem()

	// var __list<N> list<T> = []
	tempSym := &ir.Var{Name: tempName, Type: outListT, Synthesized: true}
	declStmt := &ir.LocalVar{
		Name: tempName,
		Type: outListT,
		Init: &ir.ListLit{Type: outListT},
		Sym:  tempSym,
	}

	// Ident{__item<N>} typed as elemT for use in lambda call + push.
	itemSym := &ir.LoopVar{Name: itemName, Type: elemT}
	itemIdent := &ir.Ident{Name: itemName, Type: elemT, Sym: itemSym, Synthesized: true}

	// Call to the user's lambda: (f)(item<N>)
	lambdaCall := &ir.Call{
		Type:   funcReturnType(fn),
		Callee: fn,
		Args:   []ir.CallArg{{Value: itemIdent}},
	}

	// `__list<N>.push(<pushed>)` emitted as a CallStmt — every Go
	// codegen lowers list.push into a self-reassigning `xs = append(xs, ...)`
	// statement, so we let that form do the work rather than wrapping
	// the call in an Assign (which would double-emit the `=`).
	pushFn := &ir.Func{Name: "push", Receiver: "list", Intrinsic: "ListPush"}
	tempIdent := &ir.Ident{Name: tempName, Type: outListT, Sym: tempSym, Synthesized: true}

	pushStmt := func(pushed ir.Expr) ir.Stmt {
		return &ir.CallStmt{
			Call: &ir.Call{
				Type: outListT,
				Func: pushFn,
				Args: []ir.CallArg{
					{Value: tempIdent},
					{Value: pushed},
				},
			},
		}
	}

	var loopBody []ir.Stmt
	switch n.Func.Name {
	case "filter":
		// if (f)(item<N>) { __list<N>.push(item<N>) }
		loopBody = []ir.Stmt{
			&ir.If{
				Cond: lambdaCall,
				Body: []ir.Stmt{pushStmt(itemIdent)},
			},
		}
	case "map":
		// __list<N>.push((f)(item<N>))
		loopBody = []ir.Stmt{pushStmt(lambdaCall)}
	}

	loop := &ir.For{
		Key:      itemName,
		KeySym:   itemSym,
		Iter:     xs,
		ElemType: elemT,
		Body:     loopBody,
	}

	return []ir.Stmt{declStmt, loop}, tempIdent
}

// funcReturnType extracts the return type of a function-valued expr.
// For lambdas it's Lambda.Return; for func-typed refs the carried type
// signature has it. Returns TypDyn if unavailable — callers downstream
// fall back to dyn semantics.
func funcReturnType(e ir.Expr) *ir.Type {
	if e == nil {
		return ir.TypDyn
	}
	if lam, ok := e.(*ir.Lambda); ok && lam.Func != nil && lam.Func.Return != nil {
		return lam.Func.Return
	}
	if t := e.ExprType(); t != nil && t.Kind == ir.TypeFunc && t.Sig != nil && t.Sig.Return != nil {
		return t.Sig.Return
	}
	return ir.TypDyn
}
