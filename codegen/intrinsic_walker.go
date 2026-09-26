package codegen

import (
	"context"

	"git.duckfam.us/jonathan/sngl/ir"
)

// IntrinsicTranslator is implemented by codegen platforms that consume
// lowered IR. Each method returns a slice of IR statements (rather than
// target-language source), so the platform stays language-agnostic — a
// future C, Rust, or Python frontend can render the same IR using its
// own language renderer.
//
// Expression hooks (OnIter, OnCond) return ir.Expr for the same reason.
//
// ctx carries cross-cutting state (e.g. the current language renderer
// reference, per-call diagnostics, scope info). Implementations should
// treat ctx as opaque — only consume keys they registered themselves.
type IntrinsicTranslator interface {
	OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt
	// OnCreateComponent handles a `LocalVar id = lower.CreateComponent(...)`
	// — a non-inlinable (recursive) user component left in place by the
	// lower pass. Mutation-model platforms promote id to a Model field so
	// references to it elsewhere (qualified to m.id) resolve consistently.
	//
	// id names the INSTANCE, not the widget: a component that cannot be
	// inlined still declares state, and one cell per instance is what the
	// build-time `__instN` renaming cannot provide when the count is only
	// known at runtime. ComponentRoot is how the tree gets a node back.
	OnCreateComponent(ctx context.Context, id string, call *ir.Call) []ir.Stmt
	// OnComponentRoot handles `LocalVar id = lower.ComponentRoot(inst)`,
	// binding id to the node an instance renders as, so AppendChild has
	// something to attach. Separate from CreateComponent because an instance
	// outlives any one attachment: it is detached and reattached as its
	// position in the tree comes and goes.
	OnComponentRoot(ctx context.Context, id string, inst ir.Expr) []ir.Stmt
	// OnUpdateComponent handles `lower.UpdateComponent(inst, "prop", value)`
	// — a reactive prop crossing into a live instance. The platform patches
	// whatever inside the instance reads that prop. A prop the instance
	// cannot absorb is not routed here: lowering destroys and recreates the
	// instance instead, which is what the #[construct] mark selects.
	OnUpdateComponent(ctx context.Context, inst ir.Expr, prop string, value ir.Expr) []ir.Stmt
	// OnDestroyComponent handles `lower.DestroyComponent(inst)`: the instance
	// is going away for good. Effect teardowns, timer cancels, and whatever
	// the host needs to release. Distinct from RemoveChild, which only
	// unparents a node that may well be attached again.
	OnDestroyComponent(ctx context.Context, inst ir.Expr) []ir.Stmt
	OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt
	OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt
	OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt
	// OnDetachHandler undoes one OnAttachHandler, given the same handler
	// expression. A node that outlives the render that built it keeps the
	// handler that render gave it, and that handler closed over the iteration
	// -- so re-pointing it starts by taking the old one off.
	OnDetachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt
	OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt
	OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt
	OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt
	OnIter(ctx context.Context, iter ir.Expr) ir.Expr
	OnCond(ctx context.Context, cond ir.Expr) ir.Expr
	OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt
}

// ChildInserter is implemented by a platform whose container can put a child
// at a position rather than only at the end.
//
// Optional, and the only optional operation in the protocol: a keyed
// reconciliation inserting in the middle needs it, and a toolkit whose
// container appends is not wrong for lacking one. Declared as its own
// interface rather than a method on IntrinsicTranslator so a platform that
// cannot do it says so by not writing the method, instead of writing one that
// quietly does nothing -- which is the shape every silent-drop bug in this
// package has had.
//
// A platform implementing this must also declare the matching capability, or
// lowering will never emit the op it answers; a platform declaring the
// capability without implementing this emits an op nothing dispatches. The two
// are checked against each other in codegen's tests.
type ChildInserter interface {
	// OnInsertBefore puts child into parent immediately before ref. A ref the
	// parent does not hold, or a null ref, means the end -- the same rule the
	// DOM's insertBefore follows, so a reconciliation walking a desired order
	// can pass the next surviving node without checking whether there is one.
	OnInsertBefore(ctx context.Context, parent, child, ref ir.Expr) []ir.Stmt
}

// WalkLowered iterates the lowered IR statement sequence and dispatches
// each known shape to t. Returns the concatenated emissions in source
// order. ctx is passed through to every translator method; if the
// caller has no context, pass context.Background().
func WalkLowered(ctx context.Context, stmts []ir.Stmt, t IntrinsicTranslator) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		out = append(out, walkOne(ctx, s, t)...)
	}
	return out
}

