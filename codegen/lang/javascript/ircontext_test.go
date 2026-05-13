package javascript

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestJsEmitFuncDef_PlainFunc(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	fn := &ir.Func{
		Name:   "greet",
		Params: []*ir.Param{{Name: "name", Type: ir.TypString}},
		Block: []ir.Stmt{
			&ir.Return{Value: &ir.Literal{Type: ir.TypString, Raw: "hi"}},
		},
	}
	got := strings.Join(jc.EmitFuncDef(fn), "\n")
	if !strings.Contains(got, "function greet(name)") {
		t.Errorf("expected 'function greet(name)' header; got: %s", got)
	}
	if !strings.Contains(got, `return "hi"`) {
		t.Errorf("expected return statement; got: %s", got)
	}
}

func TestJsEvalIdent_SynthesizedBareRef(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	got := jc.EvalExpr(&ir.Ident{Name: "__n0", Synthesized: true})
	if got != "__n0" {
		t.Errorf("expected bare '__n0'; got: %s", got)
	}
}
