//go:build !js

package optimize

import (
	"duckfam.us/sngl/internal/trust"
	"os/exec"
	"testing"

	"duckfam.us/sngl/ir"
)

const jspurePath = "./internal/optimize/testdata/jspure"

// One build's pending calls are collected without regard to scheme and then
// partitioned into one generated program each. Neither language can run the
// other's calls, so a batch that reached the wrong program would not build —
// and a failure in one program must not be reported as the reason a call in
// the other could not fold.
func TestBatchSpansTwoSchemes(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	ctx := purepkgCtx(dir)
	ctx.nativeImports["jspure"] = &ir.NativeImport{
		ImportPath: jspurePath,
		Funcs:      []*ir.Func{jsTag},
	}
	ctx.nativeSchemes["jspure"] = "js"

	for _, c := range []struct {
		scheme, path string
		fn           *ir.Func
		args         []any
	}{
		{"go", purepkgPath, fnGreet, []any{"world"}},
		{"js", jspurePath, jsTag, []any{"world"}},
	} {
		if _, state, err := requestPureNativeFunc(ctx, c.scheme, c.path, c.fn, c.args); state != nativePending {
			t.Fatalf("%s:// %s did not go into the batch: state %v, err %v", c.scheme, c.fn.Foreign.Name, state, err)
		}
	}
	if got := len(ctx.native.order); got != 2 {
		t.Fatalf("batch holds %d requests, want 2", got)
	}
	if errs := runNativeRequests(ctx.evalCache(), trust.AllowAll(), ctx.dir, ir.IndexNativeDecls(ctx.pkg), ctx.native.order); len(errs) > 0 {
		t.Fatalf("running the batch: %v", errs)
	}

	for _, c := range []struct {
		scheme, path string
		fn           *ir.Func
		args         []any
		want         string
	}{
		{"go", purepkgPath, fnGreet, []any{"world"}, `"Hello, world!"`},
		{"js", jspurePath, jsTag, []any{"world"}, `"world-js"`},
	} {
		v, state, err := requestPureNativeFunc(ctx, c.scheme, c.path, c.fn, c.args)
		if err != nil || state != nativeReady {
			t.Errorf("%s:// %s: state %v, err %v", c.scheme, c.fn.Foreign.Name, state, err)
			continue
		}
		lit, _ := v.(*ir.Literal)
		if lit == nil || lit.Value != c.want[1:len(c.want)-1] {
			t.Errorf("%s:// %s folded to %#v, want %s", c.scheme, c.fn.Foreign.Name, v, c.want)
		}
	}
}

var jsTag = &ir.Func{
	Name:    "tag",
	Foreign: ir.Foreign{Name: "tag", Path: jspurePath},
	Purity:  ir.PurityPure,
	Params:  []*ir.Param{{Name: "s", Type: ir.TypString}},
	Return:  ir.TypString,
}