func walkOne(ctx context.Context, s ir.Stmt, t IntrinsicTranslator) []ir.Stmt {
	switch n := s.(type) {
	case *ir.LocalVar:
		if call, ok := n.Init.(*ir.Call); ok {
			if isLowerIntrinsic(call, "CreateNode") {
				tag, _ := extractStringLit(call.Args[0].Value)
				return t.OnCreateNode(ctx, n.Name, tag)
			}
			if isLowerIntrinsic(call, "CreateComponent") {
				return t.OnCreateComponent(ctx, n.Name, walkComponentProps(ctx, call, t))
			}
			if isLowerIntrinsic(call, ir.NodeOpComponentRoot) && len(call.Args) == 1 {
				return t.OnComponentRoot(ctx, n.Name, call.Args[0].Value)
			}
		}
		if v, changed := walkExprLambdas(ctx, n.Init, t); changed {
			cp := *n
			cp.Init = v
			return []ir.Stmt{&cp}
		}
	case *ir.CallStmt:
		if n.Call != nil {
			switch {
			case isLowerIntrinsic(n.Call, "AppendChild"):
				return t.OnAppendChild(ctx, n.Call.Args[0].Value, n.Call.Args[1].Value)
			case isLowerIntrinsic(n.Call, "RemoveChild"):
				return t.OnRemoveChild(ctx, n.Call.Args[0].Value, n.Call.Args[1].Value)
			case isLowerIntrinsic(n.Call, "AttachHandler"):
				evt, _ := extractStringLit(n.Call.Args[1].Value)
				return t.OnAttachHandler(ctx, n.Call.Args[0].Value, evt,
					walkHandlerBody(ctx, n.Call.Args[2].Value, t))
			case isLowerIntrinsic(n.Call, ir.NodeOpDetachHandler) && len(n.Call.Args) == 3:
				evt, _ := extractStringLit(n.Call.Args[1].Value)
				return t.OnDetachHandler(ctx, n.Call.Args[0].Value, evt, n.Call.Args[2].Value)
			case isLowerIntrinsic(n.Call, ir.NodeOpUpdateComponent) && len(n.Call.Args) == 3:
				prop, _ := extractStringLit(n.Call.Args[1].Value)
				return t.OnUpdateComponent(ctx, n.Call.Args[0].Value, prop,
					walkHandlerBody(ctx, n.Call.Args[2].Value, t))
			case isLowerIntrinsic(n.Call, ir.NodeOpDestroyComponent) && len(n.Call.Args) == 1:
				return t.OnDestroyComponent(ctx, n.Call.Args[0].Value)
			case isLowerIntrinsic(n.Call, ir.NodeOpInsertBefore) && len(n.Call.Args) == 3:
				// Only a platform that declared the capability can be reached
				// by this op, so a translator without the interface here is a
				// capability declared and not implemented rather than an
				// ordinary absence.
				ins, ok := t.(ChildInserter)
				if !ok {
					panic("codegen: a platform emitted " + ir.NodeOpInsertBefore +
						" without implementing ChildInserter; the capability and the interface must agree")
				}
				return ins.OnInsertBefore(ctx, n.Call.Args[0].Value, n.Call.Args[1].Value, n.Call.Args[2].Value)
			}
			// A slot append as a bare call. push mutates its receiver and
			// returns nothing, so the lowering emits the call rather than an
			// assignment back to the slot; the Assign arm below still answers
			// the older shape.
			if n.Call.Func != nil && n.Call.Func.Intrinsic == "list.push" && len(n.Call.Args) == 2 {
				if id, ok := n.Call.Args[0].Value.(*ir.Ident); ok && id.Synthesized {
					if v := resolveSlotVar(id); v != nil {
						return t.OnSlotAppend(ctx, v, n.Call.Args[1].Value)
					}
				}
			}
			// Any argument may be a callback, and everything a handler would
			// have done is inside it. This was gated on an offloaded blocking
			// call, whose two halves carry the rest of the body as closures --
			// but a plain `run(func() { … })` is the same shape and got
			// nothing, so an element ref written inside one kept the name the
			// program gave it rather than the variable the page emitted, and
			// the page threw on a name nothing declared.
			call, changed := walkCallLambdas(ctx, n.Call, t)
			if caught, moved := walkCatchingHandler(ctx, call, t); moved {
				call, changed = caught, true
			}
			if changed {
				cp := *n
				cp.Call = call
				return []ir.Stmt{&cp}
			}
		}
	case *ir.Assign:
		if id, ok := n.Target.(*ir.Ident); ok && id.Synthesized {
			if ll, ok := n.Value.(*ir.ListLit); ok && len(ll.Elems) == 0 {
				if v := resolveSlotVar(id); v != nil {
					return t.OnSlotReset(ctx, v)
				}
			}
			// The older append shape, `__slotN = list.push(__slotN, x)`. Both
			// reach OnSlotAppend: which one a build emits depends on whether
			// its `push` returns the list or nothing.
			if call, ok := n.Value.(*ir.Call); ok && call.Func != nil && call.Func.Intrinsic == "list.push" && len(call.Args) == 2 {
				if v := resolveSlotVar(id); v != nil {
					return t.OnSlotAppend(ctx, v, call.Args[1].Value)
				}
			}
		}
		if sel, ok := n.Target.(*ir.Select); ok {
			if id, ok := sel.Operand.(*ir.Ident); ok && id.IsElementRef {
				return t.OnPropAssign(ctx, sel.Operand, sel.Field, n.Value)
			}
		}
		// `handle = setInterval(func() { … })` is a callback held by an
		// assignment, which is the shape this class of walk keeps missing.
		if v, changed := walkExprLambdas(ctx, n.Value, t); changed {
			cp := *n
			cp.Value = v
			return []ir.Stmt{&cp}
		}
	case *ir.For:
		iterExpr := t.OnIter(ctx, n.Iter)
		body := WalkLowered(ctx, n.Body, t)
		cp := *n
		cp.Iter = iterExpr
		cp.Body = body
		return []ir.Stmt{&cp}
	case *ir.ErrorBoundary:
		// A raise reaches its handler through Call.ResolvedHandler, so once its
		// children are flat statements the boundary itself holds nothing.
		return WalkLowered(ctx, n.Children, t)
	case *ir.If:
		cp := *n
		cp.Cond = t.OnCond(ctx, n.Cond)
		cp.Body = WalkLowered(ctx, n.Body, t)
		if len(n.Else) > 0 {
			cp.Else = WalkLowered(ctx, n.Else, t)
		}
		// A catch block renders its handler in place, so the handler's widget
		// writes are this body's and need the same translation.
		if n.Catch != nil && n.Catch.Func != nil {
			h := *n.Catch
			fn := *n.Catch.Func
			fn.Block = WalkLowered(ctx, n.Catch.Func.Block, t)
			h.Func = &fn
			cp.Catch = &h
		}
		return []ir.Stmt{&cp}
	}
	return t.OnDefault(ctx, s)
}

