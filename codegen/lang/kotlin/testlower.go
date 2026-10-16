package kotlin

import (
	"fmt"
	"strings"
	"sync"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
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

// lowerTestBody lowers a test body's statements, keeping a write and the
// synthesized calls a lowering placed after it -- passEffect's mark and settle
// -- on the UI thread together, with one wait for idle after them: waiting
// between the two let Compose recompose, rekeying a DisposableEffect, before
// the `@update` the write owed had run.
func lowerTestBody(block []ir.Stmt, surf TestSurface, compRecvs map[string]bool, ctxCounts map[string]int, mode TestEmitMode) []string {
	var out []string
	for _, s := range block {
		lines := lowerTestStmt(s, surf, compRecvs, ctxCounts, mode)
		if synthesizedCall(s) {
			if n := len(out); n >= 2 && out[n-2] == "}" && out[n-1] == testIdleLine {
				inner, wrapped := uiThreadBody(lines)
				if !wrapped {
					inner = lines
				}
				for i, l := range inner {
					if !wrapped {
						inner[i] = "    " + l
					}
				}
				out = append(append(out[:n-2], inner...), "}", testIdleLine)
				continue
			}
		}
		out = append(out, lines...)
	}
	return out
}

const testIdleLine = "composeTestRule.waitForIdle()"

// uiThreadBody is the statements lines runs on the UI thread, when lines is
// one runOnUiThread block followed by a wait for idle.
func uiThreadBody(lines []string) ([]string, bool) {
	n := len(lines)
	if n < 3 || lines[0] != "composeTestRule.runOnUiThread {" || lines[n-2] != "}" || lines[n-1] != testIdleLine {
		return nil, false
	}
	return lines[1 : n-2], true
}

func synthesizedCall(s ir.Stmt) bool {
	cs, ok := s.(*ir.CallStmt)
	return ok && cs.Call != nil && cs.Call.Func != nil && cs.Call.Func.Synthesized
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
		if c := n.Call; ir.IsTestMethodCall(c) {
			if lines, ok := lowerTestSetContext(c, ctxCounts); ok {
				return lines
			}
			// t.snapshot(name): agent-mode routes through the testagent
			// runtime's T.snapshot which invokes the platform-registered
			// capture and posts a snapshotAssert RPC. Native mode has no
			// snapshot story today, so it remains a TODO.
			if ir.IsTestCall(c, ir.TestSnapshotID) && mode == TestEmitAgent && len(c.Args) >= 2 {
				name := lowerTestExpr(c.Args[1].Value, TestSurface{}, nil)
				return []string{fmt.Sprintf("t.snapshot(%s)", name)}
			}
			return []string{fmt.Sprintf("// TODO: lower t.%s — not implemented in android test runner", c.Func.Name)}
		}
		if c := n.Call; c != nil && c.Func != nil && c.Func.IsMethod() && len(c.Args) > 0 {
			if id, ok := c.Args[0].Value.(*ir.Ident); ok && compRecvs[id.Name] {
				return []string{
					"composeTestRule.runOnUiThread {",
					"    " + lowerTestExpr(c, surf, compRecvs),
					"}",
					"composeTestRule.waitForIdle()",
				}
			}
		}
	case *ir.If:
		// A trigger on a node that may not be rendered is written under the
		// null test that says it is, so the branches are a test's own lines.
		out := []string{"if (" + lowerTestExpr(n.Cond, surf, compRecvs) + ") {"}
		for _, b := range n.Body {
			for _, l := range lowerTestStmt(b, surf, compRecvs, ctxCounts, mode) {
				out = append(out, "    "+l)
			}
		}
		if len(n.Else) > 0 {
			out = append(out, "} else {")
			for _, b := range n.Else {
				for _, l := range lowerTestStmt(b, surf, compRecvs, ctxCounts, mode) {
					out = append(out, "    "+l)
				}
			}
		}
		return append(out, "}")
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
	case *ir.Assign, *ir.Toggle:
		// A write to state -- `c.<var>…`, or a package var -- runs on the UI
		// thread so Compose recomposes before the next assertion. The write
		// itself is the ordinary translator's, which is what knows a list is a
		// SnapshotStateList and a map a read-only Map.
		if writesState(s, surf, compRecvs) {
			lines := []string{"composeTestRule.runOnUiThread {"}
			for _, l := range surf.translator().EvalStmt(s) {
				lines = append(lines, "    "+l)
			}
			return append(lines, "}", "composeTestRule.waitForIdle()")
		}
	}
	// Anything with no test-specific meaning is an ordinary statement, so the
	// ordinary translator emits it -- which is what `lowerTestExpr` already
	// does on the expression side and what Go's lowerTestStmt does here. A
	// comment instead meant a test calling one of its component's own methods
	// (`c.press(key)`) lowered to nothing at all, and the assertions after it
	// ran against a component nobody had touched.
	return surf.translator().EvalStmt(s)
}

