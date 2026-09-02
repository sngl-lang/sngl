package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passForElse rewrites an imperative `for … else` into a flag and a trailing
// `if`, which is the only shape a host loop can express.
//
//	__ran0 := false
//	for … {
//	    __ran0 = true
//	    …
//	}
//	if !__ran0 { … }
//
// Every backend already emits the three statements this leaves behind, so no
// language grows a case for the construct. Before this pass, the generic
// per-language statement path (codegen/irwalk) read a loop's head and body and
// nothing else, and a for-else written in a function body compiled to a loop
// with the else silently dropped -- so `for var x = xs { } else { }` returned
// the wrong answer in Go, JS and Kotlin while passing under the interpreter,
// which walks the IR and reads Else itself.
//
// One flag rather than a per-kind test. "The iterable was empty" is what the
// else case means for a loop that walks one, but the general statement of it
// is that the body never ran: a condition loop has no iterable to measure, and
// `len(xs) == 0` cannot be asked of an iter<T> without consuming it. Setting
// the flag as the body's first statement also settles `break`, which leaves a
// loop whose body did run.
//
// View bodies are left alone. There, a for-else is a structural conditional
// the platform emitters render themselves (one subtree when the loop produced
// nodes, the other when it did not), and a statement declared beside a node is
// not something a static renderer can write down -- the same bound passCSE
// documents at greater length.
//
// A lambda body is an imperative block wherever it appears, view prop
// included. By the time this runs, android's handler bodies are lambdas in
// NodeInst.Props rather than NodeInst.Handlers -- its primitives declare the
// callback as a prop, and platform-extension lowering has already moved it
// there -- so a hand-written descent through the view finds nothing at all on
// that target. (fyne declares a callback prop too but still carries an
// ir.EventHandler here, which is why it was not the one that caught this.)
var passForElse = pass{
	name:    "ForElse",
	enabled: func(Caps) bool { return true },
	apply:   lowerForElse,
}

func lowerForElse(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &forElseState{}
	for _, f := range pkg.Funcs {
		st.imperative(&f.Block)
	}
	for _, c := range pkg.Components {
		st.owner(c.Funcs, c.Vars, c.Timers, c.Body)
	}
	for _, w := range pkg.Windows {
		st.owner(w.Funcs, w.Vars, nil, w.Body)
		if w.ErrorHandler != nil && w.ErrorHandler.Func != nil {
			st.imperative(&w.ErrorHandler.Func.Block)
		}
	}
	for _, t := range pkg.Timers {
		if t.Handler != nil {
			st.imperative(&t.Handler.Block)
		}
	}
	for _, block := range lambdaBlocks(pkg) {
		st.imperative(block)
	}
	return nil
}

// lambdaBlocks is every lambda body in pkg, collected by the base traversal
// rather than by a second hand-written descent -- a lambda can sit in any
// expression, and the walk already knows where every expression is.
//
// Collected first and rewritten after, since the walk is read-only. The blocks
// overlap the ones above (a lambda written inside a function body is in both),
// which is harmless: a loop whose else has already been desugared no longer
// has one, so the second visit does nothing.
func lambdaBlocks(pkg *ir.Package) []*[]ir.Stmt {
	var out []*[]ir.Stmt
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if l, ok := n.(*ir.Lambda); ok && l.Func != nil {
			out = append(out, &l.Func.Block)
		}
		return nil
	})
	return out
}

type forElseState struct {
	counter int
}

// owner covers one component's or window's imperative blocks, and walks its
// view body for the handlers hanging off the nodes in it and nothing else.
func (st *forElseState) owner(funcs []*ir.Func, vars []*ir.Var, timers []*ir.Timer, body []ir.Stmt) {
	for _, f := range funcs {
		st.imperative(&f.Block)
	}
	for _, v := range vars {
		for _, h := range v.Handlers {
			if h.Func != nil {
				st.imperative(&h.Func.Block)
			}
		}
	}
	for _, t := range timers {
		if t.Handler != nil {
			st.imperative(&t.Handler.Block)
		}
	}
	st.handlersIn(body)
}

func (st *forElseState) handlersIn(stmts []ir.Stmt) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			for _, h := range n.Handlers {
				if h.Func != nil {
					st.imperative(&h.Func.Block)
				}
			}
			st.handlersIn(n.Children)
		case *ir.If:
			st.handlersIn(n.Body)
			st.handlersIn(n.Else)
		case *ir.For:
			st.handlersIn(n.Body)
			st.handlersIn(n.Else)
		case *ir.SlotInst:
			st.handlersIn(n.Children)
		case *ir.ErrorBoundary:
			st.handlersIn(n.Children)
		case *ir.ContextProvider:
			st.handlersIn(n.Children)
		case *ir.Window:
			st.handlersIn(n.Body)
		}
	}
}

// imperative rewrites one block in place, recursing into the blocks it holds.
func (st *forElseState) imperative(block *[]ir.Stmt) {
	out := make([]ir.Stmt, 0, len(*block))
	for _, s := range *block {
		switch n := s.(type) {
		case *ir.If:
			st.imperative(&n.Body)
			st.imperative(&n.Else)
		case *ir.For:
			st.imperative(&n.Body)
			st.imperative(&n.Else)
			if len(n.Else) > 0 {
				pre, post := st.desugar(n)
				out = append(out, pre, n, post)
				continue
			}
		}
		out = append(out, s)
	}
	*block = out
}

// desugar returns the flag to declare before the loop and the `if` to test
// after it, and clears the loop's else. The flag is set as the body's first
// statement so that no `continue` can skip it.
func (st *forElseState) desugar(n *ir.For) (ir.Stmt, ir.Stmt) {
	name := "__ran" + strconv.Itoa(st.counter)
	st.counter++
	sym := &ir.Var{Name: name, Type: ir.TypBool, Synthesized: true}
	read := func() ir.Expr { return &ir.Ident{Name: name, Sym: sym, Type: ir.TypBool} }
	pos := ast.Pos{}
	if n.AST != nil {
		pos = n.AST.Pos
	}

	decl := &ir.LocalVar{
		Name: name,
		Type: ir.TypBool,
		Init: &ir.Literal{Value: "false", Type: ir.TypBool},
		Sym:  sym,
	}
	n.Body = append([]ir.Stmt{&ir.Assign{
		Target: read(),
		Op:     ast.AssignSet,
		Value:  &ir.Literal{Value: "true", Type: ir.TypBool},
	}}, n.Body...)

	guard := &ir.If{
		AST:  &ast.IfStmt{Pos: pos},
		Cond: &ir.Unary{Op: ast.UnaryNot, Operand: read(), Type: ir.TypBool},
		Body: n.Else,
	}
	n.Else = nil
	return decl, guard
}
