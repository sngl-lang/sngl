// Package sngl provides parsing, type-checking, optimization, and formatting
// for SNGL documents.
package sngl

import (
	"fmt"
	"io"
	"os"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
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
//
// Like the rest of the pipeline boundary, a panic on partial/unexpected IR is
// contained (returns nil) so an embedding host — LSP, doc tooling — degrades
// instead of crashing.
func Convert(pkg *ir.Package) (doc *ast.Document) {
	defer func() {
		if p := recover(); p != nil {
			doc = nil
		}
	}()
	return ir.Convert(pkg)
}

// Check type-checks a parsed SNGL document. dir is the directory of the source
// file, used to resolve relative import paths.
//
// The checker (and the many `panic("unhandled %T")` guards it can reach)
// panicking on malformed-but-parseable input is converted to an internal-error
// diagnostic so hosts never crash — mirroring Parse.
func Check(doc *ast.Document, dir string) (pkg *ir.Package, diags []ir.Diagnostic) {
	defer func() {
		if p := recover(); p != nil {
			pkg = nil
			diags = []ir.Diagnostic{{Severity: ir.Error, Msg: fmt.Sprintf("internal checker error: %v", p)}}
		}
	}()
	return checker.Check(doc, &checker.Config{
		FS:     os.DirFS(dir),
		Dir:    dir,
		IsMain: true,
	})
}

// Features is what a target says it can generate, as its own package declares
// it. Re-exported so callers don't need to import internal/lower directly.
//
// It was `Caps`, the inverted twin of this record, and the rename is the one
// break the two-records-into-one change makes to this file's surface: there is
// no Caps to alias any more, and aliasing Features under the old name would
// have every caller reading `NoTernary` off a field called `Ternary`.
type Features = lower.Features

// Lower runs the lowering pipeline on a checked + optimized IR Package.
// feats is what the target pair declared, which codegen.CapsFor reads off
// their packages. Mutates pkg in place.
//
// The lowering passes carry 60+ `panic("unhandled %T")` guards; a panic on an
// unexpected node is converted to an error so the CLI/LSP report an internal
// error rather than crashing.
func Lower(pkg *ir.Package, feats Features) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("internal lowering error: %v", p)
		}
	}()
	return lower.Lower(pkg, feats, lower.Options{})
}
