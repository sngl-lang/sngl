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
// View bodies are left alone, because a statement declared beside a node is
// not something a static renderer can write down -- the same bound passCSE
// documents at greater length. There a for-else is the platform emitter's to
// render as a structural conditional: one subtree when the loop produced
// nodes, the other when it did not. Worth knowing that only bubbletea
// actually does today -- fyne, gtk4, android and html/none emit nothing at
// all for the else -- so leaving it alone here leaves it unrendered there,
// rather than handing it to something that handles it.
//
// Which blocks those are is imperativeBlocks' answer, shared with passCSE --
// including a lambda body wherever it appears, view prop included, which is
// the only way either pass reaches a handler on android.
var passForElse = pass{
	name:    "ForElse",
	enabled: func(Caps) bool { return true },
	apply:   lowerForElse,
}

func lowerForElse(pkg *ir.Package, _ Caps, _ Options) error {
	st := &forElseState{}
	for _, block := range imperativeBlocks(pkg) {
		st.imperative(block)
	}
	return nil
}

type forElseState struct {
	counter int
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
