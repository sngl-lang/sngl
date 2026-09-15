package golang

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

const (
	chanMakeIntrinsic   = "go.makechan"
	chanCloseIntrinsic  = "go.close"
	chanRecvIntrinsic   = "go.recv"
	chanSelectIntrinsic = "go.select"
)

// The channel vocabulary sngl:language/go declares. `make` and `close` are Go
// builtins rather than package functions, so they carry no import path and a
// `#[go.native]` cannot name them -- an intrinsic is how a declaration reaches
// a spelling that is not an identifier in some package.
//
// `go.select` is not here: a select is a *statement* whose arms hold statement
// lists, and an IntrinsicEmitter renders one expression. See selectLines.
func init() {
	codegen.RegisterIntrinsic(langGo, chanCloseIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return "close(" + tr(args[0]) + ")", nil
	})
}

// MakeChanText is make's emission. It needs the type the call was declared to
// return rather than the types of its arguments -- `makeChan<T>()` has none --
// so it is answered from the call site instead of from a registered emitter.
func (gc *GoIRContext) MakeChanText(n *ir.Call) (string, bool) {
	if n == nil || n.Func == nil || n.Func.Intrinsic != chanMakeIntrinsic {
		return "", false
	}
	return "make(" + IRTypeToGo(n.Type) + ")", true
}

// selectLines writes a real `select`, with each arm's body inlined.
//
// Inlined rather than called: an arm may `return`, and that has to leave the
// goroutine the select runs in -- which is the whole point of a `done` arm.
// Wrapped in a closure it would return from the closure and the loop would go
// round again.
//
// That is also why a select cannot be an IntrinsicEmitter: one renders a single
// expression, and these arms are statement lists.
func (gc *GoIRContext) selectLines(n *ir.CallStmt) ([]string, bool) {
	c := n.Call
	if c == nil || c.Func == nil || c.Func.Intrinsic != chanSelectIntrinsic || len(c.Args) != 1 {
		return nil, false
	}
	// The arms have to be readable here, so the list is written at the call.
	// Anything else declines and reaches the intrinsic fallback, which stops
	// the build rather than emitting a select with no cases.
	list, ok := c.Args[0].Value.(*ir.ListLit)
	if !ok {
		return nil, false
	}
	out := []string{"select {"}
	for _, el := range list.Elems {
		arm, ok := el.(*ir.Call)
		if !ok || arm.Func == nil || arm.Func.Intrinsic != chanRecvIntrinsic || len(arm.Args) != 2 {
			return nil, false
		}
		lam, ok := arm.Args[1].Value.(*ir.Lambda)
		if !ok || lam.Func == nil {
			return nil, false
		}
		ch := gc.EvalExpr(arm.Args[0].Value)
		name := ""
		if len(lam.Func.Params) == 1 && lam.Func.Params[0] != nil {
			name = lam.Func.Params[0].Name
		}
		// Bind only what the body reads: Go rejects an unused case variable,
		// and a tick that ignores the time it arrived at is the common shape.
		if name != "" && blockReadsName(lam.Func.Block, name) {
			out = append(out, "case "+name+" := <-"+ch+":")
		} else {
			out = append(out, "case <-"+ch+":")
		}
		for _, s := range lam.Func.Block {
			out = append(out, gc.EvalStmt(s)...)
		}
	}
	return append(out, "}"), true
}

// blockReadsName reports whether any identifier in block is spelled name. An
// unused case variable is a compile error in Go, and the body is the only thing
// that can say whether there would be one.
func blockReadsName(block []ir.Stmt, name string) bool {
	found := false
	for _, s := range block {
		_ = ir.Walk(s, func(nd ir.Node) error {
			if id, ok := nd.(*ir.Ident); ok && id.Name == name {
				found = true
			}
			return nil
		})
		if found {
			return true
		}
	}
	return false
}
