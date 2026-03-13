package compiler

import (
	"go/parser"
	"go/token"
	"os"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/checker"
	snglparser "git.duckfam.us/jonathan/sngl/parser"
)

func parseAndCheck(t *testing.T, path string) *ast.Document {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	doc, err := snglparser.Parse(path, f)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if err := checker.Check(doc); err != nil {
		t.Fatalf("check %s: %v", path, err)
	}
	return doc
}

func compileAndVerify(t *testing.T, doc *ast.Document) []byte {
	t.Helper()
	src, err := Compile(doc, Config{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Verify the generated code parses as valid Go
	fset := token.NewFileSet()
	_, err = parser.ParseFile(fset, "generated.go", src, parser.AllErrors)
	if err != nil {
		t.Fatalf("generated code is not valid Go:\n%s\nerror: %v", src, err)
	}
	return src
}

func TestCompileValidMinimal(t *testing.T) {
	doc := parseAndCheck(t, "../checker/testdata/valid_minimal.kdl")
	compileAndVerify(t, doc)
}

func TestCompileValidBinds(t *testing.T) {
	doc := parseAndCheck(t, "../checker/testdata/valid_binds.kdl")
	compileAndVerify(t, doc)
}

func TestCompileValidComponent(t *testing.T) {
	doc := parseAndCheck(t, "../checker/testdata/valid_component.kdl")
	compileAndVerify(t, doc)
}

func TestCompileValidFull(t *testing.T) {
	doc := parseAndCheck(t, "../checker/testdata/valid_full.kdl")
	compileAndVerify(t, doc)
}

func TestCompileTodo(t *testing.T) {
	doc := parseAndCheck(t, "../_examples/todo/todo.sngl")
	compileAndVerify(t, doc)
}
