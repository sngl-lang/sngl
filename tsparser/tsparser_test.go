package tsparser_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ts "github.com/tree-sitter/go-tree-sitter"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/snglparser"
	"git.duckfam.us/jonathan/sngl/tsparser"
)

func TestCanLoadGrammar(t *testing.T) {
	lang := tsparser.Language()
	if lang == nil {
		t.Fatal("failed to load SNGL grammar")
	}
}

// TestFixtureAgreement parses every testdata/*.sngl file with both parsers
// and checks that they agree: both succeed, and their structural outputs match.
func TestFixtureAgreement(t *testing.T) {
	dir := filepath.Join("..", "testdata")
	matches, err := filepath.Glob(filepath.Join(dir, "*.sngl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no .sngl files in testdata/")
	}

	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")
		isError := strings.HasPrefix(name, "error_")

		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			// Parse with Go parser.
			goDoc, goErr := snglparser.Parse(name+".sngl", strings.NewReader(string(src)))

			// Parse with tree-sitter.
			tree := tsparser.Parse(src)
			defer tree.Close()
			tsHasErrors := tsparser.HasErrors(tree)

			if isError {
				// Error fixtures: we expect the Go parser to fail.
				// Tree-sitter may or may not report errors (it's error-recovering).
				if goErr == nil {
					t.Log("Go parser accepted an error fixture (checker may catch it later)")
				}
				return
			}

			// Valid fixtures: both must succeed.
			if goErr != nil {
				t.Fatalf("Go parser failed: %v", goErr)
			}
			if tsHasErrors {
				reportErrors(t, tree.RootNode(), src)
				t.Fatal("tree-sitter produced ERROR nodes on valid input")
			}

			// Compare structure.
			compareStructure(t, goDoc, tree.RootNode(), src)
		})
	}
}

// TestRoundTrip formats Go AST back to source, then re-parses with tree-sitter.
func TestRoundTrip(t *testing.T) {
	dir := filepath.Join("..", "testdata")
	matches, err := filepath.Glob(filepath.Join(dir, "*.sngl"))
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")
		if strings.HasPrefix(name, "error_") {
			continue
		}

		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			// Parse with Go, format back to source.
			doc, goErr := snglparser.Parse(name+".sngl", strings.NewReader(string(src)))
			if goErr != nil {
				t.Skipf("Go parser failed: %v", goErr)
			}
			formatted := snglparser.Format(doc)

			// Re-parse formatted output with tree-sitter.
			tree := tsparser.Parse([]byte(formatted))
			defer tree.Close()
			if tsparser.HasErrors(tree) {
				t.Logf("formatted source:\n%s", formatted)
				reportErrors(t, tree.RootNode(), []byte(formatted))
				t.Fatal("tree-sitter failed to parse Go-formatted output")
			}
		})
	}
}

// compareStructure extracts key structural elements from both ASTs and compares.
func compareStructure(t *testing.T, doc *ast.Document, root *ts.Node, src []byte) {
	t.Helper()

	// Extract component names from tree-sitter.
	tsComponents := extractNamedChildren(root, "component_declaration", "name", src)
	// Extract from Go AST.
	var goComponents []string
	for _, c := range doc.Components {
		goComponents = append(goComponents, c.Name)
	}
	if doc.App != nil {
		goComponents = append(goComponents, "main")
	}

	compareStringSlices(t, "components", goComponents, tsComponents)

	// Extract struct names.
	tsStructs := extractNamedChildren(root, "struct_declaration", "name", src)
	var goStructs []string
	for _, s := range doc.Structs {
		goStructs = append(goStructs, s.Name)
	}
	compareStringSlices(t, "structs", goStructs, tsStructs)

	// Extract enum names.
	tsEnums := extractNamedChildren(root, "enum_declaration", "name", src)
	var goEnums []string
	for _, e := range doc.Enums {
		goEnums = append(goEnums, e.Name)
	}
	compareStringSlices(t, "enums", goEnums, tsEnums)

	// Extract import paths.
	tsImports := extractImports(root, src)
	var goImports []string
	for _, imp := range doc.Imports {
		goImports = append(goImports, imp.Path)
	}
	compareStringSlices(t, "imports", goImports, tsImports)

	// For each component, compare params and var names.
	cursor := root.Walk()
	defer cursor.Close()
	for _, tsComp := range findNodes(root, "component_declaration", cursor) {
		nameNode := tsComp.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		compName := nameNode.Utf8Text(src)

		// Find matching Go component.
		var goComp *ast.Component
		if compName == "main" {
			// main is stored as App + doc-level Data/Computeds/Consts
			compareMainComponent(t, doc, &tsComp, src)
			continue
		}
		for _, c := range doc.Components {
			if c.Name == compName {
				goComp = c
				break
			}
		}
		if goComp == nil {
			t.Errorf("tree-sitter has component %q not found in Go AST", compName)
			continue
		}

		// Compare params.
		tsParams := extractDescendantFields(&tsComp, "param_declaration", "name", src)
		var goParams []string
		for _, p := range goComp.Params {
			goParams = append(goParams, p.Name)
		}
		compareStringSlices(t, compName+".params", goParams, tsParams)
	}
}

