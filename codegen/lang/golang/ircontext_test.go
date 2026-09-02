package golang

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestGoIRContext_ContextVar_NativeCall verifies that a native (go:) call
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
			Foreign:       ir.Foreign{Path: "svc", Name: "svc.Fetch"},
			HasContextArg: true,
		},
		Args: []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Value: "id"}}},
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
			Foreign:       ir.Foreign{Path: "svc", Name: "svc.Fetch"},
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

// A Model field read is unexported, so the emitter has to know the receiver
// when it sees one -- and knowing it by name alone is not knowing it. A loop
// variable may shadow it (`for var m = entry().typeDoc.methods` under a receiver
// called `m`), and the fields it then reads belong to its own type, exported
// like any other Go struct's.
func TestGoIRContext_ShadowedReceiverIsNotTheModel(t *testing.T) {
	pkg := &ir.Package{}
	ctx := codegen.NewExprCtx(pkg)
	ctx.StateReceiver = "m"
	gc := NewIRContext(ctx)

	loopVar := &ir.LoopVar{Name: "m"}
	shadowed := &ir.Select{Operand: &ir.Ident{Name: "m", Sym: loopVar}, Field: "short"}
	if got, want := gc.EvalExpr(shadowed), "m.Short"; got != want {
		t.Errorf("shadowed receiver = %q; want %q", got, want)
	}

	// The receiver itself still reads its own unexported state.
	recv := &ir.Select{Operand: &ir.Ident{Name: "m"}, Field: "short"}
	if got, want := gc.EvalExpr(recv), "m.short"; got != want {
		t.Errorf("receiver = %q; want %q", got, want)
	}
}

// TestGoIRContext_MethodFields_DirectCall verifies that a Select on a
// RawFieldAccess ident whose field is in MethodFields lowers to a zero-arg
// method call `c.<field>()` instead of a raw field read. Mirrors legacy.
func TestGoIRContext_MethodFields_DirectCall(t *testing.T) {
	pkg := &ir.Package{}
	ctx := codegen.NewExprCtx(pkg)
	ctx.RawFieldAccess = map[string]bool{"c": true}
	ctx.MethodFields = map[string]bool{"items": true}
	gc := NewIRContext(ctx)
	sel := &ir.Select{Operand: &ir.Ident{Name: "c"}, Field: "items"}
	got := gc.EvalExpr(sel)
	want := "c.items()"
	if got != want {
		t.Errorf("MethodFields direct call = %q; want %q", got, want)
	}
}

// TestGoIRContext_MethodFields_ListRefPropRead verifies the test-scope
// list-ref prop read: `c.<id>[idx].<prop>` → `c.<id>()[idx].<Prop>()` when
// <id> is a MethodField on a RawFieldAccess recv. Mirrors legacy.
func TestGoIRContext_MethodFields_ListRefPropRead(t *testing.T) {
	pkg := &ir.Package{}
	ctx := codegen.NewExprCtx(pkg)
	ctx.RawFieldAccess = map[string]bool{"c": true}
	ctx.MethodFields = map[string]bool{"rows": true}
	gc := NewIRContext(ctx)
	sel := &ir.Select{
		Operand: &ir.Index{
			Operand: &ir.Select{Operand: &ir.Ident{Name: "c"}, Field: "rows"},
			Idx:     &ir.Literal{Type: ir.TypInt, Value: "0"},
		},
		Field: "text",
	}
	got := gc.EvalExpr(sel)
	want := "c.rows()[0].Text()"
	if got != want {
		t.Errorf("MethodFields list-ref prop read = %q; want %q", got, want)
	}
}

// multiBaseUnitType builds an ir.Type for a multi-base unit named
// "Measurement" with base suffixes px and em (matching the parity golden's
// shape). Used to characterize GoIRContext.Binary's component-wise expansion.
func multiBaseUnitType() *ir.Type {
	ud := &ir.UnitDef{
		Name: "Measurement",
		Suffixes: []*ir.UnitSuffix{
			{Name: "px", Factor: 1.0, BaseName: "px"},
			{Name: "em", Factor: 1.0, BaseName: "em"},
		},
	}
	return &ir.Type{Kind: ir.TypeUnit, Decl: ud}
}

// TestGoBinary_MultiBaseUnit characterizes GoIRContext.Binary's expansion of a
// multi-base-unit struct addition: the result is a component-wise struct
// literal `Measurement{Px: l.Px + r.Px, Em: l.Em + r.Em}`. Mirrors legacy
// translateMultiBaseUnitBinary.
func TestGoBinary_MultiBaseUnit(t *testing.T) {
	gc := newMinimalIRCtx()
	ut := multiBaseUnitType()
	add := &ir.Binary{
		Op:    ast.BinAdd,
		Left:  &ir.Ident{Name: "a", Type: ut},
		Right: &ir.Ident{Name: "b", Type: ut},
		Type:  ut,
	}
	got := gc.EvalExpr(add)
	want := "Measurement{Px: a.Px + b.Px, Em: a.Em + b.Em}"
	if got != want {
		t.Errorf("multi-base-unit add = %q; want %q", got, want)
	}
}

