package ir

import "testing"

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
