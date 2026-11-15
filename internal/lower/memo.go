package lower

import (
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A derived function -- `func rows() list[Row]` over the component's state --
// is called by every update that reads it, and by every derived function
// built on it: one write of a var the Pokédex's moves read built the moves
// six times. Such a function keeps what it last returned, with the count of
// writes it was computed at, and answers that again until a write of a var
// it reads moves the count (memoState.bump). A scalar it is handed -- the
// locale passContext hands one reading it -- is kept beside the answer and
// compared too.
//
// It is kept only where that is sound: the function reads nothing but vars,
// consts and what it declares itself, and calls nothing that reads anything
// else (a readonly intrinsic reads the host -- the clock, a file). Nor is it
// kept where a var's initializer calls it, which a later initializer would
// change behind it, or where work async.start begins does, which in Go runs
// on a goroutine and would write the cell beside the drawing thread.
type memoState struct {
	epoch *ir.Var
	cells map[*ir.Func]*memoCell
	// reads is every tracked var a kept function reads, whose write moves
	// the count.
	reads map[*ir.Var]bool
}

type memoCell struct {
	value, at *ir.Var
	// args is the var each keyed parameter's last value is kept in, nil for
	// the receiver: the cells are its own.
	args []*ir.Var
}

// planMemo decides which functions keep their answer, before any update is
// injected, since each write of what they read moves the count.
func (st *reactivityState) planMemo() {
	ms := &memoState{cells: map[*ir.Func]*memoCell{}, reads: map[*ir.Var]bool{}}
	st.memo = ms
	state := map[*ir.Var]bool{}
	for _, v := range st.pkg.Vars {
		state[v] = true
	}
	for _, c := range st.pkg.Components {
		for _, v := range c.Vars {
			state[v] = true
		}
	}
	unkept := map[*ir.Func]bool{}
	var reach func(f *ir.Func)
	reach = func(f *ir.Func) {
		if f == nil || unkept[f] {
			return
		}
		unkept[f] = true
		calledIn(f.Block, reach)
	}
	for v := range state {
		if v.Init != nil {
			calledIn(v.Init, reach)
		}
	}
	_ = ir.Walk(st.pkg, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && c.Func != nil && c.Func.Intrinsic == AsyncStartIntrinsic {
			for _, a := range c.Args {
				calledIn(a.Value, reach)
			}
		}
		return nil
	})
	sc := &stableCheck{st: st, state: state, done: map[*ir.Func]stableResult{}, active: map[*ir.Func]bool{}}
	owned := ownedFuncs(st.pkg)
	consider := func(f *ir.Func, owner reactivityOwner) {
		if f.Synthesized || f.IsTest || unkept[f] || f.Return == nil {
			return
		}
		if k := f.Return.Kind; k != ir.TypeList && k != ir.TypeMap {
			return
		}
		_, comp := owner.(compOwner)
		for _, p := range f.Params {
			if !(p.Receiver && comp) && !keyable(p.Type) {
				return
			}
		}
		r := sc.check(f)
		if !r.stable || len(r.reads) == 0 {
			return
		}
		if ms.epoch == nil {
			ms.epoch = &ir.Var{Name: "__memoEpoch", Type: ir.TypInt, Init: &ir.Literal{Type: ir.TypInt, Value: "0"}, Synthesized: true}
			st.pkg.Vars = append(st.pkg.Vars, ms.epoch)
		}
		cell := &memoCell{
			value: &ir.Var{Name: "__memo_" + f.Name, Type: f.Return, Init: ir.ZeroExpr(f.Return), Synthesized: true},
			at:    &ir.Var{Name: "__memoAt_" + f.Name, Type: ir.TypInt, Init: &ir.Literal{Type: ir.TypInt, Value: "-1"}, Synthesized: true},
		}
		owner.addVar(cell.value)
		owner.addVar(cell.at)
		for i, p := range f.Params {
			if p.Receiver {
				cell.args = append(cell.args, nil)
				continue
			}
			arg := &ir.Var{Name: "__memoArg" + strconv.Itoa(i) + "_" + f.Name, Type: p.Type, Init: ir.ZeroExpr(p.Type), Synthesized: true}
			owner.addVar(arg)
			cell.args = append(cell.args, arg)
		}
		ms.cells[f] = cell
		for v := range r.reads {
			ms.reads[v] = true
		}
	}
	for _, c := range st.pkg.Components {
		for _, f := range c.Funcs {
			consider(f, compOwner{c})
		}
	}
	for _, f := range st.pkg.Funcs {
		if !owned[f] {
			consider(f, pkgOwner{pkg: st.pkg})
		}
	}
}

