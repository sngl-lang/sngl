package interp

import (
	"testing"

	"duckfam.us/sngl/ir"
)

// The optimizer reads every multi-base unit literal it folds through
// UnitLiteralValue, so one compilation's literals of a unit share the table
// built from its declaration rather than building one each.
func TestUnitLiteralsShareTheirTable(t *testing.T) {
	ud := &ir.UnitDef{Name: "measurement", Suffixes: []*ir.UnitSuffix{
		{Name: "px", Factor: 1, BaseName: "px"},
		{Name: "em", Factor: 1, BaseName: "em"},
	}}
	typ := &ir.Type{Kind: ir.TypeUnit, Decl: ud}
	var tables UnitTables
	read := func(value, suffix string) unitValue {
		t.Helper()
		v, ok := UnitLiteralValue(&ir.Literal{Value: value, Suffix: suffix, Type: typ}, &tables)
		if !ok {
			t.Fatalf("%s%s is not a unit literal", value, suffix)
		}
		return v.(unitValue)
	}
	a, b := read("3", "px"), read("2", "em")
	if a.Table != b.Table {
		t.Error("two literals of one unit built two tables")
	}
	if a.Table.Decl != ud || len(a.Table.Bases) != 2 {
		t.Errorf("the shared table is not the declaration's: %+v", a.Table)
	}
}
