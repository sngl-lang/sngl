package lower

import (
	"testing"

	"duckfam.us/sngl/ir"
)

// A platform's intrinsic is what the wrappers lower *to*, so it has no body
// to inline. Without the mark, strict mode reads a bodyless component from a
// platform package as an impure wrapper and fails the build — which is the
// whole reason #[intrinsic] has to be visible to this pass.
func TestInlinePure_StrictKeepsIntrinsic(t *testing.T) {
	intrinsic := &ir.Component{Name: "Column", Intrinsic: "Column"}
	platformPkg := &ir.Package{Components: []*ir.Component{intrinsic}}
	main := &ir.Component{
		Name: "main",
		Body: []ir.Stmt{&ir.NodeInst{Name: "Column", Component: intrinsic}},
	}
	pkg := &ir.Package{
		Components: []*ir.Component{main},
		Imports:    []*ir.Import{{Path: "sngl:platform/android", Pkg: platformPkg}},
	}

	if err := lowerInlinePure(pkg, Features{}, Options{}); err != nil {
		t.Fatalf("lowerInlinePure: %v", err)
	}
	if len(main.Body) != 1 {
		t.Fatalf("main.Body = %d stmts, want the intrinsic left standing", len(main.Body))
	}
	n, ok := main.Body[0].(*ir.NodeInst)
	if !ok || n.Component != intrinsic {
		t.Fatalf("main.Body[0] = %#v, want the Column node unchanged", main.Body[0])
	}

	// Unmarked, the same declaration is a wrapper the pass must reject.
	intrinsic.Intrinsic = ""
	if err := lowerInlinePure(pkg, Features{}, Options{}); err == nil {
		t.Fatal("lowerInlinePure accepted a bodyless unmarked platform component; the mark is what excuses it")
	}
}
