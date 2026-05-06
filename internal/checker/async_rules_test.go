package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
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
	// async itself.  The checker must reject it with the parameterized-reactive
	// error because the settled-state lowering cannot key per-param.
	src := `
import api "js://app/api"

func greet(name string) => api.fetchHello(name)
`
	_, diags := checkPkgWithImports(src, asyncNativeImport("js://app/api", "fetchHello"))
	if !hasError(diags, "async expression not allowed in parameterized reactive context") {
		t.Errorf("expected parameterized-async error; got: %v", diags)
	}
}

func TestCheckAsync_ParameterizedAsync_ZeroParam_OK(t *testing.T) {
	// A zero-param async func is allowed (the settled-state lowering handles it).
	src := `
import api "js://app/api"

func greeting() => api.fetchHello("world")
`
	_, diags := checkPkgWithImports(src, asyncNativeImport("js://app/api", "fetchHello"))
	if hasError(diags, "async expression not allowed in parameterized reactive context") {
		t.Errorf("unexpected parameterized-async error for zero-param func; got: %v", diags)
	}
}
