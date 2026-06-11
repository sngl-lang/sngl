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

func TestRectEmitsContextCall(t *testing.T) {
	ctx := &ir.Ident{Name: "ctx", Type: ir.TypDyn}
	f := func(v string) ir.Expr { return &ir.Literal{Type: ir.TypFloat, Raw: v} }
	got := renderStmts(t, GoContextStmts(intrinsicCall("CanvasDrawRect", ctx, f("1"), f("2"), f("3"), f("4")), nil))
	if !strings.Contains(got, "ctx.Rect(") {
		t.Errorf("want ctx.Rect(...), got:\n%s", got)
	}
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

func TestDrawPrimitives(t *testing.T) {
	ctx := &ir.Ident{Name: "ctx", Type: ir.TypDyn}
	f := func(v string) ir.Expr { return &ir.Literal{Type: ir.TypFloat, Raw: v} }
	s := func(v string) ir.Expr { return &ir.Literal{Type: ir.TypString, Raw: v} }
	cases := []struct {
		intrinsic string
		args      []ir.Expr
		want      string
	}{
		{"CanvasDrawCircle", []ir.Expr{ctx, f("1"), f("2"), f("3")}, "ctx.Circle("},
		{"CanvasDrawEllipse", []ir.Expr{ctx, f("1"), f("2"), f("3"), f("4")}, "ctx.Ellipse("},
		{"CanvasDrawLine", []ir.Expr{ctx, f("1"), f("2"), f("3"), f("4")}, "ctx.Line("},
		{"CanvasDrawText", []ir.Expr{ctx, f("1"), f("2"), s("hi")}, "ctx.Text("},
		{"CanvasDrawImage", []ir.Expr{ctx, f("1"), f("2"), f("3"), f("4"), s("p")}, "ctx.Image("},
	}
	for _, tc := range cases {
		got := renderStmts(t, GoContextStmts(intrinsicCall(tc.intrinsic, tc.args...), nil))
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: want %q, got:\n%s", tc.intrinsic, tc.want, got)
		}
	}
}

// TestDrawPathEmitsLoop verifies CanvasDrawPath emits a range loop dispatching
// on the command op to ctx path-builder methods, then ctx.PaintPath().
func TestDrawPathEmitsLoop(t *testing.T) {
	ctx := &ir.Ident{Name: "ctx", Type: ir.TypDyn}
	cmds := &ir.Ident{Name: "cmds", Type: ir.TypDyn}
	got := renderStmts(t, GoContextStmts(intrinsicCall("CanvasDrawPath", ctx, cmds), nil))
	for _, want := range []string{"for", "ctx.MoveTo(", "ctx.LineTo(", "ctx.CubicTo(", "ctx.ClosePath(", "ctx.PaintPath("} {
		if !strings.Contains(got, want) {
			t.Errorf("CanvasDrawPath missing %q, got:\n%s", want, got)
		}
	}
}

func TestUnknownIntrinsicReturnsNil(t *testing.T) {
	ctx := &ir.Ident{Name: "ctx", Type: ir.TypDyn}
	if got := GoContextStmts(intrinsicCall("CanvasBogus", ctx), nil); got != nil {
		t.Errorf("unknown intrinsic = %v, want nil", got)
	}
}
