package golang

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestExportName(t *testing.T) {
	tr := &Translator{}
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"count", "Count"},
		{"myField", "MyField"},
	}
	for _, tt := range tests {
		got := tr.ExportName(tt.in)
		if got != tt.want {
			t.Errorf("ExportName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIRTypeToGo_TypeRef(t *testing.T) {
	refInt := &ir.Type{Kind: ir.TypeRef, Elems: []*ir.Type{{Kind: ir.TypeInt}}}
	if got := IRTypeToGo(refInt); got != "*int" {
		t.Errorf("TypeRef<int> = %q, want %q", got, "*int")
	}

	// ref with no elems falls back to unsafe.Pointer
	refEmpty := &ir.Type{Kind: ir.TypeRef}
	if got := IRTypeToGo(refEmpty); got != "unsafe.Pointer" {
		t.Errorf("TypeRef<> = %q, want %q", got, "unsafe.Pointer")
	}
}

func TestEmitCHeader_Basic(t *testing.T) {
	tr := &Translator{}
	ni := &ir.NativeImport{
		ImportPath: "c:/usr/include/test.h",
		LinkFlags:  []string{"-ltest"},
	}
	got := tr.EmitCHeader([]*ir.NativeImport{ni})
	if !strings.Contains(got, `import "C"`) {
		t.Errorf("EmitCHeader missing import \"C\"; got:\n%s", got)
	}
	if !strings.Contains(got, `#include "/usr/include/test.h"`) {
		t.Errorf("EmitCHeader missing #include; got:\n%s", got)
	}
	if !strings.Contains(got, "#cgo LDFLAGS: -ltest") {
		t.Errorf("EmitCHeader missing #cgo LDFLAGS; got:\n%s", got)
	}
}

func TestEmitCHeader_Empty(t *testing.T) {
	tr := &Translator{}
	got := tr.EmitCHeader(nil)
	if got != "" {
		t.Errorf("EmitCHeader(nil) = %q, want empty", got)
	}
}

// newMinimalIRCtx returns a GoIRContext backed by an empty package, sufficient
// for tests that only exercise builtin / i18n dispatch (no state/model fields).
func newMinimalIRCtx() *GoIRContext {
	pkg := &ir.Package{}
	ctx := codegen.NewExprCtx(pkg)
	return NewIRContext(ctx)
}

func TestEmitI18nPlural_GoIRContext(t *testing.T) {
	// Verify the Phase 3 arg-index fix: i18n.plural(count, forms) via the
	// namespace-call path (GoIRContext.evalCall → evalNamespaceCall) must emit
	// i18n.GetTranslator().Plural(count, forms) — NOT
	// i18n.GetTranslator().Plural("i18n", count) which was the pre-fix output.
	fn := &ir.Func{Name: "plural", Receiver: "i18n"}
	receiverExpr := &ir.Ident{Name: "i18n"}
	countLit := &ir.Literal{Value: "3", Type: ir.TypInt}
	formsMap := &ir.MapLitIR{}
	call := &ir.Call{
		Func:     fn,
		Receiver: receiverExpr,
		Args: []ir.CallArg{
			{Value: countLit},
			{Value: formsMap},
		},
	}
	gc := newMinimalIRCtx()
	got := gc.EvalExpr(call)
	if !strings.Contains(got, "i18n.GetTranslator().Plural(") {
		t.Errorf("i18n.plural: got %q, want call containing i18n.GetTranslator().Plural(", got)
	}
	// Must not start args with the namespace receiver ("i18n") string.
	if strings.Contains(got, `Plural("i18n"`) || strings.Contains(got, "Plural(i18n,") {
		t.Errorf("i18n.plural: got %q — namespace receiver leaked into args", got)
	}
}

func TestEmitI18nExactly_GoIRContext(t *testing.T) {
	// Verify the Phase 3 arg-index fix: i18n.exactly(0) via the namespace-call
	// path must emit i18n.Exactly(0) — NOT i18n.Exactly("i18n").
	//
	// Whether the receiver is prepended turns on the intrinsic's parameter
	// count, which a check registers off the declaration. This call is built by
	// hand, so it registers the one fact it turns on: `func exactly(n int)`
	// takes one parameter, which the single explicit arg already fills.
	ir.RegisterIntrinsic(ir.IntrinsicDef{
		Name:   "i18n.exactly",
		Params: []*ir.Param{{Name: "n", Type: ir.TypInt}},
		Pkg:    ir.I18nPkg,
	})
	fn := &ir.Func{Name: "exactly", Receiver: "i18n", Intrinsic: "i18n.exactly"}
	receiverExpr := &ir.Ident{Name: "i18n"}
	nLit := &ir.Literal{Value: "0", Type: ir.TypInt}
	call := &ir.Call{
		Func:     fn,
		Receiver: receiverExpr,
		Args: []ir.CallArg{
			{Value: nLit},
		},
	}
	gc := newMinimalIRCtx()
	got := gc.EvalExpr(call)
	if !strings.Contains(got, "i18n.Exactly(") {
		t.Errorf("i18n.exactly: got %q, want call containing i18n.Exactly(", got)
	}
	// Must not pass the namespace name as the argument.
	if strings.Contains(got, `Exactly("i18n"`) || strings.Contains(got, "Exactly(i18n,") {
		t.Errorf("i18n.exactly: got %q — namespace receiver leaked into args", got)
	}
	// Should pass the literal 0, not some other arg.
	if !strings.Contains(got, "Exactly(0)") {
		t.Errorf("i18n.exactly: got %q, want Exactly(0)", got)
	}
}

// A bodyless #[intrinsic] the Go backend has no emitter for must stop the
// build rather than emit a call to a function that does not exist. The guard
// sits on the generic-call paths, so this also pins that evalCall actually
// reaches it — an id served by the name-keyed dispatch returns before it.
func TestUnimplementedIntrinsicPanics(t *testing.T) {
	for _, tc := range []struct {
		name string
		call *ir.Call
	}{
		{"plain call", &ir.Call{Func: &ir.Func{Name: "nope", Intrinsic: "NoBackendHasThis"}}},
		{"namespace call", &ir.Call{
			Func:     &ir.Func{Name: "nope", Receiver: "stdlib", Intrinsic: "NoBackendHasThis"},
			Receiver: &ir.Ident{Name: "stdlib"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("no panic for an intrinsic no backend implements")
				}
				if !strings.Contains(r.(string), "NoBackendHasThis") {
					t.Errorf("panic %v does not name the intrinsic", r)
				}
			}()
			newMinimalIRCtx().EvalExpr(tc.call)
		})
	}
}

// The same shape with a body is fine: the body is what gets emitted.
func TestIntrinsicWithBodyDoesNotPanic(t *testing.T) {
	call := &ir.Call{Func: &ir.Func{
		Name: "ok", Intrinsic: "NoBackendHasThis", IntrinsicBodyUsable: true,
		Block: []ir.Stmt{&ir.Return{}},
	}}
	if got := newMinimalIRCtx().EvalExpr(call); got == "" {
		t.Error("usable intrinsic emitted nothing")
	}
}

// The method-call path, which the plain and namespace cases in
// TestUnimplementedIntrinsicPanics do not reach.
func TestUnimplementedIntrinsicPanicsOnMethodCall(t *testing.T) {
	call := &ir.Call{
		Func: &ir.Func{Name: "nope", Receiver: "string", Intrinsic: "NoBackendHasThis"},
		Args: []ir.CallArg{{Value: &ir.Ident{Name: "s", Type: ir.TypString}}},
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("no panic for an intrinsic this backend does not implement")
		}
	}()
	newMinimalIRCtx().EvalExpr(call)
}
