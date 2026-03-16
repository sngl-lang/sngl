// Package sngl provides parsing, type-checking, optimization, and formatting
// for SNGL documents.
package sngl

import (
	"io"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
)

// Parse reads SNGL source from r and returns the parsed document AST.
func Parse(filename string, r io.Reader) (*ast.Document, error) {
	return snglparser.Parse(filename, r)
}

// Format returns the formatted SNGL source for a document.
func Format(doc *ast.Document) string {
	return snglparser.Format(doc)
}

// FormatNode returns the formatted SNGL source for a single AST node.
func FormatNode(n ast.Node) string {
	return snglparser.FormatNode(n)
}

// Check type-checks a parsed SNGL document. dir is the directory of the source
// file, used to resolve relative import paths. It uses the default filesystem-based
// import resolver for directory imports.
func Check(doc *ast.Document, dir string) error {
	return checker.Check(doc, dir, checker.DefaultResolver())
}

// OptimizeConfig controls platform-specific AST transformations.
type OptimizeConfig = optimize.Config

// Optimize applies platform-specific transformations to a parsed and checked document.
func Optimize(doc *ast.Document, cfg OptimizeConfig) {
	optimize.Optimize(doc, cfg)
}
