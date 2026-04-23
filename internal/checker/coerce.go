package checker

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// isLiteralIntZero reports whether expr is the integer literal 0. Used to
// allow bare `0` in a unit-typed slot without requiring a unit suffix.
func isLiteralIntZero(expr ir.Expr) bool {
	lit, ok := expr.(*ir.Literal)
	if !ok {
		return false
	}
	if lit.Type == nil || lit.Type.Kind != ir.TypeInt {
		return false
	}
	return lit.Raw == "0"
}

// adaptLiteralZero wraps a literal `0` in an ir.Conversion to a unit target.
// Returns (newExpr, true) on match, (expr, false) otherwise. This is the only
// remaining int→unit path; non-zero integers must be multiplied by a unit
// literal (e.g. `5 * 1s`) to acquire a unit type.
func adaptLiteralZero(expr ir.Expr, target *ir.Type) (ir.Expr, bool) {
	if target == nil || target.Kind != ir.TypeUnit {
		return expr, false
	}
	if !isLiteralIntZero(expr) {
		return expr, false
	}
	return &ir.Conversion{Type: target, Operand: expr}, true
}

// wrapIfNeeded wraps expr in an ir.Conversion when its resolved type differs
// from the target under an implicit conversion. Called after an assignability
// check so every meaningful conversion lands as an explicit ir.Conversion node
// in the IR. Returns expr unchanged when types already match, when either
// side is dyn (dyn flows without a conversion — codegen stringifies/casts at
// its own boundary), when either side is nil, or when expr is already an
// ir.Conversion targeting the same type (avoids double-wrapping).
func wrapIfNeeded(expr ir.Expr, target *ir.Type) ir.Expr {
	if expr == nil || target == nil {
		return expr
	}
	actual := exprType(expr)
	if actual == nil || actual.Equal(target) {
		return expr
	}
	if actual.Kind == ir.TypeDyn || target.Kind == ir.TypeDyn {
		return expr
	}
	if conv, ok := expr.(*ir.Conversion); ok && conv.Type != nil && conv.Type.Equal(target) {
		return expr
	}
	return &ir.Conversion{Type: target, Operand: expr}
}

// primitiveConvertible reports whether an explicit conversion T(x) from
// fromKind to targetKind is permitted without a user-defined method. Struct,
// func, component, list, option, and null operands always require a method
// (which SNGL does not dispatch for explicit casts) and return false.
func primitiveConvertible(fromKind, targetKind ir.TypeKind) bool {
	if fromKind == ir.TypeDyn {
		return true // dyn narrows through any primitive cast
	}
	if !isConvertiblePrimitive(fromKind) {
		return false
	}
	switch targetKind {
	case ir.TypeInt, ir.TypeFloat:
		switch fromKind {
		case ir.TypeInt, ir.TypeFloat, ir.TypeString, ir.TypeBool, ir.TypeEnum, ir.TypeUnit:
			return true
		}
	case ir.TypeString:
		return true // any listed primitive → string
	case ir.TypeBool:
		switch fromKind {
		case ir.TypeBool, ir.TypeString:
			return true
		}
	}
	return false
}

func isConvertiblePrimitive(k ir.TypeKind) bool {
	switch k {
	case ir.TypeInt, ir.TypeFloat, ir.TypeString, ir.TypeBool, ir.TypeEnum, ir.TypeUnit:
		return true
	case ir.TypeColor, ir.TypeDate, ir.TypeTime, ir.TypeDateTime, ir.TypeDuration,
		ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex, ir.TypeBase64,
		ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname, ir.TypeDecimal:
		return true
	}
	return false
}
