package parser

import (
	"errors"
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"modernc.org/scanner"
)

// Parse parses SNGL v2 source into an AST Document. Panics in the parser or
// AST builder are converted into errors so callers don't crash on malformed
// input.
func Parse(filename string, src []byte) (*ast.Document, error) {
	tokens, lexErrs := Tokenize(string(src))
	doc, _, err := parseTokens(filename, tokens, lexErrs)
	return doc, err
}

// ParseNativeValue parses src as one encoded native value: a single expression
// written by a native-language encoder (see pkg/go/consteval), in which a
// value may name its own type as import("scheme://path").Name.
//
// That spelling exists only here. Parse rejects it — so a hand-written program
// cannot claim a declaration the compiler would then trust.
func ParseNativeValue(filename string, src []byte) (ast.Expr, error) {
	tokens, lexErrs := TokenizeNativeValue(string(src))
	_, native, err := parseTokens(filename, tokens, lexErrs)
	if err != nil {
		return nil, err
	}
	if native == nil {
		return nil, fmt.Errorf("%s: no value", filename)
	}
	return native, nil
}

func parseTokens(filename string, tokens []Token, lexErrs []string) (doc *ast.Document, native ast.Expr, err error) {
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
	if !p.AtEOF() {
		idx := p.TrailingTokenIndex()
		if idx >= 0 && idx < len(filtered) {
			tok := filtered[idx]
			errs = append(errs, fmt.Errorf("%s:%d:%d: unexpected trailing input near %q", filename, tok.Line, tok.Column, tok.Literal))
		} else {
			errs = append(errs, fmt.Errorf("%s: unexpected trailing input", filename))
		}
	}
	if tree == nil {
		return &ast.Document{}, nil, errors.Join(errs...)
	}

	defer func() {
		if r := recover(); r != nil {
			doc, native = &ast.Document{}, nil
			errs = append(errs, fmt.Errorf("%s: parser panic: %v", filename, r))
			err = errors.Join(errs...)
		}
	}()

	b := newBuilder(filename, filtered, comments)
	doc = b.buildDocument(body(tree))
	for _, e := range b.errors {
		errs = append(errs, fmt.Errorf("%s", e))
	}
	return doc, b.native, errors.Join(errs...)
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
		if errList[i].Err != nil {
			errList[i].Err = errors.New(prettifyParseError(errList[i].Err.Error()))
		}
	}
	return errList
}
