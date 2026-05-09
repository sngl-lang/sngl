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
		ImportPath: "c:///usr/include/test.h",
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
	countLit := &ir.Literal{Raw: "3", Type: ir.TypInt}
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
	fn := &ir.Func{Name: "exactly", Receiver: "i18n"}
	receiverExpr := &ir.Ident{Name: "i18n"}
	nLit := &ir.Literal{Raw: "0", Type: ir.TypInt}
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
