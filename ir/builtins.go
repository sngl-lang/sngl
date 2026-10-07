package ir

// ScalarType is the type a scalar built-in's declaration stands for, by the
// kind its #[builtin] mark gives it, and whether the kind is one. The name a
// program writes is the declaration's own, found in scope like any other, so
// a user declaration of the same name shadows it; nothing here knows a name.
func ScalarType(kind BuiltinKind) (*Type, bool) {
	switch kind {
	case BuiltinBool:
		return TypBool, true
	case BuiltinInt:
		return TypInt, true
	case BuiltinInt8:
		return TypInt8, true
	case BuiltinInt16:
		return TypInt16, true
	case BuiltinInt32:
		return TypInt32, true
	case BuiltinInt64:
		return TypInt64, true
	case BuiltinUint8:
		return TypUint8, true
	case BuiltinUint16:
		return TypUint16, true
	case BuiltinUint32:
		return TypUint32, true
	case BuiltinUint64:
		return TypUint64, true
	case BuiltinFloat:
		return TypFloat, true
	case BuiltinFloat32:
		return TypFloat32, true
	case BuiltinFloat64:
		return TypFloat64, true
	case BuiltinString:
		return TypString, true
	case BuiltinDyn:
		return TypDyn, true
	}
	return nil, false
}

// IsConvertibleScalar reports whether a scalar kind is a T(x) conversion
// target. `dyn` is a type and no conversion: anything already is one.
func (b BuiltinKind) IsConvertibleScalar() bool {
	_, ok := ScalarType(b)
	return ok && b != BuiltinDyn
}
