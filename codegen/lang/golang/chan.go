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
	// The other three are answered outside the emitter registry, so the
	// completeness check is told here rather than left to conclude that a build
	// reaching one emits a call to nothing.
	codegen.DeclareLangImplements(langGo, chanMakeIntrinsic, chanRecvIntrinsic, chanSelectIntrinsic)
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
	// The arms have to be readable here, so the list is written at the call. A
	// list bound to a var declines and falls through to the ordinary named-call
	// path, which emits `m.select(arms)` -- caught by `format.Source` as a
	// parse error naming `select`, with no position. That is a poor diagnostic
	// rather than a wrong program, and a checker rule is what would improve it.
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
		var param *ir.Param
		if len(lam.Func.Params) == 1 {
			param = lam.Func.Params[0]
		}
		// Bind only what the body reads: Go rejects an unused case variable,
		// and a tick that ignores the time it arrived at is the common shape.
		if param != nil && blockReadsParam(lam.Func.Block, param) {
			out = append(out, "case "+param.Name+" := <-"+ch+":")
		} else {
			out = append(out, "case <-"+ch+":")
		}
		for _, s := range lam.Func.Block {
			out = append(out, gc.EvalStmt(s)...)
		}
	}
	return append(out, "}"), true
}

// blockReadsParam reports whether block reads the case's own parameter. An
// unused case variable is a compile error in Go, and the body is the only thing
// that can say whether there would be one.
//
// By symbol and not by name: a tick body that happens to mention something else
// spelled `at` -- a state var of the program's own, which emits as `m.at` --
// would otherwise bind a case variable nothing reads, and Go rejects the file.
func blockReadsParam(block []ir.Stmt, param *ir.Param) bool {
	found := false
	for _, s := range block {
		_ = ir.Walk(s, func(nd ir.Node) error {
			if id, ok := nd.(*ir.Ident); ok && id.Sym == ir.Symbol(param) {
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
