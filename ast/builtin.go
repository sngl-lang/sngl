package ast

// BuiltinKind marks a struct declaration as a compiler built-in type. It is set
// by the #[builtin.*] macros before type-checking and read by the checker and
// IR; BuiltinNone ("") is an ordinary struct. A struct is at most one kind of
// built-in, so a single field carries both the string-repr value types and the
// generic constructors. The value doubles as the generic-constructor id.
type BuiltinKind string

const (
	BuiltinNone BuiltinKind = ""

	// Scalar primitives (#[builtin.primitive]). Only the names with a stdlib
	// struct decl are marked; the mark distinguishes the built-in's own decl
	// from a user declaration that shadows the name. The concrete singleton is
	// held by the compiler (ir.BuiltinScalar), keyed by these names.
	BuiltinInt    BuiltinKind = "int"
	BuiltinFloat  BuiltinKind = "float"
	BuiltinString BuiltinKind = "string"

	// String-representable value types (#[builtin.stringrepr]).
	BuiltinColor    BuiltinKind = "color"
	BuiltinDate     BuiltinKind = "date"
	BuiltinTime     BuiltinKind = "time"
	BuiltinDateTime BuiltinKind = "dateTime"

	// Generic constructors (#[builtin.generic]).
	BuiltinList   BuiltinKind = "list"
	BuiltinMap    BuiltinKind = "map"
	BuiltinIter   BuiltinKind = "iter"
	BuiltinRef    BuiltinKind = "ref"
	BuiltinOption BuiltinKind = "option"
)

// IsPrimitive reports whether the kind is a scalar primitive (int/float/string).
func (b BuiltinKind) IsPrimitive() bool {
	switch b {
	case BuiltinInt, BuiltinFloat, BuiltinString:
		return true
	}
	return false
}

// IsStringRepr reports whether the kind is a string-representable value type
// (color/date/time/dateTime).
func (b BuiltinKind) IsStringRepr() bool {
	switch b {
	case BuiltinColor, BuiltinDate, BuiltinTime, BuiltinDateTime:
		return true
	}
	return false
}

// IsGeneric reports whether the kind is a generic type constructor
// (list/map/iter/ref/option).
func (b BuiltinKind) IsGeneric() bool {
	switch b {
	case BuiltinList, BuiltinMap, BuiltinIter, BuiltinRef, BuiltinOption:
		return true
	}
	return false
}
