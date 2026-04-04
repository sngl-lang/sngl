package parser

import "maps"

// TokenType identifies a lexical token.
type TokenType int

const (
	// Specials
	ILLEGAL TokenType = iota
	EOF
	SEMICOLON // ; (explicit or inserted)

	// Literals
	IDENT        // identifier
	INT          // integer literal
	FLOAT        // float literal
	STRING       // "string literal"
	COLOR        // #rrggbb
	UNIT_LITERAL // 5s, 100ms, 12px, 1.5em, etc.
	ELEMENT_REF  // #identifier

	// Punctuation
	LPAREN    // (
	RPAREN    // )
	LBRACE    // {
	RBRACE    // }
	LBRACKET  // [
	RBRACKET  // ]
	COMMA     // ,
	DOT       // .
	COLON     // :
	ASSIGN    // =
	AT        // @
	PIPE  // |
	ARROW    // ->
	ELLIPSIS // ...

	// Operators
	PLUS     // +
	MINUS    // -
	STAR     // *
	SLASH    // /
	PERCENT  // %
	BANG     // !
	BANGBANG // !!
	QUESTION // ?
	EQ       // ==
	NEQ      // !=
	LT       // <
	GT       // >
	LTE      // <=
	GTE      // >=
	AND      // &&
	OR       // ||

	// Compound assignment
	PLUS_ASSIGN    // +=
	MINUS_ASSIGN   // -=
	STAR_ASSIGN    // *=
	SLASH_ASSIGN   // /=
	PERCENT_ASSIGN // %=

	// Keywords
	KW_IMPORT
	KW_OUTPUT
	KW_STRUCT
	KW_ENUM
	KW_CONST
	KW_VAR
	KW_COMPUTED // deprecated, kept for backward compat token constant
	KW_STYLE
	KW_COMPONENT
	KW_PARAM
	KW_PROP
	KW_EVENT
	KW_CHILDREN
	KW_IF
	KW_FOR
	KW_ELSE
	KW_IN
	KW_EXTERN
	KW_TRIGGER
	KW_FUNC
	KW_UNIT
	KW_TIMER
	KW_TEST
	KW_RETURN
	KW_PLATFORM
	KW_TRUE
	KW_FALSE
	KW_NULL

	// Special
	SLASHDASH     // /-
	LINE_COMMENT  // // ...
	BLOCK_COMMENT // /* ... */
)

var keywords = map[string]TokenType{
	"import":    KW_IMPORT,
	"output":    KW_OUTPUT,
	"struct":    KW_STRUCT,
	"enum":      KW_ENUM,
	"const":     KW_CONST,
	"var":       KW_VAR,
	"style":     KW_STYLE,
	"component": KW_COMPONENT,
	"if":        KW_IF,
	"for":       KW_FOR,
	"else":      KW_ELSE,
	"extern":    KW_EXTERN,
	"func":      KW_FUNC,
	"unit":      KW_UNIT,
	"timer":     KW_TIMER,
	"test":      KW_TEST,
	"return":    KW_RETURN,
	"platform":  KW_PLATFORM,
	// "in", "trigger", "true", "false", "null" are no longer keywords
}

// Keywords returns a copy of the keyword map.
func Keywords() map[string]TokenType {
	m := make(map[string]TokenType, len(keywords))
	maps.Copy(m, keywords)
	return m
}

// IsKeyword returns true if t is a keyword token.
func (t TokenType) IsKeyword() bool {
	return t >= KW_IMPORT && t <= KW_NULL
}

// LookupIdent returns the keyword token type for ident if it's a keyword,
// or IDENT otherwise.
func LookupIdent(ident string) TokenType {
	if tok, ok := keywords[ident]; ok {
		return tok
	}
	return IDENT
}

// Token is a lexical token with position information.
type Token struct {
	Type    TokenType
	Literal string
	Line    int
	Column  int
}

// insertsSemicolon reports whether a token at end-of-line triggers semicolon insertion.
func insertsSemicolon(t TokenType) bool {
	switch t {
	case IDENT, INT, FLOAT, STRING, COLOR, UNIT_LITERAL, ELEMENT_REF,
		AT, KW_EXTERN, KW_RETURN,
		RPAREN, RBRACKET, RBRACE:
		return true
	}
	return false
}

var tokenNames = map[TokenType]string{
	ILLEGAL: "ILLEGAL", EOF: "EOF", SEMICOLON: "SEMICOLON",
	IDENT: "IDENT", INT: "INT", FLOAT: "FLOAT", STRING: "STRING",
	COLOR: "COLOR", UNIT_LITERAL: "UNIT_LITERAL", ELEMENT_REF: "ELEMENT_REF",
	LPAREN: "LPAREN", RPAREN: "RPAREN", LBRACE: "LBRACE", RBRACE: "RBRACE",
	LBRACKET: "LBRACKET", RBRACKET: "RBRACKET",
	COMMA: "COMMA", DOT: "DOT", COLON: "COLON", ASSIGN: "ASSIGN",
	AT: "AT", PIPE: "PIPE", ARROW: "ARROW", ELLIPSIS: "ELLIPSIS",
	PLUS: "PLUS", MINUS: "MINUS", STAR: "STAR", SLASH: "SLASH", PERCENT: "PERCENT",
	BANG: "BANG", BANGBANG: "BANGBANG", QUESTION: "QUESTION",
	EQ: "EQ", NEQ: "NEQ", LT: "LT", GT: "GT", LTE: "LTE", GTE: "GTE",
	AND: "AND", OR: "OR",
	PLUS_ASSIGN: "PLUS_ASSIGN", MINUS_ASSIGN: "MINUS_ASSIGN",
	STAR_ASSIGN: "STAR_ASSIGN", SLASH_ASSIGN: "SLASH_ASSIGN",
	PERCENT_ASSIGN: "PERCENT_ASSIGN",
	KW_TIMER:       "KW_TIMER",
	KW_TEST:        "KW_TEST",
	KW_RETURN:      "KW_RETURN",
}