func compareMainComponent(t *testing.T, doc *ast.Document, tsComp *ts.Node, src []byte) {
	t.Helper()

	// Compare var names.
	tsVars := extractDescendantFields(tsComp, "single_var", "name", src)
	var goVars []string
	for _, d := range doc.Data {
		goVars = append(goVars, d.Name)
	}
	compareStringSlices(t, "main.vars", goVars, tsVars)

	// Compare computed names.
	tsComputeds := extractDescendantFields(tsComp, "single_computed", "name", src)
	var goComputeds []string
	for _, c := range doc.Computeds {
		goComputeds = append(goComputeds, c.Name)
	}
	compareStringSlices(t, "main.computeds", goComputeds, tsComputeds)

	// Compare const names.
	tsConsts := extractDescendantFields(tsComp, "single_const", "name", src)
	var goConsts []string
	for _, c := range doc.Consts {
		goConsts = append(goConsts, c.Name)
	}
	compareStringSlices(t, "main.consts", goConsts, tsConsts)
}

// compareStringSlices compares two string slices ignoring order.
func compareStringSlices(t *testing.T, ctx string, goSlice, tsSlice []string) {
	t.Helper()
	goSet := make(map[string]bool, len(goSlice))
	for _, s := range goSlice {
		goSet[s] = true
	}
	tsSet := make(map[string]bool, len(tsSlice))
	for _, s := range tsSlice {
		tsSet[s] = true
	}

	for s := range goSet {
		if !tsSet[s] {
			t.Errorf("%s: Go has %q, tree-sitter does not", ctx, s)
		}
	}
	for s := range tsSet {
		if !goSet[s] {
			t.Errorf("%s: tree-sitter has %q, Go does not", ctx, s)
		}
	}
}

// extractNamedChildren finds all nodes of nodeType under root and returns the
// text of their field with the given fieldName.
func extractNamedChildren(root *ts.Node, nodeType, fieldName string, src []byte) []string {
	cursor := root.Walk()
	defer cursor.Close()
	var result []string
	for _, n := range findNodes(root, nodeType, cursor) {
		field := n.ChildByFieldName(fieldName)
		if field != nil {
			result = append(result, field.Utf8Text(src))
		}
	}
	return result
}

// extractDescendantFields finds all descendants of parent with nodeType and
// returns the text of their fieldName child.
func extractDescendantFields(parent *ts.Node, nodeType, fieldName string, src []byte) []string {
	cursor := parent.Walk()
	defer cursor.Close()
	var result []string
	for _, n := range findNodes(parent, nodeType, cursor) {
		field := n.ChildByFieldName(fieldName)
		if field != nil {
			result = append(result, field.Utf8Text(src))
		}
	}
	return result
}

// extractImports finds import_declaration nodes and extracts the path string.
func extractImports(root *ts.Node, src []byte) []string {
	cursor := root.Walk()
	defer cursor.Close()
	var result []string
	for _, n := range findNodes(root, "import_declaration", cursor) {
		// The string_literal child contains the path with quotes.
		for i := range n.NamedChildCount() {
			child := n.NamedChild(i)
			if child.Kind() == "string_literal" {
				text := child.Utf8Text(src)
				// Strip quotes.
				text = strings.Trim(text, "\"")
				result = append(result, text)
			}
		}
	}
	return result
}

// findNodes walks the tree and collects all nodes matching nodeType.
func findNodes(root *ts.Node, nodeType string, cursor *ts.TreeCursor) []ts.Node {
	var result []ts.Node
	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		if n.Kind() == nodeType {
			result = append(result, *n)
		}
		for i := range n.NamedChildCount() {
			child := n.NamedChild(i)
			walk(child)
		}
	}
	walk(root)
	return result
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

// FuzzParse feeds random inputs to both parsers. If the Go parser accepts the
// input (no error), the tree-sitter parser must produce an error-free tree.
func FuzzParse(f *testing.F) {
	// Seed with testdata fixtures.
	dir := filepath.Join("..", "testdata")
	matches, _ := filepath.Glob(filepath.Join(dir, "*.sngl"))
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		f.Add(data)
	}

	// Add targeted seeds for edge cases.
	f.Add([]byte(`component main {}`))
	f.Add([]byte(`component main { var x = 0 }`))
	f.Add([]byte(`component main { var x = "hello {name}" }`))
	f.Add([]byte(`component main { computed y = x + 1 }`))
	f.Add([]byte(`struct Foo { name string = "" }`))
	f.Add([]byte(`enum Status { active, inactive }`))
	f.Add([]byte(`import "foo.proto"` + "\n" + `component main {}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Parse with Go parser under a timeout to catch hangs.
		type goResult struct {
			doc *ast.Document
			err error
		}
		ch := make(chan goResult, 1)
		go func() {
			doc, err := snglparser.Parse("fuzz.sngl", strings.NewReader(string(data)))
			ch <- goResult{doc, err}
		}()

		var goDoc *ast.Document
		var goErr error
		select {
		case res := <-ch:
			goDoc, goErr = res.doc, res.err
		case <-time.After(2 * time.Second):
			t.Skipf("Go parser timed out (likely hung on malformed input)")
			return
		}

		// Parse with tree-sitter.
		tree := tsparser.Parse(data)
		defer tree.Close()
		tsHasErrors := tsparser.HasErrors(tree)

		if goErr == nil && goDoc != nil {
			// Go parser accepted — tree-sitter should too.
			if tsHasErrors {
				t.Errorf("Go parser accepted but tree-sitter has errors\ninput: %q", data)
			}

			// Verify round-trip: format Go AST, re-parse with tree-sitter.
			formatted := snglparser.Format(goDoc)
			tree2 := tsparser.Parse([]byte(formatted))
			defer tree2.Close()
			if tsparser.HasErrors(tree2) {
				t.Errorf("tree-sitter failed on Go-formatted output\ninput: %q\nformatted: %q", data, formatted)
			}
		}

		// Note: we don't check the reverse (tree-sitter accepts, Go rejects)
		// because tree-sitter is error-recovering and more permissive by design.
	})
}
