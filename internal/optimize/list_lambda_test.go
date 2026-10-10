package optimize

import (
	"testing"

	"duckfam.us/sngl/ir"
)

// A nil list is an absent one -- what a Go value's empty slice evaluates to --
// so mapping or filtering it is the empty list rather than nothing the fold
// can answer. Answered nothing, a static page's loop over a component with no
// props was left standing, and the docs site's pages for one were refused.
func TestListLambdaOverNilIsEmpty(t *testing.T) {
	p := &ir.Param{Name: "x", Type: ir.TypInt}
	lam := &ir.Lambda{Func: &ir.Func{Params: []*ir.Param{p}, Block: []ir.Stmt{&ir.Return{Value: &ir.Ident{Name: "x", Type: ir.TypInt, Sym: p}}}}}
	ctx := &evalCtx{values: map[ir.Symbol]any{}, unroll: true}
	for _, id := range []string{"list.map", "list.filter"} {
		got, ok := evalListLambda(id, []any{nil, constLambda{lam}}, ctx)
		if !ok {
			t.Fatalf("%s over nil: not evaluated", id)
		}
		if items, _ := got.([]any); len(items) != 0 {
			t.Errorf("%s over nil = %v, want the empty list", id, got)
		}
	}
	got, ok := evalListLambda("list.map", []any{[]any{1, 2}, constLambda{lam}}, ctx)
	if items, _ := got.([]any); !ok || len(items) != 2 || items[0] != 1 || items[1] != 2 {
		t.Errorf("list.map over [1, 2] = %v, %v; want [1 2]", got, ok)
	}
}
