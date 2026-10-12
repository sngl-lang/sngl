package golang

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// UnitGoTypeKind classifies how a SNGL unit decl is represented in Go.
type UnitGoTypeKind int

const (
	// UnitDuration is the special-cased `unit duration { ms, … }` decl,
	// lowered to time.Duration rather than its own type.
	UnitDuration UnitGoTypeKind = iota
	// UnitScalar is a unit with a single base suffix; lowered as
	// `type Name float64`.
	UnitScalar
	// UnitMultiBase is a unit with two or more base suffixes; lowered as
	// `type Name struct { Base1, Base2, ... float64 }`.
	UnitMultiBase
)

// ClassifyUnit returns how a UnitDef should be represented in generated Go.
func ClassifyUnit(u *ir.UnitDef) UnitGoTypeKind {
	if u == nil {
		return UnitScalar
	}
	if u.Builtin == ir.BuiltinDuration {
		return UnitDuration
	}
	if u.IsSingleBase() {
		return UnitScalar
	}
	return UnitMultiBase
}

// UnitBases returns the IsBase suffixes of u in declaration order.
func UnitBases(u *ir.UnitDef) []*ir.UnitSuffix { return u.Bases() }

// multiBaseUnitOperand returns the UnitDef of whichever operand is a
// multi-base unit (left preferred), or (nil, false) when neither is.
// Used by GoIRContext.Binary to decide whether to expand a binary op
// component-wise over a unit struct's base fields.
func multiBaseUnitOperand(l, r ir.Expr) (*ir.UnitDef, bool) {
	if ud := unitDeclOf(l.ExprType()); ud != nil && ClassifyUnit(ud) == UnitMultiBase {
		return ud, true
	}
	if ud := unitDeclOf(r.ExprType()); ud != nil && ClassifyUnit(ud) == UnitMultiBase {
		return ud, true
	}
	return nil, false
}

// unitDeclOf extracts the UnitDef from a unit-typed ir.Type, or nil.
func unitDeclOf(t *ir.Type) *ir.UnitDef { return ir.UnitDeclOf(t) }

// isMultiBaseUnitType reports whether t is a multi-base unit type.
func isMultiBaseUnitType(t *ir.Type) bool {
	ud := unitDeclOf(t)
	return ud != nil && ClassifyUnit(ud) == UnitMultiBase
}

// EmitUnitTypeDecls renders Go `type` declarations for every UnitDef in
// units, skipping the duration special-case. Output is a series of
// top-level decls joined by blank lines.
func EmitUnitTypeDecls(units []*ir.UnitDef) string {
	var b strings.Builder
	for _, u := range units {
		switch ClassifyUnit(u) {
		case UnitDuration:
			continue
		case UnitScalar:
			fmt.Fprintf(&b, "type %s float64\n\n", ExportName(u.Name))
		case UnitMultiBase:
			fmt.Fprintf(&b, "type %s struct {\n", ExportName(u.Name))
			for _, base := range UnitBases(u) {
				fmt.Fprintf(&b, "\t%s float64\n", ExportName(base.Name))
			}
			b.WriteString("}\n\n")
		}
	}
	return b.String()
}

// LowerUnitLiteralGo lowers an ir.Literal of TypeUnit kind to its Go
// expression form. Returns ("", false) if the literal lacks the unit
// metadata needed (Type.Decl, Suffix) for the lowering.
//
//   - duration literals lower to a sum of (count * time.Unit) terms.
//   - single-base scalar units lower to TypeName(value * factor).
//   - multi-base units lower to TypeName{<base>: value * factor}.
func LowerUnitLiteralGo(lit *ir.Literal) (string, bool) {
	mag, base, ok := ir.UnitMagnitude(lit)
	if !ok {
		return "", false
	}
	ud := ir.UnitDeclOf(lit.Type)
	val := ir.FormatUnitMagnitude(mag)
	switch ClassifyUnit(ud) {
	case UnitDuration:
		return durationLiteralGo(lit, mag), true
	case UnitScalar:
		// Reduce into the (single) base; the float64-aliased type takes
		// a single scalar value.
		return fmt.Sprintf("%s(%s)", ExportName(ud.Name), val), true
	case UnitMultiBase:
		return fmt.Sprintf("%s{%s: %s}", ExportName(ud.Name), ExportName(base), val), true
	}
	return "", false
}

