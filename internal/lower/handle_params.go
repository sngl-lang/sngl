package lower

import (
	"fmt"
	"strconv"

	"duckfam.us/sngl/ir"
)

// passHandleParams lands a program's write of a node's two-way prop where the
// host's report of it lands. Written through the handle, `bound.checked =
// false` under `:checked=shown` is `shown = false`; and a call that hands a
// node's `#id` to a function writing one of the node's two-way props through
// the parameter is inlined, so its writes are such writes:
// `details.open()`, where sngl:ui declares
//
//	func window.open(w window) {
//	    w.visible = true
//	}
//
// A method written outside its component's block reads the component only
// through the receiver it is passed, so `w.visible = true` is a write of the
// instance's prop, and it lands where a write through the handle would: the
// var the call site bound (`:visible=shown` writes `shown`), or, left as
// `details.visible`, the instance's own cell, which passImplicitState names.
// Called, the function would hold a handle no target can write a prop through.
//
// Before ImplicitState, whose rewrite of `details.visible` is what the second
// half needs; after PlatformExtensionBody, so the node a handle names is the
// one this target renders.
var passHandleParams = pass{
	name:    "HandleParams",
	enabled: func(Features) bool { return true },
	apply:   lowerHandleParams,
}

func lowerHandleParams(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	nodes := map[*ir.Var]*ir.NodeInst{}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if inst, ok := n.(*ir.NodeInst); ok && inst.Handle != nil {
			if _, dup := nodes[inst.Handle]; !dup {
				nodes[inst.Handle] = inst
			}
		}
		return nil
	})
	if len(nodes) == 0 {
		return nil
	}
	st := &handleParams{nodes: nodes, writes: map[*ir.Func]map[*ir.Param]bool{}}
	err := ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
		switch x := n.(type) {
		case *ir.Assign:
			if t := st.boundTarget(x.Target); t != nil {
				x.Target = t
			}
		case *ir.Toggle:
			if t := st.boundTarget(x.Target); t != nil {
				x.Target = t
			}
		case *ir.CallStmt:
			body, ok, err := st.inline(x.Call)
			if err != nil || !ok {
				return n, err
			}
			return &ir.If{Cond: &ir.Literal{Type: ir.TypBool, Value: "true"}, Body: body}, nil
		case *ir.Call:
			if st.err == nil && st.handsHandle(x) {
				st.err = fmt.Errorf("%s: `%s` writes a prop of the node it is handed, so it is inlined where it is called and has no value to give", handleCallPos(x), x.Func.Name)
			}
		}
		return n, nil
	})
	if err != nil {
		return err
	}
	return st.err
}

// boundTarget is what a write of target lands on when target is a two-way
// prop read through a node's handle and the node binds it: `bound.checked`
// under `:checked=shown` is `shown`. Unbound, it is nil and the write is left
// standing for passImplicitState, which names the instance's cell.
func (st *handleParams) boundTarget(target ir.Expr) ir.Expr {
	sel, ok := target.(*ir.Select)
	if !ok {
		return nil
	}
	n := st.handleOf(sel.Operand)
	if n == nil {
		return nil
	}
	for _, b := range n.Bindings {
		if b.PropName == sel.Field {
			return ir.CloneExprSharingDecls(b.Target)
		}
	}
	return nil
}

type handleParams struct {
	nodes  map[*ir.Var]*ir.NodeInst
	writes map[*ir.Func]map[*ir.Param]bool
	temps  int
	err    error
}

// written is the parameters fn writes a prop through.
func (st *handleParams) written(fn *ir.Func) map[*ir.Param]bool {
	if w, ok := st.writes[fn]; ok {
		return w
	}
	var out map[*ir.Param]bool
	note := func(target ir.Expr) {
		sel, ok := target.(*ir.Select)
		if !ok {
			return
		}
		id, ok := sel.Operand.(*ir.Ident)
		if !ok {
			return
		}
		if p, ok := id.Sym.(*ir.Param); ok {
			if out == nil {
				out = map[*ir.Param]bool{}
			}
			out[p] = true
		}
	}
	_ = ir.WalkStmts(fn.Block, func(s ir.Stmt) error {
		switch x := s.(type) {
		case *ir.Assign:
			note(x.Target)
		case *ir.Toggle:
			note(x.Target)
		}
		return nil
	})
	st.writes[fn] = out
	return out
}

