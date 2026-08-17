package golang

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// testIRContext builds a GoIRContext for rendering a test body. The
// ExprCtx is backed by an empty package: idents in a test body are either
// locals (params/loop vars, registered below) or — for component state —
// reached as `c.<field>` Selects on a RawFieldAccess receiver, so no
// package-level resolution is required and an empty Pkg keeps Resolve from
// `m.`-prefixing bare idents (matching the legacy empty-scope behavior).
//
//   - Component-typed params get RawFieldAccess so `c.count` reads the
//     unexported Model field directly.
//   - MethodFields names the fields whose `recv.<field>` lowers to a
//     zero-arg method call (component methods / computeds).
//   - Each param binds as a Local.
func testIRContext(fn *ir.Func, methodFields map[string]bool) *GoIRContext {
	ctx := codegen.NewExprCtx(&ir.Package{})
	ctx.RawFieldAccess = map[string]bool{}
	ctx.MethodFields = methodFields
	gc := NewIRContext(ctx)
	for _, p := range fn.Params {
		gc.Ctx.Locals[p.Name] = true
		if p.Type != nil && p.Type.Kind == ir.TypeComponent {
			gc.Ctx.RawFieldAccess[p.Name] = true
		}
	}
	return gc
}

// lowerTestStmt translates one statement of a SNGL test body to Go.
// `t.assert(expr)` becomes an `if !(expr)` failure check; other Test-
// namespace calls (setLocale, tick, test, wait, …) are stubbed as
// comments so the lowered file still compiles while the runner's
// support for them is incrementally filled in. Everything else falls
// through to the generic Go statement renderer (assigns, toggles,
// component-method calls) on GoIRContext, which lets test bodies exercise
// component state and methods.
func lowerTestStmt(s ir.Stmt, gc *GoIRContext) []string {
	if call, ok := s.(*ir.CallStmt); ok {
		if line, ok := lowerTestAssert(call, gc); ok {
			return []string{line}
		}
		if line, ok := lowerEventTrigger(call, gc); ok {
			return []string{line}
		}
		if c := call.Call; c != nil && c.Func != nil && c.Func.Receiver == "Test" {
			if lines, ok := lowerTestSetContext(c, gc); ok {
				return lines
			}
			if line, ok := lowerTestSnapshot(c, gc); ok {
				return []string{line}
			}
			return []string{fmt.Sprintf("// TODO: lower t.%s — not implemented in this platform's test runner", c.Func.Name)}
		}
	}
	if ifStmt, ok := s.(*ir.If); ok {
		return lowerTestIf(ifStmt, gc)
	}
	if forStmt, ok := s.(*ir.For); ok {
		return lowerTestFor(forStmt, gc)
	}
	if line, ok := lowerTestRawFieldWrite(s, gc); ok {
		return []string{line}
	}
	return gc.EvalStmt(s)
}

// lowerTestRawFieldWrite handles a test-scope write to `c.<field>` where `c`
// is a RawFieldAccess receiver. The generic GoIRContext mutation path
// capitalizes the field via ExportName (`c.Sel`), but in test scope the
// lowered `_test.go` lives in the same Go package as the Model and must
// write the unexported field directly (`c.sel`). Going through the
// Set<Field> setter is platform-dependent (value-receiver returns Model on
// bubbletea, pointer-receiver returns void on fyne/gtk4), so direct field
// writes are the simplest correct lowering; tests assert on raw field state,
// not reactively-derived view output. Event-driven reactivity is exercised
// via the `c.<id>.@event()` form (lowerEventTrigger). Mirrors the legacy
// translateIRMutation Assign special case byte-for-byte.
func lowerTestRawFieldWrite(s ir.Stmt, gc *GoIRContext) (string, bool) {
	assign, ok := s.(*ir.Assign)
	if !ok {
		return "", false
	}
	sel, ok := assign.Target.(*ir.Select)
	if !ok {
		return "", false
	}
	id, ok := sel.Operand.(*ir.Ident)
	if !ok || !gc.rawFieldAccess(id) {
		return "", false
	}
	value := gc.EvalExpr(assign.Value)
	target := fmt.Sprintf("%s.%s", id.Name, sel.Field)
	return target + " " + assignOpStr(assign.Op) + " " + value, true
}

// lowerTestIf emits `if <cond> { <body> } [else { <else> }]` where each
// branch's statements recurse through lowerTestStmt so test intrinsics
// (t.assert, event triggers, t.setContext) keep their dedicated lowering
// inside conditionals.
func lowerTestIf(s *ir.If, gc *GoIRContext) []string {
	cond := gc.EvalExpr(s.Cond)
	out := []string{fmt.Sprintf("if %s {", cond)}
	for _, b := range s.Body {
		for _, line := range lowerTestStmt(b, gc) {
			out = append(out, "\t"+line)
		}
	}
	if len(s.Else) > 0 {
		out = append(out, "} else {")
		for _, b := range s.Else {
			for _, line := range lowerTestStmt(b, gc) {
				out = append(out, "\t"+line)
			}
		}
	}
	out = append(out, "}")
	return out
}

