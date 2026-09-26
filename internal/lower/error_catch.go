package lower

import (
	"fmt"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passErrorCatch wraps each handler body holding a call the checker resolved
// to a boundary's or a window's @error in one catch block (ir.If.Catch) whose
// handler is that @error. The raise then unwinds as the host's own exception
// does -- a panic in Go, a throw in JavaScript and Kotlin -- through every
// fallible caller between, and the handler runs with nothing after the raise
// running, in the function that raised or in the handler body that called it.
//
// Every call the block covers is left ErrorBubble. Two stay as they were and
// are caught where they are made (each language's catchAtCall): a call with a
// per-call @error, which answers for that call alone and lets the statements
// after it run; and a `fails` native, whose failure is its error result on Go
// and an ordinary host exception elsewhere rather than a raise. What stays
// ErrorInvokeAndTerminate otherwise is a raise in a view body -- the
// recursion bound -- which has no handler body to end and inlines the
// boundary's handler where it stands.
//
// A per-call handler answers for its own call and not for the arguments,
// which are evaluated before the call is made, so an argument that may raise
// is bound to a temp ahead of the statement.
//
// Always on: a raise is a raise on every target.
var passErrorCatch = pass{
	name:    "ErrorCatch",
	enabled: func(Features) bool { return true },
	apply:   lowerErrorCatch,
}

func lowerErrorCatch(pkg *ir.Package, _ Features, _ Options) error {
	blocks := imperativeBlocks(pkg)
	st := &errorCatchState{}
	for _, block := range blocks {
		st.perCall(block)
	}
	for _, block := range blocks {
		target, err := terminateTarget(*block)
		if err != nil {
			return err
		}
		if target == nil {
			continue
		}
		coverRaises(*block)
		*block = []ir.Stmt{catchBlock(*block, target)}
	}
	return nil
}

type errorCatchState struct {
	counter int
}

func catchBlock(body []ir.Stmt, h *ir.EventHandler) *ir.If {
	return &ir.If{Cond: &ir.Literal{Type: ir.TypBool, Value: "true"}, Body: body, Catch: h}
}

// perCall binds the raising arguments of each call statement with a per-call
// handler in the block, and in the blocks it holds.
func (st *errorCatchState) perCall(block *[]ir.Stmt) {
	out := make([]ir.Stmt, 0, len(*block))
	for _, s := range *block {
		switch n := s.(type) {
		case *ir.If:
			st.perCall(&n.Body)
			st.perCall(&n.Else)
		case *ir.For:
			st.perCall(&n.Body)
			st.perCall(&n.Else)
		case *ir.CallStmt:
			c := n.Call
			if c != nil && c.ErrorMode == ir.ErrorPerCall && c.ErrorHandler != nil && !ir.IsErrorRaiseFunc(c.Func) {
				out = append(out, st.bindRaisingArgs(c)...)
			}
		}
		out = append(out, s)
	}
	*block = out
}

// bindRaisingArgs moves the call's arguments into temps when one of them may
// raise, every argument so the order they are evaluated in is kept.
func (st *errorCatchState) bindRaisingArgs(c *ir.Call) []ir.Stmt {
	raises := false
	for _, a := range c.Args {
		if exprMayRaise(a.Value) {
			raises = true
			break
		}
	}
	if !raises {
		return nil
	}
	pre := make([]ir.Stmt, 0, len(c.Args))
	for i := range c.Args {
		v := c.Args[i].Value
		name := "__arg" + strconv.Itoa(st.counter)
		st.counter++
		typ := v.ExprType()
		sym := &ir.Var{Name: name, Type: typ, Synthesized: true}
		pre = append(pre, &ir.LocalVar{Name: name, Type: typ, Init: v, Sym: sym})
		c.Args[i].Value = &ir.Ident{Name: name, Sym: sym, Type: typ}
	}
	return pre
}

// exprMayRaise reports whether e makes a call effect analysis resolved, short
// of a lambda body: a lambda is a value, and what it raises is its caller's.
func exprMayRaise(e ir.Expr) bool {
	found := false
	_ = ir.Walk(e, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Lambda:
			return ir.SkipDir
		case *ir.Call:
			if x.ErrorMode != ir.ErrorNone {
				found = true
				return ir.SkipAll
			}
		}
		return nil
	})
	return found
}

// terminateTarget is the handler every raise the block resolved to a boundary
// or window answers to, reading through ifs and loops and into per-call
// handlers, whose raises are the statement's. Nil when there is none.
func terminateTarget(block []ir.Stmt) (*ir.EventHandler, error) {
	var target *ir.EventHandler
	var err error
	visitCoveredCalls(block, func(c *ir.Call) {
		if c.ErrorMode != ir.ErrorInvokeAndTerminate || c.ResolvedHandler == nil || err != nil {
			return
		}
		switch {
		case target == nil:
			target = c.ResolvedHandler
		case target != c.ResolvedHandler:
			// One handler body sits under one boundary, so every raise in it
			// resolves to the same place; two would need two catch points
			// and a decision about which statements each covers.
			err = fmt.Errorf("lower: one handler body resolves raises to two different handlers")
		}
	})
	return target, err
}

// coverRaises marks the calls a whole-body catch block covers as bubbling.
func coverRaises(block []ir.Stmt) {
	visitCoveredCalls(block, func(c *ir.Call) {
		if c.ErrorMode == ir.ErrorInvokeAndTerminate && (c.Func == nil || !c.Func.HasErrorReturn) {
			c.ErrorMode = ir.ErrorBubble
		}
	})
}

// visitCoveredCalls calls f with every call the block makes: in its
// statements, in the blocks they hold and in the per-call handlers the walk
// reaches through their calls, but not in a lambda body, which is a block of
// its own.
func visitCoveredCalls(block []ir.Stmt, f func(*ir.Call)) {
	for _, s := range block {
		_ = ir.Walk(s, func(n ir.Node) error {
			switch x := n.(type) {
			case *ir.Lambda:
				return ir.SkipDir
			case *ir.Call:
				f(x)
			}
			return nil
		})
	}
}
