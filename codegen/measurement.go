package codegen

import (
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A `measurement` is a magnitude per base: `px`, a device-independent pixel,
// and `em` and `rem`, multiples of a font size. html hands all three to CSS.
// Every other target has no cascade to resolve either against, so both are
// its base text size, and a length is one number: px + emPx*(em + rem).
// MeasurementPx and MeasurementSum are that sum, for a value known while
// generating and for one read at run time.

// fontRelative reports whether base is a measurement base counted in font
// sizes rather than pixels.
func fontRelative(base string) bool { return base == "em" || base == "rem" }

// MeasurementPx is a constant measurement as pixels, with emPx pixels to the
// em: a literal, or a sum or difference of them, which is what the optimizer
// folds a mixed value to. A bare number is pixels. It reports false for
// anything else, which the caller reads at run time.
func MeasurementPx(e ir.Expr, emPx float64) (float64, bool) {
	switch x := e.(type) {
	case *ir.Literal:
		if mag, base, ok := ir.UnitMagnitude(x); ok {
			if fontRelative(base) {
				return mag * emPx, true
			}
			return mag, true
		}
		return IRLiteralNumber(x)
	case *ir.Conversion:
		// The bare `0` any unit takes arrives converted, `measurement(0)`:
		// android's `gap=0` read the base fields off the literal, `(0).px`.
		if lit, ok := x.Operand.(*ir.Literal); ok {
			if n, ok := IRLiteralNumber(lit); ok && n == 0 {
				return 0, true
			}
		}
		return 0, false
	case *ir.Binary:
		if x.Op != ast.BinAdd && x.Op != ast.BinSub {
			return 0, false
		}
		l, ok := MeasurementPx(x.Left, emPx)
		if !ok {
			return 0, false
		}
		r, ok := MeasurementPx(x.Right, emPx)
		if !ok {
			return 0, false
		}
		if x.Op == ast.BinSub {
			return l - r, true
		}
		return l + r, true
	}
	return 0, false
}

// MeasurementSum spells a measurement read at run time as pixels: val is the
// value as the host spells it, field the host's name for one of its bases,
// and emPx the host expression for pixels to the em. A unit with no
// font-relative base is its first base alone.
func MeasurementSum(ud *ir.UnitDef, val string, field func(base string) string, emPx string) string {
	var px, font []string
	for _, b := range ud.Bases() {
		read := val + "." + field(b.Name)
		if fontRelative(b.Name) {
			font = append(font, read)
		} else {
			px = append(px, read)
		}
	}
	if len(font) == 0 {
		if len(px) == 0 {
			return val
		}
		return px[0]
	}
	sum := emPx + "*(" + strings.Join(font, " + ") + ")"
	if len(px) > 0 {
		sum = strings.Join(px, " + ") + " + " + sum
	}
	return "(" + sum + ")"
}

// MeasurementPxExpr is MeasurementSum as IR, for a value a platform hands on
// as an argument rather than spelling itself: the float pixels v holds.
func MeasurementPxExpr(ud *ir.UnitDef, v ir.Expr, emPx float64) ir.Expr {
	read := func(base string) ir.Expr { return &ir.Select{Operand: v, Field: base, Type: ir.TypFloat} }
	add := func(l, r ir.Expr) ir.Expr {
		if l == nil {
			return r
		}
		return &ir.Binary{Type: ir.TypFloat, Op: ast.BinAdd, Left: l, Right: r}
	}
	var px, font ir.Expr
	for _, b := range ud.Bases() {
		if fontRelative(b.Name) {
			font = add(font, read(b.Name))
		} else {
			px = add(px, read(b.Name))
		}
	}
	if font != nil {
		em := &ir.Literal{Type: ir.TypFloat, Value: ir.FormatUnitMagnitude(emPx)}
		px = add(px, &ir.Binary{Type: ir.TypFloat, Op: ast.BinMul, Left: em, Right: font})
	}
	if px == nil {
		return v
	}
	return px
}