// walkCatchingHandler walks the handler a language inlines at a fallible call
// site -- the call's own @error, or the boundary's or window's it resolved to.
// A boundary's or window's handler is also emitted where it is declared, so
// the walk rewrites a copy.
func walkCatchingHandler(ctx context.Context, call *ir.Call, t IntrinsicTranslator) (*ir.Call, bool) {
	if call == nil {
		return call, false
	}
	h := ir.CatchingHandler(call)
	if h == nil || h.Func == nil {
		return call, false
	}
	fn := *h.Func
	fn.Block = WalkLowered(ctx, h.Func.Block, t)
	walked := *h
	walked.Func = &fn
	cp := *call
	if cp.ErrorHandler == h {
		cp.ErrorHandler = &walked
	}
	if cp.ResolvedHandler == h {
		cp.ResolvedHandler = &walked
	}
	return &cp, true
}

// walkCallLambdas rewrites every callback a call hands over, answering a copy
// and whether anything moved. Copied rather than rewritten in place for the
// reason walkComponentProps gives: walking one body twice nests the
// translation inside itself.
func walkCallLambdas(ctx context.Context, call *ir.Call, t IntrinsicTranslator) (*ir.Call, bool) {
	if call == nil {
		return call, false
	}
	changed := false
	args := append([]ir.CallArg(nil), call.Args...)
	for i := range args {
		if v, moved := walkExprLambdas(ctx, args[i].Value, t); moved {
			args[i].Value = v
			changed = true
		}
	}
	if !changed {
		return call, false
	}
	cp := *call
	cp.Args = args
	return &cp, true
}

