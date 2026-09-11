package canvasutil

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

func renderStmts(t *testing.T, stmts []ir.Stmt) string {
	t.Helper()
	gc := golang.NewIRContext(codegen.NewExprCtx(&ir.Package{}))
	var b strings.Builder
	for _, s := range stmts {
		for _, ln := range gc.EvalStmt(s) {
			b.WriteString(ln + "\n")
		}
	}
	return b.String()
}

func intrinsicCall(name string, args ...ir.Expr) *ir.CallStmt {
	cargs := make([]ir.CallArg, len(args))
	for i, a := range args {
		cargs[i] = ir.CallArg{Value: a}
	}
	return &ir.CallStmt{Call: &ir.Call{Type: ir.TypVoid, Func: &ir.Func{Intrinsic: name}, Args: cargs}}
}

func TestApplyStyleEmitsSetters(t *testing.T) {
	ctx := &ir.Ident{Name: "ctx", Type: ir.TypDyn}
	style := &ir.Ident{Name: "style", Type: ir.TypDyn}
	got := renderStmts(t, GoContextStmts(intrinsicCall("CanvasApplyStyle", ctx, style), nil))
	// Style is bound once to a local, then the setters read its fields — so the
	// (possibly composite-literal/method-call) style expr is not repeated 20x.
	for _, want := range []string{"_cstyle1 := style", "ctx.SetFill(", "ctx.SetStroke(", "ctx.SetStrokeWidth(", "_cstyle1.Fill.R"} {
		if !strings.Contains(got, want) {
			t.Errorf("ApplyStyle missing %q, got:\n%s", want, got)
		}
	}
	if strings.Count(got, "style") > strings.Count(got, "_cstyle")+1 {
		// The bare style expr should appear only in the binding; everything else
		// reads _cstyleN.
		t.Errorf("style expression appears more than once outside the binding:\n%s", got)
	}
}

func TestSaveRestore(t *testing.T) {
	ctx := &ir.Ident{Name: "ctx", Type: ir.TypDyn}
	got := renderStmts(t, GoContextStmts(intrinsicCall("CanvasSave", ctx), nil))
	got += renderStmts(t, GoContextStmts(intrinsicCall("CanvasRestore", ctx), nil))
	for _, want := range []string{"ctx.Save(", "ctx.Restore("} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q, got:\n%s", want, got)
		}
	}
}

func TestUnknownIntrinsicReturnsNil(t *testing.T) {
	ctx := &ir.Ident{Name: "ctx", Type: ir.TypDyn}
	if got := GoContextStmts(intrinsicCall("CanvasBogus", ctx), nil); got != nil {
		t.Errorf("unknown intrinsic = %v, want nil", got)
	}
}
