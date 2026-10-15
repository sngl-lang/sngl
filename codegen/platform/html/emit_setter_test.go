package html

import (
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

func newMinimalHTMLGenWithDT(t *testing.T) *htmlGen {
	t.Helper()
	g := newMinimalHTMLGen(t)
	g.dt = codegen.NewDepTracker(map[ir.Symbol]struct{}{}, map[*ir.Func]struct{}{}, map[*ir.Func]map[ir.Symbol]struct{}{})
	return g
}
