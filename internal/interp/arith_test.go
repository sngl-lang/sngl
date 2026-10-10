package interp

import (
	"testing"

	"duckfam.us/sngl/ast"
)

// TestApplyOpDivModByZero guards the compound-assignment div/mod-by-zero paths
// (bugs.md #7): `x /= 0` and `x %= 0` previously produced ±Inf/NaN silently
// while the plain `/` path errored. Both must now error.
func TestApplyOpDivModByZero(t *testing.T) {
	if _, err := ApplyOp(ast.AssignDiv, 1, 0, nil); err == nil {
		t.Error("ApplyOp AssignDiv by zero: want error, got nil")
	}
	if _, err := ApplyOp(ast.AssignMod, 1, 0, nil); err == nil {
		t.Error("ApplyOp AssignMod by zero: want error, got nil")
	}
	// Non-zero divisors still work.
	if v, err := ApplyOp(ast.AssignDiv, 6, 2, nil); err != nil || toFloat(v) != 3 {
		t.Errorf("ApplyOp AssignDiv 6/2 = %v, %v; want 3, nil", v, err)
	}
	if v, err := ApplyOp(ast.AssignMod, 7, 3, nil); err != nil || toFloat(v) != 1 {
		t.Errorf("ApplyOp AssignMod 7%%3 = %v, %v; want 1, nil", v, err)
	}
}
