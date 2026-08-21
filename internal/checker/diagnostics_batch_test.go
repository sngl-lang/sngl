package checker

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// expectCheckError parses+checks src and asserts an error diagnostic whose
// message contains substr.
func expectCheckError(t *testing.T, src, substr string) {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := Check(doc, &Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, substr) {
			return
		}
	}
	var got strings.Builder
	for _, d := range diags {
		got.WriteString("\n  " + d.Error())
	}
	t.Errorf("expected error containing %q, got:%s", substr, got.String())
}

// #8 — duplicate struct field names are a hard error.
func TestDuplicateStructField(t *testing.T) {
	expectCheckError(t, `
struct P {
    x int
    y int
    x bool
}
`, "duplicate")
}

// #8 — duplicate function parameter names are a hard error.
func TestDuplicateFuncParam(t *testing.T) {
	expectCheckError(t, `
func f(x int, x int) => x
`, "duplicate")
}

// #9 — a block-bodied func with a non-void return type must return on all
// paths; a body with no return statement is an error.
func TestMissingReturn(t *testing.T) {
	expectCheckError(t, `
func mustReturn() int {
    var x = 1
}
`, "return")
}

// #9 regression guard — a for-else where both arms return must NOT be flagged
// as missing-return.
func TestMissingReturnForElseOK(t *testing.T) {
	checkNoErrors(t, `
component main {
    func first(xs list<int>) int {
        for x = xs {
            return x
        } else {
            return 0
        }
    }
    text(value="{first([1,2])}")
}
`)
}

// #9 regression guard — a func whose body ends in a call (which may raise /
// never return) must NOT be flagged as missing-return.
func TestMissingReturnTrailingCallOK(t *testing.T) {
	checkNoErrors(t, `
component main {
    func sideEffect() { }
    func boom() int {
        sideEffect()
    }
    text(value="{boom()}")
}
`)
}

// #7 — an integer literal exceeding int64 range is an error.
func TestIntegerOverflow(t *testing.T) {
	expectCheckError(t, `
component main {
    var x int = 99999999999999999999
    text(value="{x}")
}
`, "integer")
}