// walkExprLambdas walks the body of a lambda an expression *is*, or of any
// callback a call inside it hands over. It stops at the outermost lambda,
// because WalkLowered on its block reaches whatever is nested below.
func walkExprLambdas(ctx context.Context, e ir.Expr, t IntrinsicTranslator) (ir.Expr, bool) {
	switch x := e.(type) {
	case *ir.Lambda:
		if walked := walkHandlerBody(ctx, x, t); walked != e {
			return walked, true
		}
	case *ir.Call:
		if call, changed := walkCallLambdas(ctx, x, t); changed {
			return call, true
		}
	case *ir.ListLit:
		// A list of callbacks is one call argument, which is what go.select's
		// arms are. Without this the widget writes inside a select arm reach
		// the emitter untranslated -- a bare `__n0.Text =` rather than the
		// platform's setter, which is not a field any Fyne widget has.
		changed := false
		elems := append([]ir.Expr(nil), x.Elems...)
		for i := range elems {
			if v, moved := walkExprLambdas(ctx, elems[i], t); moved {
				elems[i] = v
				changed = true
			}
		}
		if changed {
			cp := *x
			cp.Elems = elems
			return &cp, true
		}
	}
	return e, false
}

// walkHandlerBody rewrites an inline handler's body through the same
// translator as any other statement block, and returns the handler unchanged
// when it is not one. A handler attached as a closure -- how one reading a
// loop variable has to be attached -- sits inside an expression, which the
// statement walk does not otherwise enter.
func walkHandlerBody(ctx context.Context, handler ir.Expr, t IntrinsicTranslator) ir.Expr {
	lam, ok := handler.(*ir.Lambda)
	if !ok || lam.Func == nil {
		return handler
	}
	cp := *lam
	fn := *lam.Func
	fn.Block = WalkLowered(ctx, lam.Func.Block, t)
	cp.Func = &fn
	return &cp
}

// walkComponentProps rewrites every handler an instantiation hands over in its
// props struct, and answers with the call to emit.
//
// A prop is where a handler crosses into an instance -- an event the call site
// subscribed to is a func-typed prop, and its lambda is a field of the struct
// the create call carries. That is two levels below an argument, so the walk
// reached it neither as a statement nor as a bare arg, and the body went to the
// language backend raw: a write to a widget the enclosing Model owns kept its
// IR shape, naming the field as a bare local the render function does not
// declare. On the Go platforms that is a file which does not compile.
//
// The call is copied rather than rewritten in place: it is the same node the
// reuse branch's UpdateComponent aliases, and walking one body twice would
// nest the translation inside itself.
func walkComponentProps(ctx context.Context, call *ir.Call, t IntrinsicTranslator) *ir.Call {
	if call == nil || len(call.Args) < 2 {
		return call
	}
	lit, ok := call.Args[1].Value.(*ir.StructLit)
	if !ok {
		return call
	}
	fields := make([]ir.FieldInit, len(lit.Fields))
	copy(fields, lit.Fields)
	changed := false
	for i := range fields {
		if w := walkHandlerBody(ctx, fields[i].Value, t); w != fields[i].Value {
			fields[i].Value = w
			changed = true
		}
	}
	if !changed {
		return call
	}
	litCopy := *lit
	litCopy.Fields = fields
	args := make([]ir.CallArg, len(call.Args))
	copy(args, call.Args)
	args[1].Value = &litCopy
	callCopy := *call
	callCopy.Args = args
	return &callCopy
}

// resolveSlotVar returns the *ir.Var pointed to by a Synthesized slot
// ident. The lower pass historically left Sym unset on synthesized
// slot idents; when that's the case we synthesize a Var carrying the
// ident's Name/Type so the translator still has the data it needs.
// resolveSlotVar returns the slot accumulator id names, or nil when it names
// something else.
//
// The name is what decides, not the Sym: a `list.push` onto a synthesized var
// is not necessarily a slot append. An instance registry is a synthesized list
// of the same shape pushed to in the same loop, and taking it for a slot
// emitted its reconcile as `m.__inst0_next = append(...)` -- a field of the
// Model, against a local the same function had just declared.
func resolveSlotVar(id *ir.Ident) *ir.Var {
	if !ir.IsSlotVarName(id.Name) {
		return nil
	}
	if v, ok := id.Sym.(*ir.Var); ok {
		return v
	}
	if id.Synthesized {
		return &ir.Var{Name: id.Name, Type: id.Type, Synthesized: true}
	}
	return nil
}

func isLowerIntrinsic(call *ir.Call, name string) bool {
	return call != nil && call.Func != nil && call.Func.Intrinsic == name
}

func extractStringLit(e ir.Expr) (string, bool) {
	if l, ok := e.(*ir.Literal); ok && l.Type == ir.TypString {
		return l.Value, true
	}
	return "", false
}

func identName(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok {
		return id.Name
	}
	return ""
}
