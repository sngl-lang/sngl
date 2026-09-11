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

func TestSaveRestore(t *testing.T) {
	ctx := &ir.Ident{Name: "ctx", Type: ir.TypDyn}
	got := renderStmts(t, GoContextStmts(intrinsicCall("CanvasSave", ctx)))
	got += renderStmts(t, GoContextStmts(intrinsicCall("CanvasRestore", ctx)))
	for _, want := range []string{"ctx.Save(", "ctx.Restore("} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q, got:\n%s", want, got)
		}
	}
}

func TestUnknownIntrinsicReturnsNil(t *testing.T) {
	ctx := &ir.Ident{Name: "ctx", Type: ir.TypDyn}
	if got := GoContextStmts(intrinsicCall("CanvasBogus", ctx)); got != nil {
		t.Errorf("unknown intrinsic = %v, want nil", got)
	}
}
