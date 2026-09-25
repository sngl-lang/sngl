package kotlin

import (
	"fmt"
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// testFallbackCtx is the KtIRContext used to render call shapes and any
// expression that lowerTestExpr's test-specific special-cases don't handle.
// It is backed by an empty package, matching the legacy empty-scope
// semantics the two fallbacks here previously relied on: idents resolve to
// themselves and no scope-specific rewriting applies. Built once and reused
// (it carries no per-test state), mirroring golang's testIRContext pattern.
var (
	testFallbackCtxOnce sync.Once
	testFallbackCtx     *KtIRContext
)

func testIRContext() *KtIRContext {
	testFallbackCtxOnce.Do(func() {
		testFallbackCtx = NewIRContext(codegen.NewExprCtx(&ir.Package{}))
	})
	return testFallbackCtx
}

func lowerTestStmt(s ir.Stmt, surf TestSurface, compRecvs map[string]bool, ctxCounts map[string]int, mode TestEmitMode) []string {
	switch n := s.(type) {
	case *ir.CallStmt:
		if line, ok := lowerTestAssert(n, surf, compRecvs, mode); ok {
			return []string{line}
		}
		if line, ok := lowerEventTrigger(n); ok {
			return []string{line}
		}
		if c := n.Call; c != nil && c.Func != nil && c.Func.Receiver == "Test" {
			if lines, ok := lowerTestSetContext(c, ctxCounts); ok {
				return lines
			}
			// t.snapshot(name): agent-mode routes through the testagent
			// runtime's T.snapshot which invokes the platform-registered
			// capture and posts a snapshotAssert RPC. Native mode has no
			// snapshot story today, so it remains a TODO.
			if c.Func.Name == "snapshot" && mode == TestEmitAgent && len(c.Args) >= 2 {
				name := lowerTestExpr(c.Args[1].Value, TestSurface{}, nil)
				return []string{fmt.Sprintf("t.snapshot(%s)", name)}
			}
			return []string{fmt.Sprintf("// TODO: lower t.%s — not implemented in android test runner", c.Func.Name)}
		}
	case *ir.LocalVar:
		// `var name T [= expr]` inside a test body. `var`, as the ordinary
		// translator spells it: a SNGL local is mutable, and a test that
		// assigns to one -- `s = s.digit(d)` in a loop -- is Kotlin refusing
		// to reassign a val. The case exists at all so the initialiser goes
		// through lowerTestExpr, which is what lets it read `c.<id>.<prop>`.
		// Initialiser is the IR-provided Init when present (ir.Normalize
		// injects the zero value otherwise).
		var init string
		if n.Init != nil {
			init = lowerTestExpr(n.Init, surf, compRecvs)
		} else {
			init = "null"
		}
		return []string{fmt.Sprintf("var %s = %s", n.Name, init)}
	case *ir.Assign:
		// `<recv>.<var> += X` etc.: mutate state on the UI thread so
		// Compose recomposition sees it before the next assertion.
		if sel, ok := n.Target.(*ir.Select); ok {
			if id, ok := sel.Operand.(*ir.Ident); ok && compRecvs[id.Name] {
				value := lowerTestExpr(n.Value, surf, compRecvs)
				op := n.Op.String()
				return []string{
					"composeTestRule.runOnUiThread {",
					fmt.Sprintf("    %s.%s %s %s", id.Name, sel.Field, op, value),
					"}",
					"composeTestRule.waitForIdle()",
				}
			}
		}
	}
	// Anything with no test-specific meaning is an ordinary statement, so the
	// ordinary translator emits it -- which is what `lowerTestExpr` already
	// does on the expression side and what Go's lowerTestStmt does here. A
	// comment instead meant a test calling one of its component's own methods
	// (`c.press(key)`) lowered to nothing at all, and the assertions after it
	// ran against a component nobody had touched.
	return testIRContext().EvalStmt(s)
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
	ctxName, val, ok := codegen.TestSetContext(c)
	if !ok {
		return nil, false
	}
	valExpr := lowerTestExpr(val, TestSurface{}, nil)
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

func lowerTestAssert(call *ir.CallStmt, surf TestSurface, compRecvs map[string]bool, mode TestEmitMode) (string, bool) {
	c := call.Call
	if c == nil || c.Func == nil || c.Func.Receiver != "Test" || c.Func.Name != "assert" {
		return "", false
	}
	if len(c.Args) != 2 {
		return "", false
	}
	expr := lowerTestExpr(c.Args[1].Value, surf, compRecvs)
	// Agent mode routes assertions through the testagent T receiver so
	// failures land in the JSON-RPC test report; native (Robolectric
	// @Test) mode falls back to junit's bundled assertTrue.
	if mode == TestEmitAgent {
		return fmt.Sprintf("t.assertTrue(%s, %q)", expr, expr), true
	}
	return fmt.Sprintf("org.junit.Assert.assertTrue(%q, %s)", expr, expr), true
}

func lowerEventTrigger(call *ir.CallStmt) (string, bool) {
	c := call.Call
	if c == nil || c.AST == nil || c.Event == "" {
		return "", false
	}
	outerSel, ok := c.AST.Func.(*ast.SelectExpr)
	if !ok {
		return "", false
	}
	innerSel, ok := outerSel.Operand.(*ast.SelectExpr)
	if !ok {
		return "", false
	}
	if _, ok := innerSel.Operand.(*ast.IdentExpr); !ok {
		return "", false
	}
	event := c.Event
	id := innerSel.Field
	finder := fmt.Sprintf("composeTestRule.onNodeWithTag(%q)", id)
	action := composeAction(event, c.Args)
	if action == "" {
		return fmt.Sprintf("// TODO: no Compose-test action mapped for @%s on #%s", event, id), true
	}
	return fmt.Sprintf("%s.%s; composeTestRule.waitForIdle()", finder, action), true
}

// composeActionImports are what lowerEventTrigger's finder and actions name:
// extension functions, so a test file that clicks does not compile without
// them. Written only into a file that triggers an event, at the marker.
const (
	composeActionImports = "import androidx.compose.ui.test.onNodeWithTag\n" +
		"import androidx.compose.ui.test.performClick\n" +
		"import androidx.compose.ui.test.performTextReplacement\n"
	composeActionMarker = "\x00composeActions\x00"
)

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
		return lowerTestExpr(args[0].Value, TestSurface{}, nil)
	}
	for _, f := range sl.Fields {
		if f.Name == "value" {
			return lowerTestExpr(f.Value, TestSurface{}, nil)
		}
	}
	return "\"\""
}

