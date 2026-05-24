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
	// Walk params for component-typed entries; the first one becomes the
	// declared local in the test body and all of them populate compRecvs
	// so `<recv>.<field>` expressions lower correctly regardless of the
	// name the test author chose. Falls back to "c" when no component
	// param is present (assertion-only tests that never reach into state).
	recv := "c"
	compRecvs := map[string]bool{}
	first := true
	for _, p := range fn.Params {
		if p.Type != nil && p.Type.Kind == ir.TypeComponent {
			compRecvs[p.Name] = true
			if first {
				recv = p.Name
				first = false
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "    @Test fun test%s() {\n", suffix)
	fmt.Fprintf(&b, "        val %s = MainScreenState()\n", recv)
	fmt.Fprintf(&b, "        composeTestRule.setContent { MainScreen(%s) }\n", recv)
	ctxCounts := map[string]int{}
	for _, s := range fn.Block {
		for _, line := range lowerTestStmt(s, methodFields, compRecvs, ctxCounts) {
			fmt.Fprintf(&b, "        %s\n", line)
		}
	}
	b.WriteString("    }\n\n")
	return b.String()
}

func lowerTestStmt(s ir.Stmt, methodFields map[string]bool, compRecvs map[string]bool, ctxCounts map[string]int) []string {
	switch n := s.(type) {
	case *ir.CallStmt:
		if line, ok := lowerTestAssert(n, methodFields, compRecvs); ok {
			return []string{line}
		}
		if line, ok := lowerEventTrigger(n); ok {
			return []string{line}
		}
		if c := n.Call; c != nil && c.Func != nil && c.Func.Receiver == "Test" {
			if lines, ok := lowerTestSetContext(c, ctxCounts); ok {
				return lines
			}
			return []string{fmt.Sprintf("// TODO: lower t.%s — not implemented in android test runner", c.Func.Name)}
		}
	case *ir.Assign:
		// `<recv>.<var> += X` etc.: mutate state on the UI thread so
		// Compose recomposition sees it before the next assertion.
		if sel, ok := n.Target.(*ir.Select); ok {
			if id, ok := sel.Operand.(*ir.Ident); ok && compRecvs[id.Name] {
				value := lowerTestExpr(n.Value, methodFields, compRecvs)
				op := assignOpStr(n.Op)
				return []string{
					"composeTestRule.runOnUiThread {",
					fmt.Sprintf("    %s.%s %s %s", id.Name, sel.Field, op, value),
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
func lowerTestSetContext(c *ir.Call, ctxCounts map[string]int) ([]string, bool) {
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
			valExpr = lowerTestExpr(a.Value, nil, nil)
		}
	}
	if ctxName == "" {
		return nil, false
	}
	if valExpr == "" {
		valExpr = `""`
	}
	varName := "__test_ctx_" + ctxName
	if ctxCounts != nil {
		if n := ctxCounts[ctxName]; n > 0 {
			varName = fmt.Sprintf("%s_%d", varName, n)
		}
		ctxCounts[ctxName]++
	}
	return []string{
		fmt.Sprintf("// t.setContext(%q, ...): context override recorded; full mount-time wiring is a TODO.", ctxName),
		fmt.Sprintf("@Suppress(\"UNUSED_VARIABLE\") val %s = %s", varName, valExpr),
	}, true
}

func lowerTestAssert(call *ir.CallStmt, methodFields map[string]bool, compRecvs map[string]bool) (string, bool) {
	c := call.Call
	if c == nil || c.Func == nil || c.Func.Receiver != "Test" || c.Func.Name != "assert" {
		return "", false
	}
	if len(c.Args) != 2 {
		return "", false
	}
	expr := lowerTestExpr(c.Args[1].Value, methodFields, compRecvs)
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
		return lowerTestExpr(args[0].Value, nil, nil)
	}
	for _, f := range sl.Fields {
		if f.Name == "value" {
			return lowerTestExpr(f.Value, nil, nil)
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
func lowerTestExpr(e ir.Expr, methodFields map[string]bool, compRecvs map[string]bool) string {
	// Special shape: `<recv>.<id> == null` / `!= null` where <id> is in
	// methodFields → presence check via Compose finder count. SNGL's
	// nilable widget semantics maps to "any nodes match this tag?".
	if bin, ok := e.(*ir.Binary); ok {
		if bin.Op == ast.BinEq || bin.Op == ast.BinNeq {
			if id, ok := composeIDRef(bin.Left, methodFields, compRecvs); ok && isNullLit(bin.Right) {
				op := "=="
				if bin.Op == ast.BinNeq {
					op = "!="
				}
				return fmt.Sprintf("(composeNodeCount(composeTestRule, %q) %s 0)", id, op)
			}
			if id, ok := composeIDRef(bin.Right, methodFields, compRecvs); ok && isNullLit(bin.Left) {
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
		left := lowerTestExpr(n.Left, methodFields, compRecvs)
		right := lowerTestExpr(n.Right, methodFields, compRecvs)
		return "(" + left + " " + binaryOpStr(n.Op) + " " + right + ")"
	case *ir.Unary:
		if n.Op == ast.UnaryNot {
			return "!" + lowerTestExpr(n.Operand, methodFields, compRecvs)
		}
		return "-" + lowerTestExpr(n.Operand, methodFields, compRecvs)
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
		// `<recv>.<id>[idx].<prop>` — Compose `onAllNodesWithTag(<id>)[idx]`
		// then read via composeNodeTextAt.
		if idx, ok := n.Operand.(*ir.Index); ok {
			if inner, ok := idx.Operand.(*ir.Select); ok {
				if id, ok := inner.Operand.(*ir.Ident); ok && compRecvs[id.Name] {
					return fmt.Sprintf("composeNodeTextAt(composeTestRule, %q, %s)",
						inner.Field, lowerTestExpr(idx.Idx, methodFields, compRecvs))
				}
			}
		}
		// `<recv>.<id>.<prop>` — Compose semantics text read for the tag.
		if inner, ok := n.Operand.(*ir.Select); ok {
			if id, ok := inner.Operand.(*ir.Ident); ok && compRecvs[id.Name] {
				return fmt.Sprintf("composeNodeText(composeTestRule, %q)", inner.Field)
			}
		}
		if id, ok := n.Operand.(*ir.Ident); ok && compRecvs[id.Name] {
			return id.Name + "." + n.Field
		}
		return lowerTestExpr(n.Operand, methodFields, compRecvs) + "." + n.Field
	case *ir.Index:
		operand := lowerTestExpr(n.Operand, methodFields, compRecvs)
		idx := lowerTestExpr(n.Idx, methodFields, compRecvs)
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

// composeIDRef returns the id when e is the bare `<recv>.<id>` shape
// (where <recv> is a component-typed test param) and <id> is in
// methodFields. Used by lowerTestExpr's null-comparison shortcut to
// detect conditional widget presence checks.
func composeIDRef(e ir.Expr, methodFields map[string]bool, compRecvs map[string]bool) (string, bool) {
	sel, ok := e.(*ir.Select)
	if !ok {
		return "", false
	}
	id, ok := sel.Operand.(*ir.Ident)
	if !ok || !compRecvs[id.Name] {
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

// TestEmitMode selects how LowerTestFile wraps per-test bodies.
type TestEmitMode int

const (
	// TestEmitNative produces a JUnit-style class with @Test methods,
	// suitable for inclusion in the user's gradle test sourceset.
	TestEmitNative TestEmitMode = iota
	// TestEmitAgent produces a Kotlin source file with standalone
	// fun testX(t: T) declarations plus a Registry.register init
	// pulling in the per-platform testagent runtime.
	TestEmitAgent
)

// LowerTestFile produces the entire source of a generated Kotlin test
// file. Body lowering is identical across modes; the wrapper differs:
//
//	Native: package + JUnit imports + class MainScreenTest { @Test fun testFoo() { ... } }
//	Agent:  package + testagent imports + fun testFoo(t: T) { ... } + Registry.register init.
//
// Each function in fns is rendered using the same lowerTestStmt walker
// LowerTestFunc uses, ensuring identical semantic translation.
func LowerTestFile(pkg string, fns []*ir.Func, suffixes []string, methodFields map[string]bool, mode TestEmitMode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	switch mode {
	case TestEmitNative:
		b.WriteString("import org.junit.Test\n")
		b.WriteString("import org.junit.Assert.assertTrue\n\n")
		b.WriteString("class MainScreenTest {\n")
	case TestEmitAgent:
		b.WriteString("import us.duckfam.git.jonathan.sngl.testagent.T\n")
		b.WriteString("import us.duckfam.git.jonathan.sngl.testagent.Registry\n\n")
	}

	for i, fn := range fns {
		suffix := suffixes[i]
		compRecvs := compReceiverSet(fn)
		ctxCounts := map[string]int{}
		switch mode {
		case TestEmitNative:
			fmt.Fprintf(&b, "    @Test fun test%s() {\n", suffix)
			b.WriteString("        val c = newTestComponent()\n")
			for _, s := range fn.Block {
				for _, line := range lowerTestStmt(s, methodFields, compRecvs, ctxCounts) {
					fmt.Fprintf(&b, "        %s\n", line)
				}
			}
			b.WriteString("    }\n\n")
		case TestEmitAgent:
			fmt.Fprintf(&b, "fun test%s(t: T) {\n", suffix)
			b.WriteString("    val c = newTestComponent()\n")
			b.WriteString("    setCurrentTestModel(c)\n")
			for _, s := range fn.Block {
				for _, line := range lowerTestStmt(s, methodFields, compRecvs, ctxCounts) {
					fmt.Fprintf(&b, "    %s\n", line)
				}
			}
			b.WriteString("}\n\n")
		}
	}

	switch mode {
	case TestEmitNative:
		b.WriteString("}\n")
	case TestEmitAgent:
		b.WriteString("private fun registerAll() {\n")
		for i := range fns {
			suffix := suffixes[i]
			fmt.Fprintf(&b, "    Registry.register(%q, ::test%s)\n", suffix, suffix)
		}
		b.WriteString("}\n\n")
		b.WriteString("val __sngl_test_init: Unit = registerAll()\n")
	}

	return b.String()
}

// compReceiverSet returns the names of fn's component-typed params.
// Mirrors the compRecvs derivation in LowerTestFunc so lowerTestStmt
// recognises `<recv>.<field>` reads regardless of the chosen param name.
func compReceiverSet(fn *ir.Func) map[string]bool {
	out := map[string]bool{}
	for _, p := range fn.Params {
		if p.Type != nil && p.Type.Kind == ir.TypeComponent {
			out[p.Name] = true
		}
	}
	return out
}
