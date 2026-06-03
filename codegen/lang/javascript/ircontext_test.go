package javascript

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
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

func TestJsBinary_IntDivisionTruncates(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	expr := &ir.Binary{
		Op:    ast.BinDiv,
		Left:  &ir.Literal{Type: ir.TypInt, Raw: "7"},
		Right: &ir.Literal{Type: ir.TypInt, Raw: "2"},
	}
	got := jc.EvalExpr(expr)
	if got != "Math.trunc(7 / 2)" {
		t.Errorf("got %q, want Math.trunc(7 / 2)", got)
	}
}

func TestJsEvalIdent_ElementRef(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	got := jc.EvalExpr(&ir.Ident{Name: "myInput", IsElementRef: true})
	want := `document.querySelector('[data-sngl-id="myInput"]')`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestJsSelect_I18nPluralKeyConst(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	expr := &ir.Select{
		Operand: &ir.Ident{Name: "i18n"},
		Field:   "other",
	}
	got := jc.EvalExpr(expr)
	if got != `"other"` {
		t.Errorf("got %q, want \"other\"", got)
	}
}

func TestJsCall_RegexBuiltin(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	call := &ir.Call{
		Func: &ir.Func{Name: "regex"},
		Args: []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: "ab+c"}}},
	}
	got := jc.EvalExpr(call)
	if got != `new RegExp("ab+c")` {
		t.Errorf("got %q", got)
	}
}

func TestJsConversion_Bool(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	conv := &ir.Conversion{
		Type:    &ir.Type{Kind: ir.TypeBool},
		Operand: &ir.Ident{Name: "x", Synthesized: true},
	}
	got := jc.EvalExpr(conv)
	if got != "Boolean(x)" {
		t.Errorf("got %q, want Boolean(x)", got)
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
