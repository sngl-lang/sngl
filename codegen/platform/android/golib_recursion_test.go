package android

import (
	"git.duckfam.us/jonathan/sngl/ir"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// TestGoLibRecursiveFuncFreeCall guards audit bugs.md #31: a recursive user
// func emitted into the android go-lib (gomobile) module must render its
// self-call as a free, exported package-level function (`Fib(...)`), not as a
// Model-receiver method (`m.fib(...)`) — there is no `m` in scope inside the
// free function, so the latter produced Go that does not compile.
func TestGoLibRecursiveFuncFreeCall(t *testing.T) {
	src := `import . "sngl:std"
func fib(n int) int {
    if n < 2 { return n }
    return fib(n - 1) + fib(n - 2)
}
component main {
    var seed = 10
    text(value=string(fib(seed)))
}`
	doc, err := parser.Parse("app.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: androidTarget(), Targets: []ir.StaticTarget{{Platform: "android", Language: "kotlin"}}})
	if hasErrors(diags) {
		t.Fatalf("check: %s", firstError(diags))
	}
	gen := &Generator{}
	kotlinLang := codegen.LookupLang("kotlin")
	if err := lower.Lower(pkg, gen.Capabilities(kotlinLang).ToLowerCaps(), lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	ctx := codegen.NewCodegenCtx(&codegen.Request{Doc: doc, Pkg: pkg}, "android")

	golib := string(emitGoLibIR(ctx))
	if !strings.Contains(golib, "func Fib(") {
		t.Fatalf("expected go-lib to emit a free func Fib; got:\n%s", golib)
	}
	if strings.Contains(golib, "m.fib") || strings.Contains(golib, "m.Fib") {
		t.Errorf("recursive call must not use a Model receiver in the free function:\n%s", golib)
	}
	if !strings.Contains(golib, "Fib((n - 1))") && !strings.Contains(golib, "Fib(n - 1)") {
		t.Errorf("recursive call should render as a free-function call Fib(n-1):\n%s", golib)
	}
}
