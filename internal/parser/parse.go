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
	stream, filtered, comments, lexErrs := scanEncoded(newLexer(string(src)), nil)
	doc, _, err := parseTokens(filename, stream, filtered, comments, lexErrs)
	return doc, err
}

// ParseNativeValue parses src as one encoded native value: a single expression
// written by a native-language encoder (see pkg/go/consteval), in which a
// value may name its own type as import("scheme://path").Name.
//
// That spelling exists only here. Parse rejects it — so a hand-written program
// cannot claim a declaration the compiler would then trust.
func ParseNativeValue(filename string, src []byte) (ast.Expr, error) {
	seed := []Token{{Type: NATIVE_VALUE, Line: 1, Column: 1}}
	stream, filtered, comments, lexErrs := scanEncoded(newLexer(string(src)), seed)
	_, native, err := parseTokens(filename, stream, filtered, comments, lexErrs)
	if err != nil {
		return nil, err
	}
	if native == nil {
		return nil, fmt.Errorf("%s: no value", filename)
	}
	return native, nil
}

func parseTokens(filename string, stream []byte, filtered, comments []Token, lexErrs []string) (doc *ast.Document, native ast.Expr, err error) {
	var errs []error
	for _, e := range lexErrs {
		errs = append(errs, fmt.Errorf("%s:%s", filename, e))
	}

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
// inInterpolation reports whether the token at idx sits inside a `{...}` hole
// of an interpolated string: the nearest preceding string segment token opened
// a hole rather than closing one.
func inInterpolation(filtered []Token, idx int) bool {
	for i := idx - 1; i >= 0; i-- {
		switch filtered[i].Type {
		case STR_START, STR_RESUME, I18N_STR_START, I18N_STR_RESUME:
			return true
		case STR_END, I18N_STR_END:
			return false
		}
	}
	return false
}

// loopVarNeedsVar reports whether the token that failed to parse is the `=` of
// a loop head whose variables were written without `var` -- `for x = xs`, the
// spelling that was valid before the declaration became explicit. The generic
// message for it names the token the grammar wanted (`{`) rather than the
// keyword that is missing, which is no help to anyone migrating.
func loopVarNeedsVar(filtered []Token, idx int) bool {
	if idx < 0 || idx >= len(filtered) || filtered[idx].Type != ASSIGN {
		return false
	}
	// Back over exactly what a loop head can hold left of the `=`: one or two
	// names, each optionally &-bound.
	for i := idx - 1; i >= 0; i-- {
		switch filtered[i].Type {
		case IDENT, COMMA, AMP:
			continue
		case KW_FOR:
			return true
		default:
			return false
		}
	}
	return false
}

func remapErrors(err error, filtered []Token) error {
	errList, ok := err.(scanner.ErrList)
	if !ok {
		return err
	}
	for i := range errList {
		// Each token is 2 bytes in the stream ([sentinel, 0x20]), so the
		// token index is the byte offset halved.
		//
		// Offset and not Column: a token's sentinel is its TokenType's byte
		// value, and UNIT_LITERAL's is 0x0A. So a file holding a `20px`
		// writes a newline into the stream, the scanner starts counting
		// columns again after it, and every error past that point remapped to
		// a token from an earlier line -- a brace inside a string reported at
		// a line 28 above the one it was written on.
		idx := errList[i].Pos.Offset / 2
		if idx >= 0 && idx < len(filtered) {
			tok := filtered[idx]
			errList[i].Pos.Line = tok.Line
			errList[i].Pos.Column = tok.Column
			errList[i].Pos.Offset = 0
		}
		if errList[i].Err != nil {
			// A hole in an interpolated string holds an expression, so the
			// generic "expected one of <every expression token>" names forty
			// alternatives and tells the reader nothing. What went wrong is
			// that this is not an expression.
			if inInterpolation(filtered, idx) {
				errList[i].Err = errors.New("invalid expression in interpolation")
				continue
			}
			if loopVarNeedsVar(filtered, idx) {
				errList[i].Err = errors.New("a loop variable is declared with var: write `for var x = xs`")
				continue
			}
			errList[i].Err = errors.New(prettifyParseError(errList[i].Err.Error()))
		}
	}
	return errList
}
