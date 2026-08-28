package codegen

import (
	"reflect"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// The analysis is a set of facts about a program and must hold no pointer into
// the IR graph it was derived from. One used to live here — a *ir.Package
// field carried solely so DepTracker() could hand it back — and it made the
// analysis unserializable: `sngl dump --stage analysis` walked the whole
// graph, sngl://std included, and encoding/json hit its cycles.
//
// The rule is checked structurally rather than by encoding a fixture, because
// what matters is that no such field exists, not that today's fixture happens
// to have an acyclic one.
func TestCommonAnalysisHoldsNoIRPackage(t *testing.T) {
	pkgType := reflect.TypeOf((*ir.Package)(nil))
	at := reflect.TypeOf(CommonAnalysis{})
	for i := range at.NumField() {
		f := at.Field(i)
		if f.Type == pkgType {
			t.Errorf("CommonAnalysis.%s is a %s; the analysis must not carry the package "+
				"it was derived from (see NewDepTrackerFromPkg)", f.Name, f.Type)
		}
	}
}

// Helpers and Styles are what a generator accumulates while it emits, not what
// analysis derived before it started. They belong to Emission; putting either
// back on CommonAnalysis makes a dump of the analysis a dump of the emitter's
// scratch space as well.
func TestEmissionOwnsWhatCodegenAccumulates(t *testing.T) {
	at := reflect.TypeOf(CommonAnalysis{})
	for _, name := range []string{"Helpers", "Styles"} {
		if _, ok := at.FieldByName(name); ok {
			t.Errorf("CommonAnalysis.%s: codegen's accumulators belong on Emission", name)
		}
	}
	et := reflect.TypeOf(Emission{})
	for _, name := range []string{"Helpers", "Styles"} {
		if _, ok := et.FieldByName(name); !ok {
			t.Errorf("Emission.%s missing", name)
		}
	}
}

// AddStyle is a set, not a list: a component registering the same rule twice
// must not emit it twice.
func TestAddStyleIgnoresDuplicates(t *testing.T) {
	e := NewEmission()
	e.AddStyle("a { color: red }")
	e.AddStyle("b { color: blue }")
	e.AddStyle("a { color: red }")
	if len(e.Styles) != 2 {
		t.Fatalf("Styles = %q, want the two distinct rules", e.Styles)
	}
}

// A nil package tracks nothing rather than panicking. The guard used to sit on
// CommonAnalysis.DepTracker, which is gone; it belongs here, where every
// caller reaches it.
func TestNewDepTrackerFromNilPackage(t *testing.T) {
	dt := NewDepTrackerFromPkg(nil)
	if dt == nil {
		t.Fatal("NewDepTrackerFromPkg(nil) = nil")
	}
	if len(dt.ModelVars) != 0 || len(dt.ComputedFuncs) != 0 || len(dt.ComputedDeps) != 0 {
		t.Errorf("NewDepTrackerFromPkg(nil) tracks something: %+v", dt)
	}
}