// durationLiteralGo emits a Go expression for a duration literal of value
// num expressed in suffix suf. Picks the closest standard time.* constant
// (Millisecond/Second/Minute/Hour) so the source `1s` reads as
// `1*time.Second` instead of `1000*time.Millisecond`.
//
// Parenthesised because the form is a product: unbracketed, `d / 1s` came out
// as `d / time.Duration(1000) * time.Millisecond`, which Go groups as
// `(d / 1000) * time.Millisecond` and is off by a factor of a million.
func durationLiteralGo(lit *ir.Literal, mag float64) string {
	if unitExpr, ok := goTimeConst[lit.Suffix]; ok {
		num, _ := parseUnitNumber(lit.Value)
		return fmt.Sprintf("(time.Duration(%s) * %s)", ir.FormatUnitMagnitude(num), unitExpr)
	}
	return fmt.Sprintf("(time.Duration(%s) * time.Millisecond)", ir.FormatUnitMagnitude(mag))
}

// goTimeConst maps duration's declared suffixes onto the time package's own
// constants, so `1s` reads as 1*time.Second rather than 1000*time.Millisecond.
// A suffix with no entry falls back to the base, which is ms.
var goTimeConst = map[string]string{
	"ms": "time.Millisecond",
	"s":  "time.Second",
	"m":  "time.Minute",
	"h":  "time.Hour",
}

