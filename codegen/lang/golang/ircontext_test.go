package golang

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

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
