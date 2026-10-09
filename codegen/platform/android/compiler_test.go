package android

import (
	"errors"
	"strings"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testutil"
	"duckfam.us/sngl/ir"
)

func hasErrors(diags []ir.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == ir.Error {
			return true
		}
	}
	return false
}

func firstError(diags []ir.Diagnostic) string {
	for _, d := range diags {
		if d.Severity == ir.Error {
			return d.Error()
		}
	}
	return ""
}

func compileAndVerify(t *testing.T, doc *ast.Document, pkg *ir.Package) []byte {
	t.Helper()
	ctx := codegen.NewCodegenCtx(&codegen.Request{Doc: doc, Pkg: pkg}, "android")
	src, err := CompileIR(ctx, Config{})
	// A second window is a program this platform refuses by design, and the
	// fixture harness compiles every fixture in testdata for it.
	if one := (*codegen.OneWindowError)(nil); errors.As(err, &one) {
		t.Skipf("shows one window: %v", one)
	}
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Verify basic structural elements of generated Kotlin
	code := string(src)
	if !strings.Contains(code, "package test.sngl.app") {
		t.Errorf("missing package declaration")
	}
	if !strings.Contains(code, "@Composable") {
		t.Errorf("missing @Composable annotation")
	}
	if !strings.Contains(code, "fun MainScreen()") {
		t.Errorf("missing MainScreen function")
	}
	// Check balanced braces
	opens := strings.Count(code, "{")
	closes := strings.Count(code, "}")
	if opens != closes {
		t.Errorf("unbalanced braces: %d opens, %d closes", opens, closes)
	}
	return src
}

// TestComputedNotDoubleEmitted guards against a regression where a nested
// component computed (registered in BOTH pkg.Funcs and main.Funcs) was emitted
// twice, producing "Conflicting declarations" in Kotlin. See codegen/iterate.go
// AllFuncs — all platforms must dedup funcs by pointer.
func TestComputedNotDoubleEmitted(t *testing.T) {
	src := `import . "sngl:ui"
import ui "sngl:ui"
ui.window {
    var count = 0
    func doubled() => count * 2
    text(value=string(doubled))
}`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: androidTarget(), Languages: androidLangs(), Targets: []ir.StaticTarget{{Platform: "android", Language: "kotlin"}}})
	if hasErrors(diags) {
		t.Fatalf("check: %s", firstError(diags))
	}
	gen := &Generator{}
	kotlinLang := codegen.LookupLang("kotlin")
	if err := lower.Lower(pkg, codegen.CapsOrNone(kotlinLang.LanguageIdentifier(), gen.PlatformIdentifier()), lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	code := string(compileAndVerify(t, doc, pkg))

	n := strings.Count(code, "by remember { derivedStateOf")
	if n != 1 {
		t.Errorf("expected computed emitted exactly once, got %d derivedStateOf decls", n)
	}
	if n < 1 {
		t.Errorf("computed appears to be dropped entirely")
	}
}

func TestFixtures(t *testing.T) {
	for s := range testutil.CodegenSamples(t) {
		if len(s.Errors) > 0 {
			continue
		}
		t.Run(s.Name, func(t *testing.T) {
			doc, err := parser.Parse(s.Filename, []byte(s.Source))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			pkg, diags := checker.Check(doc, &checker.Config{FS: s.FS, Dir: s.Dir, IsMain: true, Platforms: androidTarget(), Languages: androidLangs(), Targets: []ir.StaticTarget{{Platform: "android", Language: "kotlin"}}})
			if hasErrors(diags) {
				t.Fatalf("check: %s", firstError(diags))
			}
			gen := &Generator{}
			kotlinLang := codegen.LookupLang("kotlin")
			if err := lower.Lower(pkg, codegen.CapsOrNone(kotlinLang.LanguageIdentifier(), gen.PlatformIdentifier()), lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
				t.Fatalf("lower: %v", err)
			}
			compileAndVerify(t, doc, pkg)
		})
	}
}
