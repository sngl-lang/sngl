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
	OnCreateComponent(ctx context.Context, id string, call *ir.Call) []ir.Stmt
	OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt
	OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt
	OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt
	OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt
	OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt
	OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt
	OnIter(ctx context.Context, iter ir.Expr) ir.Expr
	OnCond(ctx context.Context, cond ir.Expr) ir.Expr
	OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt
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
				return t.OnCreateComponent(ctx, n.Name, call)
			}
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
				return t.OnAttachHandler(ctx, n.Call.Args[0].Value, evt, n.Call.Args[2].Value)
			}
			// A slot append. push mutates its receiver and returns nothing, so
			// the lowering emits the call rather than an assignment to the
			// slot -- see renderSlotBody.
			if n.Call.Func != nil && n.Call.Func.Intrinsic == "list.push" && len(n.Call.Args) == 2 {
				if id, ok := n.Call.Args[0].Value.(*ir.Ident); ok && id.Synthesized {
					if v := resolveSlotVar(id); v != nil {
						return t.OnSlotAppend(ctx, v, n.Call.Args[1].Value)
					}
				}
			}
		}
	case *ir.Assign:
		if id, ok := n.Target.(*ir.Ident); ok && id.Synthesized {
			if ll, ok := n.Value.(*ir.ListLit); ok && len(ll.Elems) == 0 {
				if v := resolveSlotVar(id); v != nil {
					return t.OnSlotReset(ctx, v)
				}
			}
		}
		if sel, ok := n.Target.(*ir.Select); ok {
			if id, ok := sel.Operand.(*ir.Ident); ok && id.IsElementRef {
				return t.OnPropAssign(ctx, sel.Operand, sel.Field, n.Value)
			}
		}
	case *ir.For:
		iterExpr := t.OnIter(ctx, n.Iter)
		body := WalkLowered(ctx, n.Body, t)
		cp := *n
		cp.Iter = iterExpr
		cp.Body = body
		return []ir.Stmt{&cp}
	case *ir.If:
		cp := *n
		cp.Cond = t.OnCond(ctx, n.Cond)
		cp.Body = WalkLowered(ctx, n.Body, t)
		if len(n.Else) > 0 {
			cp.Else = WalkLowered(ctx, n.Else, t)
		}
		return []ir.Stmt{&cp}
	}
	return t.OnDefault(ctx, s)
}

// resolveSlotVar returns the *ir.Var pointed to by a Synthesized slot
// ident. The lower pass historically left Sym unset on synthesized
// slot idents; when that's the case we synthesize a Var carrying the
// ident's Name/Type so the translator still has the data it needs.
func resolveSlotVar(id *ir.Ident) *ir.Var {
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
