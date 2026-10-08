package checker_test

import (
	"testing"

	"duckfam.us/sngl/ir"
)

// hasError returns true if any error diagnostic contains the substring needle.
func hasError(diags []ir.Diagnostic, needle string) bool {
	for _, d := range diags {
		if d.Severity == ir.Error && contains(d.Msg, needle) {
			return true
		}
	}
	return false
}

func TestCheckAsync_ReservedPrefix_Func(t *testing.T) {
	src := `
func __async_foo() => "hello"
`
	_, diags := checkPkgWithImports(src, nil)
	if !hasError(diags, "uses reserved prefix") {
		t.Errorf("expected 'uses reserved prefix' error for __async_foo; got: %v", diags)
	}
}

func TestCheckAsync_ReservedPrefix_Var(t *testing.T) {
	src := `
var __hoist_x int = 0
`
	_, diags := checkPkgWithImports(src, nil)
	if !hasError(diags, "uses reserved prefix") {
		t.Errorf("expected 'uses reserved prefix' error for __hoist_x; got: %v", diags)
	}
}

func TestCheckAsync_ReservedPrefix_Clean(t *testing.T) {
	// Ensure normal names pass without error.
	src := `
func greet() => "hello"
var count int = 0
`
	_, diags := checkPkgWithImports(src, nil)
	if hasError(diags, "uses reserved prefix") {
		t.Errorf("unexpected reserved-prefix error for normal names; got: %v", diags)
	}
}

func TestCheckAsync_ParameterizedAsync(t *testing.T) {
	// greet has a param and transitively calls an async native, so it becomes
	// async itself.  When referenced from a visual prop expression (reactive
	// context), the checker must reject it — the settled-state lowering cannot
	// key per-param.
	src := `
import api "js:app/api"

func greet(name string) => api.fetchHello(name)

window #main(title="Main") {
    text(value=greet("World"))
}
`
	_, diags := checkPkgWithImports(src, asyncNativeImport("js:app/api", "fetchHello"))
	if !hasError(diags, "async expression not allowed in parameterized reactive context") {
		t.Errorf("expected parameterized-async error; got: %v", diags)
	}
}

func TestCheckAsync_ParameterizedAsync_ZeroParam_OK(t *testing.T) {
	// A zero-param async func is allowed (the settled-state lowering handles it).
	src := `
import api "js:app/api"

func greeting() => api.fetchHello("world")
`
	_, diags := checkPkgWithImports(src, asyncNativeImport("js:app/api", "fetchHello"))
	if hasError(diags, "async expression not allowed in parameterized reactive context") {
		t.Errorf("unexpected parameterized-async error for zero-param func; got: %v", diags)
	}
}

func TestCheckAsync_ParameterizedAsyncInHandler_OK(t *testing.T) {
	// saveItem has a param and calls an async native, so it becomes async.
	// However, it is only called from a @click event handler — NOT from a
	// reactive prop expression.  Event handlers run as async wrappers and can
	// freely await parameterized async callees.  Rule 2 must NOT fire here.
	src := `
import api "js:app/api"

func saveItem(id int) { api.fetchHello("item") }

window #main(title="Main") {
    button(text="Save", @click { saveItem(42) })
}
`
	_, diags := checkPkgWithImports(src, asyncNativeImport("js:app/api", "fetchHello"))
	if hasError(diags, "async expression not allowed in parameterized reactive context") {
		t.Errorf("unexpected parameterized-async error for handler-only call; got: %v", diags)
	}
}

// TestCheckAsync_ParameterizedAsyncInLambdaInProp verifies that Rule 2 fires
// when a parameterized async func is called inside a lambda that appears as a
// prop value (reactive context). This exercises the *ir.Lambda branch added to
// collectCalleesInExpr.
func TestCheckAsync_ParameterizedAsyncInLambdaInProp(t *testing.T) {
	// greet is parameterized and async (calls api.fetchHello).
	// It is called inside a lambda passed to list.map in a reactive prop,
	// so Rule 2 must fire even though the call is inside a lambda body.
	src := `
import api "js:app/api"

func greet(name string) => api.fetchHello(name)

var names list<string> = []

window #main(title="Main") {
    text(value=list.map(names, func(x string) => greet(x))[0])
}
`
	_, diags := checkPkgWithImports(src, asyncNativeImport("js:app/api", "fetchHello"))
	if !hasError(diags, "async expression not allowed in parameterized reactive context") {
		t.Errorf("expected parameterized-async error for async call inside lambda in prop; got: %v", diags)
	}
}

func TestCheckAsync_ParameterizedAsync_NotInVisual_OK(t *testing.T) {
	// A parameterized async func that is declared but never referenced from any
	// visual prop should NOT trigger Rule 2.  It may be used elsewhere (e.g.,
	// from a regular function body or not at all).
	src := `
import api "js:app/api"

func greet(name string) => api.fetchHello(name)
`
	_, diags := checkPkgWithImports(src, asyncNativeImport("js:app/api", "fetchHello"))
	if hasError(diags, "async expression not allowed in parameterized reactive context") {
		t.Errorf("unexpected parameterized-async error for func not used in reactive context; got: %v", diags)
	}
}
