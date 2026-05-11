package html

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen/testharness"
)

// PromoteComponent is the html-package entry point preserved for
// backward compatibility; it delegates to testharness.Promote. New code
// should call testharness directly.
func PromoteComponent(doc *ast.Document, name string) *ast.Document {
	return testharness.Promote(doc, name)
}
