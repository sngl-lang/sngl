package parser

// These helpers expose just enough of the egg-generated Parser state for
// the Parse wrapper in parse.go to enforce trailing-EOF after the
// Document production. Without this check, input that contains valid
// SNGL followed by tokens the grammar can't continue from (e.g.
// `var b = 5 & 3`) silently truncates to the prefix the grammar
// accepted.
//
// Living in a sibling file rather than zparser.go so `go generate`
// regenerations don't blow them away.

// AtEOF reports whether the parser's lookahead is the EOF token. Call
// after Parser.Parse returns; if false, there is unconsumed input.
func (p *Parser) AtEOF() bool {
	return p.tok.Ch == rune(TOK_EOF)
}

// TrailingTokenIndex returns the filtered-token index of the
// next-unconsumed token. Only meaningful when AtEOF returns false.
func (p *Parser) TrailingTokenIndex() int {
	return int(p.tokIndex)
}
