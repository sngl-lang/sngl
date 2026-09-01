package optimize

import (
	"fmt"
	"slices"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// inlineCall attempts to replace a function call with its inlined body.
// Returns nil if inlining is not applicable.
//
// Purity used to be the first condition. It is what licenses *folding* a call
// -- evaluating it at build time -- and this runs inside foldExpr, so it
// inherited folding's precondition. Inlining asks something else: replacing
// f(a) with the body under a puts the effects exactly where the call already
// was, in the same order, once. A readonly function is as inlinable as a pure
// one, and an async one already was -- purity never implied synchronous, and
// the settle lowering finds the await wherever it lands.
//
// What has to hold is about the arguments, and is checked below.
func inlineCall(call *ir.Call, ctx *evalCtx) ir.Expr {
	f := call.Func
	if f == nil {
		return nil
	}
	// A function the output contains keeps its call. Inlining is how a function
	// the output does *not* contain gets there -- nothing emits an imported
	// package's declarations, so a call to one is a call to a name the output
	// lacks, and until now every library function was either pure (inlined
	// here) or an intrinsic (emitted by the backend). sngl:remote/http's
	// transport is neither.
	//
	// Applied to non-pure declarations only, because folding still wants to see
	// through a pure wrapper wherever it was written, and because a program's
	// own functions are what its generated code is made of: a computed reading
	// state is readonly, and dissolving it into every prop that names it is a
	// different program to read.
	if f.Purity != ir.PurityPure && f.Pkg == "" {
		return nil
	}
	// A foreign declaration's body describes the function rather than
	// implementing it, so splicing it into the program says something that is
	// not true -- `#[foreign("js:./api", "add")] func add(a, b) => 0` answers 0
	// to nobody. `pure` is the one thing that licenses the body as the answer:
	// it says the call may be evaluated at build time, which is only possible
	// through the body written here.
	//
	// This is the whole of what purity was guarding, in the one place the guard
	// belongs -- on a declaration that stands for something else, not on every
	// declaration.
	if f.Foreign.Path != "" && f.Purity != ir.PurityPure {
		return nil
	}
	// A declaration marked #[intrinsic] keeps its call. The mark says a
	// backend may substitute its own implementation at the call site, and
	// inlining the SNGL body — which for most of them is a placeholder the
	// backend is expected to replace — would erase the thing it substitutes
	// for. Constant arguments still fold, through the intrinsic itself.
	if f.Intrinsic != "" {
		return nil
	}
	// A query is substituted at its call site too, by passQuery, and into
	// something its body is only half of: the body becomes the thunk, and the
	// arguments become the key beside it. Inlining the body here would leave the
	// fetch where the box belongs -- `res = http.Get(url)` typed as the box that
	// no longer exists.
	if f.Query {
		return nil
	}
	if len(f.TypeParams) > 0 {
		return nil // skip generic functions
	}
	if len(f.Block) != 1 {
		return nil // only inline single-expression functions
	}
	ret, ok := f.Block[0].(*ir.Return)
	if !ok || ret.Value == nil {
		return nil
	}
	if callsFunc(ret.Value, f) {
		return nil // skip directly recursive functions
	}
	// Mutual-recursion guard: if this function is already on the inlining
	// stack (i.e., we reached it via another inlineCall expansion), skip.
	// Without this, isEven→isOdd→isEven→… expands forever when parameters
	// are unbound (no concrete value to short-circuit the ternary).
	if ctx != nil && ctx.inliningFuncs != nil {
		if ctx.inliningFuncs[f] {
			return nil
		}
	}
	// Skip inlining bodies that still contain a ContextRead. NoContext (in
	// lower) is what threads the locale context through wrapper bodies and
	// rewrites ContextRead to Ident(__ctx_<name>); it runs AFTER the first
	// optimize pass. Inlining a ContextRead-bearing body during optimize1
	// would splice the unthreaded ContextRead into the user's call site,
	// where NoContext later sees a bare ContextRead with no wrapper
	// boundary and the optimizer's substituteParams can't reconcile the
	// hidden param. The guard becomes inert after NoContext (post-lower
	// bodies no longer contain ContextRead) so optimize2 can inline freely.
	if containsContextRead(ret.Value) {
		return nil
	}

	// Build substitution map: param → argument expression.
	subs := make(map[*ir.Param]ir.Expr, len(f.Params))
	for i, p := range f.Params {
		if i < len(call.Args) {
			subs[p] = call.Args[i].Value
		} else if p.Default != nil {
			subs[p] = p.Default
		} else {
			return nil // missing argument
		}
	}

	// The condition purity was standing in for, and standing in the wrong
	// place: substitution copies the argument to wherever the parameter is
	// read, so a parameter read twice evaluates its argument twice and one
	// read nowhere drops it. Both are the argument's problem, not the
	// function's -- `double(x) => x + x` is as pure as they come, and
	// `double(bump())` counted by two.
	for p, arg := range subs {
		if effectFree(arg) {
			continue
		}
		if paramReads(ret.Value, p) != 1 {
			return nil
		}
	}

	// Clone the return expression and substitute parameters.
	result := cloneExpr(ret.Value)
	result = substituteParams(result, subs)
	return result
}

// callsFunc reports whether the expression contains a call to the given function.
func callsFunc(e ir.Expr, target *ir.Func) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *ir.Call:
		if x.Func == target {
			return true
		}
		if callsFunc(x.Receiver, target) {
			return true
		}
		for _, a := range x.Args {
			if callsFunc(a.Value, target) {
				return true
			}
		}
	case *ir.Binary:
		return callsFunc(x.Left, target) || callsFunc(x.Right, target)
	case *ir.Unary:
		return callsFunc(x.Operand, target)
	case *ir.Ternary:
		return callsFunc(x.Cond, target) || callsFunc(x.Then, target) || callsFunc(x.Else, target)
	case *ir.Conversion:
		return callsFunc(x.Operand, target)
	case *ir.Select:
		return callsFunc(x.Operand, target)
	case *ir.Index:
		return callsFunc(x.Operand, target) || callsFunc(x.Idx, target)
	case *ir.ListLit:
		for _, el := range x.Elems {
			if callsFunc(el, target) {
				return true
			}
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			if callsFunc(f.Value, target) {
				return true
			}
		}
	case *ir.Spread:
		return callsFunc(x.Operand, target)
	case *ir.Lambda:
		for _, s := range x.Func.Block {
			if callsFuncStmt(s, target) {
				return true
			}
		}
	case *ir.Closure:
		if x.Func != nil {
			for _, s := range x.Func.Block {
				if callsFuncStmt(s, target) {
					return true
				}
			}
		}
	case *ir.MapLitIR:
		for _, kv := range x.Entries {
			if callsFunc(kv.Key, target) || callsFunc(kv.Value, target) {
				return true
			}
		}
	case *ir.Literal, *ir.Ident, *ir.ContextRead:
		// No nested calls possible.
	default:
		panic(fmt.Sprintf("callsFunc: unhandled expr %T", x))
	}
	return false
}

