package lower

import (
	"strconv"

	"duckfam.us/sngl/ir"
)

// intLiteralLit is the counter, bound and index literal every pass that
// synthesizes one writes.
func intLiteralLit(n int) *ir.Literal {
	return &ir.Literal{
		Type:  ir.TypInt,
		Value: strconv.Itoa(n),
	}
}
