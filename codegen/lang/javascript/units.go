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

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
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

// UnitToString renders a unit-typed operand the way every target displays
// one -- its magnitude per base, `3px + 2em`. Reports false when the operand
// is not a unit, leaving String() in charge.
//
// An arrow call rather than an emitted helper because the operand is read
// once per base and may have side effects; JavaScript has no other place to
// bind it, this being an expression position.
func UnitToString(n *ir.Conversion, operand string) (string, bool) {
	if n == nil || n.Operand == nil {
		return "", false
	}
	ud := ir.UnitDeclOf(n.Operand.ExprType())
	if ud == nil {
		return "", false
	}
	bases := ud.Bases()
	if ud.IsSingleBase() {
		// A single-base value is the number itself, so there is nothing to
		// read a key off and nothing to join.
		return fmt.Sprintf("(String(%s) + %q)", operand, bases[0].Name), true
	}
	terms := make([]string, 0, len(bases))
	for _, b := range bases {
		terms = append(terms, fmt.Sprintf("[__u.%s, %q]", b.Name, b.Name))
	}
	return fmt.Sprintf("((__u) => [%s].filter((t) => t[0] !== 0).map((t) => String(t[0]) + t[1]).join(%q) || %q)(%s)",
		strings.Join(terms, ", "), ir.UnitTermSep, ir.FormatUnitZero(ud), operand), true
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

	// Equality yields a bool, so there are no keys to build: it is the
	// conjunction over the bases instead. `===` on two records is JS reference
	// identity, false for every pair the language calls equal, where a Go
	// struct compares field-wise and a Kotlin data class component-wise.
	if n.Op == ast.BinEq || n.Op == ast.BinNeq {
		if !leftIsRec || !rightIsRec {
			return "", false
		}
		return multiBaseUnitEqualJS(ud, n.Op, left, right), true
	}
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

// multiBaseUnitEqualJS renders == or != over every base of a multi-base unit.
// Every base is present on both sides because unitLiteralJS writes them all,
// so an absent one cannot read undefined here.
func multiBaseUnitEqualJS(ud *ir.UnitDef, op ast.BinaryOp, left, right string) string {
	parts := make([]string, 0, len(ud.Bases()))
	for _, b := range ud.Bases() {
		parts = append(parts, fmt.Sprintf("(%s).%s === (%s).%s", left, b.Name, right, b.Name))
	}
	eq := "(" + strings.Join(parts, " && ") + ")"
	if op == ast.BinNeq {
		return "!" + eq
	}
	return eq
}

// colorEqualJS renders == or != between two colors channel by channel: a
// color is an object in JS, and `===` on two objects is reference identity.
// An operand that is not a literal is bound once, as color.hex binds its own.
func colorEqualJS(n *ir.Binary, left, right string) (string, bool) {
	if n.Op != ast.BinEq && n.Op != ast.BinNeq || !ir.IsColorStruct(n.Left.ExprType()) || !ir.IsColorStruct(n.Right.ExprType()) {
		return "", false
	}
	var params, args []string
	side := func(e ir.Expr, js, name string) func(string) string {
		if ch, ok := colorLiteralChannels(e); ok {
			return func(c string) string { return ch[c] }
		}
		params, args = append(params, name), append(args, js)
		return func(c string) string { return name + "." + c }
	}
	l, r := side(n.Left, left, "a"), side(n.Right, right, "b")
	parts := make([]string, 0, 4)
	for _, c := range []string{"r", "g", "b", "a"} {
		parts = append(parts, l(c)+" === "+r(c))
	}
	eq := strings.Join(parts, " && ")
	if len(params) > 0 {
		eq = "((" + strings.Join(params, ", ") + ") => " + eq + ")(" + strings.Join(args, ", ") + ")"
	} else {
		eq = "(" + eq + ")"
	}
	if n.Op == ast.BinNeq {
		return "!" + eq, true
	}
	return eq, true
}

// colorLiteralChannels reads a color literal's four channels, an omitted one
// at its declared default.
func colorLiteralChannels(e ir.Expr) (map[string]string, bool) {
	sl, ok := e.(*ir.StructLit)
	if !ok || sl.Def == nil {
		return nil, false
	}
	ch := map[string]string{}
	for _, f := range sl.Def.Fields {
		if lit, ok := f.Default.(*ir.Literal); ok {
			ch[f.Name] = lit.Value
		}
	}
	for _, f := range sl.Fields {
		lit, ok := f.Value.(*ir.Literal)
		if !ok {
			return nil, false
		}
		ch[f.Name] = lit.Value
	}
	return ch, len(ch) == 4
}