// writesState reports whether an assignment's target is rooted at the
// instance a test holds, or is a member the instance holds by name.
func writesState(s ir.Stmt, surf TestSurface, compRecvs map[string]bool) bool {
	var target ir.Expr
	switch n := s.(type) {
	case *ir.Assign:
		target = n.Target
	case *ir.Toggle:
		target = n.Target
	}
	for {
		switch n := target.(type) {
		case *ir.Select:
			target = n.Operand
		case *ir.Index:
			target = n.Operand
		case *ir.Ident:
			if compRecvs[n.Name] {
				return true
			}
			kt := surf.translator()
			_, member := kt.IdentRewrites[n.Name]
			return member
		default:
			return false
		}
	}
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
	if !ir.IsTestCall(c, ir.TestAssertID) {
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
	tt, ok := codegen.EventTriggerTarget(c)
	if !ok {
		return "", false
	}
	id, event := tt.ID, c.Event
	// One copy of a node loops rendered is the node carrying its tag and, in
	// SnglCopy, its copy's key.
	tag := fmt.Sprintf("androidx.compose.ui.test.hasTestTag(%q)", id)
	if key := tt.Key(); key != nil {
		tag = fmt.Sprintf("(%s and androidx.compose.ui.test.SemanticsMatcher.expectValue(%s, %s))", tag, CopySemanticsKey, lowerTestExpr(key, TestSurface{}, nil))
	}
	if carriesState(c.Args) {
		// The tag sits on the override's root, the Row around the control and
		// its label, so the click targets the toggleable inside it. It flips
		// the control whatever the test passed: this is the input path, as
		// bubbletea's key press is.
		return fmt.Sprintf("composeTestRule.onNode(androidx.compose.ui.test.isToggleable() and (%s or androidx.compose.ui.test.hasAnyAncestor(%s)))%s; composeTestRule.waitForIdle()", tag, tag, clickAction), true
	}
	finder := "composeTestRule.onNode(" + tag + ")"
	action := composeAction(event, c.Args)
	if action == "" {
		return fmt.Sprintf("// TODO: no Compose-test action mapped for @%s on #%s", event, id), true
	}
	return fmt.Sprintf("%s.%s; composeTestRule.waitForIdle()", finder, action), true
}

// clickAction clicks the node a trigger found by running its click action, as
// fyne and gtk4 run a widget's callback, rather than by a touch: a touch lands
// where the node is drawn, and on a node past the bottom of the screen it
// lands nowhere.
const clickAction = ".performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.OnClick)"

// composeActionImports are what lowerEventTrigger's finder and actions name:
// extension functions, so a test file that clicks does not compile without
// them. Written only into a file that triggers an event, at the marker.
const (
	composeActionImports = "import androidx.compose.ui.test.onNodeWithTag\n" +
		"import androidx.compose.ui.test.performSemanticsAction\n" +
		"import androidx.compose.ui.test.performTextReplacement\n"
	composeActionMarker = "\x00composeActions\x00"
)

// composeAction maps a SNGL event name + invocation args to its
// Compose-UI-test equivalent. Input/change events take the text the field
// now holds, which performTextReplacement puts there: it clears the field
// first and then inserts, matching the "test sets the value" semantic
// regardless of prior content.
func composeAction(event string, args []ir.CallArg) string {
	switch event {
	case "click":
		return strings.TrimPrefix(clickAction, ".")
	case "input", "change", "changed":
		text := extractEventValue(args)
		return fmt.Sprintf("performTextReplacement(%s)", text)
	}
	return ""
}

// carriesState reports whether a trigger hands a control its on/off state --
// a checkbox's or a toggle's `@change(checked bool)`.
func carriesState(args []ir.CallArg) bool {
	if len(args) != 1 || args[0].Value == nil {
		return false
	}
	t := args[0].Value.ExprType()
	return t != nil && t.Kind == ir.TypeBool
}

// extractEventValue is the Kotlin source for the value a trigger hands an
// input or change event, its one argument, or an empty literal when it hands
// none.
func extractEventValue(args []ir.CallArg) string {
	if len(args) != 1 {
		return "\"\""
	}
	return lowerTestExpr(args[0].Value, TestSurface{}, nil)
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
	// `<recv>.<id> == null` asks whether the node is rendered now, which is
	// whether the semantics tree holds a node carrying its tag.
	if recv, id, present, ok := codegen.RefNullTest(e); ok && compRecvs[recv] {
		check := "isEmpty"
		if present {
			check = "isNotEmpty"
		}
		return fmt.Sprintf("composeTestRule.onAllNodesWithTag(%q).fetchSemanticsNodes().%s()", id, check)
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
		if compRecvs[n.Name] {
			return n.Name
		}
		return surf.translator().EvalExpr(n)
	case *ir.Select:
		// `<recv>.<id>[idx].<prop>` — Compose `onAllNodesWithTag(<id>)[idx]`
		// then read via composeNodeTextAt. Not for a list the state object
		// holds, whose element's field is an ordinary read.
		if idx, ok := n.Operand.(*ir.Index); ok {
			if inner, ok := idx.Operand.(*ir.Select); ok {
				if id, ok := inner.Operand.(*ir.Ident); ok && compRecvs[id.Name] && !surf.StateFields[inner.Field] {
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
			return id.Name + "." + SafeIdent(n.Field)
		}
		return lowerTestExpr(n.Operand, surf, compRecvs) + "." + selectField(n)
	case *ir.Index:
		operand := lowerTestExpr(n.Operand, surf, compRecvs)
		idx := lowerTestExpr(n.Idx, surf, compRecvs)
		return operand + "[" + idx + "]"
	case *ir.Call:
		// `c.f(…)` on the instance: a method of the state object, or a
		// `derivedStateOf` property when f is a computed.
		if f := n.Func; f != nil && f.IsMethod() && len(n.Args) > 0 {
			if id, ok := n.Args[0].Value.(*ir.Ident); ok && compRecvs[id.Name] {
				if codegen.IsComputed(f) {
					return id.Name + "." + f.Name
				}
				args := make([]string, 0, len(n.Args)-1)
				for _, a := range n.Args[1:] {
					args = append(args, lowerTestExpr(a.Value, surf, compRecvs))
				}
				return id.Name + "." + f.Name + "(" + strings.Join(args, ", ") + ")"
			}
		}
		// Delegate to the full expression translator for calls and any
		// shape lowerTestExpr's special-cases above don't already cover.
		// Method calls like `c.formatted()` and stdlib calls inside test
		// assertions both flow through here.
		return surf.translator().EvalExpr(e)
	}
	// Final fallback: defer to the full IR translator. Keeps test
	// expressions in lockstep with non-test Kotlin codegen rather than
	// emitting a TODO placeholder that fails Kotlin compilation.
	return surf.translator().EvalExpr(e)
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
			for _, line := range lowerTestBody(fn.Block, surf.forInstance(recv, fn), compRecvs, ctxCounts, TestEmitNative) {
				fmt.Fprintf(&b, "        %s\n", line)
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
			for _, line := range lowerTestBody(fn.Block, surf.forInstance(testInstanceVar, fn), compRecvs, ctxCounts, TestEmitAgent) {
				fmt.Fprintf(&b, "    %s\n", line)
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
	if strings.Contains(src, ".onAllNodesWithTag(") {
		imports = "import androidx.compose.ui.test.onAllNodesWithTag\n"
	}
	if strings.Contains(src, ".onNodeWithTag(") || strings.Contains(src, ".performSemanticsAction(") || strings.Contains(src, ".performTextReplacement(") {
		imports += composeActionImports
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
	// Pkg is the package under test, and Members what MainScreen spells each
	// state member as (`state.<name>`). With both, an expression the test
	// lowerer has no special case for renders the way MainScreen renders it,
	// through the instance the test holds.
	Pkg     *ir.Package
	Members map[string]string
	// Platform is the build's, which a platform's intrinsic emitters are
	// keyed by: android answers sngl:ui/nav's `current` that way, read
	// through the instance as MainScreen reads it.
	Platform string

	kt *KtIRContext
}

// forInstance is surf with its translator bound to the instance named recv.
// A test's own locals are left out of the rewrites, so a local sharing a
// state member's name stays a local.
func (surf TestSurface) forInstance(recv string, fn *ir.Func) TestSurface {
	if surf.Pkg == nil {
		return surf
	}
	locals := map[string]bool{}
	for _, p := range fn.Params {
		locals[p.Name] = true
	}
	_ = ir.WalkStmts(fn.Block, func(s ir.Stmt) error {
		switch n := s.(type) {
		case *ir.LocalVar:
			locals[n.Name] = true
		case *ir.For:
			locals[n.Key] = true
			if n.Value != "" {
				locals[n.Value] = true
			}
		}
		return nil
	})
	rewrites := map[string]string{}
	for name, spelled := range surf.Members {
		if locals[name] {
			continue
		}
		rewrites[name] = recv + strings.TrimPrefix(spelled, "state")
	}
	ec := codegen.NewExprCtx(surf.Pkg)
	ec.Platform = surf.Platform
	kc := NewIRContext(ec)
	kc.IdentRewrites = rewrites
	surf.kt = kc
	return surf
}

func (surf TestSurface) translator() *KtIRContext {
	if surf.kt != nil {
		return surf.kt
	}
	return testIRContext()
}

// A test body's sngl:test calls are answered here, where the body is lowered,
// rather than by an emitter.
func init() {
	codegen.DeclareLangImplements("kotlin", ir.TestAssertID, ir.TestSnapshotID, ir.TestSetContextID)
}

// CopySemanticsKey is the semantics property the screen declares and sets on
// each copy of a node a test indexes, holding the copy's key (ir.CopyProp).
const CopySemanticsKey = "SnglCopy"
