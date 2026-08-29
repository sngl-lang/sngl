package checker

import (
	"math/big"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// untypedNumericConst reports whether expr is an untyped numeric constant — a
// bare int/float literal of unspecified width, or the negation of one. These
// adapt to a sized numeric target (with a range check) instead of requiring an
// explicit cast. Returns the signed raw text and whether it is a float. A
// literal already retyped to a sized width is no longer untyped.
func untypedNumericConst(expr ir.Expr) (raw string, isFloat, ok bool) {
	switch e := expr.(type) {
	case *ir.Literal:
		if e.Type == nil || e.Type.Bits != 0 {
			return "", false, false
		}
		switch e.Type.Kind {
		case ir.TypeInt:
			return e.Value, false, true
		case ir.TypeFloat:
			return e.Value, true, true
		}
	case *ir.Unary:
		if e.Op == ast.UnaryNeg {
			if r, f, ok := untypedNumericConst(e.Operand); ok {
				return "-" + r, f, true
			}
		}
	}
	return "", false, false
}

func isUntypedNumericConst(expr ir.Expr) bool {
	_, _, ok := untypedNumericConst(expr)
	return ok
}

// unifyNumericOperands adapts a numeric binary/ternary operand pair whose types
// differ so both share one type. Rules:
//   - one untyped constant + one concrete type → adapt the constant to the
//     concrete type (a float constant cannot adapt to an int target)
//   - two untyped constants that differ (int vs float) → the int constant
//     becomes float (constant representability, not a value-level cast)
//   - two concrete-but-different types → left unchanged; the caller's per-
//     operator check reports the mismatch and requires an explicit conversion
func (c *checker) unifyNumericOperands(leftExpr, rightExpr ir.Expr, pos ast.Pos) (ir.Expr, ir.Expr) {
	left, right := exprType(leftExpr), exprType(rightExpr)
	lU := isUntypedNumericConst(leftExpr)
	rU := isUntypedNumericConst(rightExpr)
	switch {
	case lU && !rU:
		return c.adaptNumericOperand(leftExpr, right, pos), rightExpr
	case rU && !lU:
		return leftExpr, c.adaptNumericOperand(rightExpr, left, pos)
	case lU && rU:
		// Both untyped: promote an int constant to meet a float constant.
		if left.Kind == ir.TypeInt && right.Kind == ir.TypeFloat {
			return c.adaptNumericOperand(leftExpr, TypFloat, pos), rightExpr
		}
		if left.Kind == ir.TypeFloat && right.Kind == ir.TypeInt {
			return leftExpr, c.adaptNumericOperand(rightExpr, TypFloat, pos)
		}
	}
	return leftExpr, rightExpr
}

func signedBounds(bits uint8) (*big.Int, *big.Int) {
	max := new(big.Int).Lsh(big.NewInt(1), uint(bits-1)) // 2^(bits-1)
	min := new(big.Int).Neg(max)                         // -2^(bits-1)
	max.Sub(max, big.NewInt(1))                          // 2^(bits-1) - 1
	return min, max
}

func unsignedMax(bits uint8) *big.Int {
	m := new(big.Int).Lsh(big.NewInt(1), uint(bits))
	return m.Sub(m, big.NewInt(1))
}

// checkIntFits reports whether value fits target (a sized int type), emitting
// an "overflows" diagnostic at pos when it does not. Plain int (Bits==0) is
// range-checked against int64.
func (c *checker) checkIntFits(pos ast.Pos, value *big.Int, target *ir.Type) {
	if target.Bits == 0 {
		// Plain int: int64 range.
		min, max := signedBounds(64)
		if value.Cmp(min) < 0 || value.Cmp(max) > 0 {
			c.error(pos, "integer literal %s overflows int (max %d)", value.String(), int64(^uint64(0)>>1))
		}
		return
	}
	if target.Unsigned {
		if value.Sign() < 0 || value.Cmp(unsignedMax(target.Bits)) > 0 {
			c.error(pos, "integer literal %s overflows %s", value.String(), target)
		}
		return
	}
	min, max := signedBounds(target.Bits)
	if value.Cmp(min) < 0 || value.Cmp(max) > 0 {
		c.error(pos, "integer literal %s overflows %s", value.String(), target)
	}
}

// typeIntLiteral resolves the type of an integer literal given the expected
// type, range-checking the (optionally negated) magnitude. Returns the
// resolved *ir.Type. Behavior:
//   - expected is a sized/plain int  → that int type, magnitude range-checked
//   - expected is a float type       → that float type (untyped int constant
//     is representable as a float; no wrap)
//   - otherwise                      → plain int, checked against int64
func (c *checker) typeIntLiteral(pos ast.Pos, magnitude string, negative bool, expected *ir.Type) *ir.Type {
	value, ok := new(big.Int).SetString(magnitude, 0)
	if !ok {
		return TypInt
	}
	if negative {
		value.Neg(value)
	}
	if expected != nil && expected.Kind == ir.TypeInt {
		c.checkIntFits(pos, value, expected)
		return expected
	}
	if expected != nil && expected.Kind == ir.TypeFloat {
		return expected
	}
	// No numeric hint: type as plain int. Defer the range check — the literal
	// may still be adapted to a wider/unsigned type by a binary operand or
	// assignment (e.g. compared against a uint64). Only a value that no 64-bit
	// numeric type could hold is unconditionally an error here.
	if value.Cmp(new(big.Int).Neg(unsignedMax(64))) < 0 || value.Cmp(unsignedMax(64)) > 0 {
		c.error(pos, "integer literal %s overflows int (max %d)", value.String(), int64(^uint64(0)>>1))
	}
	return TypInt
}

// typeFloatLiteral resolves the type of a float literal. A float literal only
// adapts to a float target; against anything else it stays plain float (and a
// non-float target surfaces as a downstream type-mismatch).
func (c *checker) typeFloatLiteral(expected *ir.Type) *ir.Type {
	if expected != nil && expected.Kind == ir.TypeFloat {
		return expected
	}
	return TypFloat
}

// adaptNumericOperand retypes an untyped numeric literal operand to target
// (a concrete numeric type), range-checking the value. Non-literal or already-
// typed operands are returned unchanged. Used to unify binary-operator operands
// so arithmetic runs at a single width.
func (c *checker) adaptNumericOperand(expr ir.Expr, target *ir.Type, pos ast.Pos) ir.Expr {
	if target == nil || !target.IsNumeric() {
		return expr
	}
	raw, isFloat, ok := untypedNumericConst(expr)
	if !ok {
		return expr
	}
	// A float constant cannot adapt to an integer target.
	if isFloat && target.Kind == ir.TypeInt {
		return expr
	}
	if !isFloat && target.Kind == ir.TypeInt {
		if value, ok := new(big.Int).SetString(raw, 0); ok {
			c.checkIntFits(pos, value, target)
		}
	}
	return &ir.Literal{Type: target, Value: raw}
}
