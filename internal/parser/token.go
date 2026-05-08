package parser

// TokenType identifies a lexical token.
// The underlying byte value is the sentinel used in the egg parser's token stream.
// Byte 0x20 (space) is reserved as the stream separator and must not be a token value.
type TokenType byte

const (
	streamSep byte = 0x20 // emitted between every token in the egg stream (white_space)
)

const (
	EOF TokenType = 0x00 // end of input

	// Specials
	ILLEGAL   TokenType = 0x01
	SEMICOLON TokenType = 0x02 // ; (explicit or ASI-inserted)

	// Literals
	IDENT        TokenType = 0x03
	INT          TokenType = 0x04 // decimal only in v2 (no 0x/0o/0b)
	FLOAT        TokenType = 0x05
	STR_FULL     TokenType = 0x06 // "text" — complete string, no interpolation
	TRIPLE_FULL  TokenType = 0x07 // """text""" — complete, no interpolation
	RAW_STRING   TokenType = 0x08 // `...`
	COLOR        TokenType = 0x09 // #rrggbb or #rrggbbaa
	UNIT_LITERAL TokenType = 0x0A // 5px, 1.5em, 100ms …
	ELEMENT_REF  TokenType = 0x0B // #identifier

	// Punctuation
	LPAREN    TokenType = 0x0C // (
	RPAREN    TokenType = 0x0D // )
	LBRACE    TokenType = 0x0E // {
	RBRACE    TokenType = 0x0F // }
	LBRACKET  TokenType = 0x10 // [
	RBRACKET  TokenType = 0x11 // ]
	COMMA     TokenType = 0x12 // ,
	DOT       TokenType = 0x13 // .
	COLON     TokenType = 0x14 // :
	ASSIGN    TokenType = 0x15 // =
	AT        TokenType = 0x16 // @
	PIPE      TokenType = 0x17 // |
	ARROW     TokenType = 0x18 // ->
	FAT_ARROW TokenType = 0x19 // =>
	ELLIPSIS  TokenType = 0x1A // ...

	// Operators
	PLUS    TokenType = 0x1B // +
	MINUS   TokenType = 0x1C // -
	STAR    TokenType = 0x1D // *
	SLASH   TokenType = 0x1E // /
	PERCENT TokenType = 0x1F // %
	// 0x20 reserved: stream separator
	BANG     TokenType = 0x21 // !
	BANGBANG TokenType = 0x22 // !!
	QUESTION TokenType = 0x23 // ?
	EQ       TokenType = 0x24 // ==
	NEQ      TokenType = 0x25 // !=
	LT       TokenType = 0x26 // <
	GT       TokenType = 0x27 // >
	LTE      TokenType = 0x28 // <=
	GTE      TokenType = 0x29 // >=
	AND      TokenType = 0x2A // &&
	OR       TokenType = 0x2B // ||
	AMP      TokenType = 0x4E // & (single ampersand — address-of)

	// Compound assignment
	PLUS_ASSIGN    TokenType = 0x2C // +=
	MINUS_ASSIGN   TokenType = 0x2D // -=
	STAR_ASSIGN    TokenType = 0x2E // *=
	SLASH_ASSIGN   TokenType = 0x2F // /=
	PERCENT_ASSIGN TokenType = 0x30 // %=

	// Postfix inc/dec (statement-level only)
	PLUS_PLUS   TokenType = 0x32 // ++
	MINUS_MINUS TokenType = 0x37 // --

	// Keywords
	KW_IMPORT    TokenType = 0x31
	KW_STRUCT    TokenType = 0x33
	KW_ENUM      TokenType = 0x34
	KW_CONST     TokenType = 0x35
	KW_VAR       TokenType = 0x36
	KW_COMPONENT TokenType = 0x38
	KW_IF        TokenType = 0x39
	KW_FOR       TokenType = 0x3A
	KW_ELSE      TokenType = 0x3B
	KW_FUNC      TokenType = 0x3C
	KW_UNIT      TokenType = 0x3D
	// 0x3E freed: timer is now a pre-declared identifier
	KW_BREAK    TokenType = 0x3F // reserved
	KW_RETURN   TokenType = 0x40
	KW_CONTINUE TokenType = 0x41 // reserved
	KW_PLATFORM TokenType = 0x42
	// 0x43-0x45 freed: true, false, null are now pre-declared identifiers

	// Special
	SLASHDASH     TokenType = 0x46 // /-
	LINE_COMMENT  TokenType = 0x47 // // …
	BLOCK_COMMENT TokenType = 0x48 // /* … */

	// String interpolation boundary tokens
	STR_START    TokenType = 0x49 // "text{  — opening segment
	STR_END      TokenType = 0x4A // }text" — closing segment
	STR_RESUME   TokenType = 0x4B // }text{ — middle segment (both " and """)
	TRIPLE_START TokenType = 0x4C // """text{ — opening segment
	TRIPLE_END   TokenType = 0x4D // }text""" — closing segment
)

var keywords = map[string]TokenType{
	"import": KW_IMPORT,
	"struct": KW_STRUCT,
	"enum":   KW_ENUM,
	"const":  KW_CONST,
	"var":    KW_VAR,
	// style is now a pre-declared identifier, not a keyword
	"component": KW_COMPONENT,
	"if":        KW_IF,
	"for":       KW_FOR,
	"else":      KW_ELSE,
	"func":      KW_FUNC,
	"unit":      KW_UNIT,
	"break":     KW_BREAK,
	"return":    KW_RETURN,
	"continue":  KW_CONTINUE,
	"platform":  KW_PLATFORM,
	// output, timer, window, true, false, null are pre-declared identifiers, not keywords
}

// LookupIdent returns the keyword TokenType for s, or IDENT if not a keyword.
func LookupIdent(s string) TokenType {
	if t, ok := keywords[s]; ok {
		return t
	}
	return IDENT
}

// Token is a lexical token with source location.
type Token struct {
	Type    TokenType
	Literal string
	Line    int
	Column  int
}

// insertsSemicolon reports whether a token at end-of-line triggers ASI.
func insertsSemicolon(t TokenType) bool {
	switch t {
	case IDENT, INT, FLOAT, STR_FULL, TRIPLE_FULL, RAW_STRING, COLOR, UNIT_LITERAL, ELEMENT_REF,
		STR_END, TRIPLE_END,
		KW_RETURN,
		AT, RPAREN, RBRACKET, RBRACE, BANGBANG, PLUS_PLUS, MINUS_MINUS:
		return true
	}
	return false
}

func isDigit(ch rune) bool      { return ch >= '0' && ch <= '9' }
func isLetter(ch rune) bool     { return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') }
func isIdentStart(ch rune) bool { return isLetter(ch) || ch == '_' }
func isIdentCont(ch rune) bool  { return isLetter(ch) || isDigit(ch) || ch == '_' }
func isHexDigit(ch rune) bool {
	return isDigit(ch) || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func hexVal(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10
	case r >= 'A' && r <= 'F':
		return int(r-'A') + 10
	}
	return -1
}
