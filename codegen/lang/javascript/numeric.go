package javascript

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// JS has a single number type (IEEE float64), so sized SNGL numerics need
// explicit width enforcement in the emitted code:
//   - float32           → Math.fround
//   - int/uint 8/16/32  → bit-masking (JS bit ops are 32-bit)
//   - int64/uint64      → BigInt (Number can't represent the range exactly),
//     wrapped with BigInt.asIntN / asUintN
// Plain int/float (Bits==0) keep native number semantics and are untouched, so
// existing generated code is unchanged.

// jsIsBigInt reports whether a numeric IR type is represented as a JS BigInt.
func jsIsBigInt(t *ir.Type) bool {
	return t != nil && t.Kind == ir.TypeInt && t.Bits == 64
}

// jsWrapArith wraps the JS text of an arithmetic (or negation) result so it
// carries type t's declared width. Non-sized and non-numeric types pass
// through unchanged.
func jsWrapArith(expr string, t *ir.Type) string {
	if t == nil {
		return expr
	}
	switch t.Kind {
	case ir.TypeFloat:
		if t.Bits == 32 {
			return "Math.fround(" + expr + ")"
		}
		return expr
	case ir.TypeInt:
		return jsWrapInt(expr, t)
	}
	return expr
}

// jsWrapInt masks an integer-valued JS expression to width t. For ≤32-bit
// widths JS bit operators both truncate and mask; the 64-bit widths use BigInt
// (expr must already evaluate to a BigInt).
func jsWrapInt(expr string, t *ir.Type) string {
	switch t.Bits {
	case 0:
		return expr // plain int
	case 64:
		if t.Unsigned {
			return "BigInt.asUintN(64, " + expr + ")"
		}
		return "BigInt.asIntN(64, " + expr + ")"
	case 32:
		if t.Unsigned {
			return "((" + expr + ") >>> 0)"
		}
		return "((" + expr + ") | 0)"
	case 8, 16:
		if t.Unsigned {
			return fmt.Sprintf("((%s) & 0x%X)", expr, (1<<t.Bits)-1)
		}
		shift := 32 - int(t.Bits)
		return fmt.Sprintf("((%s) << %d >> %d)", expr, shift, shift)
	}
	return expr
}

// jsConvert renders an explicit numeric conversion of operand from src to dst,
// bridging the number/BigInt domains as needed.
func jsConvert(operand string, src, dst *ir.Type) string {
	srcBig := jsIsBigInt(src)
	switch dst.Kind {
	case ir.TypeInt:
		if dst.Bits == 64 {
			// Into a BigInt width. From a Number, truncate then lift to BigInt;
			// from a BigInt, re-wrap directly.
			if srcBig {
				return jsWrapInt(operand, dst)
			}
			return jsWrapInt("BigInt(Math.trunc("+operand+"))", dst)
		}
		// Into a ≤32-bit or plain int.
		base := operand
		if srcBig {
			base = "Number(" + operand + ")"
		}
		if dst.Bits == 0 {
			return "Math.trunc(" + base + ")"
		}
		// Bit-mask (also truncates any fractional part).
		return jsWrapInt(base, dst)
	case ir.TypeFloat:
		base := operand
		if srcBig {
			base = "Number(" + operand + ")"
		}
		if dst.Bits == 32 {
			return "Math.fround(" + base + ")"
		}
		if srcBig {
			return base // already a Number-producing expression
		}
		return "parseFloat(" + base + ")"
	}
	return operand
}
