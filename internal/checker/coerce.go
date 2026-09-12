package checker

import (
	"strings"

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
	return lit.Value == "0"
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
	// Promoting into an option is two conversions, not one: `option<float>(1)`
	// says nothing about which of the two steps a backend is looking at, and
	// ir.IsOptionWrap can only recognise the promotion when the operand is
	// already the element type. So the element conversion goes on the inside
	// and the wrap stays a single, recognisable node.
	if target.Kind == ir.TypeOption && len(target.Elems) == 1 && target.Elems[0] != nil &&
		actual.Kind != ir.TypeOption && actual.Kind != ir.TypeNull {
		expr = wrapIfNeeded(expr, target.Elems[0])
	}
	return &ir.Conversion{Type: target, Operand: expr}
}

// wrapOptionIfNeeded is wrapIfNeeded restricted to the T -> option<T>
// promotion. Every other assignment position calls wrapIfNeeded outright; a
// struct literal's fields deliberately do not, because a platform's style
// emitter reads them by pattern -- `display="flex"` against a `Display` field
// reaches html's CSS writer as the string literal it was written as, and a
// general wrap would hand it `Display("flex")` and drop the declaration.
//
// The option promotion is not optional in the same way: Go's option<T> is *T,
// so a bare T left standing there is code that does not compile.
func wrapOptionIfNeeded(expr ir.Expr, target *ir.Type) ir.Expr {
	if expr == nil || target == nil || target.Kind != ir.TypeOption {
		return expr
	}
	actual := exprType(expr)
	if actual == nil {
		return expr
	}
	switch actual.Kind {
	case ir.TypeOption, ir.TypeNull, ir.TypeDyn, ir.TypeInvalid:
		return expr
	}
	return wrapIfNeeded(expr, target)
}

// primitiveConvertible reports whether an explicit conversion T(x) from
// fromKind to targetKind is permitted without a user-defined method. Struct,
// func, component, list, option, and null operands always require a method
// (which SNGL does not dispatch for explicit casts) and return false.
// The string-repr structs (color, date, time, datetime) are the exception
// among structs: each carries a canonical string form, so each converts to and
// from string, and to itself. They are compared by declaration rather than by
// kind, because every one of them is a TypeStruct and a kind-level test cannot
// tell date from color — which is what the former TypeColor normalization did,
// and why `date(aColor)` type-checked.
func primitiveConvertible(from, target *ir.Type) bool {
	if from == nil || target == nil {
		return false
	}
	if from.Kind == ir.TypeDyn {
		return true // dyn narrows through any primitive cast
	}
	fromStr, targetStr := ir.StringReprStruct(from), ir.StringReprStruct(target)
	switch {
	case fromStr:
		// To string, or to its own type — `date(d)` is identity.
		return target.Kind == ir.TypeString || (targetStr && from.Equal(target))
	case targetStr:
		// Only a string produces one; the parse/validate happens at
		// platform-codegen time.
		return from.Kind == ir.TypeString
	}
	if !isConvertiblePrimitive(from.Kind) {
		return false
	}
	switch target.Kind {
	case ir.TypeInt, ir.TypeFloat:
		switch from.Kind {
		case ir.TypeInt, ir.TypeFloat, ir.TypeString, ir.TypeBool, ir.TypeEnum, ir.TypeUnit:
			return true
		}
	case ir.TypeString:
		return true // any listed primitive → string
	case ir.TypeBool:
		switch from.Kind {
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
	}
	return false
}

// multiBaseUnitCast reports the UnitDef behind a numeric cast of a multi-base
// unit, or nil for any other conversion. There is no one number to produce:
// the value is a magnitude per base, which is what the checker already says
// about ordering two of them (Type.IsSingleBaseUnit gates <, <=, >, >=) and
// says here about int() and float().
func multiBaseUnitCast(from, target *ir.Type) *ir.UnitDef {
	if from == nil || target == nil || from.Kind != ir.TypeUnit {
		return nil
	}
	if target.Kind != ir.TypeInt && target.Kind != ir.TypeFloat {
		return nil
	}
	if from.IsSingleBaseUnit() {
		return nil
	}
	return ir.UnitDeclOf(from)
}

// baseNameList spells a unit's bases as the member reads that replace a cast.
func baseNameList(u *ir.UnitDef) string {
	bases := u.Bases()
	names := make([]string, len(bases))
	for i, b := range bases {
		names[i] = "." + b.Name
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}
