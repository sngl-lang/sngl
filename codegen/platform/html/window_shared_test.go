package html

import (
	"reflect"
	"testing"

	"duckfam.us/sngl/codegen/canvasutil"

	"duckfam.us/sngl/ir"
)

// TestWindowSharedDerivesPackageAnalysisOnce pins the split a site of many
// pages depends on: the package-derived analysis is walked once per
// compilation, and each window still gets one of its own to emit against.
func TestWindowSharedDerivesPackageAnalysisOnce(t *testing.T) {
	pkg := &ir.Package{
		Vars: []*ir.Var{{Name: "count", Type: ir.TypInt}},
		Body: []ir.Stmt{
			&ir.NodeInst{Name: "alpha"},
			&ir.NodeInst{Name: "beta"},
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
	if len(owners) != 1 {
		t.Errorf("ownerList = %d owners, want 1 (the package)", len(owners))
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

// The var registry is two maps for two lifetimes, and a test for each: the
// package's model vars are one table however many pages read it, and what a
// page synthesizes for a name no declaration owns stays that page's.
func TestVarRegistrySharesTheModelVarsAndNotThePlaceholders(t *testing.T) {
	count := &ir.Var{Name: "count"}
	pkg := &ir.Package{
		Vars: []*ir.Var{count},
		Body: []ir.Stmt{&ir.NodeInst{Name: "alpha"}, &ir.NodeInst{Name: "beta"}},
	}
	shared := newWindowShared("", nil)

	base := shared.modelVarsByName(pkg)
	if again := shared.modelVarsByName(pkg); !sameVarMap(base, again) {
		t.Error("the model-var table is rebuilt per call; it is the package's, and a page does not change it")
	}
	if base["count"] != count {
		t.Fatalf("the table lost the package's own var: %v", base)
	}

	alpha, beta := newVarRegistry(base), newVarRegistry(base)
	if alpha.lookup("count") != count || beta.lookup("count") != count {
		t.Error("a declared var did not resolve to the declaration")
	}

	a, b := alpha.lookup("__promoted"), beta.lookup("__promoted")
	if a == nil || b == nil {
		t.Fatal("an undeclared name got no placeholder")
	}
	if a == b {
		t.Error("two pages share one placeholder; each synthesizes its own")
	}
	if _, leaked := base["__promoted"]; leaked {
		t.Error("a page's placeholder reached the shared table, where every other page reads it")
	}
}

func sameVarMap(x, y map[string]*ir.Var) bool {
	return reflect.ValueOf(x).UnsafePointer() == reflect.ValueOf(y).UnsafePointer()
}

// The package's script is walked once per compilation for what its node ops
// build and for the funcs a document's writes are dropped from; a document's
// own body is walked for its own.
func TestWindowSharedWalksTheScriptOnce(t *testing.T) {
	writeVisible := func() ir.Stmt {
		return &ir.Assign{
			Target: &ir.Select{Operand: &ir.Ident{Name: "page"}, Field: "visible"},
			Value:  &ir.Literal{Type: ir.TypBool, Value: "false"},
		}
	}
	create := &ir.Func{Name: ir.NodeOpCreateNode, Intrinsic: ir.NodeOpCreateNode}
	shared := &ir.Func{Name: "shared", Block: []ir.Stmt{
		&ir.LocalVar{Name: "w", Init: &ir.Call{Func: create, Args: []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Value: windowElement}}}}},
		writeVisible(),
	}}
	pkg := &ir.Package{Funcs: []*ir.Func{shared}}
	ws := newWindowShared("", nil)

	first := newHTMLGen(pkg, nil, htmlConfig{}, ws)
	second := newHTMLGen(pkg, nil, htmlConfig{}, ws)
	if !first.windowIDs()["w"] {
		t.Fatalf("windowIDs = %v, want w", first.windowIDs())
	}
	if reflect.ValueOf(first.windowIDs()).UnsafePointer() != reflect.ValueOf(second.windowIDs()).UnsafePointer() {
		t.Error("each document re-walks the package for the names a Window is created under")
	}

	doc := &ir.NodeInst{ID: "page"}
	handler := &ir.Func{Block: []ir.Stmt{writeVisible()}}
	body := []ir.Stmt{&ir.NodeInst{Name: "div", Handlers: []ir.EventHandler{{Name: "click", Func: handler}}}}
	ws.respellWindowWrites(pkg, doc, body)
	if len(shared.Block) != 1 || len(handler.Block) != 0 {
		t.Fatalf("a write to the document survived: package %d stmts, body %d", len(shared.Block), len(handler.Block))
	}
	funcs := ws.pkgFuncs
	ws.respellWindowWrites(pkg, doc, nil)
	if len(funcs) == 0 || &funcs[0] != &ws.pkgFuncs[0] {
		t.Error("each document re-collects the package's funcs")
	}
}
