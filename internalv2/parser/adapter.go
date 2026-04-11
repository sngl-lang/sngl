package parser

// encode converts a token slice into the byte stream consumed by the egg-generated parser.
//
// Encoding: each token is represented as two bytes — [sentinel, streamSep] — where sentinel
// is byte(tok.Type) and streamSep (0x20, space) is the white_space egg skips between tokens.
// Comments (LINE_COMMENT, BLOCK_COMMENT) and ILLEGAL tokens are stripped; their original
// positions are returned separately in comments for use by the AST builder.
//
// The returned filtered slice is parallel to the egg token stream: filtered[n] is the Token
// whose sentinel byte is at stream offset n*2. When walking the egg []int32 parse tree,
// a terminal leaf value n indexes filtered[n].
func encode(tokens []Token) (stream []byte, filtered []Token, comments []Token) {
	stream = make([]byte, 0, len(tokens)*2)
	filtered = make([]Token, 0, len(tokens))

	for _, tok := range tokens {
		switch tok.Type {
		case EOF:
			// Don't emit EOF — egg detects it from stream exhaustion.
		case ILLEGAL:
			// Skip; caller should check for lexer errors separately.
		case LINE_COMMENT, BLOCK_COMMENT:
			comments = append(comments, tok)
		default:
			stream = append(stream, byte(tok.Type), streamSep)
			filtered = append(filtered, tok)
		}
	}
	return stream, filtered, comments
}

// tokenAt returns the original Token for terminal leaf index n in the egg []int32 parse tree.
// n is the token-consumption index assigned by the egg RecScanner (0-based, whitespace skipped).
func tokenAt(filtered []Token, n int32) Token {
	if int(n) < 0 || int(n) >= len(filtered) {
		return Token{Type: ILLEGAL, Literal: "<out of range>"}
	}
	return filtered[n]
}
