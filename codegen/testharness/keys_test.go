package testharness

import "testing"

func TestIsCanonicalKey(t *testing.T) {
	for _, k := range []string{
		"Enter", "Tab", "Escape", "Space", "Backspace", "Delete",
		"ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight",
		"Home", "End", "PageUp", "PageDown",
		"A", "Z", "0", "9",
	} {
		if !IsCanonicalKey(k) {
			t.Errorf("expected %q to be canonical", k)
		}
	}
	for _, k := range []string{"enter", "RETURN", "Esc", "Foo", ""} {
		if IsCanonicalKey(k) {
			t.Errorf("expected %q to be rejected", k)
		}
	}
}

func TestCanonicalKeys_complete(t *testing.T) {
	keys := CanonicalKeys()
	if len(keys) < 20 {
		t.Errorf("canonical key set too small: %d", len(keys))
	}
}
