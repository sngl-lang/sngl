package ir

import "testing"

// TestScalarType pins the type each scalar kind stands for: the declarations
// in sngl:builtin carry the kinds, and this is the whole of what the compiler
// adds to them.
func TestScalarType(t *testing.T) {
	want := map[BuiltinKind]*Type{
		BuiltinBool:    TypBool,
		BuiltinInt:     TypInt,
		BuiltinInt8:    TypInt8,
		BuiltinInt16:   TypInt16,
		BuiltinInt32:   TypInt32,
		BuiltinInt64:   TypInt64,
		BuiltinUint8:   TypUint8,
		BuiltinUint16:  TypUint16,
		BuiltinUint32:  TypUint32,
		BuiltinUint64:  TypUint64,
		BuiltinFloat:   TypFloat,
		BuiltinFloat32: TypFloat32,
		BuiltinFloat64: TypFloat64,
		BuiltinString:  TypString,
	}
	for kind, typ := range want {
		got, ok := ScalarType(kind)
		if !ok || got != typ {
			t.Errorf("%s: got %v, %v", kind, got, ok)
		}
		if conv := kind.IsConvertibleScalar(); !conv {
			t.Errorf("%s: convertible = %v", kind, conv)
		}
	}
	if _, ok := ScalarType(BuiltinColor); ok {
		t.Error("color is a struct, not a scalar")
	}
}
