package kotlin

// A unit value in Kotlin is its magnitudes, matching Go and JS. `duration`
// has one base (ms) so it is a Long of milliseconds; `measurement` has five
// so it is a generated data class of Doubles, and a binary op distributes
// over its fields.
//
// Before this, a unit was typed String and a unit literal emitted its own
// spelling, so `3px + 4px` came out of the IR walk as the Kotlin source
// `(3px + 4px)` -- which is not a Kotlin expression at all. Android did not
// compute the wrong number here; it emitted a file that does not compile.

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// UnitKtType is the Kotlin type a value of unit u is held in.
func UnitKtType(u *ir.UnitDef) string {
	if u == nil {
		return "Double"
	}
	if !u.IsSingleBase() {
		return exportName(u.Name)
	}
	if u.Builtin == ir.BuiltinDuration {
		return "Long" // milliseconds -- duration's own base
	}
	return "Double"
}

// EmitUnitDataClasses renders a `data class` per multi-base unit, one Double
// field per base. Single-base units need none: they are a plain number.
func EmitUnitDataClasses(units []*ir.UnitDef) string {
	var b strings.Builder
	for _, u := range units {
		if u.IsSingleBase() {
			continue
		}
		fmt.Fprintf(&b, "data class %s(\n", exportName(u.Name))
		bases := u.Bases()
		for i, base := range bases {
			comma := ","
			if i == len(bases)-1 {
				comma = ""
			}
			fmt.Fprintf(&b, "    val %s: Double = 0.0%s\n", base.Name, comma)
		}
		b.WriteString(")\n\n")
	}
	return b.String()
}

// UnitLiteralKt renders a unit literal as its magnitude(s). Reports false
// when n carries no resolvable unit.
func UnitLiteralKt(n *ir.Literal) (string, bool) {
	mag, base, ok := ir.UnitMagnitude(n)
	if !ok {
		return "", false
	}
	ud := ir.UnitDeclOf(n.Type)
	if ud.IsSingleBase() {
		if UnitKtType(ud) == "Long" {
			return fmt.Sprintf("%sL", ir.FormatUnitMagnitude(mag)), true
		}
		return ktDouble(mag), true
	}
	// Only the base actually written is named; the data class defaults the
	// rest to 0.0, so a literal stays one argument wide however many bases
	// the unit declares.
	return fmt.Sprintf("%s(%s = %s)", exportName(ud.Name), base, ktDouble(mag)), true
}

// multiBaseUnitBinaryKt distributes a binary op over the fields of a
// multi-base unit operand. Returns ("", false) when neither side is one.
func multiBaseUnitBinaryKt(n *ir.Binary, left, right string) (string, bool) {
	ud := ir.UnitDeclOf(n.Left.ExprType())
	if ud == nil || ud.IsSingleBase() {
		ud = ir.UnitDeclOf(n.Right.ExprType())
	}
	if ud == nil || ud.IsSingleBase() {
		return "", false
	}
	// A comparison yields a Boolean, which has no fields to build. Kotlin's
	// data class equality is structural, so == and != are already right.
	if n.Type == nil || !n.Type.IsNumericOrUnit() {
		return "", false
	}
	leftIsRec := isMultiBaseUnitKt(n.Left.ExprType())
	rightIsRec := isMultiBaseUnitKt(n.Right.ExprType())
	op := n.Op.String()

	parts := make([]string, 0, len(ud.Bases()))
	for _, b := range ud.Bases() {
		lhs, rhs := left, right
		if leftIsRec {
			lhs = fmt.Sprintf("(%s).%s", left, b.Name)
		}
		if rightIsRec {
			rhs = fmt.Sprintf("(%s).%s", right, b.Name)
		}
		parts = append(parts, fmt.Sprintf("%s = %s %s %s", b.Name, lhs, op, rhs))
	}
	return fmt.Sprintf("%s(%s)", exportName(ud.Name), strings.Join(parts, ", ")), true
}

func isMultiBaseUnitKt(t *ir.Type) bool {
	ud := ir.UnitDeclOf(t)
	return ud != nil && !ud.IsSingleBase()
}

// ktDouble renders a magnitude as a Kotlin Double literal. A bare `7` is an
// Int there, and Int does not assign to a Double field.
func ktDouble(v float64) string {
	s := ir.FormatUnitMagnitude(v)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}
