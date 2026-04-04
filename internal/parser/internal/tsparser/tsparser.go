// Package tsparser provides a Go wrapper around the tree-sitter SNGL parser.
package tsparser

//go:generate go run ../../../../internal/cmd/tsgen

// #cgo CFLAGS: -std=c11 -fPIC -I../../../../editors/tree-sitter-sngl/src
// #include "../../../../editors/tree-sitter-sngl/src/parser.c"
// #include "../../../../editors/tree-sitter-sngl/src/scanner.c"
import "C"

import (
	_ "embed"
	"unsafe"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// grammarHash is embedded to invalidate the Go build cache when the generated
// C source changes. Without this, cgo compilation is cached and won't pick up
// changes to the included parser.c/scanner.c files.
//
//go:embed grammar_hash.txt
var grammarHash string

// Language returns the tree-sitter Language for SNGL.
func Language() *ts.Language {
	return ts.NewLanguage(unsafe.Pointer(C.tree_sitter_sngl()))
}

// Parse parses SNGL source code and returns a tree-sitter Tree.
// The caller must call tree.Close() when done.
func Parse(source []byte) *ts.Tree {
	parser := ts.NewParser()
	defer parser.Close()
	parser.SetLanguage(Language())
	return parser.Parse(source, nil)
}

// HasErrors returns true if any node in the tree is an ERROR or MISSING node.
func HasErrors(tree *ts.Tree) bool {
	return tree.RootNode().HasError()
}
