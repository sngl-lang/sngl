package c

import (
	"fmt"

	"duckfam.us/sngl/ir"
)

// cTypeName is t as a C declaration names it, or an error naming what C does
// not yet write.
//
// A SNGL struct is the C struct the program declares for it, a C struct
// (`#[cnative]` with fields) included: it is converted field by field at
// each call, as on Go, since its fields are SNGL's numbers and C's are its
// own. A `#[cnative]` type with no fields is the C type its name spells.
func cTypeName(t *ir.Type) (string, error) {
	if t == nil {
		return "void", nil
	}
	if ct := ir.CTypeOf(t); ct != nil && !isProgramStruct(t) {
		return ct.String(), nil
	}
	switch t.Kind {
	case ir.TypeVoid:
		return "void", nil
	case ir.TypeBool:
		return "bool", nil
	case ir.TypeInt:
		return intType(t), nil
	case ir.TypeFloat:
		if t.Bits == 32 {
			return "float", nil
		}
		return "double", nil
	case ir.TypeString:
		return "sngl_string", nil
	case ir.TypeEnum:
		// passEnum makes a member the integer its position is.
		return "int64_t", nil
	case ir.TypeStruct:
		if sd, ok := t.Decl.(*ir.StructDef); ok {
			return Global(sd.Name), nil
		}
	}
	return "", fmt.Errorf("C does not yet write a %s", t)
}

// intType is t, an int, at its width: `int` is 64 bits, as on Go and Kotlin.
func intType(t *ir.Type) string {
	bits := t.Bits
	if bits == 0 {
		bits = 64
	}
	if t.Unsigned {
		return fmt.Sprintf("uint%d_t", bits)
	}
	return fmt.Sprintf("int%d_t", bits)
}

// isProgramStruct reports whether t is a struct the program declares in C,
// which a C struct with fields is: the program holds its own.
func isProgramStruct(t *ir.Type) bool {
	if t == nil || t.Kind != ir.TypeStruct {
		return false
	}
	sd, ok := t.Decl.(*ir.StructDef)
	return ok && (sd.Foreign.C == nil || sd.IsCStruct())
}
