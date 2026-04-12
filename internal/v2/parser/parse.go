package parser

import (
	"errors"
	"fmt"

	"git.duckfam.us/jonathan/sngl/internal/v2/ast"
)

// Parse parses SNGL v2 source into an AST Document.
func Parse(filename string, src []byte) (*ast.Document, error) {
	tokens, lexErrs := Tokenize(string(src))
	var errs []error
	for _, e := range lexErrs {
		errs = append(errs, fmt.Errorf("%s: %s", filename, e))
	}

	stream, filtered, comments := encode(tokens)

	p := &Parser{}
	tree, parseErr := p.Parse(filename, stream)
	if parseErr != nil {
		errs = append(errs, parseErr)
	}
	if tree == nil {
		return &ast.Document{}, errors.Join(errs...)
	}

	b := newBuilder(filtered, comments)
	doc := b.buildDocument(body(tree))
	return doc, errors.Join(errs...)
}
