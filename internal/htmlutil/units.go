package htmlutil

import (
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A unit value is a magnitude and the base it is in; CSS is where that gets
// spelled. The base name is the CSS unit -- px, em, rem -- because the suffix a
// stylesheet author writes is the one SNGL borrowed.

// UnitLiteralToCSS renders a unit literal as a CSS length -- 7px, 1.5rem, 18em.
// Reports false when lit is not a unit literal, leaving the caller's own
// handling in charge.
//
// Before this, the suffix was dropped and a bare number handed to
// IsSizeProp, which appends "px" to anything numeric: `width = 2em` came
// out as `width:2px`. Valid CSS, wrong length.
func UnitLiteralToCSS(lit *ir.Literal) (string, bool) {
	mag, base, ok := ir.UnitMagnitude(lit)
	if !ok {
		return "", false
	}
	// A single-base unit has nothing dimensional to say in CSS -- a duration
	// is not a length -- so it renders as its bare magnitude and whatever
	// property it lands on decides.
	if ud := ir.UnitDeclOf(lit.Type); ud != nil && ud.IsSingleBase() {
		return ir.FormatUnitMagnitude(mag), true
	}
	return ir.FormatUnitMagnitude(mag) + base, true
}

// UnitSumToCSS renders a sum or difference of unit literals as a CSS calc():
// `16em + 2px` is what a folded value of a multi-base unit spells, one literal
// per base it carries. Reports false for anything else.
func UnitSumToCSS(b *ir.Binary) (string, bool) {
	var terms []string
	var walk func(e ir.Expr, sign string) bool
	walk = func(e ir.Expr, sign string) bool {
		switch x := e.(type) {
		case *ir.Literal:
			css, ok := UnitLiteralToCSS(x)
			if !ok {
				return false
			}
			if len(terms) > 0 {
				css = sign + " " + css
			} else if sign == "-" {
				css = "-" + css
			}
			terms = append(terms, css)
			return true
		case *ir.Binary:
			if x.Op != ast.BinAdd && x.Op != ast.BinSub {
				return false
			}
			if !walk(x.Left, sign) {
				return false
			}
			right := "+"
			if (x.Op == ast.BinSub) != (sign == "-") {
				right = "-"
			}
			return walk(x.Right, right)
		}
		return false
	}
	if !walk(b, "+") || len(terms) < 2 {
		return "", false
	}
	return "calc(" + strings.Join(terms, " ") + ")", true
}
