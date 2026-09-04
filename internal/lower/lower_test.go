package lower

import (
	"fmt"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestPassRegistry_OrderAndUniqueness(t *testing.T) {
	expectedOrder := []string{
		"RootWindow",
		"HoistState",
		"ForeignPrimitive",
		"PlatformExtensionBody",
		"PropBindings",
		"RefLoop",
		"NoUnit",
		"NoEnum",
		"Query",
		"NoAsyncReactive",
		"NoComputed",
		"NoLambda",
		"NoListLambdas",
		"NoToggle",
		"Context",
		"InlinePure",
		"NoInlineComponents",
		"RecursionDepth",
		"NoStructSpread",
		"NoImplicitRecv",
		"Canvas",
		"Effect",
		"InstanceEvents",
		"ComponentProps",
		"NoReactivity",
		"NoTernary",
		"CanvasReactivity",
		"NoTimer",
		"FocusOrder",
		"InstanceBodies",
		"NoDeclarative",
		"NodeEscape",
		"NoRef",
		"IndexedIter",
		"ForElse",
		"CSE",
		"IterKind",
		"StampUsage",
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
		if err := p.apply(nil, Caps{}, Options{}); err != nil {
			t.Errorf("pass %s stub returned error: %v", p.name, err)
		}
	}
}

func TestLower_NoCapsIsNoop(t *testing.T) {
	pkg := &ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}
	if err := Lower(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("Lower: %v", err)
	}
}

func TestLower_RunsEnabledPassesInOrder(t *testing.T) {
	var ran []string
	orig := passes
	t.Cleanup(func() { passes = orig })
	passes = []pass{
		{name: "NoUnit", enabled: func(c Caps) bool { return c.NoUnit }, apply: func(*ir.Package, Caps, Options) error { ran = append(ran, "NoUnit"); return nil }},
		{name: "NoEnum", enabled: func(c Caps) bool { return c.NoEnum }, apply: func(*ir.Package, Caps, Options) error { ran = append(ran, "NoEnum"); return nil }},
		{name: "NoToggle", enabled: func(c Caps) bool { return c.NoToggle }, apply: func(*ir.Package, Caps, Options) error { ran = append(ran, "NoToggle"); return nil }},
	}
	if err := Lower(&ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}, Caps{NoUnit: true, NoToggle: true}, Options{}); err != nil {
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
		{name: "NoUnit", enabled: func(c Caps) bool { return true }, apply: func(*ir.Package, Caps, Options) error { ran = append(ran, "NoUnit"); return nil }},
		{name: "NoEnum", enabled: func(c Caps) bool { return true }, apply: func(*ir.Package, Caps, Options) error { ran = append(ran, "NoEnum"); return nil }},
		{name: "NoToggle", enabled: func(c Caps) bool { return true }, apply: func(*ir.Package, Caps, Options) error { ran = append(ran, "NoToggle"); return nil }},
	}
	if err := Lower(&ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}, Caps{}, Options{StopAfter: "NoEnum"}); err != nil {
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
		{name: "NoUnit", enabled: func(c Caps) bool { return true }, apply: func(*ir.Package, Caps, Options) error { ran = append(ran, "NoUnit"); return nil }},
	}
	if err := Lower(&ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}, Caps{NoUnit: true}, Options{StopAfter: "none"}); err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if len(ran) != 0 {
		t.Errorf("ran = %v; want []", ran)
	}
}

func TestLower_StopAfterUnknown(t *testing.T) {
	err := Lower(&ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}, Caps{}, Options{StopAfter: "NoBogus"})
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
		{name: "NoUnit", enabled: func(c Caps) bool { return true }, apply: func(*ir.Package, Caps, Options) error { return wantErr }},
	}
	err := Lower(&ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}, Caps{NoUnit: true}, Options{})
	if err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("Lower err = %v; want wrapped %q", err, wantErr)
	}
}

func TestPassNames(t *testing.T) {
	names := PassNames()
	if len(names) != len(passes) {
		t.Fatalf("PassNames len = %d; want %d", len(names), len(passes))
	}
	for i, p := range passes {
		if names[i] != p.name {
			t.Errorf("PassNames[%d] = %q; want %q", i, names[i], p.name)
		}
	}
}

func TestEnabledPasses(t *testing.T) {
	got := EnabledPasses(Caps{NoToggle: true, NoReactivity: true})
	want := []string{"RootWindow", "HoistState", "ForeignPrimitive", "PlatformExtensionBody", "PropBindings", "RefLoop", "Query", "NoToggle", "InlinePure", "RecursionDepth", "InstanceEvents", "ComponentProps", "NoReactivity", "InstanceBodies", "IndexedIter", "ForElse", "CSE", "IterKind", "StampUsage"}
	if len(got) != len(want) {
		t.Fatalf("got = %v; want %v", got, want)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("got[%d] = %q; want %q", i, got[i], n)
		}
	}
}
