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

// Arith applies a numeric binary op (Add/Sub/Mul/Div/Mod). When both operands
// are int it uses integer semantics — integer division and modulo, exact past
// 2^53. Otherwise it computes in float64 and keeps a float64 result (a float
// operand makes the result a float; `3.0` stays `3.0`, distinct from int `3`).
// Div/mod by zero and non-numeric operands return an error; callers handle
// string concatenation before calling.
func Arith(op ast.BinaryOp, left, right any) (any, error) {
	if li, ok := left.(int); ok {
		if ri, ok := right.(int); ok {
			switch op {
			case ast.BinAdd:
				return li + ri, nil
			case ast.BinSub:
				return li - ri, nil
			case ast.BinMul:
				return li * ri, nil
			case ast.BinDiv:
				if ri == 0 {
					return nil, fmt.Errorf("division by zero")
				}
				return li / ri, nil
			case ast.BinMod:
				if ri == 0 {
					return nil, fmt.Errorf("modulo by zero")
				}
				return li % ri, nil
			}
			return nil, fmt.Errorf("opeval: unsupported op %d", op)
		}
	}
	lf, lok := asFloat(left)
	rf, rok := asFloat(right)
	if !lok || !rok {
		return nil, fmt.Errorf("opeval: non-numeric operands %T, %T", left, right)
	}
	switch op {
	case ast.BinAdd:
		return lf + rf, nil
	case ast.BinSub:
		return lf - rf, nil
	case ast.BinMul:
		return lf * rf, nil
	case ast.BinDiv:
		if rf == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return lf / rf, nil
	case ast.BinMod:
		if rf == 0 {
			return nil, fmt.Errorf("modulo by zero")
		}
		return math.Mod(lf, rf), nil
	}
	return nil, fmt.Errorf("opeval: unsupported op %d", op)
}

// asFloat converts int/float64 to float64. Only genuine numeric types
// convert; strings/bools/etc. do not (arithmetic on them is a checker error).
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}
