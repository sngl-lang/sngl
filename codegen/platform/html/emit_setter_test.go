package html

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func newMinimalHTMLGenWithDT(t *testing.T) *htmlGen {
	t.Helper()
	g := newMinimalHTMLGen(t)
	g.dt = codegen.NewDepTracker(map[ir.Symbol]struct{}{}, map[*ir.Func]struct{}{}, map[*ir.Func]map[ir.Symbol]struct{}{})
	return g
}

func TestEmitSetter_AsyncChangeHandler(t *testing.T) {
	g := newMinimalHTMLGenWithDT(t)
	dv := &ir.Var{
		Name: "count",
		Handlers: []*ir.EventHandler{
			{
				Name: "change",
				Func: &ir.Func{
					Block: []ir.Stmt{
						&ir.CallStmt{Call: &ir.Call{
							Func: &ir.Func{Name: "loadAll", IsAsync: true},
						}},
					},
				},
			},
		},
	}
	var b strings.Builder
	g.emitSetter(&b, dv)
	out := b.String()
	if !strings.Contains(out, "async function $set_count(v)") {
		t.Fatalf("expected async setter, got: %s", out)
	}
}

func TestEmitSetter_SyncChangeHandler(t *testing.T) {
	g := newMinimalHTMLGenWithDT(t)
	dv := &ir.Var{
		Name: "count",
		Handlers: []*ir.EventHandler{
			{
				Name: "change",
				Func: &ir.Func{
					Block: []ir.Stmt{
						&ir.CallStmt{Call: &ir.Call{
							Func: &ir.Func{Name: "doStuff", IsAsync: false},
						}},
					},
				},
			},
		},
	}
	var b strings.Builder
	g.emitSetter(&b, dv)
	out := b.String()
	if !strings.Contains(out, "function $set_count(v)") || strings.Contains(out, "async function") {
		t.Fatalf("expected sync setter, got: %s", out)
	}
}
