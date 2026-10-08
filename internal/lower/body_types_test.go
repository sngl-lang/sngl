package lower

import (
	"testing"

	"duckfam.us/sngl/ir"
)

// TestHoistBodyTypesDisambiguatesOneOwnerName is the branch no fixture
// reaches. Two bodies cannot share a name at the top level, but two *nested*
// component bodies can -- `a { helper { struct L } }` beside
// `b { helper { struct L } }` -- so `L__helper` is claimed twice and the
// counter is what keeps the second declaration off it.
func TestHoistBodyTypesDisambiguatesOneOwnerName(t *testing.T) {
	first := &ir.StructDef{Name: "L", BodyOwner: "helper"}
	second := &ir.StructDef{Name: "L", BodyOwner: "helper"}
	third := &ir.StructDef{Name: "L", BodyOwner: "helper"}
	pkg := &ir.Package{Structs: []*ir.StructDef{
		{Name: "L"}, // top level: it keeps the name whatever order registration used
		first, second, third,
	}}
	if err := hoistBodyTypes(pkg, Features{}, Options{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"L", "L__helper", "L__helper__2", "L__helper__3"}
	for i, sd := range pkg.Structs {
		if sd.Name != want[i] {
			t.Errorf("Structs[%d].Name = %q; want %q", i, sd.Name, want[i])
		}
	}
}

// TestHoistBodyTypesIsIdempotent matters because a checked package is lowered
// once per target in a multi-target build, and the second run sees names the
// first already moved.
func TestHoistBodyTypesIsIdempotent(t *testing.T) {
	pkg := &ir.Package{Structs: []*ir.StructDef{
		{Name: "Row"},
		{Name: "Row", BodyOwner: "card"},
	}}
	for range 2 {
		if err := hoistBodyTypes(pkg, Features{}, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	if got := pkg.Structs[1].Name; got != "Row__card" {
		t.Errorf("second run renamed again: %q; want %q", got, "Row__card")
	}
}

// TestHoistBodyTypesLeavesAnUncontestedNameAlone states the other half: what a
// body compiles to depends on that body, not on whether some unrelated
// declaration happens to reuse the name.
func TestHoistBodyTypesLeavesAnUncontestedNameAlone(t *testing.T) {
	sd := &ir.StructDef{Name: "Cell", BodyOwner: "pick"}
	ed := &ir.EnumDef{Name: "Phase", BodyOwner: "pick"}
	ud := &ir.UnitDef{Name: "Tick", BodyOwner: "pick"}
	pkg := &ir.Package{
		Structs: []*ir.StructDef{sd},
		Enums:   []*ir.EnumDef{ed},
		Units:   []*ir.UnitDef{ud},
	}
	if err := hoistBodyTypes(pkg, Features{}, Options{}); err != nil {
		t.Fatal(err)
	}
	if sd.Name != "Cell" || ed.Name != "Phase" || ud.Name != "Tick" {
		t.Errorf("renamed an uncontested name: %q %q %q", sd.Name, ed.Name, ud.Name)
	}
}

// TestHoistBodyTypesReservesFuncsAndComponents is the collision a host has
// that SNGL does not: Go gets `type Local struct` beside `func Local()`, and
// the interim checker error this pass replaced measured a body-local name
// against every top-level declaration rather than only the types.
func TestHoistBodyTypesReservesFuncsAndComponents(t *testing.T) {
	fnName := &ir.StructDef{Name: "total", BodyOwner: "sum"}
	compName := &ir.StructDef{Name: "card", BodyOwner: "sum"}
	method := &ir.StructDef{Name: "shout", BodyOwner: "sum"}
	pkg := &ir.Package{
		Structs:    []*ir.StructDef{fnName, compName, method},
		Funcs:      []*ir.Func{{Name: "total"}, {Name: "shout", Receiver: "Local"}},
		Components: []*ir.Component{{Name: "card"}},
	}
	if err := hoistBodyTypes(pkg, Features{}, Options{}); err != nil {
		t.Fatal(err)
	}
	if fnName.Name != "total__sum" {
		t.Errorf("a top-level func's name was not reserved: %q", fnName.Name)
	}
	if compName.Name != "card__sum" {
		t.Errorf("a component's name was not reserved: %q", compName.Name)
	}
	// A method is spelled under its receiver on every target, so it claims
	// nothing in the type namespace.
	if method.Name != "shout" {
		t.Errorf("a method's name was reserved: %q", method.Name)
	}
}

// TestHoistBodyTypesRenamesTheReceiver is the one reference site that does not
// ride on the declaration pointer.
func TestHoistBodyTypesRenamesTheReceiver(t *testing.T) {
	shout := &ir.Func{Name: "shout", Receiver: "Local"}
	sd := &ir.StructDef{
		Name:      "Local",
		BodyOwner: "second",
		Methods:   map[string]*ir.Func{"shout": shout},
	}
	pkg := &ir.Package{Structs: []*ir.StructDef{{Name: "Local"}, sd}}
	if err := hoistBodyTypes(pkg, Features{}, Options{}); err != nil {
		t.Fatal(err)
	}
	if shout.Receiver != "Local__second" {
		t.Errorf("Receiver = %q; want %q", shout.Receiver, "Local__second")
	}
}
