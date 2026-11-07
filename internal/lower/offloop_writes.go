package lower

import (
	"strconv"

	"duckfam.us/sngl/ir"
)

// passOffLoopWrites posts back each write of an owner's state that work run
// off the drawing thread makes: `async.start`'s, among them the goroutine
// passAsyncOffload gives a call that waits. A query's fetch is called there,
// so `fetch=func(k) { var r = get(k); mon = "b"; return r }` wrote `mon` from
// the goroutine -- a race on fyne and gtk4, and on bubbletea a write of the
// snapshot the goroutine reads, which nothing shows and nothing rekeys, where
// every other target writes the page's `mon`. Each such write is made on the
// loop instead, its value computed where it was written:
//
//	var __offw0 = "b"
//	async.do(func() { mon = __offw0; <what lowering put after the write> })
//
// with the updaters and settles passReactivity and passEffect put after it,
// which have to run where the write lands. What the work reads after the
// write is what it read before it, until the post has run: an answer that
// arrives at once is no earlier than a request's.
//
// Only what the work runs in place: a function literal it calls where it
// stands, as the fetch is once spliced, and not one it hands on, or posts.
// A named function the work calls runs on the state it was handed (#273).
//
// After passAsyncOffload, which makes the goroutines.
var passOffLoopWrites = pass{
	name:    "OffLoopWrites",
	enabled: func(c Features) bool { return !c.AsyncCalls },
	apply:   lowerOffLoopWrites,
}

// lowerOffLoopWrites reads AsyncPost: a target with no way back to its loop
// had every waiting call refused by passAsyncOffload, so it starts no work.
func lowerOffLoopWrites(pkg *ir.Package, caps Features, _ Options) error {
	if pkg == nil || !caps.AsyncPost {
		return nil
	}
	w := &offLoopWrites{state: collectReactiveVars(pkg)}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		c, ok := n.(*ir.Call)
		if !ok || c.Func == nil || c.Func.Intrinsic != AsyncStartIntrinsic || len(c.Args) != 1 {
			return nil
		}
		if fn := callbackFunc(c.Args[0].Value); fn != nil {
			fn.Block = w.block(fn.Block)
		}
		return nil
	})
	return nil
}

type offLoopWrites struct {
	state map[*ir.Var]bool
	n     int
}

// block is stmts with each write of state posted.
func (w *offLoopWrites) block(stmts []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for i := 0; i < len(stmts); i++ {
		s := stmts[i]
		for _, b := range ir.TransparentBlocks(s) {
			*b = w.block(*b)
		}
		w.inPlace(s)
		if !w.writesState(s) {
			out = append(out, s)
			continue
		}
		post := []ir.Stmt{s}
		for i+1 < len(stmts) && synthesizedCall(stmts[i+1]) {
			i++
			post = append(post, stmts[i])
		}
		if a, ok := s.(*ir.Assign); ok {
			name := "__offw" + strconv.Itoa(w.n)
			w.n++
			t := a.Value.ExprType()
			sym := &ir.Var{Name: name, Type: t, Synthesized: true}
			out = append(out, &ir.LocalVar{Name: name, Type: t, Init: a.Value, Sym: sym})
			back := *a
			back.Value = &ir.Ident{Name: name, Type: t, Sym: sym, Synthesized: true}
			post[0] = &back
		}
		out = append(out, callStmt(AsyncDoIntrinsic, closure(post)))
	}
	return out
}

// inPlace posts the writes of each function literal s calls where it stands.
func (w *offLoopWrites) inPlace(s ir.Stmt) {
	_ = ir.Walk(s, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Lambda:
			// Handed on or posted, not run here.
			return ir.SkipDir
		case *ir.Call:
			if l, ok := x.Callee.(*ir.Lambda); ok && l.Func != nil {
				l.Func.Block = w.block(l.Func.Block)
			}
		}
		return nil
	})
}

// writesState reports whether s writes an owner's state: assigns it, toggles
// it, or calls a method mutating it, `log.push(x)`.
func (w *offLoopWrites) writesState(s ir.Stmt) bool {
	n := ir.Node(s)
	if cs, ok := s.(*ir.CallStmt); ok {
		n = cs.Call
	}
	for _, place := range placesWritten(n) {
		if v, ok := exprRoot(place).(*ir.Var); ok && w.state[v] {
			return true
		}
	}
	return false
}
