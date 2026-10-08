package codegen

import (
	"reflect"
	"slices"
	"testing"

	"duckfam.us/sngl/ir"
)

// The analysis must hold no pointer into the IR graph it was derived from:
// `sngl dump --stage analysis` serializes it and that graph has cycles.
func TestCommonAnalysisHoldsNoIRPackage(t *testing.T) {
	pkgType := reflect.TypeFor[*ir.Package]()
	at := reflect.TypeFor[CommonAnalysis]()
	for f := range at.Fields() {
		if f.Type == pkgType {
			t.Errorf("CommonAnalysis.%s is a %s; the analysis must not carry the package "+
				"it was derived from (see NewDepTrackerFromPkg)", f.Name, f.Type)
		}
	}
}

// A window's generator mutates its own analysis, so a map Clone forgets is one
// every window shares. The check is over the type rather than over a list of
// field names: forgetting the copy and forgetting to extend this test are the
// same omission, and only reflection makes a new field fail without an edit.
func TestCommonAnalysisCloneCopiesEveryMap(t *testing.T) {
	a := &CommonAnalysis{
		ModelFields:    map[string]bool{"a": true},
		ComputedFields: map[string]bool{"b": true},
		ComputedDeps:   map[string]map[string]bool{"b": {"a": true}},
		FuncNames:      map[string]bool{"f": true},
		ExternFuncs:    map[string]bool{"g": true},
		ExternVars:     map[string]bool{"v": true},
		StructFields:   map[string][]string{"S": {"x"}},
		UsedComponents: map[string]bool{"text": true},
		Timers:         []TimerInfo{{Index: 0, LocalRefs: map[string]bool{"__n0": true}}},
	}
	b := a.Clone()

	av, bv := reflect.ValueOf(a).Elem(), reflect.ValueOf(b).Elem()
	for i, f := range slices.Collect(av.Type().Fields()) {
		if f.Type.Kind() != reflect.Map {
			continue
		}
		if av.Field(i).IsNil() {
			t.Fatalf("CommonAnalysis.%s: this test must populate every map field to say anything about it", f.Name)
		}
		if av.Field(i).UnsafePointer() == bv.Field(i).UnsafePointer() {
			t.Errorf("Clone shares CommonAnalysis.%s; one generator's writes would reach every other's", f.Name)
		}
	}

	// ComputedDeps' values and a TimerInfo's LocalRefs are the maps one level
	// down, which a shallow copy of the field above would leave shared.
	if sameMap(a.ComputedDeps["b"], b.ComputedDeps["b"]) {
		t.Error("Clone shares a ComputedDeps entry")
	}
	if sameMap(a.Timers[0].LocalRefs, b.Timers[0].LocalRefs) {
		t.Error("Clone shares a TimerInfo's LocalRefs")
	}

	b.ModelFields["a"] = false
	delete(b.ComputedFields, "b")
	if !a.ModelFields["a"] || !a.ComputedFields["b"] {
		t.Error("writing through a clone reached the original")
	}
}

func sameMap(x, y map[string]bool) bool {
	return reflect.ValueOf(x).UnsafePointer() == reflect.ValueOf(y).UnsafePointer()
}

// Helpers and Styles are accumulated during emit, so a dump of the analysis
// carrying them would be a dump of the emitter's scratch space.
func TestEmissionOwnsWhatCodegenAccumulates(t *testing.T) {
	at := reflect.TypeFor[CommonAnalysis]()
	for _, name := range []string{"Helpers", "Styles"} {
		if _, ok := at.FieldByName(name); ok {
			t.Errorf("CommonAnalysis.%s: codegen's accumulators belong on Emission", name)
		}
	}
	et := reflect.TypeFor[Emission]()
	for _, name := range []string{"Helpers", "Styles"} {
		if _, ok := et.FieldByName(name); !ok {
			t.Errorf("Emission.%s missing", name)
		}
	}
}

func TestAddStyleIgnoresDuplicates(t *testing.T) {
	e := NewEmission()
	e.AddStyle("a { color: red }")
	e.AddStyle("b { color: blue }")
	e.AddStyle("a { color: red }")
	if len(e.Styles) != 2 {
		t.Fatalf("Styles = %q, want the two distinct rules", e.Styles)
	}
}

func TestNewDepTrackerFromNilPackage(t *testing.T) {
	dt := NewDepTrackerFromPkg(nil)
	if dt == nil {
		t.Fatal("NewDepTrackerFromPkg(nil) = nil")
	}
	if len(dt.ModelVars) != 0 || len(dt.ComputedFuncs) != 0 || len(dt.ComputedDeps) != 0 {
		t.Errorf("NewDepTrackerFromPkg(nil) tracks something: %+v", dt)
	}
}
