// Package sngl provides parsing, type-checking, optimization, and formatting
// for SNGL documents.
package sngl

import (
	"fmt"
	"io"
	"os"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Parse reads SNGL source from r and returns the parsed document AST.
//
// Parser panics (e.g. unhandled grammar paths) are converted to errors so
// callers — CLI, LSP, playground — never crash on malformed input.
func Parse(filename string, r io.Reader) (doc *ast.Document, err error) {
	src, rerr := io.ReadAll(r)
	if rerr != nil {
		return nil, rerr
	}
	defer func() {
		if p := recover(); p != nil {
			doc = nil
			err = fmt.Errorf("parser panic on %s: %v", filename, p)
		}
	}()
	return parser.Parse(filename, src)
}

// ExpandPre runs pre-check macro expansion on docs. Call after Parse and before
// Check. Modifies docs in place. Returns diagnostics for any expansion errors.
func ExpandPre(docs []*ast.Document) []ir.Diagnostic {
	return expand.ExpandPre(docs)
}

// ExpandPost runs post-check macro expansion on pkg. Call after Check and before
// Lower. Currently a no-op; reserved for future behavioral macros.
func ExpandPost(pkg *ir.Package) []ir.Diagnostic {
	return expand.ExpandPost(pkg)
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

// FormatType returns the formatted SNGL source for a single type expression.
func FormatType(te ast.TypeExpr) string {
	return parser.FormatType(te)
}

// Convert builds a fresh AST Document from a type-checked IR Package,
// without referencing any embedded AST pointers.
func Convert(pkg *ir.Package) *ast.Document {
	return ir.Convert(pkg)
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

// Caps is the capability struct used by Lower. Re-exported so callers don't
// need to import internal/lower directly.
type Caps = lower.Caps

// Lower runs the lowering pipeline on a checked + optimized IR Package.
// caps comes from merging the target platform's and language's
// Capabilities(). Mutates pkg in place.
func Lower(pkg *ir.Package, caps Caps) error {
	return lower.Lower(pkg, caps, lower.Options{})
}
