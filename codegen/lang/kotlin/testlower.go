package kotlin

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// LowerTestFunc renders a SNGL test function as one Kotlin
// `@Test` method body suitable for inclusion inside a
// Robolectric-driven Compose UI test class. Idioms map as
// follows:
//
//	t.assert(expr)                  → org.junit.Assert.assertTrue(...)
//	c.<id>.@<event>()               → composeTestRule.onNodeWithTag(id).performClick() / etc.
//	c.<id>.<prop> (Text widget)     → composeTestRule.onNodeWithTag(id) text fetched via SemanticsNode
//	c.<var> = X / c.<var> += X      → state mutation on the hoisted MainScreenState
//	c.<var>                         → direct field read on MainScreenState
//
// The Kotlin source lives in src/test/kotlin/, runs under
// RobolectricTestRunner, and constructs MainScreenState directly
// (no Activity needed).
func LowerTestFunc(fn *ir.Func, suffix string, methodFields map[string]bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "    @Test fun test%s() {\n", suffix)
	b.WriteString("        val c = MainScreenState()\n")
	b.WriteString("        composeTestRule.setContent { MainScreen(c) }\n")
	for _, s := range fn.Block {
		for _, line := range lowerTestStmt(s, methodFields) {
			fmt.Fprintf(&b, "        %s\n", line)
		}
	}
	b.WriteString("    }\n\n")
	return b.String()
}

func lowerTestStmt(s ir.Stmt, methodFields map[string]bool) []string {
	switch n := s.(type) {
	case *ir.CallStmt:
		if line, ok := lowerTestAssert(n, methodFields); ok {
			return []string{line}
		}
		if line, ok := lowerEventTrigger(n); ok {
			return []string{line}
		}
		if c := n.Call; c != nil && c.Func != nil && c.Func.Receiver == "Test" {
			if lines, ok := lowerTestSetContext(c); ok {
				return lines
			}
			return []string{fmt.Sprintf("// TODO: lower t.%s — not implemented in android test runner", c.Func.Name)}
		}
	case *ir.Assign:
		// `c.<var> += X` etc.: mutate state on the UI thread so
		// Compose recomposition sees it before the next assertion.
		if sel, ok := n.Target.(*ir.Select); ok {
			if id, ok := sel.Operand.(*ir.Ident); ok && id.Name == "c" {
				value := lowerTestExpr(n.Value, methodFields)
				op := assignOpStr(n.Op)
				return []string{
					"composeTestRule.runOnUiThread {",
					fmt.Sprintf("    c.%s %s %s", sel.Field, op, value),
					"}",
					"composeTestRule.waitForIdle()",
				}
			}
		}
	}
	// Fall through: emit a comment so unrecognised stmts surface in
	// the generated source rather than vanishing.
	return []string{fmt.Sprintf("// TODO: lower stmt %T", s)}
}

// lowerTestSetContext recognises a `t.setContext(ctxName, value)` call and
// emits a placeholder val for the context override. The emitted val
// `__test_ctx_<name>` records the intended value so it is visible in the
// generated source, but does NOT yet wire the override into the composable
// tree at test mount time — that requires deeper integration with the
// Android test scaffold and is left for a follow-up task.
//
// The generated statement always compiles: a leading @Suppress annotation
// prevents unused-variable warnings.
func lowerTestSetContext(c *ir.Call) ([]string, bool) {
	if c.Func == nil || c.Func.Receiver != "Test" || c.Func.Name != "setContext" {
		return nil, false
	}
	// Args: [0] = t receiver (implicit), [1] = context name (*ir.ContextRead),
	// [2] = value — locate the ContextRead and the value among the args.
	var ctxName string
	var valExpr string
	for _, a := range c.Args {
		if cr, ok := a.Value.(*ir.ContextRead); ok && ctxName == "" {
			ctxName = cr.Ref.Name
		} else if ctxName != "" && valExpr == "" {
			valExpr = lowerTestExpr(a.Value, nil)
		}
	}
	if ctxName == "" {
		return nil, false
	}
	if valExpr == "" {
		valExpr = `""`
	}
	varName := "__test_ctx_" + ctxName
	return []string{
		fmt.Sprintf("// t.setContext(%q, ...): context override recorded; full mount-time wiring is a TODO.", ctxName),
		fmt.Sprintf("@Suppress(\"UNUSED_VARIABLE\") val %s = %s", varName, valExpr),
	}, true
}

