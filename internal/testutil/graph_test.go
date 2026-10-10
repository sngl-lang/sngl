package testutil

import (
	"reflect"
	"testing"
)

type graphLeaf struct {
	n    int
	tags map[string]*graphLeaf
}

type graphRoot struct {
	leaves []*graphLeaf
	any    any
}

func TestGraphNamesWhatWasWritten(t *testing.T) {
	build := func() *graphRoot {
		shared := &graphLeaf{n: 1}
		return &graphRoot{
			leaves: []*graphLeaf{shared, {n: 2, tags: map[string]*graphLeaf{"a": shared, "b": {n: 3}}}},
			any:    shared,
		}
	}
	writes := map[string]func(*graphRoot){
		"a field":         func(r *graphRoot) { r.leaves[0].n = 9 },
		"a map entry":     func(r *graphRoot) { r.leaves[1].tags["b"] = r.leaves[0] },
		"a map key":       func(r *graphRoot) { r.leaves[1].tags["c"] = r.leaves[1].tags["b"]; delete(r.leaves[1].tags, "b") },
		"a new object":    func(r *graphRoot) { r.leaves[1].tags["d"] = &graphLeaf{} },
		"an interface":    func(r *graphRoot) { r.any = r.leaves[1] },
		"a slice element": func(r *graphRoot) { r.leaves[0], r.leaves[1] = r.leaves[1], r.leaves[0] },
	}
	var f Fingerprinter
	for name, write := range writes {
		root := build()
		before, fingerprint := GraphOf(root), f.Of(root)
		write(root)
		if changed := before.Changed(GraphOf(root)); len(changed) == 0 {
			t.Errorf("writing %s changed nothing the graph saw", name)
		}
		if f.Of(root) == fingerprint {
			t.Errorf("writing %s left the fingerprint as it was", name)
		}
	}
	root := build()
	before, fingerprint := GraphOf(root), f.Of(root)
	for range 20 {
		if changed := before.Changed(GraphOf(root)); len(changed) > 0 {
			t.Fatalf("an unchanged graph changed:\n%v", changed)
		}
		if f.Of(root) != fingerprint {
			t.Fatal("an unchanged graph changed its fingerprint")
		}
	}
}

func TestFingerprintSkipsWhatItIsTold(t *testing.T) {
	root := &graphRoot{leaves: []*graphLeaf{{n: 1}}}
	f := Fingerprinter{Skip: func(v reflect.Value) bool { return v.Type().Elem() == reflect.TypeFor[graphLeaf]() }}
	before := f.Of(root)
	root.leaves[0].n = 2
	if f.Of(root) != before {
		t.Error("a write inside a skipped object changed the fingerprint")
	}
	root.leaves[0] = &graphLeaf{n: 2}
	if f.Of(root) == before {
		t.Error("replacing a skipped object left the fingerprint as it was")
	}
}
