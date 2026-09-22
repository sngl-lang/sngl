package checker

import "slices"

import "git.duckfam.us/jonathan/sngl/ir"

// analyzeErrors runs error-effect analysis on the package after type
// checking. It computes CanError on each function via fixed-point over
// call edges, then walks the visual and function trees to set ErrorMode
// and ResolvedHandler on every fallible call site.
//
// Fallibility sources:
//   - Intrinsic "error.raise" (user raise).
//   - Func.HasErrorReturn (native import returning (T, error)).
//   - Call to any Func with CanError set.
//
// Handler resolution, innermost-first: per-call @error on the call,
// nearest enclosing boundary @error, window @error. No handler
// found ⇒ ErrorPropagateNative. Calls inside a function body (not an
// event handler) default to ErrorBubble — the enclosing fallible
// function threads the error back to its caller, which resolves.
func (c *checker) analyzeErrors() {
	pkg := c.pkg
	if pkg == nil {
		return
	}

	// Phase 1: fixed-point CanError over all package/component/window funcs.
	funcs := allFuncs(pkg)
	for {
		changed := false
		for _, fn := range funcs {
			if fn.CanError {
				continue
			}
			if blockHasErrorOp(fn.Block) {
				fn.CanError = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	// Phase 2: walk visual trees with a scope stack of error handlers.
	for _, w := range pkg.Windows {
		var scope []*ir.EventHandler
		if w.ErrorHandler != nil {
			scope = append(scope, w.ErrorHandler)
		}
		walkVisualErrors(w.Children, scope)
	}
	for _, comp := range pkg.Components {
		walkVisualErrors(comp.Body, nil)
	}

	// Phase 3: walk function bodies — fallible calls inside bubble up.
	for _, fn := range funcs {
		resolveCallsInBlock(fn.Block, nil, true)
	}
}

// allFuncs gathers every ir.Func in the package, including component- and
// window-scoped functions. Lambdas and event-handler funcs are handled as
// part of the statement walk (they do not live in these lists).
func allFuncs(pkg *ir.Package) []*ir.Func {
	var out []*ir.Func
	out = append(out, pkg.Funcs...)
	for _, comp := range pkg.Components {
		out = append(out, comp.Funcs...)
	}
	return out
}

// blockHasErrorOp reports whether any statement in the block contains a
// fallible call. Used by the CanError fixed-point.
func blockHasErrorOp(stmts []ir.Stmt) bool {
	return slices.ContainsFunc(stmts, stmtHasErrorOp)
}

func stmtHasErrorOp(s ir.Stmt) bool {
	switch x := s.(type) {
	case *ir.CallStmt:
		if x.Call != nil && callIsFallible(x.Call) {
			return true
		}
		// Per-call @error absorbs fallibility at this site, so the
		// enclosing function does not become fallible through this call.
		if x.Call != nil && exprContainsFallibleCall(x.Call, true) {
			return true
		}
	case *ir.Assign:
		return exprContainsFallibleCall(x.Value, false)
	case *ir.LocalVar:
		return exprContainsFallibleCall(x.Init, false)
	case *ir.Return:
		return exprContainsFallibleCall(x.Value, false)
	case *ir.If:
		if exprContainsFallibleCall(x.Cond, false) {
			return true
		}
		return blockHasErrorOp(x.Body) || blockHasErrorOp(x.Else)
	case *ir.For:
		if exprContainsFallibleCall(x.Iter, false) {
			return true
		}
		return blockHasErrorOp(x.Body) || blockHasErrorOp(x.Else)
	}
	return false
}

// exprContainsFallibleCall walks an expression looking for a fallible call.
// If skipPerCallAbsorbed is true, a top-level Call with an ErrorHandler is
// considered absorbed (its fallibility does not mark the enclosing func).
func exprContainsFallibleCall(e ir.Expr, skipPerCallAbsorbed bool) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *ir.Call:
		if callIsFallible(x) {
			if skipPerCallAbsorbed && x.ErrorHandler != nil {
				return false
			}
			return true
		}
		for _, a := range x.Args {
			if exprContainsFallibleCall(a.Value, false) {
				return true
			}
		}
		if x.Receiver != nil && exprContainsFallibleCall(x.Receiver, false) {
			return true
		}
	case *ir.Binary:
		return exprContainsFallibleCall(x.Left, false) || exprContainsFallibleCall(x.Right, false)
	case *ir.Unary:
		return exprContainsFallibleCall(x.Operand, false)
	case *ir.Ternary:
		return exprContainsFallibleCall(x.Cond, false) ||
			exprContainsFallibleCall(x.Then, false) ||
			exprContainsFallibleCall(x.Else, false)
	case *ir.Conversion:
		return exprContainsFallibleCall(x.Operand, false)
	case *ir.Select:
		return exprContainsFallibleCall(x.Operand, false)
	case *ir.Index:
		return exprContainsFallibleCall(x.Operand, false) || exprContainsFallibleCall(x.Idx, false)
	case *ir.ListLit:
		for _, el := range x.Elems {
			if exprContainsFallibleCall(el, false) {
				return true
			}
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			if exprContainsFallibleCall(f.Value, false) {
				return true
			}
		}
	case *ir.Spread:
		return exprContainsFallibleCall(x.Operand, false)
	}
	return false
}

func callIsFallible(call *ir.Call) bool {
	if call == nil || call.Func == nil {
		return false
	}
	if ir.IsErrorRaiseFunc(call.Func) {
		return true
	}
	if call.Func.HasErrorReturn {
		return true
	}
	return call.Func.CanError
}

// isRaiseFunc identifies the stdlib error.raise function. Stdlib wrapper

// walkVisualErrors traverses a visual subtree (window body / component body)
// maintaining a scope stack of error handlers. For each event handler
// attached to a NodeInst, walks the handler body resolving fallible calls
// against the current scope. Nested errorBoundaries push their handler.
func walkVisualErrors(stmts []ir.Stmt, scope []*ir.EventHandler) {
	for _, s := range stmts {
		// A boundary is the one transparent statement that is not: it pushes
		// its handler onto the scope, which is the whole of what it does. The
		// fallback is under that handler too -- passBoundaryFailed lowers it to
		// `if __failed { FALLBACK } else { CONTENT }` in place of the content,
		// so it sits exactly where the content sat.
		if b, ok := s.(*ir.ErrorBoundary); ok {
			inner := scope
			if b.Handler != nil {
				inner = append(append([]*ir.EventHandler{}, b.Handler), scope...)
			}
			walkVisualErrors(b.Children, inner)
			walkVisualErrors(b.Failed, inner)
			continue
		}
		// The rest of treeTransparent's list carries the scope unchanged. This
		// walk had its own copy of it and was missing the context provider, so
		// a raise from a handler under one resolved past every boundary it was
		// written inside and came out uncaught.
		if blocks, _, ok := treeTransparent(s); ok {
			for _, b := range blocks {
				walkVisualErrors(b, scope)
			}
			continue
		}
		switch x := s.(type) {
		case *ir.NodeInst:
			for i := range x.Handlers {
				h := &x.Handlers[i]
				if h.Func != nil && blockHasErrorOp(h.Func.Block) {
					h.CanError = true
				}
				resolveCallsInBlock(h.Func.Block, scope, false)
			}
			walkVisualErrors(x.Children, scope)
		case *ir.SlotInst:
			walkVisualErrors(x.Children, scope)
		case *ir.CallStmt:
			resolveCall(x.Call, scope, false)
		}
	}
}

// resolveCallsInBlock walks statements inside an event handler body or a
// function body, resolving each fallible call. bubble=true marks calls
// inside function bodies (no surrounding handler scope known statically —
// the enclosing fallible function will propagate the error upward).
func resolveCallsInBlock(stmts []ir.Stmt, scope []*ir.EventHandler, bubble bool) {
	for _, s := range stmts {
		switch x := s.(type) {
		case *ir.CallStmt:
			resolveCall(x.Call, scope, bubble)
		case *ir.Assign:
			resolveCallsInExpr(x.Value, scope, bubble)
		case *ir.LocalVar:
			resolveCallsInExpr(x.Init, scope, bubble)
		case *ir.Return:
			resolveCallsInExpr(x.Value, scope, bubble)
		case *ir.If:
			resolveCallsInExpr(x.Cond, scope, bubble)
			resolveCallsInBlock(x.Body, scope, bubble)
			resolveCallsInBlock(x.Else, scope, bubble)
		case *ir.For:
			resolveCallsInExpr(x.Iter, scope, bubble)
			resolveCallsInBlock(x.Body, scope, bubble)
			resolveCallsInBlock(x.Else, scope, bubble)
		}
	}
}

func resolveCallsInExpr(e ir.Expr, scope []*ir.EventHandler, bubble bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Call:
		resolveCall(x, scope, bubble)
		for _, a := range x.Args {
			resolveCallsInExpr(a.Value, scope, bubble)
		}
		if x.Receiver != nil {
			resolveCallsInExpr(x.Receiver, scope, bubble)
		}
	case *ir.Binary:
		resolveCallsInExpr(x.Left, scope, bubble)
		resolveCallsInExpr(x.Right, scope, bubble)
	case *ir.Unary:
		resolveCallsInExpr(x.Operand, scope, bubble)
	case *ir.Ternary:
		resolveCallsInExpr(x.Cond, scope, bubble)
		resolveCallsInExpr(x.Then, scope, bubble)
		resolveCallsInExpr(x.Else, scope, bubble)
	case *ir.Conversion:
		resolveCallsInExpr(x.Operand, scope, bubble)
	case *ir.Select:
		resolveCallsInExpr(x.Operand, scope, bubble)
	case *ir.Index:
		resolveCallsInExpr(x.Operand, scope, bubble)
		resolveCallsInExpr(x.Idx, scope, bubble)
	case *ir.ListLit:
		for _, el := range x.Elems {
			resolveCallsInExpr(el, scope, bubble)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			resolveCallsInExpr(f.Value, scope, bubble)
		}
	case *ir.Spread:
		resolveCallsInExpr(x.Operand, scope, bubble)
	}
}

// resolveCall sets the error-mode + resolved handler on a single call.
// Non-fallible calls keep ErrorNone.
func resolveCall(call *ir.Call, scope []*ir.EventHandler, bubble bool) {
	if !callIsFallible(call) {
		return
	}
	if call.ErrorHandler != nil {
		call.ResolvedHandler = call.ErrorHandler
		call.ErrorMode = ir.ErrorPerCall
		return
	}
	if len(scope) > 0 {
		call.ResolvedHandler = scope[0]
		call.ErrorMode = ir.ErrorInvokeAndTerminate
		return
	}
	if bubble {
		call.ErrorMode = ir.ErrorBubble
		return
	}
	call.ErrorMode = ir.ErrorPropagateNative
}
