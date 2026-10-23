// Package irwalk drives the recursive walk over IR expression and
// statement trees that each backend language used to duplicate inside
// its own IRContext.EvalExpr / EvalStmt. Languages implement Renderer
// to plug in their leaf encoding, name mangling, and lang-owned
// subgraphs (Call, Conversion, Lambda); irwalk owns the dispatch and
// the structural recursion.
package irwalk

import (
	"fmt"

	"duckfam.us/sngl/ir"
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
	// MatchExpr renders a match expression: the language's own switch
	// expression, or the ternaries it means (ir.MatchExpr.TernaryChain).
	MatchExpr(*ir.MatchExpr) string

	// — Stmt single-liners (walker handles StmtPrefix)
	AssignText(n *ir.Assign, target, value string) string
	ToggleText(n *ir.Toggle, target string) string
	CallStmtLines(*ir.CallStmt) []string
	EmitText(n *ir.Emit, argStrs []string) string
	LocalVarText(n *ir.LocalVar, initStr string) string
	ReturnText(n *ir.Return, valueStr string) string
	// BreakText is a break, naming its Label where the language's switch
	// would otherwise take it as its own.
	BreakText(n *ir.Break) string

	// — Stmt blocks (walker controls body recursion, indent, close)
	// ForHead opens a loop, labeled with n.Label where the language's
	// BreakText names one.
	ForHead(n *ir.For, iterStr string) string
	IfHead(n *ir.If, condStr string) string
	// Catch renders a catch block (If.Catch set): body is the rendered Body,
	// and the renderer renders the handler itself.
	Catch(n *ir.If, body []string) []string
	// Finally renders body, then finally however body ends (If.Finally set),
	// both already rendered.
	Finally(n *ir.If, body, finally []string) []string
	ElseHead() string
	// Match renders a match statement as the language's own switch: its
	// subject rendered ("" for a headless match), each arm's patterns
	// rendered, and each arm's body rendered unindented. Nil asks the walker
	// to write it as the if chain it means (ir.Match.Chain).
	Match(n *ir.Match, subject string, patterns, bodies [][]string) []string
	BlockEnd() string
	Indent() string

	// — Mut targets (leaf hooks; walker composes Select/Index)
	MutTargetIdent(*ir.Ident) string
	MutTargetField(*ir.Select) string

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
	case *ir.MatchExpr:
		return r.MatchExpr(n)
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
	case *ir.Break:
		return []string{r.BreakText(n)}
	case *ir.Continue:
		return []string{"continue"}
	case *ir.For:
		return evalFor(r, n)
	case *ir.If:
		return evalIf(r, n)
	case *ir.Match:
		return evalMatch(r, n)
	case *ir.NodeInst:
		// UI-tree statements are platform-specific; the generic
		// per-language stmt path emits nothing for them.
		return nil
	case *ir.CanvasRedrawStmt:
		// Platform-specific canvas redraw; the generic path emits nothing.
		return nil
	default:
		panic(fmt.Sprintf("irwalk.EvalStmt: unhandled ir.Stmt %T", s))
	}
}

// evalFor renders a loop and its body. n.Else is not read: passForElse has
// already rewritten an imperative for-else into a flag and a trailing `if`,
// which is what a host loop can express.
func evalFor(r Renderer, n *ir.For) []string {
	// A loop with no head has no expression to render, and asking for one
	// would render the language's nil.
	iter := ""
	if n.Iter != nil {
		iter = EvalExpr(r, n.Iter)
	}
	child := r.Scoped(n.Key)
	if n.Value != "" {
		child = child.Scoped(n.Value)
	}
	lines := []string{r.ForHead(n, iter)}
	lines = append(lines, IndentLines(r.Indent(), EvalBlock(child, n.Body))...)
	if end := r.BlockEnd(); end != "" {
		lines = append(lines, end)
	}
	return lines
}

// EvalBlock renders a block's statements in order, each local it declares a
// local of r from its declaration on (Renderer.Scoped). An emitter that binds
// nothing of a body up front still reads a nested `var count` as the local
// and a read of the page's `count` before it as the page's: one path that
// bound no body locals read the local as the page's var of its name.
func EvalBlock(r Renderer, stmts []ir.Stmt) []string {
	var lines []string
	for _, s := range stmts {
		lines = append(lines, EvalStmt(r, s)...)
		if lv, ok := s.(*ir.LocalVar); ok && lv.Sym != nil && lv.Name != "" {
			r = r.Scoped(lv.Name)
		}
	}
	return lines
}

func evalIf(r Renderer, n *ir.If) []string {
	if len(n.Finally) > 0 {
		body := EvalBlock(r, n.Body)
		if n.Catch != nil {
			body = r.Catch(n, body)
		}
		return r.Finally(n, body, EvalBlock(r, n.Finally))
	}
	if n.Catch != nil {
		return r.Catch(n, EvalBlock(r, n.Body))
	}
	cond := EvalExpr(r, n.Cond)
	lines := []string{r.IfHead(n, cond)}
	indent := r.Indent()
	lines = append(lines, IndentLines(indent, EvalBlock(r, n.Body))...)
	if len(n.Else) > 0 {
		lines = append(lines, r.ElseHead())
		lines = append(lines, IndentLines(indent, EvalBlock(r, n.Else))...)
	}
	if end := r.BlockEnd(); end != "" {
		lines = append(lines, end)
	}
	return lines
}

func evalMatch(r Renderer, n *ir.Match) []string {
	subject := ""
	if n.Subject != nil {
		subject = EvalExpr(r, n.Subject)
	}
	patterns := make([][]string, len(n.Arms))
	bodies := make([][]string, len(n.Arms))
	for i, arm := range n.Arms {
		for _, p := range arm.Patterns {
			patterns[i] = append(patterns[i], EvalExpr(r, p))
		}
		bodies[i] = EvalBlock(r, arm.Body)
	}
	if lines := r.Match(n, subject, patterns, bodies); lines != nil {
		return lines
	}
	var lines []string
	for _, s := range n.Chain() {
		lines = append(lines, EvalStmt(r, s)...)
	}
	return lines
}

// IndentLines puts indent before each of lines.
func IndentLines(indent string, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = indent + l
	}
	return out
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
		return EvalMutTarget(r, n.Operand) + "." + r.MutTargetField(n)
	case *ir.Index:
		return EvalMutTarget(r, n.Operand) + "[" + EvalExpr(r, n.Idx) + "]"
	default:
		return EvalExpr(r, e)
	}
}
