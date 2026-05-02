package lower

import "testing"

func TestPassRegistry_OrderAndUniqueness(t *testing.T) {
	expectedOrder := []string{
		"NoUnit",
		"NoEnum",
		"NoTernary",
		"NoComputed",
		"NoLambda",
		"NoToggle",
		"NoReactivity",
		"NoTimer",
		"NoDeclarative",
	}
	if len(passes) != len(expectedOrder) {
		t.Fatalf("passes length = %d; want %d", len(passes), len(expectedOrder))
	}
	seen := make(map[string]bool)
	for i, p := range passes {
		if p.name != expectedOrder[i] {
			t.Errorf("passes[%d].name = %q; want %q", i, p.name, expectedOrder[i])
		}
		if seen[p.name] {
			t.Errorf("duplicate pass name %q", p.name)
		}
		seen[p.name] = true
		if p.apply == nil {
			t.Errorf("passes[%d] (%s) has nil apply", i, p.name)
		}
		if p.enabled == nil {
			t.Errorf("passes[%d] (%s) has nil enabled", i, p.name)
		}
	}
}

func TestPassRegistry_StubsAreNoOps(t *testing.T) {
	for _, p := range passes {
		if err := p.apply(nil); err != nil {
			t.Errorf("pass %s stub returned error: %v", p.name, err)
		}
	}
}
