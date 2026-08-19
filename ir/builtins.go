package ir

// BuiltinScalar describes one compiler-defined scalar primitive type name.
//
// This is the single source of truth for the scalar built-ins. It replaces
// what were four hand-synced tables — NewBaseScope (ir/scope.go), the type
// resolver's name switch (checker/resolve.go), the T(x) conversion switch
// (checker/expr.go), and isBuiltinTypeName (checker/checker.go) — whose
// membership drifted whenever a new sized numeric was added. Each site now
// consults this registry for the scalar portion of its decision, keyed by the
// capability flags below.
//
// Generic constructors (list, map, iter, ref, option, component, shape) and
// the string-repr structs (color, date, time, dateTime) are intentionally NOT
// here: the former carry bespoke arity/error logic, the latter are stdlib
// StructDef-backed and resolved through scope. Both are folded into the
// declared built-ins package in a later phase (see
// docs/superpowers/specs/2026-08-18-builtins-stdlib-split-design.md).
type BuiltinScalar struct {
	Name string
	Type *Type
	// Universe is true when the name is predeclared in the base scope — usable
	// in value position (e.g. as a conversion callee) and resolvable before the
	// stdlib is loaded.
	Universe bool
	// Convertible is true when the name is valid as a T(x) primitive
	// conversion target.
	Convertible bool
}

// builtinScalars is the canonical scalar built-in table. Order is preserved by
// BuiltinScalars() so base-scope population is deterministic.
var builtinScalars = []BuiltinScalar{
	{Name: "bool", Type: TypBool, Universe: true, Convertible: true},
	{Name: "int", Type: TypInt, Universe: true, Convertible: true},
	{Name: "int8", Type: TypInt8, Convertible: true},
	{Name: "int16", Type: TypInt16, Convertible: true},
	{Name: "int32", Type: TypInt32, Convertible: true},
	{Name: "int64", Type: TypInt64, Convertible: true},
	{Name: "uint8", Type: TypUint8, Convertible: true},
	{Name: "uint16", Type: TypUint16, Convertible: true},
	{Name: "uint32", Type: TypUint32, Convertible: true},
	{Name: "uint64", Type: TypUint64, Convertible: true},
	{Name: "float", Type: TypFloat, Universe: true, Convertible: true},
	{Name: "float32", Type: TypFloat32, Convertible: true},
	{Name: "float64", Type: TypFloat64, Convertible: true},
	{Name: "string", Type: TypString, Universe: true, Convertible: true},
	{Name: "duration", Type: TypDuration, Universe: true, Convertible: true},
	// Resolvable in type position but neither predeclared as a value nor a
	// conversion target.
	{Name: "dyn", Type: TypDyn},
	{Name: "null", Type: TypNull},
}

var builtinScalarByName = func() map[string]BuiltinScalar {
	m := make(map[string]BuiltinScalar, len(builtinScalars))
	for _, b := range builtinScalars {
		m[b.Name] = b
	}
	return m
}()

// BuiltinScalars returns the scalar built-in table in declaration order.
func BuiltinScalars() []BuiltinScalar { return builtinScalars }

// LookupBuiltinScalar returns the scalar built-in for name, if any.
func LookupBuiltinScalar(name string) (BuiltinScalar, bool) {
	b, ok := builtinScalarByName[name]
	return b, ok
}
