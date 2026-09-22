package lower_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A platform primitive's #[intrinsic] id is the emitting codegen's dispatch
// key, so one platform's means nothing to another: naming it from a build
// targeting something else has no answer, and html invented one --
// `<Switch onCheckedChange="...">`.
//
// A platform that does implement someone else's says so, which is what keeps
// this a rule rather than a wall. Both directions are asserted: without the
// claim the build fails, with it the node survives.
func TestAForeignPlatformPrimitiveIsRejectedUnlessClaimed(t *testing.T) {
	build := func(claims func(string) bool) error {
		foreign := &ir.Component{Name: "Switch", Intrinsic: "android:Switch"}
		main := &ir.Component{Name: "main"}
		main.Body = []ir.Stmt{&ir.NodeInst{Name: "Switch", Component: foreign}}
		pkg := &ir.Package{Components: []*ir.Component{foreign, main}, Symbols: ir.NewSymbolTable()}
		return lower.Lower(pkg, lower.Features{}, lower.Options{
			Platform:        "html",
			ClaimsIntrinsic: claims,
		})
	}

	err := build(nil)
	if err == nil {
		t.Fatal("a build targeting html accepted an android primitive")
	}
	if !strings.Contains(err.Error(), "android") || !strings.Contains(err.Error(), "html") {
		t.Errorf("the error names neither platform: %v", err)
	}

	// The claim is consulted, and it is asked for the id it would emit.
	var asked []string
	if err := build(func(id string) bool {
		asked = append(asked, id)
		return id == "android:Switch"
	}); err != nil {
		t.Fatalf("a platform claiming the primitive was still refused: %v", err)
	}
	if len(asked) != 1 || asked[0] != "android:Switch" {
		t.Errorf("ClaimsIntrinsic asked for %v, want [android:Switch]", asked)
	}

	// A claim for something else does not let it through.
	if err := build(func(id string) bool { return id == "android:Column" }); err == nil {
		t.Error("a platform claiming a different id let this one through")
	}

	// And the platform's own primitive needs no claim at all.
	own := &ir.Component{Name: "Div", Intrinsic: "html:Div"}
	main := &ir.Component{Name: "main"}
	main.Body = []ir.Stmt{&ir.NodeInst{Name: "Div", Component: own}}
	pkg := &ir.Package{Components: []*ir.Component{own, main}, Symbols: ir.NewSymbolTable()}
	if err := lower.Lower(pkg, lower.Features{}, lower.Options{Platform: "html"}); err != nil {
		t.Errorf("html refused its own primitive: %v", err)
	}
}
