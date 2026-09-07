package javascript

// A unit value in JS is its magnitudes, never its spelling. A single-base
// unit -- `duration`, whose base is ms -- is a plain number, so `100ms + 1s`
// is `1100` and `setInterval(f, interval)` takes it directly. A multi-base
// unit -- `measurement`, with five -- is an object keyed by base name, and a
// binary op distributes over the keys the way Go's unit struct does.
//
// Before this, a unit literal was emitted as a quoted string carrying its own
// suffix, so `+` concatenated: `3px + 4px` produced "3px4px" and there was no
// numeric value at runtime to recover.

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// UnitLiteral renders a unit literal as its magnitude(s) for a platform
// emitting its own JS. Falls back to "null" for a literal carrying no
// resolvable unit, which is what every other literal kind does there.
func UnitLiteral(n *ir.Literal) string {
	if s, ok := unitLiteralJS(n); ok {
		return s
	}
	return "null"
}

// unitLiteralJS renders a unit literal as its magnitude(s). Reports false
// when n is not a unit literal or its suffix does not resolve, leaving the
// caller's own fallback in charge.
func unitLiteralJS(n *ir.Literal) (string, bool) {
	mag, base, ok := ir.UnitMagnitude(n)
	if !ok {
		return "", false
	}
	ud := ir.UnitDeclOf(n.Type)
	val := ir.FormatUnitMagnitude(mag)
	if ud.IsSingleBase() {
		return val, true
	}
	// Every base gets a key: a missing one would make the other operand's
	// arithmetic NaN, and a value's shape must not depend on which suffix it
	// happened to be written with.
	parts := make([]string, 0, len(ud.Bases()))
	for _, b := range ud.Bases() {
		v := "0"
		if b.Name == base {
			v = val
		}
		parts = append(parts, fmt.Sprintf("%s: %s", b.Name, v))
	}
	return "{ " + strings.Join(parts, ", ") + " }", true
}

// isMultiBaseUnitJS reports whether t is represented as a per-base object.
func isMultiBaseUnitJS(t *ir.Type) bool {
	ud := ir.UnitDeclOf(t)
	return ud != nil && !ud.IsSingleBase()
}

// multiBaseUnitBinaryJS distributes a binary op over the base keys of a
// multi-base unit operand, projecting a unit operand's key and using a scalar
// operand (unit * 2) whole. Returns ("", false) when neither side is one.
func multiBaseUnitBinaryJS(n *ir.Binary, left, right string) (string, bool) {
	ud := ir.UnitDeclOf(n.Left.ExprType())
	if ud == nil || ud.IsSingleBase() {
		ud = ir.UnitDeclOf(n.Right.ExprType())
	}
	if ud == nil || ud.IsSingleBase() {
		return "", false
	}
	op := binaryOpStr(n.Op)
	leftIsRec := isMultiBaseUnitJS(n.Left.ExprType())
	rightIsRec := isMultiBaseUnitJS(n.Right.ExprType())

	// Comparison yields a bool, not a measurement, so there are no keys to
	// build. Equality over records is left to the caller's default, which is
	// reference equality -- wrong, but no more wrong than it was, and out of
	// scope here.
	if !n.Type.IsNumericOrUnit() {
		return "", false
	}

	parts := make([]string, 0, len(ud.Bases()))
	for _, b := range ud.Bases() {
		lhs, rhs := left, right
		if leftIsRec {
			lhs = fmt.Sprintf("(%s).%s", left, b.Name)
		}
		if rightIsRec {
			rhs = fmt.Sprintf("(%s).%s", right, b.Name)
		}
		parts = append(parts, fmt.Sprintf("%s: %s %s %s", b.Name, lhs, op, rhs))
	}
	return "{ " + strings.Join(parts, ", ") + " }", true
}
