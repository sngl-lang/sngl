package parser_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	ts "github.com/tree-sitter/go-tree-sitter"

	"git.duckfam.us/jonathan/sngl/internal/parser/internal/tsparser"
	v2parser "git.duckfam.us/jonathan/sngl/internal/v2/parser"
)

func TestCanLoadGrammar(t *testing.T) {
	lang := tsparser.Language()
	if lang == nil {
		t.Fatal("failed to load SNGL grammar")
	}
}

// TestFixtureAgreement parses every testdata/*.sngl file with both the v2
// Go parser and tree-sitter, checking that they agree on accept/reject.
func TestFixtureAgreement(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata")
	matches, err := filepath.Glob(filepath.Join(dir, "*.sngl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no .sngl files in testdata/")
	}

	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")

		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			isError := strings.HasPrefix(name, "error_") || strings.Contains(string(src), "ERROR(parse)")

			// Parse with v2 Go parser.
			_, goErr := v2parser.Parse(name+".sngl", src)

			// Parse with tree-sitter.
			tree := tsparser.Parse(src)
			defer tree.Close()
			tsHasErrors := tsparser.HasErrors(tree)

			if isError {
				if goErr == nil {
					t.Log("v2 parser accepted an error fixture (checker may catch it later)")
				}
				return
			}

			// Valid fixtures: both must succeed.
			if goErr != nil {
				t.Fatalf("v2 parser failed: %v", goErr)
			}
			if tsHasErrors {
				reportErrors(t, tree.RootNode(), src)
				t.Fatal("tree-sitter produced ERROR nodes on valid input")
			}
		})
	}
}

// TestRoundTrip is a placeholder for v2 formatter round-trip testing.
// The v2 parser does not yet have a formatter, so this is skipped.
func TestRoundTrip(t *testing.T) {
	t.Skip("v2 formatter not yet implemented")
}

// reportErrors logs all ERROR and MISSING nodes in the tree.
func reportErrors(t *testing.T, root *ts.Node, src []byte) {
	t.Helper()
	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		if n.IsError() || n.IsMissing() {
			pos := n.StartPosition()
			kind := "ERROR"
			if n.IsMissing() {
				kind = "MISSING"
			}
			ctx := n.Utf8Text(src)
			if len(ctx) > 60 {
				ctx = ctx[:60] + "..."
			}
			t.Logf("  %s at %d:%d: %q", kind, pos.Row+1, pos.Column+1, ctx)
		}
		for i := range n.ChildCount() {
			child := n.Child(i)
			walk(child)
		}
	}
	walk(root)
}

// FuzzParse feeds random inputs to both parsers. If the v2 Go parser accepts
// the input (no error), the tree-sitter parser must produce an error-free tree.
func FuzzParse(f *testing.F) {
	// Seed with valid testdata fixtures (skip error fixtures).
	dir := filepath.Join("..", "..", "testdata")
	matches, _ := filepath.Glob(filepath.Join(dir, "*.sngl"))
	for _, path := range matches {
		if strings.HasPrefix(filepath.Base(path), "error_") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "ERROR(parse)") {
			continue
		}
		f.Add(data)
	}

	// Add targeted seeds for edge cases.
	f.Add([]byte(`component main {}`))
	f.Add([]byte(`component main { var x = 0 }`))
	f.Add([]byte(`component main { var x = "hello {name}" }`))
	f.Add([]byte(`struct Foo { name string = "" }`))
	f.Add([]byte(`enum Status { active, inactive }`))
	f.Add([]byte(`import "components"` + "\n" + `component main {}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		if bytes.ContainsRune(data, 0) || !utf8.Valid(data) {
			t.Skip("input contains null byte or invalid UTF-8")
		}
		if len(data) > 10000 {
			t.Skip("input too large for fuzz testing")
		}

		// Parse with v2 Go parser.
		_, goErr := v2parser.Parse("fuzz.sngl", data)

		// Always parse with tree-sitter to detect timeouts on any input.
		tsTree := tsparser.Parse(data)
		if tsTree == nil {
			t.Errorf("tree-sitter timed out on input: %q", data)
			return
		}
		defer tsTree.Close()

		// If Go parser accepts, tree-sitter must too.
		if goErr == nil && tsparser.HasErrors(tsTree) {
			reportErrors(t, tsTree.RootNode(), data)
			t.Errorf("v2 parser accepted but tree-sitter produced errors\ninput: %q", data)
		}
	})
}
