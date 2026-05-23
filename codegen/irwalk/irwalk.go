// Package irwalk drives the recursive walk over IR expression and
// statement trees that each backend language used to duplicate inside
// its own IRContext.EvalExpr / EvalStmt. Languages implement Renderer
// to plug in their leaf encoding, name mangling, and lang-owned
// subgraphs (Call, Conversion, Lambda); irwalk owns the dispatch and
// the structural recursion.
package irwalk

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// Renderer is the per-language plug-in for irwalk. Each Render*
// method returns the rendered string (or lines) for one IR node;
// composites have their children pre-walked by irwalk and passed in
// as already-rendered strings. The three big subgraphs — Call,
// Conversion, Lambda — are renderer-driven because their internal
// shape varies too much per language; those methods can call back
// into irwalk.EvalExpr to render their own sub-expressions.
type Renderer interface {
	// — Leaves
	Literal(*ir.Literal) string
	Ident(*ir.Ident) string
	NilExpr() string

	// — Composites (children pre-walked by irwalk)
	Binary(n *ir.Binary, left, right string) string
	Unary(n *ir.Unary, operand string) string
	Ternary(n *ir.Ternary, cond, then_, else_ string) string
	Select(n *ir.Select, operand string) string
	Index(n *ir.Index, operand, idx string) string
	ListLit(n *ir.ListLit, elems []string) string
	MapLit(n *ir.MapLitIR, keys, vals []string) string
	StructLit(n *ir.StructLit, fieldStrs []string) string
	Spread(n *ir.Spread, operand string) string

	// — Lang-owned subgraphs (renderer drives, may call back via irwalk.EvalExpr)
	Call(*ir.Call) string
	Conversion(*ir.Conversion) string
	Lambda(*ir.Lambda) string

	// — Stmt single-liners (walker handles StmtPrefix)
	AssignText(n *ir.Assign, target, value string) string
	ToggleText(n *ir.Toggle, target string) string
	CallStmtLines(*ir.CallStmt) []string
	EmitText(n *ir.Emit, argStrs []string) string
	LocalVarText(n *ir.LocalVar, initStr string) string
	ReturnText(n *ir.Return, valueStr string) string

	// — Stmt blocks (walker controls body recursion, indent, close)
	ForHead(n *ir.For, iterStr string) string
	IfHead(n *ir.If, condStr string) string
	ElseHead() string
	BlockEnd() string
	Indent() string

	// — Mut targets (leaf hooks; walker composes Select/Index)
	MutTargetIdent(*ir.Ident) string
	MutTargetField(field string) string

	// — Hooks
	StmtPrefix(s ir.Stmt) []string
	Scoped(name string) Renderer
}

// EvalExpr dispatches an IR expression through r, recursing into
// children via this same function. Call / Conversion / Lambda are
// renderer-driven; everything else has its children pre-walked.
func EvalExpr(r Renderer, e ir.Expr) string {
	if e == nil {
		return r.NilExpr()
	}
	switch n := e.(type) {
	case *ir.Literal:
		return r.Literal(n)
	case *ir.Ident:
		return r.Ident(n)
	case *ir.Binary:
		return r.Binary(n, EvalExpr(r, n.Left), EvalExpr(r, n.Right))
	case *ir.Unary:
		return r.Unary(n, EvalExpr(r, n.Operand))
	case *ir.Ternary:
		return r.Ternary(n, EvalExpr(r, n.Cond), EvalExpr(r, n.Then), EvalExpr(r, n.Else))
	case *ir.Select:
		return r.Select(n, EvalExpr(r, n.Operand))
	case *ir.Index:
		return r.Index(n, EvalExpr(r, n.Operand), EvalExpr(r, n.Idx))
	case *ir.ListLit:
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = EvalExpr(r, el)
		}
		return r.ListLit(n, parts)
	case *ir.MapLitIR:
		keys := make([]string, len(n.Entries))
		vals := make([]string, len(n.Entries))
		for i, ent := range n.Entries {
			keys[i] = EvalExpr(r, ent.Key)
			vals[i] = EvalExpr(r, ent.Value)
		}
		return r.MapLit(n, keys, vals)
	case *ir.StructLit:
		fieldStrs := make([]string, len(n.Fields))
		for i, f := range n.Fields {
			fieldStrs[i] = EvalExpr(r, f.Value)
		}
		return r.StructLit(n, fieldStrs)
	case *ir.Spread:
		return r.Spread(n, EvalExpr(r, n.Operand))
	case *ir.Call:
		return r.Call(n)
	case *ir.Conversion:
		return r.Conversion(n)
	case *ir.Lambda:
		return r.Lambda(n)
	default:
		panic(fmt.Sprintf("irwalk.EvalExpr: unhandled ir.Expr %T", e))
	}
}