// TestGoBinary_MultiBaseUnit_Equality verifies that == / != on two unit
// structs emits the plain Go struct-equality form, not a component-wise
// expansion. Mirrors legacy.
func TestGoBinary_MultiBaseUnit_Equality(t *testing.T) {
	gc := newMinimalIRCtx()
	ut := multiBaseUnitType()
	eq := &ir.Binary{
		Op:    ast.BinEq,
		Left:  &ir.Ident{Name: "a", Type: ut},
		Right: &ir.Ident{Name: "b", Type: ut},
		Type:  &ir.Type{Kind: ir.TypeBool},
	}
	got := gc.EvalExpr(eq)
	want := "(a == b)"
	if got != want {
		t.Errorf("multi-base-unit equality = %q; want %q", got, want)
	}
}

// TestGoBinary_MultiBaseUnit_ScalarOperand verifies scalar * unit: the scalar
// operand is used verbatim (not field-projected) on each component. Mirrors
// legacy (!leftIsStruct → lhs = left).
func TestGoBinary_MultiBaseUnit_ScalarOperand(t *testing.T) {
	gc := newMinimalIRCtx()
	ut := multiBaseUnitType()
	mul := &ir.Binary{
		Op:    ast.BinMul,
		Left:  &ir.Literal{Type: ir.TypInt, Value: "2"},
		Right: &ir.Ident{Name: "b", Type: ut},
		Type:  ut,
	}
	got := gc.EvalExpr(mul)
	want := "Measurement{Px: 2 * b.Px, Em: 2 * b.Em}"
	if got != want {
		t.Errorf("scalar * unit = %q; want %q", got, want)
	}
}

func TestEvalStmt_If(t *testing.T) {
	gc := newMinimalIRCtx()
	stmt := &ir.If{
		Cond: &ir.Literal{Type: ir.TypBool, Value: "true"},
		Body: []ir.Stmt{
			&ir.Assign{
				Target: &ir.Ident{Name: "x"},
				Value:  &ir.Literal{Type: ir.TypInt, Value: "1"},
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
		Cond: &ir.Literal{Type: ir.TypBool, Value: "true"},
		Body: []ir.Stmt{&ir.Assign{Target: &ir.Ident{Name: "x"}, Value: &ir.Literal{Type: ir.TypInt, Value: "1"}}},
		Else: []ir.Stmt{&ir.Assign{Target: &ir.Ident{Name: "x"}, Value: &ir.Literal{Type: ir.TypInt, Value: "2"}}},
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
		Func:     &ir.Func{Foreign: ir.Foreign{Path: "C", Name: "gtk_label_new"}},
		Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypNull}}},
	}
	got := gc.EvalExpr(call)
	want := "C.gtk_label_new(nil)"
	if got != want {
		t.Errorf("EvalExpr cgo call = %q; want %q", got, want)
	}
}

func TestEvalCall_LegacyCgoPrefix(t *testing.T) {
	// Existing callers may still pass "C.foo" in Foreign.Name.
	// Renderer must not double up the prefix.
	gc := newMinimalIRCtx()
	call := &ir.Call{
		Receiver: &ir.Ident{Name: "C"},
		Func:     &ir.Func{Foreign: ir.Foreign{Path: "C", Name: "C.gtk_label_new"}},
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
			&ir.Return{Value: &ir.Literal{Type: ir.TypString, Value: "hi"}},
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

// TestRequireImportAs_ForcedDeconfliction verifies a path-derived default alias
// that clashes with a forced alias yields a suffixed default, while the forced
// path keeps its exact alias (call sites reference it literally).
func TestRequireImportAs_ForcedDeconfliction(t *testing.T) {
	gc := newMinimalIRCtx()
	gc.RequireImportAs("git.duckfam.us/jonathan/sngl/pkg/go/canvas", "snglcanvas")
	gc.RequireImport("fyne.io/fyne/v2/canvas")
	if a := gc.ForcedAlias("git.duckfam.us/jonathan/sngl/pkg/go/canvas"); a != "snglcanvas" {
		t.Errorf("forced alias = %q, want snglcanvas", a)
	}
	if a := gc.ForcedAlias("fyne.io/fyne/v2/canvas"); a != "" {
		t.Errorf("non-forced path must have no forced alias, got %q", a)
	}
}

// TestRequireImportAs_SameAliasTwoPathsPanics verifies forcing two distinct
// paths onto the same alias panics — call sites qualify with the literal alias,
// so silently suffixing one would dangle its references.
func TestRequireImportAs_SameAliasTwoPathsPanics(t *testing.T) {
	gc := newMinimalIRCtx()
	gc.RequireImportAs("a/b/canvas", "snglcanvas")
	defer func() {
		if recover() == nil {
			t.Error("expected panic on forced-alias collision")
		}
	}()
	gc.RequireImportAs("c/d/canvas", "snglcanvas")
}

// TestRequireImportAs_Idempotent verifies re-forcing the same (path, alias) is
// a no-op, not a self-collision panic.
func TestRequireImportAs_Idempotent(t *testing.T) {
	gc := newMinimalIRCtx()
	gc.RequireImportAs("a/b/canvas", "snglcanvas")
	gc.RequireImportAs("a/b/canvas", "snglcanvas")
	if a := gc.ForcedAlias("a/b/canvas"); a != "snglcanvas" {
		t.Errorf("forced alias = %q, want snglcanvas", a)
	}
}