func lowerTestAssert(call *ir.CallStmt, methodFields map[string]bool) (string, bool) {
	c := call.Call
	if c == nil || c.Func == nil || c.Func.Receiver != "Test" || c.Func.Name != "assert" {
		return "", false
	}
	if len(c.Args) != 2 {
		return "", false
	}
	expr := lowerTestExpr(c.Args[1].Value, methodFields)
	// Pretty-print the asserted source for the failure message.
	return fmt.Sprintf("org.junit.Assert.assertTrue(%q, %s)", expr, expr), true
}

func lowerEventTrigger(call *ir.CallStmt) (string, bool) {
	c := call.Call
	if c == nil || c.AST == nil {
		return "", false
	}
	outerSel, ok := c.AST.Func.(*ast.SelectExpr)
	if !ok || outerSel.Kind != ast.SelectEvent {
		return "", false
	}
	innerSel, ok := outerSel.Operand.(*ast.SelectExpr)
	if !ok {
		return "", false
	}
	if _, ok := innerSel.Operand.(*ast.IdentExpr); !ok {
		return "", false
	}
	event := outerSel.Field
	id := innerSel.Field
	finder := fmt.Sprintf("composeTestRule.onNodeWithTag(%q)", id)
	action := composeAction(event, c.Args)
	if action == "" {
		return fmt.Sprintf("// TODO: no Compose-test action mapped for @%s on #%s", event, id), true
	}
	return fmt.Sprintf("%s.%s; composeTestRule.waitForIdle()", finder, action), true
}

// composeAction maps a SNGL event name + invocation args to its
// Compose-UI-test equivalent. Input/change events accept a single
// stdlib event struct (InputEvent{value="x"}); composeAction pulls
// the `value` field out and feeds it to performTextReplacement,
// which clears the field first and then inserts — matching the
// "test sets the value" semantic regardless of prior content.
func composeAction(event string, args []ir.CallArg) string {
	switch event {
	case "click":
		return "performClick()"
	case "input", "change", "changed":
		text := extractEventValue(args)
		return fmt.Sprintf("performTextReplacement(%s)", text)
	}
	return ""
}

// extractEventValue pulls the `value` field expression out of a
// single-arg InputEvent / ChangeEvent struct literal, returning a
// Kotlin source string for the underlying value. Falls back to an
// empty literal when the shape doesn't match.
func extractEventValue(args []ir.CallArg) string {
	if len(args) != 1 {
		return "\"\""
	}
	sl, ok := args[0].Value.(*ir.StructLit)
	if !ok {
		return lowerTestExpr(args[0].Value, nil)
	}
	for _, f := range sl.Fields {
		if f.Name == "value" {
			return lowerTestExpr(f.Value, nil)
		}
	}
	return "\"\""
}

