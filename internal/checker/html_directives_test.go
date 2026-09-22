package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testtargets"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestHtmlPlacementDirectivesResolve verifies that the checker resolves
// html.frontend / html.backend as generic identity funcs (the wrapped value's
// type flows through) with no diagnostics.
func TestHtmlPlacementDirectivesResolve(t *testing.T) {
	src := `import . "sngl:ui"
import "sngl:platform/html"

component main node {
    var n = 0
    text(value="v {html.frontend(n)}")
    text(value="w {html.backend(n)}")
}`
	for _, d := range checkWithTargets(t, src) {
		t.Fatalf("unexpected diag: %s", d.Msg)
	}
}

// TestHtmlDirectiveIsPreservedIntrinsic verifies that after
// parse→check→optimize→lower an html.frontend(...) call still exists carrying
// Func.Intrinsic == "html.frontend" — i.e. the identity directive is NOT folded
// away by InlinePure, so the html placement analysis can still see it.
func TestHtmlDirectiveIsPreservedIntrinsic(t *testing.T) {
	src := "import . \"sngl:ui\"\nimport \"sngl:platform/html\"\n\ncomponent main node { var n = 0  text(value=\"x {html.frontend(n)}\") }"
	pkg := checkOptimizeLower(t, src)
	if !pkgHasIntrinsicCall(pkg, "html.frontend") {
		t.Fatal("html.frontend call was erased; must survive as an intrinsic for placement analysis")
	}
}

// checkOptimizeLower runs the full parse→check→optimize→lower pipeline that
// codegen sees, returning the lowered package.
func checkOptimizeLower(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	langs, plats := testtargets.Targets()
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Languages: langs, Platforms: plats})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Msg)
		}
	}
	if pkg == nil {
		t.Fatal("nil pkg")
	}
	if err := optimize.Optimize(pkg, &optimize.Config{}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	if err := lower.Lower(pkg, lower.Features{}, lower.Options{}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	return pkg
}

// pkgHasIntrinsicCall reports whether any expression in the package is a call
// to a func with the given Intrinsic id.
func pkgHasIntrinsicCall(pkg *ir.Package, id string) bool {
	found := false
	ir.WalkExprs(pkg, func(e ir.Expr) error {
		if c, ok := e.(*ir.Call); ok && c.Func != nil && c.Func.Intrinsic == id {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// checkWithTargets checks src with every registered target available. The
// placement directives are the html platform's own declarations now, so
// resolving `html.frontend` means the platform has to be registered -- the
// bare checkSrc config registers none.
func checkWithTargets(t *testing.T, src string) []ir.Diagnostic {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	langs, plats := testtargets.Targets()
	_, diags := checker.Check(doc, &checker.Config{IsMain: true, Languages: langs, Platforms: plats})
	var errs []ir.Diagnostic
	for _, d := range diags {
		if d.Severity == ir.Error {
			errs = append(errs, d)
		}
	}
	return errs
}