func callsFuncStmt(s ir.Stmt, target *ir.Func) bool {
	switch n := s.(type) {
	case *ir.Return:
		return callsFunc(n.Value, target)
	case *ir.If:
		if callsFunc(n.Cond, target) {
			return true
		}
		for _, s := range n.Body {
			if callsFuncStmt(s, target) {
				return true
			}
		}
		for _, s := range n.Else {
			if callsFuncStmt(s, target) {
				return true
			}
		}
	case *ir.For:
		if callsFunc(n.Iter, target) {
			return true
		}
		for _, s := range n.Body {
			if callsFuncStmt(s, target) {
				return true
			}
		}
	case *ir.NodeInst:
		for _, p := range n.Props {
			if callsFunc(p.Value, target) {
				return true
			}
		}
		for _, child := range n.Children {
			if callsFuncStmt(child, target) {
				return true
			}
		}
		for _, h := range n.Handlers {
			if h.Func != nil {
				for _, s := range h.Func.Block {
					if callsFuncStmt(s, target) {
						return true
					}
				}
			}
		}
	case *ir.Window:
		for _, s := range n.Body {
			if callsFuncStmt(s, target) {
				return true
			}
		}
	case *ir.Assign:
		return callsFunc(n.Value, target)
	case *ir.CallStmt:
		if n.Call != nil {
			return callsFunc(n.Call.Receiver, target) || callsFuncInArgs(n.Call.Args, target)
		}
	case *ir.LocalVar:
		return callsFunc(n.Init, target)
	case *ir.Emit:
		return callsFuncInArgs(n.Args, target)
	case *ir.Toggle:
		return callsFunc(n.Target, target)
	case *ir.SlotInst:
		for _, c := range n.Children {
			if callsFuncStmt(c, target) {
				return true
			}
		}
	case *ir.ErrorBoundary:
		for _, c := range n.Children {
			if callsFuncStmt(c, target) {
				return true
			}
		}
	case *ir.ContextProvider:
		if callsFunc(n.Value, target) {
			return true
		}
		for _, c := range n.Children {
			if callsFuncStmt(c, target) {
				return true
			}
		}
	case *ir.CanvasRedrawStmt:
		// No func calls.
	default:
		panic(fmt.Sprintf("callsFuncStmt: unhandled stmt %T", n))
	}
	return false
}

