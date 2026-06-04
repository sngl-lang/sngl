package golang

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestGoIRContext_ContextVar_NativeCall verifies that a native (go://) call
// whose imported signature has a context arg receives gc.Ctx.ContextVar as
// its first argument — mirroring legacy translateIRNativeCall.
func TestGoIRContext_ContextVar_NativeCall(t *testing.T) {
	pkg := &ir.Package{}
	ctx := codegen.NewExprCtx(pkg)
	ctx.ContextVar = "r.Context()"
	gc := NewIRContext(ctx)
	call := &ir.Call{
		Receiver: &ir.Ident{Name: "svc"},
		Func: &ir.Func{
			Name:          "Fetch",
			NativePkg:     "svc",
			NativeName:    "svc.Fetch",
			HasContextArg: true,
		},
		Args: []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: "id"}}},
	}
	got := gc.EvalExpr(call)
	want := `svc.Fetch(r.Context(), "id")`
	if got != want {
		t.Errorf("ContextVar native call = %q; want %q", got, want)
	}
}

// TestGoIRContext_ContextVar_DefaultBackground verifies that with no
// ContextVar set, a context-taking native call defaults to
// context.Background() — mirroring legacy.
func TestGoIRContext_ContextVar_DefaultBackground(t *testing.T) {
	pkg := &ir.Package{}
	ctx := codegen.NewExprCtx(pkg)
	gc := NewIRContext(ctx)
	call := &ir.Call{
		Receiver: &ir.Ident{Name: "svc"},
		Func: &ir.Func{
			Name:          "Fetch",
			NativePkg:     "svc",
			NativeName:    "svc.Fetch",
			HasContextArg: true,
		},
	}
	got := gc.EvalExpr(call)
	want := "svc.Fetch(context.Background())"
	if got != want {
		t.Errorf("default context native call = %q; want %q", got, want)
	}
}

// TestGoIRContext_RawFieldAccess_DirectRead verifies that a Select on a
// RawFieldAccess ident reads the unexported field directly (no ExportName)
// — mirroring legacy translateIRExpr's Select case for test recvs.
func TestGoIRContext_RawFieldAccess_DirectRead(t *testing.T) {
	pkg := &ir.Package{}
	ctx := codegen.NewExprCtx(pkg)
	ctx.RawFieldAccess = map[string]bool{"c": true}
	gc := NewIRContext(ctx)
	sel := &ir.Select{Operand: &ir.Ident{Name: "c"}, Field: "count"}
	got := gc.EvalExpr(sel)
	want := "c.count"
	if got != want {
		t.Errorf("RawFieldAccess direct read = %q; want %q", got, want)
	}
}

// TestGoIRContext_RawFieldAccess_NestedGetter verifies the test-scope
// property read on an id'd child node: `c.<id>.<prop>` →
// `<id>.<inner.Field><Prop>()`. Mirrors legacy.
func TestGoIRContext_RawFieldAccess_NestedGetter(t *testing.T) {
	pkg := &ir.Package{}
	ctx := codegen.NewExprCtx(pkg)
	ctx.RawFieldAccess = map[string]bool{"c": true}
	gc := NewIRContext(ctx)
	sel := &ir.Select{
		Operand: &ir.Select{Operand: &ir.Ident{Name: "c"}, Field: "label0"},
		Field:   "text",
	}
	got := gc.EvalExpr(sel)
	want := "c.label0Text()"
	if got != want {
		t.Errorf("RawFieldAccess nested getter = %q; want %q", got, want)
	}
}

// TestGoIRContext_RawFieldAccess_NonRawUnaffected verifies that a Select on
// a non-RawFieldAccess ident still capitalizes the field via ExportName.
func TestGoIRContext_RawFieldAccess_NonRawUnaffected(t *testing.T) {
	pkg := &ir.Package{}
	ctx := codegen.NewExprCtx(pkg)
	gc := NewIRContext(ctx)
	sel := &ir.Select{Operand: &ir.Ident{Name: "c"}, Field: "count"}
	got := gc.EvalExpr(sel)
	want := "c.Count"
	if got != want {
		t.Errorf("non-raw Select = %q; want %q", got, want)
	}
}

func TestEvalStmt_If(t *testing.T) {
	gc := newMinimalIRCtx()
	stmt := &ir.If{
		Cond: &ir.Literal{Type: ir.TypBool, Raw: "true"},
		Body: []ir.Stmt{
			&ir.Assign{
				Target: &ir.Ident{Name: "x"},
				Value:  &ir.Literal{Type: ir.TypInt, Raw: "1"},
			},
		},
	}
	lines := gc.EvalStmt(stmt)
	joined := strings.Join(lines, "\n")
	if !strings.HasPrefix(joined, "if true {") {
		t.Errorf("expected 'if true {' prefix, got: %s", joined)
	}
	if !strings.Contains(joined, "x = 1") {
		t.Errorf("expected body 'x = 1', got: %s", joined)
	}
	if !strings.HasSuffix(joined, "}") {
		t.Errorf("expected closing '}', got: %s", joined)
	}
}

