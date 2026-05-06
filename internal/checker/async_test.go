package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// asyncNativeImport builds a *ir.NativeImport whose single function is async.
func asyncNativeImport(scheme, name string) map[string]*ir.NativeImport {
	fn := &ir.Func{
		Name:       name,
		Params:     []*ir.Param{{Name: "url", Type: &ir.Type{Kind: ir.TypeString}}},
		Return:     &ir.Type{Kind: ir.TypeString},
		IsAsync:    true,
		NativePkg:  "api",
		NativeName: "api." + name,
	}
	return map[string]*ir.NativeImport{
		scheme: {
			ImportPath: "api",
			Funcs:      []*ir.Func{fn},
		},
	}
}

// checkPkgWithImports is like checkWithImports but returns the package too.
func checkPkgWithImports(src string, native map[string]*ir.NativeImport) (*ir.Package, []ir.Diagnostic) {
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		return nil, []ir.Diagnostic{{Severity: ir.Error, Msg: err.Error()}}
	}
	r := &importResolver{native: native}
	return checker.Check(doc, &checker.Config{IsMain: true, Resolver: r})
}

// findFunc returns the *ir.Func with the given name from pkg.Funcs, or nil.
func findFunc(pkg *ir.Package, name string) *ir.Func {
	for _, f := range pkg.Funcs {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// TestAsyncPropagatesThroughLambda verifies that a function whose body
// contains a lambda that calls an async native is itself marked IsAsync
// after analyzeAsync() runs.
func TestAsyncPropagatesThroughLambda(t *testing.T) {
	src := `
import api "go://api"

func outer() {
    var f = func() { api.fetchHello("x") }
    f()
}
`
	pkg, diags := checkPkgWithImports(src, asyncNativeImport("go://api", "fetchHello"))
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Logf("diagnostic: %s", d.Error())
		}
	}
	if pkg == nil {
		t.Fatal("pkg is nil")
	}
	outer := findFunc(pkg, "outer")
	if outer == nil {
		t.Fatal("outer not found in pkg.Funcs")
	}
	if !outer.IsAsync {
		t.Fatalf("outer.IsAsync = false; want true (lambda body calls async native)")
	}
}
