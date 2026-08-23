package marks

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestIntrinsicMarksFunc(t *testing.T) {
	decl, diags := expandOne(t, `import . "sngl://internal/marks"

#[intrinsic("string.upper")]
func string.upper(s string) => s
`)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	fn, ok := decl.(*ast.FuncDef)
	if !ok {
		t.Fatalf("got %T, want *ast.FuncDef", decl)
	}
	if fn.Intrinsic.ID != "string.upper" {
		t.Errorf("Intrinsic = %q, want StrUpper", fn.Intrinsic.ID)
	}
	if fn.Intrinsic.BodyUsable {
		t.Error("body should not be usable without the flag")
	}
}

func TestIntrinsicUsableFlag(t *testing.T) {
	decl, diags := expandOne(t, `import . "sngl://internal/marks"

#[intrinsic("int.min", usable)]
func int.min(a int, b int) => a < b ? a : b
`)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	fn := decl.(*ast.FuncDef)
	if !fn.Intrinsic.BodyUsable {
		t.Error("usable flag not recorded")
	}
}

func TestIntrinsicRejectsUnknownFlag(t *testing.T) {
	_, diags := expandOne(t, `import . "sngl://internal/marks"

#[intrinsic("int.min", inlinable)]
func int.min(a int, b int) => a
`)
	if !hasDiag(diags, "unknown value \"inlinable\"") {
		t.Errorf("want unknown-flag diagnostic, got %v", diags)
	}
}

func TestIntrinsicTakesSeveralFlags(t *testing.T) {
	decl, diags := expandOne(t, `import . "sngl://internal/marks"

#[intrinsic("list.push", usable, mutates, mutatesReceiver)]
func list<T>.push(item T) => this
`)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	m := decl.(*ast.FuncDef).Intrinsic
	if !m.BodyUsable || !m.Mutates || !m.MutatesReceiver || m.Readonly {
		t.Errorf("flags not recorded: %+v", m)
	}
}

func TestIntrinsicRejectsContradictoryFlags(t *testing.T) {
	_, diags := expandOne(t, `import . "sngl://internal/marks"

#[intrinsic("File.pick", mutates, readonly)]
func File.pick() => ""
`)
	if !hasDiag(diags, "either has an effect or only reads host state") {
		t.Errorf("want contradiction diagnostic, got %v", diags)
	}
}

func TestIntrinsicRejectsRepeatedFlag(t *testing.T) {
	_, diags := expandOne(t, `import . "sngl://internal/marks"

#[intrinsic("File.pick", readonly, readonly)]
func File.pick() => ""
`)
	if !hasDiag(diags, "repeats flag readonly") {
		t.Errorf("want repeated-flag diagnostic, got %v", diags)
	}
}

func TestIntrinsicCannotMarkAStruct(t *testing.T) {
	_, diags := expandOne(t, `import . "sngl://internal/marks"

#[intrinsic("string.upper")]
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
