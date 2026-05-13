package ir

import "testing"

func TestLookupLowerIntrinsic(t *testing.T) {
	for _, name := range []string{"LowerCreateNode", "LowerAppendChild", "LowerRemoveChild", "LowerAttachHandler"} {
		if def := LookupIntrinsic(name); def == nil {
			t.Errorf("LookupIntrinsic(%q) returned nil; expected definition", name)
		}
	}
}
