package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// win is what buildWindow produces, minus everything these tests do not read:
// the declaration carrying the #[builtin("window")] mark is what IsWindowNode
// matches, and a literal without it is an ordinary node.
func win(id string, body ...ir.Stmt) *ir.Window {
	return &ir.Window{
		Name:      "window",
		ID:        id,
		Component: &ir.Component{Name: "window", Builtin: ir.BuiltinWindow},
		Children:  body,
	}
}

// The pass searches every window's body for a window, which is the same search
// ir.Owners runs on every call. Handing the answer forward is what stops the
// passes after this one -- and codegen after them -- from walking every page
// again to reach the same nothing.
func TestWindowNestingHandsItsProofForward(t *testing.T) {
	pkg := &ir.Package{Windows: []*ir.Window{win("home")}}
	if err := lowerWindowNesting(pkg, Features{}, Options{}); err != nil {
		t.Fatalf("lowerWindowNesting: %v", err)
	}
	if !pkg.WindowsFlat {
		t.Error("the pass proved no window holds another and did not record it; ir.Owners re-proves it per call")
	}
}

// A refused program records nothing: the claim is only ever true of a package
// that got past this pass.
func TestWindowNestingRecordsNothingWhenItRefuses(t *testing.T) {
	pkg := &ir.Package{Windows: []*ir.Window{win("home", win("inner"))}}
	if err := lowerWindowNesting(pkg, Features{}, Options{}); err == nil {
		t.Fatal("a window nested in a window was not refused")
	}
	if pkg.WindowsFlat {
		t.Error("a refused package was marked flat")
	}
}
