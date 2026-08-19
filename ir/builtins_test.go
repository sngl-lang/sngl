package ir

import "testing"

// TestBuiltinScalarRegistry pins the scalar built-in table so the four
// consumers (base scope, type resolver, conversion switch, isBuiltinTypeName)
// can't silently drift when a numeric type is added or removed.
func TestBuiltinScalarRegistry(t *testing.T) {
	want := map[string]struct {
		typ         *Type
		universe    bool
		convertible bool
	}{
		"bool":     {TypBool, true, true},
		"int":      {TypInt, true, true},
		"int8":     {TypInt8, false, true},
		"int16":    {TypInt16, false, true},
		"int32":    {TypInt32, false, true},
		"int64":    {TypInt64, false, true},
		"uint8":    {TypUint8, false, true},
		"uint16":   {TypUint16, false, true},
		"uint32":   {TypUint32, false, true},
		"uint64":   {TypUint64, false, true},
		"float":    {TypFloat, true, true},
		"float32":  {TypFloat32, false, true},
		"float64":  {TypFloat64, false, true},
		"string":   {TypString, true, true},
		"duration": {TypDuration, true, true},
		"dyn":      {TypDyn, false, false},
		"null":     {TypNull, false, false},
	}

	if got := len(BuiltinScalars()); got != len(want) {
		t.Fatalf("registry has %d entries, want %d", got, len(want))
	}
	for name, w := range want {
		b, ok := LookupBuiltinScalar(name)
		if !ok {
			t.Errorf("%q missing from registry", name)
			continue
		}
		if b.Type != w.typ {
			t.Errorf("%q: Type pointer mismatch", name)
		}
		if b.Universe != w.universe {
			t.Errorf("%q: Universe=%v, want %v", name, b.Universe, w.universe)
		}
		if b.Convertible != w.convertible {
			t.Errorf("%q: Convertible=%v, want %v", name, b.Convertible, w.convertible)
		}
	}

	// Every Universe scalar must also be resolvable via LookupBuiltinScalar
	// (NewBaseScope depends on this pairing).
	for _, b := range BuiltinScalars() {
		if _, ok := LookupBuiltinScalar(b.Name); !ok {
			t.Errorf("%q in slice but not in lookup map", b.Name)
		}
	}
}
