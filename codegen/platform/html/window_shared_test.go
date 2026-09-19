package html

import (
	"reflect"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/canvasutil"

	"git.duckfam.us/jonathan/sngl/ir"
)

// TestWindowSharedDerivesPackageAnalysisOnce pins the split a site of many
// pages depends on: the package-derived analysis is walked once per
// compilation, and each window still gets one of its own to emit against.
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

	// Derived once. Each is asserted by identity across the two windows, since
	// two equal answers are what a per-window walk produces too.
	if !shared.derived {
		t.Fatal("windowShared derived nothing: every window re-walks the package")
	}
	if first.dt != second.dt {
		t.Error("the dep tracker is rebuilt per window")
	}
	firstIDs, _ := shared.canvases(pkg)
	secondIDs, _ := shared.canvases(pkg)
	if !sameCanvasMap(firstIDs, secondIDs) {
		t.Error("the canvas metadata is recollected per window")
	}
	owners := shared.ownerList(pkg)
	if again := shared.ownerList(pkg); len(owners) == 0 || len(again) == 0 || &owners[0] != &again[0] {
		t.Error("the owner list is re-walked per call")
	}
	if len(owners) != 3 {
		t.Errorf("ownerList = %d owners, want 3 (package + two windows)", len(owners))
	}

	// And the analysis, alone among them, is a copy: html writes to one
	// through codegen.OptimizeMutation.
	if first.CommonAnalysis == shared.common || second.CommonAnalysis == shared.common {
		t.Error("a window emits against the shared analysis itself; its writes would reach every other window")
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
}

func sameCanvasMap(x, y map[string]*canvasutil.Meta) bool {
	return reflect.ValueOf(x).UnsafePointer() == reflect.ValueOf(y).UnsafePointer()
}
