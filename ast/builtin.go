package ast

// BuiltinKind marks a declaration as one of the compiler's built-ins. It is set
// by the #[builtin] macro before type-checking and read by the checker and IR;
// BuiltinNone ("") is an ordinary declaration.
//
// Type kinds (primitive, string-repr, generic) mark a struct; node kinds mark a
// component. A declaration is at most one kind, so a single field carries them
// all, and the value doubles as the generic-constructor id.
//
// The kind classifies one declaration — it does not make two declarations the
// same type. Type identity is per-declaration (ir.Type.Equal compares Decl), so
// two structs sharing a mark would be two incompatible types, not aliases; the
// checker rejects a duplicated node mark outright.
type BuiltinKind string

const (
	BuiltinNone BuiltinKind = ""

	// Scalar primitives. Only the names with a stdlib struct decl are marked;
	// the mark distinguishes the built-in's own decl from a user declaration
	// that shadows the name. The concrete singleton is held by the compiler
	// (ir.BuiltinScalar), keyed by these names.
	BuiltinInt    BuiltinKind = "int"
	BuiltinFloat  BuiltinKind = "float"
	BuiltinString BuiltinKind = "string"

	// String-representable value types.
	BuiltinColor    BuiltinKind = "color"
	BuiltinDate     BuiltinKind = "date"
	BuiltinTime     BuiltinKind = "time"
	BuiltinDateTime BuiltinKind = "datetime"

	// Generic constructors.
	BuiltinList   BuiltinKind = "list"
	BuiltinMap    BuiltinKind = "map"
	BuiltinIter   BuiltinKind = "iter"
	BuiltinRef    BuiltinKind = "ref"
	BuiltinOption BuiltinKind = "option"

	// Built-in visual nodes. Unlike the type marks above, these annotate a
	// component declaration: the checker dispatches a visual node to the
	// matching compiler construct (ir.Window, ir.Timer, ir.SlotInst,
	// ir.ErrorBoundary) when its target resolves to the marked component,
	// rather than matching a literal name.
	BuiltinWindow        BuiltinKind = "window"
	BuiltinTimer         BuiltinKind = "timer"
	BuiltinSlot          BuiltinKind = "slot"
	BuiltinErrorBoundary BuiltinKind = "errorBoundary"

	// Predeclared constants. These annotate a const declaration whose written
	// value is a placeholder: the real one is not known until a build picks a
	// target, so the compiler supplies it.
	BuiltinPlatform BuiltinKind = "platform"
	BuiltinLanguage BuiltinKind = "language"
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
// (color/date/time/datetime).
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

// IsNode reports whether the kind marks a built-in visual node. Node kinds are
// stamped on component declarations, not structs.
func (b BuiltinKind) IsNode() bool {
	switch b {
	case BuiltinWindow, BuiltinTimer, BuiltinSlot, BuiltinErrorBoundary:
		return true
	}
	return false
}

// IsConst reports whether the kind marks a predeclared constant. Const kinds
// are stamped on const declarations, and the compiler replaces the declared
// type and value.
func (b BuiltinKind) IsConst() bool {
	switch b {
	case BuiltinPlatform, BuiltinLanguage:
		return true
	}
	return false
}

// AllBuiltinKinds returns every valid kind, in declaration order.
func AllBuiltinKinds() []BuiltinKind {
	return []BuiltinKind{
		BuiltinInt, BuiltinFloat, BuiltinString,
		BuiltinColor, BuiltinDate, BuiltinTime, BuiltinDateTime,
		BuiltinList, BuiltinMap, BuiltinIter, BuiltinRef, BuiltinOption,
		BuiltinWindow, BuiltinTimer, BuiltinSlot, BuiltinErrorBoundary,
		BuiltinPlatform, BuiltinLanguage,
	}
}

// Valid reports whether the kind names a built-in (i.e. is not BuiltinNone and
// not an unrecognised string).
func (b BuiltinKind) Valid() bool {
	return b.IsPrimitive() || b.IsStringRepr() || b.IsGeneric() || b.IsNode() || b.IsConst()
}

// SetBuiltin stamps the mark onto a declaration. The #[builtin] macro asserts
// this interface rather than switching on the kind, so which declaration forms
// can carry a mark is a property of the AST, not knowledge the macro holds.
type BuiltinTaggable interface {
	SetBuiltin(BuiltinKind)
}

func (c *ComponentDecl) SetBuiltin(k BuiltinKind) { c.Builtin = k }
func (s *StructDef) SetBuiltin(k BuiltinKind)     { s.Builtin = k }
func (c *ConstDecl) SetBuiltin(k BuiltinKind)     { c.Builtin = k }