// bindings is each of fn's parameters paired with what call hands it: the
// receiver, when the call is made through one, to the first.
func bindings(call *ir.Call) ([]*ir.Param, []ir.Expr) {
	fn := call.Func
	args := make([]ir.Expr, 0, len(fn.Params))
	if call.Receiver != nil {
		args = append(args, call.Receiver)
	}
	for _, a := range call.Args {
		args = append(args, a.Value)
	}
	if len(args) != len(fn.Params) {
		return nil, nil
	}
	return fn.Params, args
}

// handsHandle reports whether call hands a node's handle to a parameter its
// callee writes a prop through.
func (st *handleParams) handsHandle(call *ir.Call) bool {
	if call == nil || call.Func == nil || len(call.Func.Block) == 0 || call.Func.Intrinsic != "" || call.Func.Foreign.Name != "" {
		return false
	}
	written := st.written(call.Func)
	if len(written) == 0 {
		return false
	}
	params, args := bindings(call)
	for i, p := range params {
		if written[p] && st.handleOf(args[i]) != nil {
			return true
		}
	}
	return false
}

func (st *handleParams) handleOf(e ir.Expr) *ir.NodeInst {
	id, ok := e.(*ir.Ident)
	if !ok {
		return nil
	}
	h, ok := id.Sym.(*ir.Var)
	if !ok || !h.NodeHandle {
		return nil
	}
	return st.nodes[h]
}

// inline is the callee's body where call is made: each parameter its
// argument -- a handle as itself, anything else in a temp, so it is
// evaluated once and first -- and each write of a bound prop through a handle
// the var it is bound to.
func (st *handleParams) inline(call *ir.Call) ([]ir.Stmt, bool, error) {
	if !st.handsHandle(call) {
		return nil, false, nil
	}
	fn := call.Func
	var returns bool
	_ = ir.WalkStmts(fn.Block, func(s ir.Stmt) error {
		if _, ok := s.(*ir.Return); ok {
			returns = true
		}
		return nil
	})
	if returns {
		return nil, false, fmt.Errorf("%s: `%s` writes a prop of the node it is handed, so it is inlined where it is called, and cannot return from the middle", handleCallPos(call), fn.Name)
	}
	params, args := bindings(call)
	var out []ir.Stmt
	subst := map[*ir.Param]func() ir.Expr{}
	nodeOf := map[*ir.Param]*ir.NodeInst{}
	for i, p := range params {
		arg := args[i]
		if n := st.handleOf(arg); n != nil {
			nodeOf[p] = n
			subst[p] = func() ir.Expr { return ir.CloneExprSharingDecls(arg) }
			continue
		}
		tmp := &ir.Var{Name: "__arg" + strconv.Itoa(st.temps), Type: p.Type, Init: ir.CloneExprSharingDecls(arg), Synthesized: true}
		st.temps++
		out = append(out, &ir.LocalVar{Name: tmp.Name, Type: tmp.Type, Init: tmp.Init, Sym: tmp})
		subst[p] = func() ir.Expr { return &ir.Ident{Name: tmp.Name, Type: tmp.Type, Sym: tmp} }
	}
	body := deepCloneStmts(fn.Block)
	// A bound prop's write goes to the var it is bound to; an unbound one is
	// left a write through the handle, which passImplicitState names.
	bound := func(e ir.Expr) ir.Expr {
		sel, ok := e.(*ir.Select)
		if !ok {
			return nil
		}
		id, ok := sel.Operand.(*ir.Ident)
		if !ok {
			return nil
		}
		p, ok := id.Sym.(*ir.Param)
		if !ok || nodeOf[p] == nil {
			return nil
		}
		for _, b := range nodeOf[p].Bindings {
			if b.PropName == sel.Field {
				return ir.CloneExprSharingDecls(b.Target)
			}
		}
		return nil
	}
	err := ir.RewriteExprs(body, func(e ir.Expr) (ir.Expr, error) {
		if t := bound(e); t != nil {
			return t, ir.SkipDir
		}
		if id, ok := e.(*ir.Ident); ok {
			if p, ok := id.Sym.(*ir.Param); ok && subst[p] != nil {
				return subst[p](), nil
			}
		}
		return e, nil
	})
	if err != nil {
		return nil, false, err
	}
	return append(out, body...), true, nil
}