func TestEvalStmt_IfElse(t *testing.T) {
	gc := newMinimalIRCtx()
	stmt := &ir.If{
		Cond: &ir.Literal{Type: ir.TypBool, Raw: "true"},
		Body: []ir.Stmt{&ir.Assign{Target: &ir.Ident{Name: "x"}, Value: &ir.Literal{Type: ir.TypInt, Raw: "1"}}},
		Else: []ir.Stmt{&ir.Assign{Target: &ir.Ident{Name: "x"}, Value: &ir.Literal{Type: ir.TypInt, Raw: "2"}}},
	}
	joined := strings.Join(gc.EvalStmt(stmt), "\n")
	if !strings.Contains(joined, "} else {") {
		t.Errorf("expected '} else {' separator, got: %s", joined)
	}
	if !strings.Contains(joined, "x = 2") {
		t.Errorf("expected else body 'x = 2', got: %s", joined)
	}
}

func TestIRTypeToGo_NativeCgoPointer(t *testing.T) {
	typ := ir.NativePointerOf("GtkLabel")
	got := IRTypeToGo(typ)
	if got != "*C.GtkLabel" {
		t.Errorf("IRTypeToGo(NativePointer GtkLabel) = %q; want %q", got, "*C.GtkLabel")
	}
}

func TestIRTypeToGo_NativeGoPointer(t *testing.T) {
	typ := ir.NativeGoPointerOf("fyne.Container")
	got := IRTypeToGo(typ)
	if got != "*fyne.Container" {
		t.Errorf("IRTypeToGo(NativeGoPointer fyne.Container) = %q; want %q", got, "*fyne.Container")
	}
}

func TestEvalConversion_NativeCgoPointerCast(t *testing.T) {
	gc := newMinimalIRCtx()
	operand := &ir.Ident{Name: "raw"}
	conv := &ir.Conversion{Type: ir.NativePointerOf("GtkLabel"), Operand: operand}
	got := gc.EvalExpr(conv)
	want := "(*C.GtkLabel)(unsafe.Pointer(raw))"
	if got != want {
		t.Errorf("EvalExpr cgo cast = %q; want %q", got, want)
	}
}

func TestEvalCall_CgoNativePrefix(t *testing.T) {
	gc := newMinimalIRCtx()
	call := &ir.Call{
		Receiver: &ir.Ident{Name: "C"},
		Func:     &ir.Func{NativePkg: "C", NativeName: "gtk_label_new"},
		Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypNull}}},
	}
	got := gc.EvalExpr(call)
	want := "C.gtk_label_new(nil)"
	if got != want {
		t.Errorf("EvalExpr cgo call = %q; want %q", got, want)
	}
}

func TestEvalCall_LegacyCgoPrefix(t *testing.T) {
	// Existing callers may still pass "C.foo" in NativeName.
	// Renderer must not double up the prefix.
	gc := newMinimalIRCtx()
	call := &ir.Call{
		Receiver: &ir.Ident{Name: "C"},
		Func:     &ir.Func{NativePkg: "C", NativeName: "C.gtk_label_new"},
	}
	got := gc.EvalExpr(call)
	want := "C.gtk_label_new()"
	if got != want {
		t.Errorf("EvalExpr legacy cgo call = %q; want %q", got, want)
	}
}

func TestEmitFuncDef_PlainFunc(t *testing.T) {
	gc := newMinimalIRCtx()
	fn := &ir.Func{
		Name:   "greet",
		Params: []*ir.Param{{Name: "name", Type: ir.TypString}},
		Return: ir.TypString,
		Block: []ir.Stmt{
			&ir.Return{Value: &ir.Literal{Type: ir.TypString, Raw: "hi"}},
		},
	}
	got := strings.Join(gc.EmitFuncDef(fn), "\n")
	want := "func greet(name string) string {\n\treturn \"hi\"\n}"
	if got != want {
		t.Errorf("EmitFuncDef plain func mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestEmitFuncDef_ModelMethod(t *testing.T) {
	gc := newMinimalIRCtx()
	fn := &ir.Func{
		Name:     "Click",
		Receiver: "Model",
		Block:    []ir.Stmt{},
	}
	got := strings.Join(gc.EmitFuncDef(fn), "\n")
	want := "func (m *Model) Click() {\n}"
	if got != want {
		t.Errorf("EmitFuncDef Model method mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestEmitFuncDef_NativeParam(t *testing.T) {
	gc := newMinimalIRCtx()
	fn := &ir.Func{
		Name:     "__renderSlot0",
		Receiver: "Model",
		Params:   []*ir.Param{{Name: "container", Type: ir.NativePointerOf("GtkBox")}},
		Return:   ir.TypVoid,
	}
	got := strings.Join(gc.EmitFuncDef(fn), "\n")
	want := "func (m *Model) __renderSlot0(container *C.GtkBox) {\n}"
	if got != want {
		t.Errorf("EmitFuncDef native param mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