// lowerTestExpr translates a SNGL test expression to Kotlin source.
// `c.<var>` is a direct field read on MainScreenState; `c.<id>.<prop>`
// is fetched via the Compose semantics tree.
//
// methodFields names component ids that are NOT MainScreenState
// fields — they correspond to widgets gated by `if` / `for`. For
// those, presence and per-prop reads go through Compose finders
// rather than the state object.
func lowerTestExpr(e ir.Expr, methodFields map[string]bool) string {
	// Special shape: `c.<id> == null` / `!= null` where <id> is in
	// methodFields → presence check via Compose finder count. SNGL's
	// nilable widget semantics maps to "any nodes match this tag?".
	if bin, ok := e.(*ir.Binary); ok {
		if bin.Op == ast.BinEq || bin.Op == ast.BinNeq {
			if id, ok := composeIDRef(bin.Left, methodFields); ok && isNullLit(bin.Right) {
				op := "=="
				if bin.Op == ast.BinNeq {
					op = "!="
				}
				return fmt.Sprintf("(composeNodeCount(composeTestRule, %q) %s 0)", id, op)
			}
			if id, ok := composeIDRef(bin.Right, methodFields); ok && isNullLit(bin.Left) {
				op := "=="
				if bin.Op == ast.BinNeq {
					op = "!="
				}
				return fmt.Sprintf("(composeNodeCount(composeTestRule, %q) %s 0)", id, op)
			}
		}
	}
	switch n := e.(type) {
	case *ir.Binary:
		left := lowerTestExpr(n.Left, methodFields)
		right := lowerTestExpr(n.Right, methodFields)
		return "(" + left + " " + binaryOpStr(n.Op) + " " + right + ")"
	case *ir.Unary:
		if n.Op == ast.UnaryNot {
			return "!" + lowerTestExpr(n.Operand, methodFields)
		}
		return "-" + lowerTestExpr(n.Operand, methodFields)
	case *ir.Literal:
		switch n.Type.Kind {
		case ir.TypeString:
			return fmt.Sprintf("%q", n.Raw)
		case ir.TypeNull:
			return "null"
		}
		return n.Raw
	case *ir.Ident:
		return n.Name
	case *ir.Select:
		// `c.<id>[idx].<prop>` — Compose `onAllNodesWithTag(<id>)[idx]`
		// then read via composeNodeTextAt.
		if idx, ok := n.Operand.(*ir.Index); ok {
			if inner, ok := idx.Operand.(*ir.Select); ok {
				if id, ok := inner.Operand.(*ir.Ident); ok && id.Name == "c" {
					return fmt.Sprintf("composeNodeTextAt(composeTestRule, %q, %s)",
						inner.Field, lowerTestExpr(idx.Idx, methodFields))
				}
			}
		}
		// `c.<id>.<prop>` — Compose semantics text read for the tag.
		if inner, ok := n.Operand.(*ir.Select); ok {
			if id, ok := inner.Operand.(*ir.Ident); ok && id.Name == "c" {
				return fmt.Sprintf("composeNodeText(composeTestRule, %q)", inner.Field)
			}
		}
		if id, ok := n.Operand.(*ir.Ident); ok && id.Name == "c" {
			return "c." + n.Field
		}
		return lowerTestExpr(n.Operand, methodFields) + "." + n.Field
	case *ir.Index:
		operand := lowerTestExpr(n.Operand, methodFields)
		idx := lowerTestExpr(n.Idx, methodFields)
		return operand + "[" + idx + "]"
	case *ir.Call:
		// Delegate to the full expression translator for calls and any
		// shape lowerTestExpr's special-cases above don't already cover.
		// Method calls like `c.formatted()` and stdlib calls inside test
		// assertions both flow through here.
		return translateIRExpr(e, &codegen.ExprScope{})
	}
	// Final fallback: defer to the full IR translator. Keeps test
	// expressions in lockstep with non-test Kotlin codegen rather than
	// emitting a TODO placeholder that fails Kotlin compilation.
	return translateIRExpr(e, &codegen.ExprScope{})
}

// composeIDRef returns the id when e is the bare `c.<id>` shape and
// <id> is in methodFields. Used by lowerTestExpr's null-comparison
// shortcut to detect conditional widget presence checks.
func composeIDRef(e ir.Expr, methodFields map[string]bool) (string, bool) {
	sel, ok := e.(*ir.Select)
	if !ok {
		return "", false
	}
	id, ok := sel.Operand.(*ir.Ident)
	if !ok || id.Name != "c" {
		return "", false
	}
	if methodFields == nil || !methodFields[sel.Field] {
		return "", false
	}
	return sel.Field, true
}

func isNullLit(e ir.Expr) bool {
	lit, ok := e.(*ir.Literal)
	return ok && lit.Type != nil && lit.Type.Kind == ir.TypeNull
}
