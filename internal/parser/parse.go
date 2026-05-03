package parser

import (
	"errors"
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"modernc.org/scanner"
)

// Parse parses SNGL v2 source into an AST Document. Panics in the parser or
// AST builder are converted into errors so callers don't crash on malformed
// input (e.g. the `->` arrow form which the lexer accepts but the grammar
// has no rule for).
func Parse(filename string, src []byte) (doc *ast.Document, err error) {
	tokens, lexErrs := Tokenize(string(src))
	var errs []error
	for _, e := range lexErrs {
		errs = append(errs, fmt.Errorf("%s: %s", filename, e))
	}

	stream, filtered, comments := encode(tokens)

	p := &Parser{}
	tree, parseErr := p.Parse(filename, stream)
	if parseErr != nil {
		errs = append(errs, remapErrors(parseErr, filtered))
	}
	if tree == nil {
		return &ast.Document{}, errors.Join(errs...)
	}

	defer func() {
		if r := recover(); r != nil {
			doc = &ast.Document{}
			errs = append(errs, fmt.Errorf("%s: parser panic: %v", filename, r))
			err = errors.Join(errs...)
		}
	}()

	b := newBuilder(filename, filtered, comments)
	doc = b.buildDocument(body(tree))
	return doc, errors.Join(errs...)
}

// remapErrors translates byte-stream positions from the egg parser back to
// source file line:column using the filtered token array.
func remapErrors(err error, filtered []Token) error {
	errList, ok := err.(scanner.ErrList)
	if !ok {
		return err
	}
	for i := range errList {
		// Each token is 2 bytes in the stream ([sentinel, 0x20]).
		// Position.Column is 1-based, so token index = (col-1)/2.
		idx := (errList[i].Pos.Column - 1) / 2
		if idx >= 0 && idx < len(filtered) {
			tok := filtered[idx]
			errList[i].Pos.Line = tok.Line
			errList[i].Pos.Column = tok.Column
			errList[i].Pos.Offset = 0
		}
	}
	return errList
}
