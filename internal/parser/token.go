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
	IDENT       TokenType = 0x03
	INT         TokenType = 0x04 // decimal only in v2 (no 0x/0o/0b)
	FLOAT       TokenType = 0x05
	STR_FULL    TokenType = 0x06 // "text" — complete string, no interpolation
	TRIPLE_FULL TokenType = 0x07 // """text""" — complete, no interpolation
	RAW_STRING  TokenType = 0x08 // `...`
	// 0x09 retired (was COLOR): #-prefixed tokens are unified as HASH below.
	UNIT_LITERAL TokenType = 0x0A // 5px, 1.5em, 100ms …
	// HASH is `#` followed by identifier/digit chars. It is resolved by
	// position downstream: a color literal in value position (#rrggbb /
	// #rrggbbaa, validated by the checker) or an element reference in a
	// node-naming tag, selection, or context-decl position.
	HASH TokenType = 0x0B // #rrggbb, #rrggbbaa, #identifier

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
	// Declares a named slot in a component's parameter list, and populates one
	// from a callsite body. A keyword rather than a built-in node because it is
	// a declaration form like func/component/struct, not a rendered node; the
	// anonymous slot's bare `slot` insertion is the one render-flavoured use.
	KW_SLOT     TokenType = 0x3E
	KW_BREAK    TokenType = 0x3F // reserved
	KW_RETURN   TokenType = 0x40
	KW_CONTINUE TokenType = 0x41 // reserved
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

	// Translatable string tokens ($"..." / $"""...""")
	I18N_STR_FULL     TokenType = 0x50 // $"text" — complete, no interpolation
	I18N_STR_START    TokenType = 0x51 // $"text{ — opening segment
	I18N_STR_END      TokenType = 0x52 // }text" — closing segment of $"..."
	I18N_STR_RESUME   TokenType = 0x53 // }text{ — middle segment of $"..."
	I18N_TRIPLE_FULL  TokenType = 0x54 // $"""text""" — complete, no interpolation
	I18N_TRIPLE_START TokenType = 0x55 // $"""text{ — opening segment
	I18N_TRIPLE_END   TokenType = 0x56 // }text""" — closing segment of $"""..."""

	// Case-body boundary tokens (inside MsgCase bodies of plural/select/selectordinal).
	// Emitted when scanning a {<selector>{<body>}} construct inside an i18n placeholder.
	I18N_CASE_FULL  TokenType = 0x57 // {literal text} — case body with no nested placeholders
	I18N_CASE_START TokenType = 0x58 // {literal text { — opening segment of body with placeholders
	I18N_CASE_END   TokenType = 0x59 // }literal text} — closing segment of case body

	// Macro attribute
	ATTR_OPEN TokenType = 0x5A // #[ — start of macro attribute

	// NATIVE_VALUE cannot be lexed from ordinary source: TokenizeNativeValue
	// prepends it, and it is what the grammar's native-value Document
	// alternative predicts on.
	NATIVE_VALUE TokenType = 0x5B // leading sentinel of a native-value parse
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
	"slot":      KW_SLOT,
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
	case IDENT, INT, FLOAT, STR_FULL, TRIPLE_FULL, RAW_STRING, UNIT_LITERAL, HASH,
		STR_END, TRIPLE_END, I18N_STR_FULL, I18N_TRIPLE_FULL, I18N_STR_END, I18N_TRIPLE_END,
		// KW_RETURN and KW_SLOT are keywords whose tail is optional, so each can
		// be a whole statement. Without ASI a bare `slot` on its own line takes
		// the next statement's identifier as its name.
		KW_RETURN, KW_SLOT,
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