// lowerTestFor mirrors GoIRContext's component-method for-loop lowering
// but recurses on body statements through lowerTestStmt so test intrinsics
// inside the loop body still get their dedicated lowering. Loop vars bind
// as locals on a forked context.
func lowerTestFor(s *ir.For, gc *GoIRContext) []string {
	iterExpr := gc.EvalExpr(s.Iter)
	loopGC := gc.WithLocal(s.Key)
	if s.Value != "" {
		loopGC = loopGC.WithLocal(s.Value)
	}

	var lines []string
	iterType := s.Iter.ExprType()
	if iterType != nil && iterType.Kind == ir.TypeMap {
		valueVar := s.Value
		if valueVar == "" {
			valueVar = "_"
		}
		lines = append(lines, fmt.Sprintf("for %s, %s := range %s {", s.Key, valueVar, iterExpr))
	} else {
		indexVar := "_"
		if s.Value != "" {
			indexVar = s.Value
		}
		lines = append(lines, fmt.Sprintf("for %s, %s := range %s {", indexVar, s.Key, iterExpr))
	}
	for _, stmt := range s.Body {
		for _, l := range lowerTestStmt(stmt, loopGC) {
			lines = append(lines, "\t"+l)
		}
	}
	lines = append(lines, "}")
	return lines
}

// lowerTestSetContext recognises a `t.setContext(ctxName, value)` call and
// emits a placeholder variable assignment for the context override. The
// emitted var `__test_ctx_<name>` records the intended value so it is
// visible in the generated source, but does NOT yet wire the override into
// the test component's mount-time provider tree — that requires deeper
// integration with the platform's newTestComponent scaffold and is left for
// a follow-up task.
//
// The generated statement always compiles: a blank-identifier assignment
// prevents "declared and not used" errors when the var is referenced nowhere
// else.
func lowerTestSetContext(c *ir.Call, gc *GoIRContext) ([]string, bool) {
	if c.Func == nil || c.Func.Receiver != "Test" || c.Func.Name != "setContext" {
		return nil, false
	}
	// Args: [0] = t receiver (implicit), [1] = context name (*ir.ContextRead),
	// [2] = value — the checker emits three args total (receiver + 2 user args).
	// Locate the ContextRead among the args: it is the first arg after the
	// receiver that is an *ir.ContextRead.
	var ctxName string
	var valExpr string
	for _, a := range c.Args {
		if cr, ok := a.Value.(*ir.ContextRead); ok && ctxName == "" {
			ctxName = cr.Ref.Name
		} else if ctxName != "" && valExpr == "" {
			valExpr = gc.EvalExpr(a.Value)
		}
	}
	if ctxName == "" {
		return nil, false
	}
	if valExpr == "" {
		valExpr = `""`
	}
	// Direct field assignment: after NoContext, every component in
	// Reach(ctx) has a synthesized __ctx_<name> Var on its Model. Tests
	// reach state via the same-package field (newTestComponent returns
	// the Model with raw-field access) so the override propagates into
	// any subsequent c.<method>() call without needing a setter.
	fieldName := "__ctx_" + ctxName
	// Find the component-typed test param to assign on. Tests typically
	// declare a single component param (e.g. `c Counter`); for multiple,
	// emit one assignment per matching receiver to keep behaviour explicit.
	var receivers []string
	for name := range gc.Ctx.RawFieldAccess {
		receivers = append(receivers, name)
	}
	if len(receivers) == 0 {
		// Fallback: no component-typed param. Record intent so the test still
		// compiles; the override won't reach a Model field, but the value is
		// referenced (via blank) to avoid unused-variable errors.
		return []string{
			fmt.Sprintf("// t.setContext(%q, ...): no component receiver in scope; override discarded.", ctxName),
			fmt.Sprintf("_ = %s", valExpr),
		}, true
	}
	lines := make([]string, 0, len(receivers))
	for _, r := range receivers {
		lines = append(lines, fmt.Sprintf("%s.%s = %s", r, fieldName, valExpr))
	}
	return lines, true
}

