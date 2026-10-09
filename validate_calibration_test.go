package sngl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sngl "duckfam.us/sngl"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// TestValidateNoFalsePositives calibrates ir.Validate: every testdata fixture
// that checks cleanly (no error diagnostics, no ERROR directive) must produce
// zero Validate violations on its post-check IR. This guards against the
// validator asserting invariants that valid IR does not actually satisfy.
func TestValidateNoFalsePositives(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/*.sngl")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// Skip fixtures that intentionally fail parse/check.
		if strings.Contains(string(src), "ERROR(") {
			continue
		}
		t.Run(filepath.Base(f), func(t *testing.T) {
			doc, err := sngl.Parse(f, strings.NewReader(string(src)))
			if err != nil {
				t.Skipf("parse error: %v", err)
			}
			pkg, diags := sngl.Check(doc, "testdata")
			for _, d := range diags {
				if d.Severity == ir.Error {
					t.Skipf("check error: %v", d.Msg)
				}
			}
			if pkg == nil {
				t.Skip("nil package")
			}
			for _, v := range ir.Validate(pkg) {
				t.Errorf("%s: %v", filepath.Base(f), v)
			}
		})
	}
}

// allLoweringCaps requests almost every lowering pass at once. No real target
// asks for exactly this set, but a target asking for a subset produces a
// subset of the IR shapes, so validating the union covers each pass's output.
//
// Not simply the zero Features, which under the polarity would be every pass:
// that adds passAsyncOffload, and without a target granting AsyncPost or
// AsyncSpawn to land the answer on, passAsyncCapable refuses the program
// rather than lowering it. The set below is the one this test has always run,
// written the way the record now reads.
func allLoweringCaps() sngl.Features {
	f := sngl.Features(lower.NoLowering())
	f.Toggle, f.Ternary, f.Lambda, f.Ref = false, false, false, false
	f.Unit, f.Enum, f.AsyncReactive, f.Computed = false, false, false, false
	f.Reactivity, f.Declarative, f.ListLambdas = false, false, false
	f.InlineComponents, f.ImplicitRecv, f.StructSpread = false, false, false
	f.StructComponents, f.StdlibContextParam = true, true
	f.FocusOrder, f.Canvas, f.ReactiveCanvas = true, true, true
	return f
}

// TestValidateNoFalsePositivesAfterLower is TestValidateNoFalsePositives for
// post-lower IR. It is the assertion behind the rule that a pass which
// synthesizes an identifier knows the declaration it refers to: every Ident a
// lowering pass emits for a Var, Param or LoopVar it created carries that
// symbol, so the only Sym-less Idents left are element refs naming a node in
// the emitted tree.
func TestValidateNoFalsePositivesAfterLower(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/*.sngl")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "ERROR(") {
			continue
		}
		t.Run(filepath.Base(f), func(t *testing.T) {
			doc, err := sngl.Parse(f, strings.NewReader(string(src)))
			if err != nil {
				t.Skipf("parse error: %v", err)
			}
			pkg, diags := sngl.Check(doc, "testdata")
			for _, d := range diags {
				if d.Severity == ir.Error {
					t.Skipf("check error: %v", d.Msg)
				}
			}
			if pkg == nil {
				t.Skip("nil package")
			}
			if err := sngl.Lower(pkg, allLoweringCaps()); err != nil {
				t.Skipf("lower error: %v", err)
			}
			for _, v := range ir.Validate(pkg) {
				t.Errorf("%s: %v", filepath.Base(f), v)
			}
		})
	}
}