// lowerTestExpr translates a SNGL test expression to Kotlin source.
// `c.<var>` is a direct field read on MainScreenState; `c.<id>.<prop>`
// is fetched via the Compose semantics tree.
//
// surf is what the receiver exposes, and the two halves answer different
// questions. MethodFields names component ids that are NOT MainScreenState
// fields — they correspond to widgets gated by `if` / `for`, so presence and
// per-prop reads go through Compose finders rather than the state object.
// StateFields names the cells the state object does declare, which is what
// separates `c.state.entry` from `c.<id>.<prop>`: without it every `c.X.Y`
// read as a node's text, so a test asserting on its component's own state
// asked the view tree for a tag no widget carries.
func lowerTestExpr(e ir.Expr, surf TestSurface, compRecvs map[string]bool) string {
	// Special shape: `<recv>.<id> == null` / `!= null` where <id> is in
	// methodFields → presence check via Compose finder count. SNGL's
	// nilable widget semantics maps to "any nodes match this tag?".
	if bin, ok := e.(*ir.Binary); ok {
		if bin.Op == ast.BinEq || bin.Op == ast.BinNeq {
			if id, ok := composeIDRef(bin.Left, surf, compRecvs); ok && isNullLit(bin.Right) {
				op := "=="
				if bin.Op == ast.BinNeq {
					op = "!="
				}
				return fmt.Sprintf("(composeNodeCount(composeTestRule, %q) %s 0)", id, op)
			}
			if id, ok := composeIDRef(bin.Right, surf, compRecvs); ok && isNullLit(bin.Left) {
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
		left := lowerTestExpr(n.Left, surf, compRecvs)
		right := lowerTestExpr(n.Right, surf, compRecvs)
		return "(" + left + " " + n.Op.String() + " " + right + ")"
	case *ir.Unary:
		if n.Op == ast.UnaryNot {
			return "!" + lowerTestExpr(n.Operand, surf, compRecvs)
		}
		return "-" + lowerTestExpr(n.Operand, surf, compRecvs)
	case *ir.Literal:
		if ir.StringReprStruct(n.Type) {
			return fmt.Sprintf("%q", n.Value)
		}
		switch n.Type.Kind {
		case ir.TypeString:
			return fmt.Sprintf("%q", n.Value)
		case ir.TypeNull:
			return "null"
		case ir.TypeFloat:
			// Kotlin types an unsuffixed whole number as Int, so a float
			// whose raw form lost its fraction compares and assigns against
			// Double as a type error rather than a widening.
			if !strings.Contains(n.Value, ".") {
				return n.Value + ".0"
			}
		}
		return n.Value
	case *ir.Ident:
		return n.Name
	case *ir.Select:
		// `<recv>.<id>[idx].<prop>` — Compose `onAllNodesWithTag(<id>)[idx]`
		// then read via composeNodeTextAt.
		if idx, ok := n.Operand.(*ir.Index); ok {
			if inner, ok := idx.Operand.(*ir.Select); ok {
				if id, ok := inner.Operand.(*ir.Ident); ok && compRecvs[id.Name] {
					return fmt.Sprintf("composeNodeTextAt(composeTestRule, %q, %s)",
						inner.Field, lowerTestExpr(idx.Idx, surf, compRecvs))
				}
			}
		}
		// `<recv>.<id>.<prop>` — Compose semantics text read for the tag.
		// A cell the state object declares is not that: it is an ordinary
		// field, and the read is an ordinary one.
		if inner, ok := n.Operand.(*ir.Select); ok {
			if id, ok := inner.Operand.(*ir.Ident); ok && compRecvs[id.Name] && !surf.StateFields[inner.Field] {
				return fmt.Sprintf("composeNodeText(composeTestRule, %q)", inner.Field)
			}
		}
		if id, ok := n.Operand.(*ir.Ident); ok && compRecvs[id.Name] {
			return id.Name + "." + n.Field
		}
		return lowerTestExpr(n.Operand, surf, compRecvs) + "." + n.Field
	case *ir.Index:
		operand := lowerTestExpr(n.Operand, surf, compRecvs)
		idx := lowerTestExpr(n.Idx, surf, compRecvs)
		return operand + "[" + idx + "]"
	case *ir.Call:
		// Delegate to the full expression translator for calls and any
		// shape lowerTestExpr's special-cases above don't already cover.
		// Method calls like `c.formatted()` and stdlib calls inside test
		// assertions both flow through here.
		return testIRContext().EvalExpr(e)
	}
	// Final fallback: defer to the full IR translator. Keeps test
	// expressions in lockstep with non-test Kotlin codegen rather than
	// emitting a TODO placeholder that fails Kotlin compilation.
	return testIRContext().EvalExpr(e)
}

// composeIDRef returns the id when e is the bare `<recv>.<id>` shape
// (where <recv> is a component-typed test param) and <id> is in
// surf.MethodFields. Used by lowerTestExpr's null-comparison shortcut to
// detect conditional widget presence checks.
func composeIDRef(e ir.Expr, surf TestSurface, compRecvs map[string]bool) (string, bool) {
	sel, ok := e.(*ir.Select)
	if !ok {
		return "", false
	}
	id, ok := sel.Operand.(*ir.Ident)
	if !ok || !compRecvs[id.Name] {
		return "", false
	}
	if !surf.MethodFields[sel.Field] {
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
//	        Wraps each @Test body with a Compose test rule so that
//	        mutableStateOf-backed MainScreenState fields work without
//	        the "Composer not present" crash. The JUnit runner is
//	        selected via testRunner:
//	          "robolectric" / "" → RobolectricTestRunner (JVM unit tests)
//	          "device"           → AndroidJUnit4         (instrumented tests)
//	Agent:  package + testagent imports + fun testFoo(t: T) { ... } + Registry.register init.
//
// Both modes render every function in fns through the same lowerTestStmt
// walker, so a test body translates identically either way.
// testInstanceVar names the component a test drives. Deliberately not a name
// SNGL source can produce, so a local in the test body never collides.
const testInstanceVar = "__snglTestComponent"

func LowerTestFile(pkg string, fns []*ir.Func, suffixes []string, surf TestSurface, mode TestEmitMode, testRunner string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	switch mode {
	case TestEmitNative:
		// Default to robolectric since that's android.sngl's default
		// for testRunner — native mode without an explicit runner
		// means "run under :app:testDebugUnitTest".
		runner := testRunner
		if runner == "" {
			runner = "robolectric"
		}
		switch runner {
		case "device":
			b.WriteString("import androidx.activity.ComponentActivity\n")
			b.WriteString("import androidx.compose.ui.test.junit4.createAndroidComposeRule\n")
			b.WriteString(composeActionMarker)
			b.WriteString("import androidx.test.ext.junit.runners.AndroidJUnit4\n")
			b.WriteString("import org.junit.Assert.assertTrue\n")
			b.WriteString("import org.junit.Rule\n")
			b.WriteString("import org.junit.Test\n")
			b.WriteString("import org.junit.runner.RunWith\n\n")
			b.WriteString("@RunWith(AndroidJUnit4::class)\n")
			b.WriteString("class MainScreenTest {\n")
			b.WriteString("    @get:Rule val composeRule = createAndroidComposeRule<ComponentActivity>()\n\n")
		default: // robolectric
			b.WriteString("import androidx.activity.ComponentActivity\n")
			b.WriteString("import androidx.compose.ui.test.junit4.createAndroidComposeRule\n")
			b.WriteString(composeActionMarker)
			b.WriteString("import org.junit.Assert.assertTrue\n")
			b.WriteString("import org.junit.Rule\n")
			b.WriteString("import org.junit.Test\n")
			b.WriteString("import org.junit.runner.RunWith\n")
			b.WriteString("import org.robolectric.RobolectricTestRunner\n")
			b.WriteString("import org.robolectric.annotation.Config\n")
			b.WriteString("import org.robolectric.annotation.GraphicsMode\n\n")
			b.WriteString("@RunWith(RobolectricTestRunner::class)\n")
			b.WriteString("@Config(sdk = [33])\n")
			b.WriteString("@GraphicsMode(GraphicsMode.Mode.NATIVE)\n")
			b.WriteString("class MainScreenTest {\n")
			b.WriteString("    @get:Rule val composeRule = createAndroidComposeRule<ComponentActivity>()\n\n")
		}
	case TestEmitAgent:
		b.WriteString("import androidx.compose.ui.test.junit4.ComposeContentTestRule\n")
		b.WriteString(composeActionMarker)
		b.WriteString("import us.duckfam.git.jonathan.sngl.testagent.T\n")
		b.WriteString("import us.duckfam.git.jonathan.sngl.testagent.Registry\n\n")
		// Test bodies emitted below reference `composeTestRule` for
		// runOnUiThread / waitForIdle / onNodeWithTag. These functions
		// are package-level (not members of the JUnit wrapper class
		// MainScreenAgentTest), so they can't see the @Rule field
		// directly. Expose the rule as a package-scope lateinit var
		// initialised by MainScreenAgentTest before its @Test body
		// touches anything that calls into the test functions.
		b.WriteString("lateinit var composeTestRule: ComposeContentTestRule\n\n")
	}

	for i, fn := range fns {
		suffix := suffixes[i]
		compRecvs := compReceiverSet(fn)
		// Pick the first component-typed param's name as the local
		// receiver so `<recv>.<field>` reads in the lowered body
		// resolve. Falls back to "c" for assertion-only tests with
		// no component param.
		recv := "c"
		for _, p := range fn.Params {
			if p.Type != nil && p.Type.Kind == ir.TypeComponent {
				recv = p.Name
				break
			}
		}
		ctxCounts := map[string]int{}
		switch mode {
		case TestEmitNative:
			fmt.Fprintf(&b, "    @Test fun test%s() {\n", suffix)
			// Native mode has no TestModelAccessor; construct the
			// hoisted state directly. composeRule.setContent mounts
			// the screen so mutableStateOf-backed fields work.
			fmt.Fprintf(&b, "        val %s = MainScreenState()\n", recv)
			// lowerTestStmt emits `composeTestRule`; the native
			// wrapper names the rule `composeRule`. Bind one to the
			// other so those per-stmt lowerings resolve.
			b.WriteString("        @Suppress(\"UNUSED_VARIABLE\") val composeTestRule = composeRule\n")
			fmt.Fprintf(&b, "        composeRule.setContent { MainScreen(%s) }\n", recv)
			for _, s := range fn.Block {
				for _, line := range lowerTestStmt(s, surf, compRecvs, ctxCounts, TestEmitNative) {
					fmt.Fprintf(&b, "        %s\n", line)
				}
			}
			b.WriteString("    }\n\n")
		case TestEmitAgent:
			fmt.Fprintf(&b, "fun test%s(t: T) {\n", suffix)
			// See the Go and JS lowerers: the instance is always built, the
			// declared name bound only when the test declared one.
			b.WriteString("    val " + testInstanceVar + " = newTestComponent()\n")
			b.WriteString("    setCurrentTestModel(" + testInstanceVar + ")\n")
			if recv := codegen.TestComponentParam(fn); recv != "" {
				fmt.Fprintf(&b, "    val %s = %s\n", recv, testInstanceVar)
			}
			for _, s := range fn.Block {
				for _, line := range lowerTestStmt(s, surf, compRecvs, ctxCounts, TestEmitAgent) {
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
		// SnglTestRegistration is a singleton with an init {} block so
		// callers can force-load registration by referencing the class
		// (Kotlin top-level `val` initialisers only run when something
		// touches the file's facade class, which can be optimised away
		// on Android).
		b.WriteString("object SnglTestRegistration {\n")
		b.WriteString("    init {\n")
		for i := range fns {
			suffix := suffixes[i]
			fmt.Fprintf(&b, "        Registry.register(%q, ::test%s)\n", suffix, suffix)
		}
		b.WriteString("    }\n")
		b.WriteString("    fun ensure() {}\n")
		b.WriteString("}\n")
	}

	src := b.String()
	imports := ""
	if strings.Contains(src, ".onNodeWithTag(") {
		imports = composeActionImports
	}
	return strings.Replace(src, composeActionMarker, imports, 1)
}

// compReceiverSet returns the names of fn's component-typed params. It is what
// lowerTestStmt takes as compRecvs, so `<recv>.<field>` reads are recognised
// whatever the param is called.
func compReceiverSet(fn *ir.Func) map[string]bool {
	out := map[string]bool{}
	for _, p := range fn.Params {
		if p.Type != nil && p.Type.Kind == ir.TypeComponent {
			out[p.Name] = true
		}
	}
	return out
}

// TestSurface is what a test's component receiver exposes, as the emitters
// need to tell one read from another. Two sets rather than two parameters
// because every function that takes one takes the other.
type TestSurface struct {
	// MethodFields is every component func and computed package func: a name
	// the state object does not hold a cell for.
	MethodFields map[string]bool
	// StateFields is every var a window, the package or a component owns --
	// the cells the state object declares.
	StateFields map[string]bool
}