func callsFuncInArgs(args []ir.CallArg, target *ir.Func) bool {
	for _, a := range args {
		if callsFunc(a.Value, target) {
			return true
		}
	}
	return false
}

// containsContextRead reports whether e (or any subexpression) is a
// *ir.ContextRead. Used by inlineCall to defer inlining a context-reading
// wrapper body until after NoContext (in lower) has threaded the hidden
// context param and rewritten the ContextRead to a plain Ident.
func containsContextRead(e ir.Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *ir.ContextRead:
		return true
	case *ir.Call:
		if containsContextRead(x.Receiver) {
			return true
		}
		for _, a := range x.Args {
			if containsContextRead(a.Value) {
				return true
			}
		}
	case *ir.Binary:
		return containsContextRead(x.Left) || containsContextRead(x.Right)
	case *ir.Unary:
		return containsContextRead(x.Operand)
	case *ir.Ternary:
		return containsContextRead(x.Cond) || containsContextRead(x.Then) || containsContextRead(x.Else)
	case *ir.Conversion:
		return containsContextRead(x.Operand)
	case *ir.Select:
		return containsContextRead(x.Operand)
	case *ir.Index:
		return containsContextRead(x.Operand) || containsContextRead(x.Idx)
	case *ir.ListLit:
		if slices.ContainsFunc(x.Elems, containsContextRead) {
			return true
		}
	case *ir.MapLitIR:
		for _, kv := range x.Entries {
			if containsContextRead(kv.Key) || containsContextRead(kv.Value) {
				return true
			}
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			if containsContextRead(f.Value) {
				return true
			}
		}
	case *ir.Spread:
		return containsContextRead(x.Operand)
	case *ir.Lambda, *ir.Closure:
		// Lambda/closure bodies are opaque to this scan; the body is
		// rewritten when NoContext processes its enclosing func.
	case *ir.Literal, *ir.Ident:
		// No subexpressions.
	default:
		panic(fmt.Sprintf("containsContextRead: unhandled expr %T", x))
	}
	return false
}

// substituteParams walks the expression tree, replacing parameter references
// with the corresponding argument expressions.
// paramReads counts the references to p in e. Substitution copies the argument
// once per reference, so this is how many times the argument would be
// evaluated.
func paramReads(e ir.Expr, p *ir.Param) int {
	n := 0
	_ = ir.WalkExprs(e, func(x ir.Expr) error {
		if id, ok := x.(*ir.Ident); ok && id.Sym == ir.Symbol(p) {
			n++
		}
		return nil
	})
	return n
}

// effectFree reports whether e can be copied or dropped without changing what
// the program does: every call it contains is pure, and nothing in it writes.
//
// Cost is not the question -- duplicating an expensive pure call is a slower
// program, not a different one, and that was already true of every inline this
// pass has ever done.
func effectFree(e ir.Expr) bool {
	free := true
	_ = ir.WalkExprs(e, func(x ir.Expr) error {
		switch n := x.(type) {
		case *ir.Call:
			// A call with no resolved declaration says nothing about itself.
			if n.Func == nil || n.Func.Purity != ir.PurityPure {
				free = false
				return ir.SkipAll
			}
		case *ir.Unary:
			// &x aliases storage and *p reads through an alias: what either
			// answers depends on when it is evaluated.
			if n.Op == ast.UnaryAddr || n.Op == ast.UnaryDeref {
				free = false
				return ir.SkipAll
			}
		case *ir.ContextRead:
			// Threaded into a hidden parameter later; copying one moves it
			// across that rewrite.
			free = false
			return ir.SkipAll
		}
		return nil
	})
	return free
}

