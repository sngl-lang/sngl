package golang

import (
	"fmt"
	"strings"

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
func LowerTestFunc(fn *ir.Func, suffix string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "func Test%s(t *testing.T) {\n", suffix)
	b.WriteString("\tc := newTestComponent()\n")
	scope := &codegen.ExprScope{
		LocalVars:      map[string]bool{},
		RawFieldAccess: map[string]bool{},
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
		if c := call.Call; c != nil && c.Func != nil && c.Func.Receiver == "Test" {
			return []string{fmt.Sprintf("// TODO: lower t.%s — not implemented in this platform's test runner", c.Func.Name)}
		}
	}
	return translateIRMutation(s, scope)
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
