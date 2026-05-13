package ir

import "testing"

func TestLookupLowerIntrinsic(t *testing.T) {
	for _, name := range []string{"CreateNode", "AppendChild", "RemoveChild", "AttachHandler"} {
		if def := LookupIntrinsic(name); def == nil {
			t.Errorf("LookupIntrinsic(%q) returned nil; expected definition", name)
		}
	}
}