func substituteParams(e ir.Expr, subs map[*ir.Param]ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Ident:
		if p, ok := x.Sym.(*ir.Param); ok {
			if arg, found := subs[p]; found {
				return cloneExpr(arg)
			}
		}
		return x
	case *ir.Binary:
		x.Left = substituteParams(x.Left, subs)
		x.Right = substituteParams(x.Right, subs)
	case *ir.Unary:
		x.Operand = substituteParams(x.Operand, subs)
	case *ir.Ternary:
		x.Cond = substituteParams(x.Cond, subs)
		x.Then = substituteParams(x.Then, subs)
		x.Else = substituteParams(x.Else, subs)
	case *ir.Call:
		x.Receiver = substituteParams(x.Receiver, subs)
		for i := range x.Args {
			x.Args[i].Value = substituteParams(x.Args[i].Value, subs)
		}
	case *ir.Conversion:
		x.Operand = substituteParams(x.Operand, subs)
	case *ir.Select:
		x.Operand = substituteParams(x.Operand, subs)
	case *ir.Index:
		x.Operand = substituteParams(x.Operand, subs)
		x.Idx = substituteParams(x.Idx, subs)
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = substituteParams(x.Elems[i], subs)
		}
	case *ir.StructLit:
		for i := range x.Fields {
			x.Fields[i].Value = substituteParams(x.Fields[i].Value, subs)
		}
	case *ir.Spread:
		x.Operand = substituteParams(x.Operand, subs)
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = substituteParams(x.Entries[i].Key, subs)
			x.Entries[i].Value = substituteParams(x.Entries[i].Value, subs)
		}
	case *ir.Literal, *ir.ContextRead:
		// No parameter references.
	case *ir.Lambda, *ir.Closure:
		// Lambda bodies are opaque to substitution (treated as opaque
		// by cloneExpr too); their bodies are substituted when the
		// enclosing func is inlined.
	default:
		panic(fmt.Sprintf("substituteParams: unhandled expr %T", x))
	}
	return e
}

// cloneExpr creates a deep copy of an expression tree.
func cloneExpr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Literal:
		cp := *x
		return &cp
	case *ir.Ident:
		cp := *x
		return &cp
	case *ir.Binary:
		cp := *x
		cp.Left = cloneExpr(x.Left)
		cp.Right = cloneExpr(x.Right)
		return &cp
	case *ir.Unary:
		cp := *x
		cp.Operand = cloneExpr(x.Operand)
		return &cp
	case *ir.Ternary:
		cp := *x
		cp.Cond = cloneExpr(x.Cond)
		cp.Then = cloneExpr(x.Then)
		cp.Else = cloneExpr(x.Else)
		return &cp
	case *ir.Call:
		cp := *x
		cp.Receiver = cloneExpr(x.Receiver)
		cp.Args = make([]ir.CallArg, len(x.Args))
		for i, a := range x.Args {
			cp.Args[i] = ir.CallArg{Name: a.Name, NamePos: a.NamePos, Value: cloneExpr(a.Value)}
		}
		return &cp
	case *ir.Conversion:
		cp := *x
		cp.Operand = cloneExpr(x.Operand)
		return &cp
	case *ir.Select:
		cp := *x
		cp.Operand = cloneExpr(x.Operand)
		return &cp
	case *ir.Index:
		cp := *x
		cp.Operand = cloneExpr(x.Operand)
		cp.Idx = cloneExpr(x.Idx)
		return &cp
	case *ir.ListLit:
		cp := *x
		cp.Elems = make([]ir.Expr, len(x.Elems))
		for i, el := range x.Elems {
			cp.Elems[i] = cloneExpr(el)
		}
		return &cp
	case *ir.StructLit:
		cp := *x
		cp.Fields = make([]ir.FieldInit, len(x.Fields))
		for i, f := range x.Fields {
			cp.Fields[i] = ir.FieldInit{Name: f.Name, Value: cloneExpr(f.Value), Spread: f.Spread}
		}
		return &cp
	case *ir.Spread:
		cp := *x
		cp.Operand = cloneExpr(x.Operand)
		return &cp
	case *ir.Lambda:
		// The body is not opaque any more — foldNodeInst folds a func-typed
		// prop's lambda so an unrolled loop reaches it — so each copy needs
		// its own. The parameters keep their identity: a reference in the
		// cloned body still names the parameter it was checked against.
		cp := *x
		if x.Func != nil {
			fn := *x.Func
			fn.Block = cloneStmts(x.Func.Block)
			cp.Func = &fn
		}
		return &cp
	case *ir.Closure:
		// Treat closures as opaque (matches Lambda).
		cp := *x
		return &cp
	case *ir.MapLitIR:
		cp := *x
		cp.Entries = make([]ir.MapEntry, len(x.Entries))
		for i, kv := range x.Entries {
			cp.Entries[i] = ir.MapEntry{Key: cloneExpr(kv.Key), Value: cloneExpr(kv.Value)}
		}
		return &cp
	case *ir.ContextRead:
		cp := *x
		return &cp
	default:
		panic(fmt.Sprintf("cloneExpr: unhandled expr %T", x))
	}
}

