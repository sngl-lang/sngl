package ir

import "testing"

// TestNodeOpsAreNotIntrinsicDefs pins the split: the node operations are ids a
// lowering pass stamps on a synthesized call, matched by codegen.WalkLowered.
// They carry no signature because no caller and no emitter reads one.
func TestNodeOpsAreNotIntrinsicDefs(t *testing.T) {
	for _, op := range NodeOps {
		if def := LookupIntrinsic(op); def != nil {
			t.Errorf("%s has an IntrinsicDef; node operations are ids, not declarations", op)
		}
	}
}
