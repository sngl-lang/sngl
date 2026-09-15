package ir

import "git.duckfam.us/jonathan/sngl/ast"

// IsUnitRatio reports whether n divides one unit value by another, which the
// checker types as the dimensionless float the two magnitudes divide to.
//
// Here rather than in each backend because three of them need the same answer
// and it is a fact about the node: JavaScript carries a single-base unit as a
// number and already divides as floats, while Go and Kotlin carry one as an
// integer and each has to widen. Single-base only, for the reason the
// interpreter refuses the rest -- a multi-base value is a magnitude per base,
// so a ratio would need one answer per base and there is none to pick.
func IsUnitRatio(n *Binary) bool {
	if n == nil || n.Op != ast.BinDiv {
		return false
	}
	return isSingleBaseUnitType(n.Left.ExprType()) && isSingleBaseUnitType(n.Right.ExprType())
}

func isSingleBaseUnitType(t *Type) bool {
	if t == nil || t.Kind != TypeUnit {
		return false
	}
	ud := UnitDeclOf(t)
	return ud != nil && ud.IsSingleBase()
}
