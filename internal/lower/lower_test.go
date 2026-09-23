package lower

import (
	"fmt"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestPassRegistry_StubsAreNoOps(t *testing.T) {
	for _, p := range passes {
		if err := p.apply(nil, Features{}, Options{}); err != nil {
			t.Errorf("pass %s stub returned error: %v", p.name, err)
		}
	}
}

func TestLower_NoCapsIsNoop(t *testing.T) {
	pkg := &ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}
	if err := Lower(pkg, Features{}, Options{}); err != nil {
		t.Fatalf("Lower: %v", err)
	}
}

func TestLower_RunsEnabledPassesInOrder(t *testing.T) {
	var ran []string
	orig := passes
	t.Cleanup(func() { passes = orig })
	passes = []pass{
		{name: "NoUnit", enabled: func(c Features) bool { return !c.Unit }, apply: func(*ir.Package, Features, Options) error { ran = append(ran, "NoUnit"); return nil }},
		{name: "NoEnum", enabled: func(c Features) bool { return !c.Enum }, apply: func(*ir.Package, Features, Options) error { ran = append(ran, "NoEnum"); return nil }},
		{name: "NoToggle", enabled: func(c Features) bool { return !c.Toggle }, apply: func(*ir.Package, Features, Options) error { ran = append(ran, "NoToggle"); return nil }},
	}
	if err := Lower(&ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}, without("unitType", "toggle"), Options{}); err != nil {
		t.Fatalf("Lower: %v", err)
	}
	want := []string{"NoUnit", "NoToggle"}
	if len(ran) != len(want) {
		t.Fatalf("ran = %v; want %v", ran, want)
	}
	for i, n := range want {
		if ran[i] != n {
			t.Errorf("ran[%d] = %q; want %q", i, ran[i], n)
		}
	}
}

func TestLower_StopAfter(t *testing.T) {
	var ran []string
	orig := passes
	t.Cleanup(func() { passes = orig })
	passes = []pass{
		{name: "NoUnit", enabled: func(c Features) bool { return true }, apply: func(*ir.Package, Features, Options) error { ran = append(ran, "NoUnit"); return nil }},
		{name: "NoEnum", enabled: func(c Features) bool { return true }, apply: func(*ir.Package, Features, Options) error { ran = append(ran, "NoEnum"); return nil }},
		{name: "NoToggle", enabled: func(c Features) bool { return true }, apply: func(*ir.Package, Features, Options) error { ran = append(ran, "NoToggle"); return nil }},
	}
	if err := Lower(&ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}, Features{}, Options{StopAfter: "NoEnum"}); err != nil {
		t.Fatalf("Lower: %v", err)
	}
	want := []string{"NoUnit", "NoEnum"}
	if len(ran) != len(want) {
		t.Fatalf("ran = %v; want %v", ran, want)
	}
	for i, n := range want {
		if ran[i] != n {
			t.Errorf("ran[%d] = %q; want %q", i, ran[i], n)
		}
	}
}

func TestLower_StopAfterNone(t *testing.T) {
	var ran []string
	orig := passes
	t.Cleanup(func() { passes = orig })
	passes = []pass{
		{name: "NoUnit", enabled: func(c Features) bool { return true }, apply: func(*ir.Package, Features, Options) error { ran = append(ran, "NoUnit"); return nil }},
	}
	if err := Lower(&ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}, without("unitType"), Options{StopAfter: "none"}); err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if len(ran) != 0 {
		t.Errorf("ran = %v; want []", ran)
	}
}

func TestLower_StopAfterUnknown(t *testing.T) {
	err := Lower(&ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}, Features{}, Options{StopAfter: "NoBogus"})
	if err == nil {
		t.Fatal("Lower: want error for unknown StopAfter, got nil")
	}
	if !strings.Contains(err.Error(), "NoBogus") {
		t.Errorf("err = %v; want mention of NoBogus", err)
	}
}

func TestLower_PropagatesPassError(t *testing.T) {
	orig := passes
	t.Cleanup(func() { passes = orig })
	wantErr := fmt.Errorf("kaboom")
	passes = []pass{
		{name: "NoUnit", enabled: func(c Features) bool { return true }, apply: func(*ir.Package, Features, Options) error { return wantErr }},
	}
	err := Lower(&ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}, without("unitType"), Options{})
	if err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("Lower err = %v; want wrapped %q", err, wantErr)
	}
}