// cloneStmt creates a deep copy of a statement.
func cloneStmt(s ir.Stmt) ir.Stmt {
	if s == nil {
		return nil
	}
	switch n := s.(type) {
	case *ir.NodeInst:
		cp := *n
		cp.Props = make([]ir.Arg, len(n.Props))
		for i, p := range n.Props {
			cp.Props[i] = ir.Arg{Name: p.Name, Value: cloneExpr(p.Value)}
		}
		cp.Handlers = make([]ir.EventHandler, len(n.Handlers))
		for i, h := range n.Handlers {
			cp.Handlers[i] = h
			// The handler's body is folded against whatever the copy is being
			// made for -- an unrolled loop's iteration, an inlined component's
			// arguments -- so each copy needs a body of its own. Sharing one
			// meant the first fold substituted its values into it and every
			// later copy found nothing left to substitute: twenty keypad keys,
			// each reporting the press of the first.
			if h.Func != nil {
				fn := *h.Func
				fn.Block = cloneStmts(h.Func.Block)
				cp.Handlers[i].Func = &fn
			}
		}
		cp.Children = cloneStmts(n.Children)
		return &cp
	case *ir.If:
		cp := *n
		cp.Cond = cloneExpr(n.Cond)
		cp.Body = cloneStmts(n.Body)
		cp.Else = cloneStmts(n.Else)
		return &cp
	case *ir.For:
		cp := *n
		cp.Iter = cloneExpr(n.Iter)
		cp.Body = cloneStmts(n.Body)
		cp.Else = cloneStmts(n.Else)
		return &cp
	case *ir.Assign:
		cp := *n
		cp.Value = cloneExpr(n.Value)
		return &cp
	case *ir.CallStmt:
		cp := *n
		if n.Call != nil {
			call := *n.Call
			call.Receiver = cloneExpr(n.Call.Receiver)
			call.Args = make([]ir.CallArg, len(n.Call.Args))
			for i, a := range n.Call.Args {
				call.Args[i] = ir.CallArg{Name: a.Name, NamePos: a.NamePos, Value: cloneExpr(a.Value)}
			}
			cp.Call = &call
		}
		return &cp
	case *ir.LocalVar:
		cp := *n
		cp.Init = cloneExpr(n.Init)
		return &cp
	case *ir.Return:
		cp := *n
		cp.Value = cloneExpr(n.Value)
		return &cp
	case *ir.Emit:
		cp := *n
		cp.Args = make([]ir.CallArg, len(n.Args))
		for i, a := range n.Args {
			cp.Args[i] = ir.CallArg{Name: a.Name, NamePos: a.NamePos, Value: cloneExpr(a.Value)}
		}
		return &cp
	case *ir.Toggle:
		cp := *n
		cp.Target = cloneExpr(n.Target)
		return &cp
	case *ir.SlotInst:
		cp := *n
		cp.Children = cloneStmts(n.Children)
		return &cp
	case *ir.ContextProvider:
		cp := *n
		cp.Value = cloneExpr(n.Value)
		cp.Children = cloneStmts(n.Children)
		return &cp
	case *ir.Window:
		cp := *n
		cp.Href = cloneExpr(n.Href)
		cp.Title = cloneExpr(n.Title)
		cp.Favicon = cloneExpr(n.Favicon)
		cp.Body = cloneStmts(n.Body)
		return &cp
	case *ir.ErrorBoundary:
		cp := *n
		cp.Children = cloneStmts(n.Children)
		return &cp
	case *ir.CanvasRedrawStmt:
		cp := *n
		return &cp
	default:
		panic(fmt.Sprintf("cloneStmt: unhandled stmt %T", n))
	}
}

func cloneStmts(stmts []ir.Stmt) []ir.Stmt {
	if stmts == nil {
		return nil
	}
	out := make([]ir.Stmt, len(stmts))
	for i, s := range stmts {
		out[i] = cloneStmt(s)
	}
	return out
}
