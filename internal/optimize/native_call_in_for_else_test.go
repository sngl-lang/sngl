package optimize

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// The guard walks a loop's else, the same way it walks an `if`'s. A native
// call written only in the else was invisible to it, so the enclosing function
// was handed to the build-time interpreter on the strength of a body that had
// not been read in full.
//
// This is a unit test on the guard rather than a fixture, because the omission
// has no end-to-end witness: the folds that reach a native call in a real
// program arrive by another route -- an unresolved import call makes the
// interpreter fail and decline the fold anyway, and a #[foreign] const func
// call is inlined into its caller before this guard is asked about it. What
// the guard answers is still the property being fixed, and it is asked
// directly here.
func TestBodyUsesNativeCallReadsALoopsElse(t *testing.T) {
	// A native call is one the checker left unresolved: Func is nil because
	// the resolution lives in NativeImport rather than in pkg.Funcs.
	native := func() ir.Stmt {
		return &ir.CallStmt{Call: &ir.Call{Callee: &ir.Ident{Name: "srv.Fallback"}}}
	}
	plain := func() ir.Stmt {
		return &ir.CallStmt{Call: &ir.Call{Func: &ir.Func{Name: "noop", Purity: ir.PurityPure}}}
	}

	for _, tt := range []struct {
		name string
		loop *ir.For
		want bool
	}{
		{"in the else", &ir.For{Body: []ir.Stmt{plain()}, Else: []ir.Stmt{native()}}, true},
		{"in the body", &ir.For{Body: []ir.Stmt{native()}, Else: []ir.Stmt{plain()}}, true},
		{"in neither", &ir.For{Body: []ir.Stmt{plain()}, Else: []ir.Stmt{plain()}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fn := &ir.Func{Name: "pick", Purity: ir.PurityPure, Block: []ir.Stmt{tt.loop}}
			if got := bodyUsesNativeCall(fn); got != tt.want {
				t.Errorf("bodyUsesNativeCall = %v, want %v", got, tt.want)
			}
		})
	}
}
