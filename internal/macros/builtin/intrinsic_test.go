package builtin

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestIntrinsicMarksFunc(t *testing.T) {
	decl, diags := expandOne(t, `import . "sngl://internal/builtin"

#[intrinsic("StrUpper")]
func string.upper(s string) => s
`)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	fn, ok := decl.(*ast.FuncDef)
	if !ok {
		t.Fatalf("got %T, want *ast.FuncDef", decl)
	}
	if fn.Intrinsic != "StrUpper" {
		t.Errorf("Intrinsic = %q, want StrUpper", fn.Intrinsic)
	}
	if fn.IntrinsicBodyUsable {
		t.Error("body should not be usable without the flag")
	}
}

func TestIntrinsicUsableFlag(t *testing.T) {
	decl, diags := expandOne(t, `import . "sngl://internal/builtin"

#[intrinsic("IntMin", usable)]
func int.min(a int, b int) => a < b ? a : b
`)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	fn := decl.(*ast.FuncDef)
	if !fn.IntrinsicBodyUsable {
		t.Error("usable flag not recorded")
	}
}

func TestIntrinsicRejectsUnknownFlag(t *testing.T) {
	_, diags := expandOne(t, `import . "sngl://internal/builtin"

#[intrinsic("IntMin", inlinable)]
func int.min(a int, b int) => a
`)
	if !hasDiag(diags, "unknown #[intrinsic] flag") {
		t.Errorf("want unknown-flag diagnostic, got %v", diags)
	}
}

func TestIntrinsicCannotMarkAStruct(t *testing.T) {
	_, diags := expandOne(t, `import . "sngl://internal/builtin"

#[intrinsic("StrUpper")]
struct S { x int = 0 }
`)
	if !hasDiag(diags, "cannot mark") {
		t.Errorf("want cannot-mark diagnostic, got %v", diags)
	}
}

func hasDiag(diags []ir.Diagnostic, want string) bool {
	for _, d := range diags {
		if strings.Contains(d.Msg, want) {
			return true
		}
	}
	return false
}
