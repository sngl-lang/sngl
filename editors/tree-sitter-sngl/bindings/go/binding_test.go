package tree_sitter_sngl_test

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_sngl "sngl.duckfam.us//bindings/go"
)

func TestCanLoadGrammar(t *testing.T) {
	language := tree_sitter.NewLanguage(tree_sitter_sngl.Language())
	if language == nil {
		t.Errorf("Error loading SNGL grammar")
	}
}
