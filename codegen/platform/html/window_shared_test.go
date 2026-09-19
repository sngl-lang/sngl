package html

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// TestWindowSharedDerivesPackageAnalysisOnce pins the split a site of many
// pages depends on: the package-derived analysis is walked once per
// compilation, and each window still gets one of its own to emit against.
//
// Without it the walk is per window over a package that holds every window's
// body, so a site is quadratic in its page count -- 8m27s of codegen for the
// docs site's 1044 pages.
func TestWindowSharedDerivesPackageAnalysisOnce(t *testing.T) {
	pkg := &ir.Package{
		Vars: []*ir.Var{{Name: "count", Type: ir.TypInt}},
		Windows: []*ir.Window{
			{Name: "alpha"},
			{Name: "beta"},
		},
	}
	shared := newWindowShared("", nil)

	first := newHTMLGen(pkg, nil, htmlConfig{}, shared)
	second := newHTMLGen(pkg, nil, htmlConfig{}, shared)

	if shared.common == nil {
		t.Fatal("windowShared holds no derived analysis: every window re-walks the package")
	}
	if first.CommonAnalysis == shared.common || second.CommonAnalysis == shared.common {
		t.Error("a window emits against the shared analysis itself; its emission would reach every other window")
	}
	if first.CommonAnalysis == second.CommonAnalysis {
		t.Error("two windows share one analysis")
	}
	if !first.ModelFields["count"] || !second.ModelFields["count"] {
		t.Error("a copied analysis lost the package's state")
	}
	first.ModelFields["count"] = false
	if !second.ModelFields["count"] {
		t.Error("one window's analysis reaches another's")
	}

	if first.dt != second.dt {
		t.Error("the dep tracker is rebuilt per window; it is a function of the package alone")
	}
	if got := shared.ownerList(pkg); len(got) != 3 {
		t.Errorf("ownerList = %d owners, want 3 (package + two windows)", len(got))
	}
}
