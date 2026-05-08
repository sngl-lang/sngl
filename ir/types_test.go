package ir

import "testing"

func TestIterOfString(t *testing.T) {
	it := IterOf(TypString)
	if got := it.String(); got != "iter<string>" {
		t.Errorf("got %q, want iter<string>", got)
	}
	if it.Kind != TypeIter {
		t.Errorf("Kind = %v, want TypeIter", it.Kind)
	}
}

func TestMapOfString(t *testing.T) {
	m := MapOf(TypString, TypInt)
	if got := m.String(); got != "map<string, int>" {
		t.Errorf("MapOf(string,int).String() = %q, want %q", got, "map<string, int>")
	}
	if m.Kind != TypeMap {
		t.Errorf("Kind = %v, want TypeMap", m.Kind)
	}
	if len(m.Elems) != 2 {
		t.Errorf("len(Elems) = %d, want 2", len(m.Elems))
	}
}
