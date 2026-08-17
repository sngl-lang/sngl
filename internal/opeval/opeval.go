// Package opeval holds the value-level operator semantics shared by the
// interpreter (internal/interp) and the constant folder
// (internal/optimize). Both used to carry their own arithmetic and drifted
// apart — the folder did integer division and kept floats as float64, while
// the interpreter routed everything through float64 and then collapsed whole
// results back to int, so `3/2` was `1` when folded but `1.5` when
// interpreted (audit bugs.md #8/#10). Defining the semantics once here makes
// that class of divergence impossible.
//
// It is a leaf package (only ast + math) so both callers can import it;
// internal/optimize already depends on internal/interp, so the shared code
// cannot live in either.
package opeval

import (
	"fmt"
	"math"

	"git.duckfam.us/jonathan/sngl/ast"
)

// NumKind describes the numeric type an arithmetic op produces, so the shared
// evaluator can apply the correct width semantics. Bits==0 with Float==false
// is the unspecified default int (Go int, 64-bit wrap); Float==true selects
// IEEE float (Bits==32 rounds through float32). A non-zero Bits with
// Float==false is a sized integer: results wrap two's-complement to the width,
// and Unsigned selects unsigned semantics. The zero value is the default int.
type NumKind struct {
	Bits     uint8
	Unsigned bool
	Float    bool
}

// Arith applies a numeric binary op (Add/Sub/Mul/Div/Mod) at the width and
// signedness described by kind. Div/mod by zero and non-numeric operands
// return an error; callers handle string concatenation before calling.
//
// Runtime carriers: default and sized-signed and unsigned-below-64 integers
// travel as Go int; uint64 travels as Go uint64 (so the full range above 2^63
// is exact); floats travel as float64 (float32 values pre-rounded).
func Arith(op ast.BinaryOp, left, right any, kind NumKind) (any, error) {
	if kind.Float {
		return floatArith(op, left, right, kind.Bits)
	}
	if kind.Bits != 0 {
		return intArith(op, left, right, kind.Bits, kind.Unsigned)
	}
	// Unspecified: default int semantics when both operands are integers,
	// otherwise fall back to float (covers dyn operands whose static type the
	// caller could not pin to a concrete width).
	if _, ok := asInt64(left); ok {
		if _, ok := asInt64(right); ok {
			return intArith(op, left, right, 64, false)
		}
	}
	return floatArith(op, left, right, 64)
}

// floatArith computes in float64, rounding the result through float32 when
// bits==32 so single-precision arithmetic matches native float32 targets.
func floatArith(op ast.BinaryOp, left, right any, bits uint8) (any, error) {
	lf, lok := asFloat(left)
	rf, rok := asFloat(right)
	if !lok || !rok {
		return nil, fmt.Errorf("opeval: non-numeric operands %T, %T", left, right)
	}
	var r float64
	switch op {
	case ast.BinAdd:
		r = lf + rf
	case ast.BinSub:
		r = lf - rf
	case ast.BinMul:
		r = lf * rf
	case ast.BinDiv:
		if rf == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		r = lf / rf
	case ast.BinMod:
		if rf == 0 {
			return nil, fmt.Errorf("modulo by zero")
		}
		r = math.Mod(lf, rf)
	default:
		return nil, fmt.Errorf("opeval: unsupported op %d", op)
	}
	if bits == 32 {
		return float64(float32(r)), nil
	}
	return r, nil
}

// intArith computes an integer op with two's-complement wraparound to the given
// width. add/sub/mul are computed in the uint64 modular ring and masked;
// div/mod use signed or unsigned Go division as appropriate.
func intArith(op ast.BinaryOp, left, right any, bits uint8, unsigned bool) (any, error) {
	lu, lok := asUint64(left)
	ru, rok := asUint64(right)
	if !lok || !rok {
		return nil, fmt.Errorf("opeval: non-numeric operands %T, %T", left, right)
	}
	var res uint64
	switch op {
	case ast.BinAdd:
		res = lu + ru
	case ast.BinSub:
		res = lu - ru
	case ast.BinMul:
		res = lu * ru
	case ast.BinDiv, ast.BinMod:
		if ru == 0 {
			if op == ast.BinDiv {
				return nil, fmt.Errorf("division by zero")
			}
			return nil, fmt.Errorf("modulo by zero")
		}
		if unsigned {
			if op == ast.BinDiv {
				res = lu / ru
			} else {
				res = lu % ru
			}
		} else {
			li, ri := int64(lu), int64(ru)
			if op == ast.BinDiv {
				res = uint64(li / ri)
			} else {
				res = uint64(li % ri)
			}
		}
	default:
		return nil, fmt.Errorf("opeval: unsupported op %d", op)
	}
	return maskResult(res, bits, unsigned), nil
}

// maskResult reduces a raw two's-complement result to width bits and returns it
// as the appropriate Go carrier: uint64 for unsigned 64-bit, otherwise int
// (sign-extended for signed widths).
func maskResult(res uint64, bits uint8, unsigned bool) any {
	b := uint(bits)
	if bits == 0 {
		b = 64
	}
	if b < 64 {
		res &= (uint64(1) << b) - 1
	}
	if unsigned {
		if bits == 64 {
			return res
		}
		return int(res)
	}
	// Signed: sign-extend from b bits.
	if b < 64 {
		shift := uint(64) - b
		return int(int64(res<<shift) >> shift)
	}
	return int(int64(res))
}

// ConvertInt reinterprets a numeric value as an integer of the given width and
// signedness, truncating/wrapping to width. The bool is false when v is not a
// numeric carrier (the caller then applies string/bool parsing). Shared by the
// interpreter and const folder so `int8(x)` folds and runs identically.
func ConvertInt(v any, bits uint8, unsigned bool) (any, bool) {
	u, ok := asUint64(v)
	if !ok {
		return v, false
	}
	return maskResult(u, bits, unsigned), true
}

// ConvertFloat reinterprets a numeric value as a float of the given width,
// rounding through float32 when bits==32. The bool is false for non-numeric v.
func ConvertFloat(v any, bits uint8) (any, bool) {
	f, ok := asFloat(v)
	if !ok {
		return v, false
	}
	if bits == 32 {
		return float64(float32(f)), true
	}
	return f, true
}

// asFloat converts int/uint64/float64 to float64.
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case uint64:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// asInt64 reports whether v is an integer carrier (not float).
func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case uint64:
		return int64(n), true
	}
	return 0, false
}

// asUint64 reinterprets an integer carrier as uint64 (two's complement for
// negatives). Floats are truncated. Non-numerics fail.
func asUint64(v any) (uint64, bool) {
	switch n := v.(type) {
	case int:
		return uint64(n), true
	case int64:
		return uint64(n), true
	case uint64:
		return n, true
	case float64:
		return uint64(int64(n)), true
	}
	return 0, false
}
