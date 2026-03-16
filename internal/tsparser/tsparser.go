// Package tsparser provides a Go wrapper around the tree-sitter SNGL parser.
package tsparser

//go:generate sh -c "cd ../../editors/tree-sitter-sngl && tree-sitter generate"
//go:generate sh -c "cd ../../editors/tree-sitter-sngl && cc -o sngl.so -shared -fPIC -Isrc src/parser.c src/scanner.c -Os"
//go:generate sh -c "mkdir -p $HOME/.local/share/nvim/lazy/nvim-treesitter/parser && cp ../../editors/tree-sitter-sngl/sngl.so $HOME/.local/share/nvim/lazy/nvim-treesitter/parser/sngl.so"
//go:generate sh -c "mkdir -p ../../editors/neovim/queries/sngl && cp ../../editors/tree-sitter-sngl/queries/*.scm ../../editors/neovim/queries/sngl/"

// #cgo CFLAGS: -std=c11 -fPIC -I../../editors/tree-sitter-sngl/src
// #include "../../editors/tree-sitter-sngl/src/parser.c"
// #include "../../editors/tree-sitter-sngl/src/scanner.c"
import "C"

import (
	"unsafe"

	ts "github.com/tree-sitter/go-tree-sitter"
)

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