// bump is what a write of v runs before its updates: nothing, or a move of
// the count every kept function compares against.
func (ms *memoState) bump(v *ir.Var) []ir.Stmt {
	if ms == nil || ms.epoch == nil || v == nil || !ms.reads[v] {
		return nil
	}
	return []ir.Stmt{&ir.Assign{
		Target: ms.ident(ms.epoch),
		Op:     ast.AssignSet,
		Value:  &ir.Binary{Op: ast.BinAdd, Left: ms.ident(ms.epoch), Right: &ir.Literal{Type: ir.TypInt, Value: "1"}, Type: ir.TypInt},
	}}
}

func (ms *memoState) ident(v *ir.Var) *ir.Ident {
	return &ir.Ident{Name: v.Name, Type: v.Type, Sym: v, Synthesized: true}
}

// apply rewrites each kept function to answer its cell while the count is
// the one it was computed at, and to fill it at each return otherwise.
func (ms *memoState) apply() {
	for f, cell := range ms.cells {
		value, at := ms.ident(cell.value), ms.ident(cell.at)
		var current ir.Expr = &ir.Binary{Op: ast.BinEq, Left: at, Right: ms.ident(ms.epoch), Type: ir.TypBool}
		var keep []ir.Stmt
		for i, arg := range cell.args {
			if arg == nil {
				continue
			}
			p := f.Params[i]
			param := &ir.Ident{Name: p.Name, Type: p.Type, Sym: p}
			same := &ir.Binary{Op: ast.BinEq, Left: ms.ident(arg), Right: param, Type: ir.TypBool}
			current = &ir.Binary{Op: ast.BinAnd, Left: current, Right: same, Type: ir.TypBool}
			keep = append(keep, &ir.Assign{Target: ms.ident(arg), Op: ast.AssignSet, Value: &ir.Ident{Name: p.Name, Type: p.Type, Sym: p}})
		}
		// A lambda's return is the lambda's.
		inner := map[*ir.Return]bool{}
		_ = ir.Walk(f.Block, func(n ir.Node) error {
			if l, ok := n.(*ir.Lambda); ok && l.Func != nil {
				_ = ir.Walk(l.Func.Block, func(n ir.Node) error {
					if r, ok := n.(*ir.Return); ok {
						inner[r] = true
					}
					return nil
				})
			}
			return nil
		})
		body := ir.RewriteBlocks(f.Block, func(stmts []ir.Stmt) []ir.Stmt {
			var out []ir.Stmt
			for _, s := range stmts {
				ret, ok := s.(*ir.Return)
				if !ok || ret.Value == nil || inner[ret] {
					out = append(out, s)
					continue
				}
				out = append(out,
					&ir.Assign{Target: ms.ident(cell.value), Op: ast.AssignSet, Value: ret.Value},
					&ir.Assign{Target: ms.ident(cell.at), Op: ast.AssignSet, Value: ms.ident(ms.epoch)},
				)
				out = append(out, keep...)
				out = append(out, &ir.Return{Value: ms.ident(cell.value)})
			}
			return out
		})
		f.Block = append([]ir.Stmt{&ir.If{Cond: current, Body: []ir.Stmt{&ir.Return{Value: value}}}}, body...)
	}
}

// keyable reports whether a parameter of type t can be kept and compared.
func keyable(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeString, ir.TypeInt, ir.TypeBool:
		return true
	}
	return false
}

// calledIn hands each function root calls to fn, a lambda's calls included.
func calledIn(root any, fn func(*ir.Func)) {
	_ = ir.Walk(root, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Call:
			fn(x.Func)
		case *ir.Closure:
			fn(x.Func)
		}
		return nil
	})
}