// lowerEventTrigger matches the IR shape produced by an SNGL test body
// line like `c.inc.click()` — a CallStmt the checker tagged with an
// Event name (its callee is a chain of SelectExprs ending in the event
// field). Lowers to `<receiver>.<id><Event>()`, which platform codegen
// (gtk4 today) surfaces as a method on *Model that fires the matching
// widget signal / event so the test exercises the real bridge.
func lowerEventTrigger(call *ir.CallStmt, gc *GoIRContext) (string, bool) {
	c := call.Call
	if c == nil || c.AST == nil || c.Event == "" {
		return "", false
	}
	outerSel, ok := c.AST.Func.(*ast.SelectExpr)
	if !ok {
		return "", false
	}
	// outerSel.Operand is `c.inc` — another SelectExpr Operand:Ident{c},Field:"inc".
	innerSel, ok := outerSel.Operand.(*ast.SelectExpr)
	if !ok {
		return "", false
	}
	recvIdent, ok := innerSel.Operand.(*ast.IdentExpr)
	if !ok {
		return "", false
	}
	methodName := innerSel.Field + ExportName(c.Event)
	args := make([]string, len(c.Args))
	for i, a := range c.Args {
		args[i] = gc.EvalExpr(a.Value)
	}
	return fmt.Sprintf("%s.%s(%s)", recvIdent.Name, methodName, strings.Join(args, ", ")), true
}

// lowerTestSnapshot recognises a `t.snapshot(name)` call and emits a
// `t.Snapshot(name)` invocation. The capitalized method matches the
// runtime exposed by *testing.T-style wrappers (testagent.T.Snapshot in
// agent mode; native mode uses a small shim — see platform code).
func lowerTestSnapshot(c *ir.Call, gc *GoIRContext) (string, bool) {
	if c.Func == nil || c.Func.Receiver != "Test" || c.Func.Name != "snapshot" {
		return "", false
	}
	// Args[0] is the t receiver; Args[1] is the snapshot name.
	if len(c.Args) != 2 {
		return "", false
	}
	nameExpr := gc.EvalExpr(c.Args[1].Value)
	return fmt.Sprintf("t.Snapshot(%s)", nameExpr), true
}

func lowerTestAssert(call *ir.CallStmt, gc *GoIRContext) (string, bool) {
	c := call.Call
	if c == nil || c.Func == nil {
		return "", false
	}
	if c.Func.Receiver != "Test" || c.Func.Name != "assert" {
		return "", false
	}
	// Args[0] is the t receiver; assert expression is Args[1].
	if len(c.Args) != 2 {
		return "", false
	}
	exprGo := gc.EvalExpr(c.Args[1].Value)
	return fmt.Sprintf("if !(%s) { t.Errorf(\"assert failed: %%s\", %q) }", exprGo, exprGo), true
}

// TestEmitMode selects how LowerTestFile wraps the per-test bodies.
type TestEmitMode int

const (
	// TestEmitNative produces *_test.go-style funcs taking *testing.T;
	// the resulting file is consumed by `go test`.
	TestEmitNative TestEmitMode = iota
	// TestEmitAgent produces test funcs taking *testagent.T plus an
	// init() that RegisterTests each one. The resulting file is
	// linked alongside main.go in the agent-mode binary.
	TestEmitAgent
)

// LowerTestFile produces the entire source of a generated test file.
// Each function in `fns` is rendered through a body lowering identical
// to LowerTestFunc's (testagent.T mirrors *testing.T's API). The
// wrapper differs:
//
//   - Native: `package <pkg>` + import "testing" + funcs `func Test<X>(t *testing.T)`.
//   - Agent:  `package <pkg>` + import "git.duckfam.us/jonathan/sngl/pkg/go/testagent" + funcs `func test<X>(t *testagent.T)` + an init() that RegisterTests them.
func LowerTestFile(pkg string, fns []*ir.Func, suffixes []string, methodFields map[string]bool, mode TestEmitMode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	switch mode {
	case TestEmitNative:
		b.WriteString("import \"testing\"\n\n")
	case TestEmitAgent:
		b.WriteString("import \"git.duckfam.us/jonathan/sngl/pkg/go/testagent\"\n\n")
	}

	for i, fn := range fns {
		suffix := suffixes[i]
		funcName, paramType := wrapperHeader(suffix, mode)
		fmt.Fprintf(&b, "func %s(t *%s) {\n", funcName, paramType)
		b.WriteString("\tc := newTestComponent()\n")
		if mode == TestEmitAgent {
			b.WriteString("\tsetCurrentTestModel(c)\n")
		}
		b.WriteString("\t_ = c\n")
		gc := testIRContext(fn, methodFields)
		for _, s := range fn.Block {
			for _, line := range lowerTestStmt(s, gc) {
				fmt.Fprintf(&b, "\t%s\n", line)
			}
		}
		b.WriteString("}\n\n")
	}

	if mode == TestEmitAgent {
		b.WriteString("func init() {\n")
		for i := range fns {
			suffix := suffixes[i]
			fmt.Fprintf(&b, "\ttestagent.RegisterTest(%q, test%s)\n", suffix, suffix)
		}
		b.WriteString("}\n")
	}

	return b.String()
}

func wrapperHeader(suffix string, mode TestEmitMode) (funcName, paramType string) {
	switch mode {
	case TestEmitNative:
		return "Test" + suffix, "testing.T"
	case TestEmitAgent:
		return "test" + suffix, "testagent.T"
	}
	return "Test" + suffix, "testing.T"
}
