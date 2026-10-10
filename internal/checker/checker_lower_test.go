package checker

import (
	"strings"
	"testing"

	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// A program cannot name a node operation: there is no package to import that
// would let it.
func TestNodeOpsAreNotNameable(t *testing.T) {
	src := `func test() => lower.CreateNode("text")`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := Check(doc, &Config{IsMain: true})
	var msgs []string
	for _, d := range diags {
		if d.Severity == ir.Error {
			msgs = append(msgs, d.Msg)
		}
	}
	if len(msgs) == 0 {
		t.Fatal("lower.CreateNode resolved; the node operations are not a SNGL surface")
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "lower") {
		t.Errorf("want a diagnostic naming lower, got %v", msgs)
	}
}

// TestNodeOpPackageIsGone guards against the package coming back by accident:
// importing it should fail, not resolve to an empty macro package.
func TestNodeOpPackageIsGone(t *testing.T) {
	src := `import "sngl:internal/lower"` + "\n" + `func test() => 1`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := Check(doc, &Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, "internal/lower") {
			return
		}
	}
	t.Error("importing sngl:internal/lower did not fail")
}