// parseUnitNumber parses a unit literal's numeric half. Handles `_` digit
// separators.
func parseUnitNumber(num string) (float64, bool) {
	numStr := strings.ReplaceAll(num, "_", "")
	if numStr == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// LowerTimeLiteralGo lowers a date / time / datetime literal to a call
// against the platform-emitted helpers (mustParseDate, mustParseTime,
// mustParseDateTime). Returns ("", false) for any other literal shape.
// Platforms that emit time-typed binds are responsible for declaring
// these helpers in their generated code.
//
// The conversion type for a date-typed var initialized from a string
// literal lives on the ir.Conversion wrapper rather than the inner
// literal; LowerTypedLiteralGo handles that case by accepting the
// var/conversion type explicitly.
func LowerTimeLiteralGo(lit *ir.Literal) (string, bool) {
	if lit == nil || lit.Type == nil {
		return "", false
	}
	return LowerTypedLiteralGo(lit, lit.Type)
}

// LowerTypedLiteralGo is like LowerTimeLiteralGo but uses an explicit
// target type — used at var-init sites where the literal is wrapped in
// an ir.Conversion that the caller has already unwrapped.
func LowerTypedLiteralGo(lit *ir.Literal, target *ir.Type) (string, bool) {
	if lit == nil || target == nil {
		return "", false
	}
	switch {
	case ir.IsDateStruct(target):
		return fmt.Sprintf("mustParseDate(%q)", lit.Value), true
	case ir.IsTimeStruct(target):
		return fmt.Sprintf("mustParseTime(%q)", lit.Value), true
	case ir.IsDateTimeStruct(target):
		return fmt.Sprintf("mustParseDateTime(%q)", lit.Value), true
	}
	return "", false
}

// LowerVarInit emits the Go expression for a state-var initializer.
// Routes Literal inits through typed/unit helpers first; falls back to
// the full context-aware GoIRContext.EvalExpr for non-literal exprs
// (calls, idents, binary ops) so enum members, computed-into-init
// references, and i18n.tr expressions all resolve correctly. Used by
// every Go-emitting platform (fyne / bubbletea / gtk4).
func LowerVarInit(v *ir.Var, gc *GoIRContext) string {
	if v == nil {
		return ""
	}
	return LowerBindInit(v.Type, v.Init, gc)
}

// LowerBindInit is LowerVarInit for a binding that is not a declaration: a
// a document's route parameters are the *ir.Param its slot population declares,
// and a target with no request stores them as a Model field like any other,
// initialised to the zero of their type because nothing in the program writes
// one.
func LowerBindInit(t *ir.Type, init ir.Expr, gc *GoIRContext) string {
	v := &ir.Var{Type: t, Init: init}
	if v.Init == nil {
		return ZeroValueGo(IRTypeToGo(v.Type))
	}
	// The checker wraps implicit type coercions (string → date, etc.)
	// in ir.Conversion; peel so typed-literal fast paths see the
	// underlying ir.Literal.
	initExpr := v.Init
	if conv, ok := initExpr.(*ir.Conversion); ok {
		initExpr = conv.Operand
	}
	if lit, ok := initExpr.(*ir.Literal); ok {
		if out, ok := LowerTypedLiteralGo(lit, v.Type); ok {
			return out
		}
		if out, ok := LowerUnitLiteralGo(lit); ok {
			return out
		}
	}
	// Non-literal initializers (idents resolving to enums, calls to
	// i18n.tr, binary ops, ternaries, etc.) need context-aware eval so
	// model-field reads route through `m.<field>` and enum members
	// emit as quoted strings.
	if gc != nil {
		switch v.Init.(type) {
		case *ir.Literal, *ir.Lambda:
			// Fall through to IRLiteralToGo: a scalar literal carries no
			// sub-expression, and a lambda body is emitted by its own path.
		default:
			// A composite literal is literal-SHAPED, not literal: every
			// element is an arbitrary expression. Sent to the literal-only
			// translator, an element that is not itself a literal reached its
			// "" fallback, so `Node{value=first.value}` emitted `Value: ""`.
			// It compiled wherever the field was `dyn`, because Go's `any`
			// accepts a string.
			return gc.EvalExpr(v.Init)
		}

	}
	return IRLiteralToGo(v.Init)
}

// goScalarUnitScale writes a single-base unit or a duration scaled by a plain
// number. Go multiplies two values of one type only, and a unit is a named
// type -- `type Tick float64`, or time.Duration -- so the scalar has to become
// the unit's type, which an untyped constant does by itself and a variable
// does not: `m.one * m.n` is a time.Duration times an int, which no Go
// compiler accepts. A float on a duration is the one case converting the
// scalar cannot express, since time.Duration is an integer count of
// nanoseconds and `time.Duration(0.5)` is zero; the product is taken in
// float64 and converted back, which is what a fraction of a duration means.
func goScalarUnitScale(n *ir.Binary, left, right string) (string, bool) {
	if n.Op != ast.BinMul && n.Op != ast.BinDiv {
		return "", false
	}
	ud, scalar, unitLeft := scaledUnit(n)
	if ud == nil {
		return "", false
	}
	kind := ClassifyUnit(ud)
	goT := IRTypeToGo(n.ExprType())
	scalarT := scalar.ExprType()
	if kind == UnitDuration && scalarT.Kind == ir.TypeFloat {
		if unitLeft {
			left = "float64(" + left + ")"
		} else {
			right = "float64(" + right + ")"
		}
		return fmt.Sprintf("%s(%s %s %s)", goT, left, irBinaryOp(n.Op), right), true
	}
	if _, lit := scalar.(*ir.Literal); lit {
		return "", false
	}
	if unitLeft {
		right = goT + "(" + right + ")"
	} else {
		left = goT + "(" + left + ")"
	}
	return "(" + left + " " + irBinaryOp(n.Op) + " " + right + ")", true
}

// scaledUnit is the single-base unit or duration one operand of n holds when
// the other is a plain number, that number, and whether the unit is on the
// left.
func scaledUnit(n *ir.Binary) (*ir.UnitDef, ir.Expr, bool) {
	numeric := func(e ir.Expr) bool {
		t := e.ExprType()
		return t != nil && (t.Kind == ir.TypeInt || t.Kind == ir.TypeFloat)
	}
	single := func(e ir.Expr) *ir.UnitDef {
		if ud := unitDeclOf(e.ExprType()); ud != nil && ClassifyUnit(ud) != UnitMultiBase {
			return ud
		}
		return nil
	}
	if ud := single(n.Left); ud != nil && numeric(n.Right) {
		return ud, n.Right, true
	}
	if ud := single(n.Right); ud != nil && numeric(n.Left) {
		return ud, n.Left, false
	}
	return nil, nil, false
}

func isIntVar(e ir.Expr) bool {
	if _, lit := e.(*ir.Literal); lit {
		return false
	}
	t := e.ExprType()
	return t != nil && t.Kind == ir.TypeInt
}
