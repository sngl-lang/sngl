package codegen

import (
	"reflect"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// The analysis must hold no pointer into the IR graph it was derived from:
// `sngl dump --stage analysis` serializes it and that graph has cycles.
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

// Helpers and Styles are accumulated during emit, so a dump of the analysis
// carrying them would be a dump of the emitter's scratch space.
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
