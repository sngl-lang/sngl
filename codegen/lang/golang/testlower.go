package golang

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// LowerTestFunc renders a SNGL test function as a Go *testing.T test
// function. The output is a single self-contained Go source block
// suitable for inclusion in a `_test.go` file inside the temp module
// emitted by a platform RunTests.
//
// The caller is responsible for declaring a `newTestComponent()` helper
// in the same file; the lowered body references `c := newTestComponent()`.
//
// Component-typed params (e.g. `c counter`) are marked for raw-field
// access on the scope so reads and writes to component state lower as
// direct unexported field access (`c.count`, `c.count = 1`) — valid
// because the lowered test lives in the same Go package as the
// generated Model.
func LowerTestFunc(fn *ir.Func, suffix string, methodFields map[string]bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "func Test%s(t *testing.T) {\n", suffix)
	b.WriteString("\tc := newTestComponent()\n")
	// Tests that exercise pure literal arithmetic don't reference `c`;
	// `_ = c` keeps the local valid under Go's unused-variable rule.
	b.WriteString("\t_ = c\n")
	scope := &codegen.ExprScope{
		LocalVars:      map[string]bool{},
		RawFieldAccess: map[string]bool{},
		MethodFields:   methodFields,
	}
	for _, p := range fn.Params {
		scope.LocalVars[p.Name] = true
		if p.Type != nil && p.Type.Kind == ir.TypeComponent {
			scope.RawFieldAccess[p.Name] = true
		}
	}
	for _, s := range fn.Block {
		for _, line := range lowerTestStmt(s, scope) {
			fmt.Fprintf(&b, "\t%s\n", line)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// lowerTestStmt translates one statement of a SNGL test body to Go.
// `t.assert(expr)` becomes an `if !(expr)` failure check; other Test-
// namespace calls (setLocale, tick, test, wait, …) are stubbed as
// comments so the lowered file still compiles while the runner's
// support for them is incrementally filled in. Everything else falls
// through to the generic Go mutation translator (assigns, toggles,
// component-method calls), which lets test bodies exercise component
// state and methods.
func lowerTestStmt(s ir.Stmt, scope *codegen.ExprScope) []string {
	if call, ok := s.(*ir.CallStmt); ok {
		if line, ok := lowerTestAssert(call, scope); ok {
			return []string{line}
		}
		if line, ok := lowerEventTrigger(call, scope); ok {
			return []string{line}
		}
		if c := call.Call; c != nil && c.Func != nil && c.Func.Receiver == "Test" {
			if lines, ok := lowerTestSetContext(c, scope); ok {
				return lines
			}
			return []string{fmt.Sprintf("// TODO: lower t.%s — not implemented in this platform's test runner", c.Func.Name)}
		}
	}
	return translateIRMutation(s, scope)
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
func lowerTestSetContext(c *ir.Call, scope *codegen.ExprScope) ([]string, bool) {
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
			valExpr = translateIRExpr(a.Value, scope)
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
	for name := range scope.RawFieldAccess {
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
// line like `c.inc.@click()` — a CallStmt whose AST callee is a chain
// of SelectExprs ending in a field that starts with "@". Lowers to
// `<receiver>.<id><Event>()`, which platform codegen (gtk4 today)
// surfaces as a method on *Model that fires the matching widget
// signal / event so the test exercises the real bridge.
func lowerEventTrigger(call *ir.CallStmt, scope *codegen.ExprScope) (string, bool) {
	c := call.Call
	if c == nil || c.AST == nil {
		return "", false
	}
	outerSel, ok := c.AST.Func.(*ast.SelectExpr)
	if !ok || outerSel.Kind != ast.SelectEvent {
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
	event := outerSel.Field
	if event == "" {
		return "", false
	}
	methodName := innerSel.Field + ExportName(event)
	args := make([]string, len(c.Args))
	for i, a := range c.Args {
		args[i] = translateIRExpr(a.Value, scope)
	}
	return fmt.Sprintf("%s.%s(%s)", recvIdent.Name, methodName, strings.Join(args, ", ")), true
}

func lowerTestAssert(call *ir.CallStmt, scope *codegen.ExprScope) (string, bool) {
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
	exprGo := translateIRExpr(c.Args[1].Value, scope)
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
		b.WriteString("\t_ = c\n")
		scope := scopeFor(fn, methodFields)
		for _, s := range fn.Block {
			for _, line := range lowerTestStmt(s, scope) {
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

// scopeFor mirrors LowerTestFunc's scope construction so LowerTestFile
// shares identical state shape.
func scopeFor(fn *ir.Func, methodFields map[string]bool) *codegen.ExprScope {
	scope := &codegen.ExprScope{
		LocalVars:      map[string]bool{},
		RawFieldAccess: map[string]bool{},
		MethodFields:   methodFields,
	}
	for _, p := range fn.Params {
		scope.LocalVars[p.Name] = true
		if p.Type != nil && p.Type.Kind == ir.TypeComponent {
			scope.RawFieldAccess[p.Name] = true
		}
	}
	return scope
}
