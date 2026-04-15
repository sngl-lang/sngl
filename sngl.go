// Package sngl provides parsing, type-checking, optimization, and formatting
// for SNGL documents.
package sngl

import (
	"io"
	"os"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Parse reads SNGL source from r and returns the parsed document AST.
func Parse(filename string, r io.Reader) (*ast.Document, error) {
	src, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return parser.Parse(filename, src)
}

// Format returns the formatted SNGL source for a document.
func Format(doc *ast.Document) string {
	return parser.Format(doc)
}

// FormatTo writes the formatted SNGL source for doc to w.
func FormatTo(doc *ast.Document, w io.Writer) (int, error) {
	return parser.FormatTo(doc, w)
}

// FormatExpr returns the formatted SNGL source for a single expression.
func FormatExpr(e ast.Expr) string {
	return parser.FormatExpr(e)
}

// Check type-checks a parsed SNGL document. dir is the directory of the source
// file, used to resolve relative import paths.
func Check(doc *ast.Document, dir string) (*ir.Package, []ir.Diagnostic) {
	return checker.Check(doc, &checker.Config{
		FS:     os.DirFS(dir),
		Dir:    dir,
		IsMain: true,
	})
}