// EvalStmt dispatches an IR statement through r, recursing into
// nested statement bodies (For/If) with a child renderer obtained
// via r.WithLocal for loop-variable scope.
func EvalStmt(r Renderer, s ir.Stmt) []string {
	lines := evalStmtImpl(r, s)
	if lines == nil {
		return nil
	}
	if prefix := r.StmtPrefix(s); len(prefix) > 0 {
		lines = append(prefix, lines...)
	}
	return lines
}

func evalStmtImpl(r Renderer, s ir.Stmt) []string {
	switch n := s.(type) {
	case *ir.Assign:
		return []string{r.AssignText(n, EvalMutTarget(r, n.Target), EvalExpr(r, n.Value))}
	case *ir.Toggle:
		return []string{r.ToggleText(n, EvalMutTarget(r, n.Target))}
	case *ir.CallStmt:
		return r.CallStmtLines(n)
	case *ir.Emit:
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = EvalExpr(r, a.Value)
		}
		return []string{r.EmitText(n, argStrs)}
	case *ir.LocalVar:
		initStr := ""
		if n.Init != nil {
			initStr = EvalExpr(r, n.Init)
		}
		return []string{r.LocalVarText(n, initStr)}
	case *ir.Return:
		valueStr := ""
		if n.Value != nil {
			valueStr = EvalExpr(r, n.Value)
		}
		return []string{r.ReturnText(n, valueStr)}
	case *ir.For:
		return evalFor(r, n)
	case *ir.If:
		return evalIf(r, n)
	case *ir.NodeInst:
		// UI-tree statements are platform-specific; the generic
		// per-language stmt path emits nothing for them.
		return nil
	default:
		panic(fmt.Sprintf("irwalk.EvalStmt: unhandled ir.Stmt %T", s))
	}
}

func evalFor(r Renderer, n *ir.For) []string {
	iter := EvalExpr(r, n.Iter)
	child := r.Scoped(n.Key)
	if n.Value != "" {
		child = child.Scoped(n.Value)
	}
	lines := []string{r.ForHead(n, iter)}
	indent := r.Indent()
	for _, stmt := range n.Body {
		for _, l := range EvalStmt(child, stmt) {
			lines = append(lines, indent+l)
		}
	}
	if end := r.BlockEnd(); end != "" {
		lines = append(lines, end)
	}
	return lines
}

func evalIf(r Renderer, n *ir.If) []string {
	cond := EvalExpr(r, n.Cond)
	lines := []string{r.IfHead(n, cond)}
	indent := r.Indent()
	for _, s := range n.Body {
		for _, l := range EvalStmt(r, s) {
			lines = append(lines, indent+l)
		}
	}
	if len(n.Else) > 0 {
		lines = append(lines, r.ElseHead())
		for _, s := range n.Else {
			for _, l := range EvalStmt(r, s) {
				lines = append(lines, indent+l)
			}
		}
	}
	if end := r.BlockEnd(); end != "" {
		lines = append(lines, end)
	}
	return lines
}

// EvalMutTarget renders the left-hand-side of an assignment.
// Recurses on Select/Index using MutTargetIdent at the leaf and
// MutTargetField for Select's field name; index expressions render
// as ordinary expressions via EvalExpr. Falls back to EvalExpr for
// anything that isn't a structural lvalue.
func EvalMutTarget(r Renderer, e ir.Expr) string {
	switch n := e.(type) {
	case *ir.Ident:
		return r.MutTargetIdent(n)
	case *ir.Select:
		return EvalMutTarget(r, n.Operand) + "." + r.MutTargetField(n.Field)
	case *ir.Index:
		return EvalMutTarget(r, n.Operand) + "[" + EvalExpr(r, n.Idx) + "]"
	default:
		return EvalExpr(r, e)
	}
}

// EvalCallArgs is a convenience for Renderer.Call implementations
// that just need each call argument rendered as a Go-string in
// positional order.
func EvalCallArgs(r Renderer, args []ir.CallArg) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = EvalExpr(r, a.Value)
	}
	return out
}