type stableResult struct {
	stable bool
	reads  map[*ir.Var]bool
}

// stableCheck answers whether a function's result follows from tracked state
// alone, and which of it.
type stableCheck struct {
	st    *reactivityState
	state map[*ir.Var]bool
	done  map[*ir.Func]stableResult
	// active is the checks under way, and cyclic says one of them was
	// called back into.
	active map[*ir.Func]bool
	cyclic bool
}

func (sc *stableCheck) check(f *ir.Func) stableResult {
	if r, ok := sc.done[f]; ok {
		return r
	}
	// A call back into a function being checked answers for nothing more
	// than what that check is already reading; every answer that leaned on
	// it is partial until the outermost check ends, so none is kept.
	if sc.active[f] {
		sc.cyclic = true
		return stableResult{stable: true}
	}
	sc.active[f] = true
	defer delete(sc.active, f)
	r := stableResult{stable: f.Purity != ir.PurityMutates, reads: map[*ir.Var]bool{}}
	for _, v := range f.Reads {
		if !sc.readable(v, r.reads) {
			r.stable = false
		}
	}
	_ = ir.Walk(f.Block, func(n ir.Node) error {
		if !r.stable {
			return ir.SkipAll
		}
		switch x := n.(type) {
		case *ir.Ident:
			switch sym := x.Sym.(type) {
			case *ir.Var:
				r.stable = sc.readable(sym, r.reads)
			case *ir.Func:
				r.stable = sc.callee(sym, r.reads)
			}
		case *ir.ContextRead, *ir.Emit, *ir.Toggle:
			r.stable = false
		case *ir.Assign:
			if v, ok := rootIdentVar(x.Target); !ok || sc.state[v] {
				r.stable = false
			}
		case *ir.Closure:
			r.stable = sc.callee(x.Func, r.reads)
		case *ir.Call:
			r.stable = sc.call(x, r.reads)
		}
		return nil
	})
	if len(sc.active) == 1 {
		sc.cyclic = false
		sc.done[f] = r
	} else if !sc.cyclic {
		sc.done[f] = r
	}
	return r
}

// readable reports whether a read of v leaves a result that only a write of
// tracked state changes, recording the tracked var it is.
func (sc *stableCheck) readable(v *ir.Var, reads map[*ir.Var]bool) bool {
	if !sc.state[v] || v.IsConst {
		return true
	}
	if !sc.st.reactiveVars[v] || v.NodeHandle {
		return false
	}
	reads[v] = true
	return true
}

func (sc *stableCheck) call(c *ir.Call, reads map[*ir.Var]bool) bool {
	f := c.Func
	if f == nil {
		// A function value is a lambda this walk reads, or a local or a
		// parameter, which the walk read where it was bound.
		switch x := c.Callee.(type) {
		case *ir.Lambda:
			return true
		case *ir.Closure:
			return sc.callee(x.Func, reads)
		case *ir.Ident:
			switch sym := x.Sym.(type) {
			case *ir.Param:
				return true
			case *ir.Var:
				return !sc.state[sym]
			}
		}
		return false
	}
	if f.Intrinsic != "" {
		switch f.Purity {
		case ir.PurityReadonly:
			return false
		case ir.PurityMutates:
			if len(c.Args) == 0 {
				return false
			}
			v, ok := rootIdentVar(c.Args[0].Value)
			return ok && !sc.state[v]
		}
		return true
	}
	if !writtenBody(f) {
		return f.Purity == ir.PurityPure
	}
	return sc.callee(f, reads)
}

func (sc *stableCheck) callee(f *ir.Func, reads map[*ir.Var]bool) bool {
	if f == nil {
		return false
	}
	r := sc.check(f)
	for v := range r.reads {
		reads[v] = true
	}
	return r.stable
}

// rootIdentVar is the var a place is rooted at: `xs` of `xs[i].name`.
func rootIdentVar(e ir.Expr) (*ir.Var, bool) {
	for {
		switch x := e.(type) {
		case *ir.Select:
			e = x.Operand
		case *ir.Index:
			e = x.Operand
		case *ir.Ident:
			v, ok := x.Sym.(*ir.Var)
			return v, ok
		default:
			return nil, false
		}
	}
}
