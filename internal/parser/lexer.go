package parser

import (
	"fmt"
	"strings"
)

// interpFrame tracks one level of string interpolation nesting.
type interpFrame struct {
	triple bool // true for triple-quoted string
	depth  int  // brace nesting within this interpolation expression
}

// lexer scans SNGL v2 source text into tokens.
// v2 changes vs v1:
//   - Non-base-10 integer literals (0x…, 0o…, 0b…) are rejected (ILLEGAL).
//   - true, false, null are keyword tokens (KW_TRUE/KW_FALSE/KW_NULL).
type lexer struct {
	input       []rune
	pos         int
	line        int
	col         int
	prevTok     TokenType
	errors      []string
	interpStack []interpFrame // active string interpolation nesting
}

func newLexer(src string) *lexer {
	return &lexer{input: []rune(src), line: 1, col: 1}
}

func (l *lexer) peek() rune {
	if l.pos >= len(l.input) {
		return 0
	}
	return l.input[l.pos]
}

func (l *lexer) peekAt(offset int) rune {
	i := l.pos + offset
	if i >= len(l.input) {
		return 0
	}
	return l.input[i]
}

func (l *lexer) advance() rune {
	if l.pos >= len(l.input) {
		return 0
	}
	ch := l.input[l.pos]
	l.pos++
	if ch == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return ch
}

func (l *lexer) tok(typ TokenType, lit string, line, col int) Token {
	l.prevTok = typ
	return Token{Type: typ, Literal: lit, Line: line, Column: col}
}

// NextToken returns the next token from the source.
func (l *lexer) NextToken() Token {
	for {
		// Skip whitespace; insert semicolons at newlines.
		for l.pos < len(l.input) {
			ch := l.input[l.pos]
			if ch == '\n' {
				if insertsSemicolon(l.prevTok) {
					semi := l.tok(SEMICOLON, ";", l.line, l.col)
					l.advance()
					return semi
				}
				l.advance()
				continue
			}
			if ch == ' ' || ch == '\t' || ch == '\r' {
				l.advance()
				continue
			}
			break
		}

		if l.pos >= len(l.input) {
			if insertsSemicolon(l.prevTok) {
				return l.tok(SEMICOLON, ";", l.line, l.col)
			}
			return l.tok(EOF, "", l.line, l.col)
		}

		ch := l.input[l.pos]
		startLine, startCol := l.line, l.col

		// /- slashdash (before // and /* checks)
		if ch == '/' && l.peekAt(1) == '-' {
			l.advance()
			l.advance()
			return l.tok(SLASHDASH, "/-", startLine, startCol)
		}

		// Line comment
		if ch == '/' && l.peekAt(1) == '/' {
			if insertsSemicolon(l.prevTok) {
				return l.tok(SEMICOLON, ";", l.line, l.col)
			}
			start := l.pos
			for l.pos < len(l.input) && l.input[l.pos] != '\n' {
				l.advance()
			}
			return l.tok(LINE_COMMENT, string(l.input[start:l.pos]), startLine, startCol)
		}

		// Block comment (nested)
		if ch == '/' && l.peekAt(1) == '*' {
			if insertsSemicolon(l.prevTok) {
				return l.tok(SEMICOLON, ";", l.line, l.col)
			}
			start := l.pos
			l.advance() // /
			l.advance() // *
			depth := 1
			for l.pos < len(l.input) && depth > 0 {
				if l.input[l.pos] == '/' && l.peekAt(1) == '*' {
					l.advance()
					l.advance()
					depth++
				} else if l.input[l.pos] == '*' && l.peekAt(1) == '/' {
					l.advance()
					l.advance()
					depth--
				} else {
					l.advance()
				}
			}
			if depth > 0 {
				return l.tok(ILLEGAL, "unterminated block comment", startLine, startCol)
			}
			return l.tok(BLOCK_COMMENT, string(l.input[start:l.pos]), startLine, startCol)
		}

		// Color (#rrggbb / #rrggbbaa) or element reference (#id)
		if ch == '#' && l.pos+1 < len(l.input) {
			next := l.peekAt(1)
			if isHexDigit(next) || isIdentStart(next) {
				return l.scanHashToken(startLine, startCol)
			}
		}

		// Number
		if isDigit(ch) {
			return l.scanNumber(startLine, startCol)
		}

		// Strings
		if ch == '"' {
			if l.pos+2 < len(l.input) && l.input[l.pos+1] == '"' && l.input[l.pos+2] == '"' {
				return l.scanTripleString(startLine, startCol)
			}
			return l.scanString(startLine, startCol)
		}
		if ch == '`' {
			return l.scanRawString(startLine, startCol)
		}

		// Identifier or keyword
		if isIdentStart(ch) {
			return l.scanIdent(startLine, startCol)
		}

		// Punctuation and operators
		l.advance()
		switch ch {
		case '(':
			return l.tok(LPAREN, "(", startLine, startCol)
		case ')':
			return l.tok(RPAREN, ")", startLine, startCol)
		case '{':
			if len(l.interpStack) > 0 {
				l.interpStack[len(l.interpStack)-1].depth++
			}
			return l.tok(LBRACE, "{", startLine, startCol)
		case '}':
			if len(l.interpStack) > 0 {
				top := &l.interpStack[len(l.interpStack)-1]
				if top.depth == 0 {
					triple := top.triple
					l.interpStack = l.interpStack[:len(l.interpStack)-1]
					return l.scanStringContent(true, triple, startLine, startCol)
				}
				top.depth--
			}
			return l.tok(RBRACE, "}", startLine, startCol)
		case '[':
			return l.tok(LBRACKET, "[", startLine, startCol)
		case ']':
			return l.tok(RBRACKET, "]", startLine, startCol)
		case ',':
			return l.tok(COMMA, ",", startLine, startCol)
		case '.':
			if l.peek() == '.' && l.peekAt(1) == '.' {
				l.advance()
				l.advance()
				return l.tok(ELLIPSIS, "...", startLine, startCol)
			}
			return l.tok(DOT, ".", startLine, startCol)
		case ':':
			return l.tok(COLON, ":", startLine, startCol)
		case '@':
			return l.tok(AT, "@", startLine, startCol)
		case '?':
			return l.tok(QUESTION, "?", startLine, startCol)
		case ';':
			return l.tok(SEMICOLON, ";", startLine, startCol)
		case '+':
			if l.peek() == '=' {
				l.advance()
				return l.tok(PLUS_ASSIGN, "+=", startLine, startCol)
			}
			if l.peek() == '+' {
				l.advance()
				return l.tok(PLUS_PLUS, "++", startLine, startCol)
			}
			return l.tok(PLUS, "+", startLine, startCol)
		case '-':
			if l.peek() == '=' {
				l.advance()
				return l.tok(MINUS_ASSIGN, "-=", startLine, startCol)
			}
			if l.peek() == '>' {
				l.advance()
				return l.tok(ARROW, "->", startLine, startCol)
			}
			if l.peek() == '-' {
				l.advance()
				return l.tok(MINUS_MINUS, "--", startLine, startCol)
			}
			return l.tok(MINUS, "-", startLine, startCol)
		case '*':
			if l.peek() == '=' {
				l.advance()
				return l.tok(STAR_ASSIGN, "*=", startLine, startCol)
			}
			return l.tok(STAR, "*", startLine, startCol)
		case '/':
			if l.peek() == '=' {
				l.advance()
				return l.tok(SLASH_ASSIGN, "/=", startLine, startCol)
			}
			return l.tok(SLASH, "/", startLine, startCol)
		case '%':
			if l.peek() == '=' {
				l.advance()
				return l.tok(PERCENT_ASSIGN, "%=", startLine, startCol)
			}
			return l.tok(PERCENT, "%", startLine, startCol)
		case '!':
			if l.peek() == '=' {
				l.advance()
				return l.tok(NEQ, "!=", startLine, startCol)
			}
			if l.peek() == '!' {
				l.advance()
				return l.tok(BANGBANG, "!!", startLine, startCol)
			}
			return l.tok(BANG, "!", startLine, startCol)
		case '=':
			if l.peek() == '=' {
				l.advance()
				return l.tok(EQ, "==", startLine, startCol)
			}
			if l.peek() == '>' {
				l.advance()
				return l.tok(FAT_ARROW, "=>", startLine, startCol)
			}
			return l.tok(ASSIGN, "=", startLine, startCol)
		case '<':
			if l.peek() == '=' {
				l.advance()
				return l.tok(LTE, "<=", startLine, startCol)
			}
			return l.tok(LT, "<", startLine, startCol)
		case '>':
			if l.peek() == '=' {
				l.advance()
				return l.tok(GTE, ">=", startLine, startCol)
			}
			return l.tok(GT, ">", startLine, startCol)
		case '&':
			if l.peek() == '&' {
				l.advance()
				return l.tok(AND, "&&", startLine, startCol)
			}
			return l.tok(ILLEGAL, "&", startLine, startCol)
		case '|':
			if l.peek() == '|' {
				l.advance()
				return l.tok(OR, "||", startLine, startCol)
			}
			return l.tok(PIPE, "|", startLine, startCol)
		default:
			return l.tok(ILLEGAL, string(ch), startLine, startCol)
		}
	}
}

func (l *lexer) scanIdent(startLine, startCol int) Token {
	start := l.pos
	for l.pos < len(l.input) && isIdentCont(l.input[l.pos]) {
		l.pos++
		l.col++
	}
	lit := string(l.input[start:l.pos])
	return l.tok(LookupIdent(lit), lit, startLine, startCol)
}

// scanNumber scans integer and float literals.
// v2: decimal only. 0x/0o/0b prefixes produce ILLEGAL.
func (l *lexer) scanNumber(startLine, startCol int) Token {
	var sb strings.Builder

	// Reject non-decimal prefixes
	if l.peek() == '0' && l.pos+1 < len(l.input) {
		next := l.input[l.pos+1]
		if next == 'x' || next == 'X' || next == 'o' || next == 'O' || next == 'b' || next == 'B' {
			sb.WriteRune(l.advance()) // 0
			sb.WriteRune(l.advance()) // prefix letter
			l.errors = append(l.errors, fmt.Sprintf("%d:%d: non-decimal integer literals are not supported in v2", startLine, startCol))
			return l.tok(ILLEGAL, sb.String(), startLine, startCol)
		}
	}

	for l.pos < len(l.input) && (isDigit(l.input[l.pos]) || l.input[l.pos] == '_') {
		sb.WriteRune(l.advance())
	}

	// Float
	if l.peek() == '.' && l.pos+1 < len(l.input) && isDigit(l.peekAt(1)) {
		sb.WriteRune(l.advance()) // .
		for l.pos < len(l.input) && (isDigit(l.input[l.pos]) || l.input[l.pos] == '_') {
			sb.WriteRune(l.advance())
		}
		// Unit suffix on float
		if l.pos < len(l.input) && isLetter(l.input[l.pos]) {
			return l.scanUnitSuffix(&sb, startLine, startCol)
		}
		return l.tok(FLOAT, sb.String(), startLine, startCol)
	}

	// Unit suffix on integer
	if l.pos < len(l.input) && isLetter(l.input[l.pos]) {
		return l.scanUnitSuffix(&sb, startLine, startCol)
	}

	return l.tok(INT, sb.String(), startLine, startCol)
}

func (l *lexer) scanUnitSuffix(sb *strings.Builder, startLine, startCol int) Token {
	for l.pos < len(l.input) && isLetter(l.input[l.pos]) {
		sb.WriteRune(l.advance())
	}
	return l.tok(UNIT_LITERAL, sb.String(), startLine, startCol)
}

func (l *lexer) scanString(startLine, startCol int) Token {
	l.advance() // opening "
	return l.scanStringContent(false, false, startLine, startCol)
}

// scanStringContent scans string text until a closing quote or interpolation {.
// resume: true when resuming after } closes an interpolation.
// triple: true for triple-quoted strings.
func (l *lexer) scanStringContent(resume, triple bool, startLine, startCol int) Token {
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]

		// Closing quote
		if !triple && ch == '"' {
			l.advance()
			if resume {
				return l.tok(STR_END, sb.String(), startLine, startCol)
			}
			return l.tok(STR_FULL, sb.String(), startLine, startCol)
		}
		if triple && ch == '"' && l.pos+2 < len(l.input) && l.input[l.pos+1] == '"' && l.input[l.pos+2] == '"' {
			l.advance()
			l.advance()
			l.advance()
			text := sb.String()
			if !resume {
				text = dedent(text)
			}
			if resume {
				return l.tok(TRIPLE_END, text, startLine, startCol)
			}
			return l.tok(TRIPLE_FULL, text, startLine, startCol)
		}

		// Interpolation start
		if ch == '{' {
			l.advance()
			l.interpStack = append(l.interpStack, interpFrame{triple: triple})
			if resume {
				return l.tok(STR_RESUME, sb.String(), startLine, startCol)
			}
			if triple {
				return l.tok(TRIPLE_START, sb.String(), startLine, startCol)
			}
			return l.tok(STR_START, sb.String(), startLine, startCol)
		}

		// Escape sequences
		if ch == '\\' {
			l.advance()
			if l.pos < len(l.input) {
				esc := l.input[l.pos]
				l.advance()
				switch esc {
				case 'n':
					sb.WriteRune('\n')
				case 't':
					sb.WriteRune('\t')
				case 'r':
					sb.WriteRune('\r')
				case '"':
					sb.WriteRune('"')
				case '\\':
					sb.WriteRune('\\')
				case '{':
					sb.WriteRune('{')
				case '0':
					sb.WriteRune(0)
				case 'x':
					if l.pos+1 < len(l.input) {
						hi := hexVal(l.input[l.pos])
						lo := hexVal(l.input[l.pos+1])
						if hi >= 0 && lo >= 0 {
							sb.WriteRune(rune(hi*16 + lo))
							l.advance()
							l.advance()
						} else {
							l.errors = append(l.errors, fmt.Sprintf("%d:%d: invalid hex escape", l.line, l.col))
							sb.WriteRune('\\')
							sb.WriteRune('x')
						}
					}
				default:
					l.errors = append(l.errors, fmt.Sprintf("%d:%d: unknown escape \\%c", l.line, l.col, esc))
					sb.WriteRune('\\')
					sb.WriteRune(esc)
				}
			}
			continue
		}

		sb.WriteRune(ch)
		l.advance()
	}
	if triple {
		return l.tok(ILLEGAL, "unterminated triple-quoted string", startLine, startCol)
	}
	return l.tok(ILLEGAL, "unterminated string", startLine, startCol)
}

func (l *lexer) scanTripleString(startLine, startCol int) Token {
	l.advance() // "
	l.advance() // "
	l.advance() // "
	return l.scanStringContent(false, true, startLine, startCol)
}

func (l *lexer) scanRawString(startLine, startCol int) Token {
	l.advance() // `
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == '`' {
			l.advance()
			return l.tok(RAW_STRING, sb.String(), startLine, startCol)
		}
		sb.WriteRune(ch)
		l.advance()
	}
	return l.tok(ILLEGAL, "unterminated raw string", startLine, startCol)
}

func (l *lexer) scanHashToken(startLine, startCol int) Token {
	l.advance() // #
	start := l.pos
	for l.pos < len(l.input) && isIdentCont(l.input[l.pos]) {
		l.pos++
		l.col++
	}
	name := string(l.input[start:l.pos])
	if (len(name) == 6 || len(name) == 8) && isAllHex(name) {
		return l.tok(COLOR, "#"+name, startLine, startCol)
	}
	if len(name) == 0 || !isIdentStart(rune(name[0])) {
		return l.tok(ILLEGAL, "#"+name, startLine, startCol)
	}
	return l.tok(ELEMENT_REF, name, startLine, startCol)
}

func isAllHex(s string) bool {
	for _, ch := range s {
		if !isHexDigit(ch) {
			return false
		}
	}
	return true
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= 1 {
		return s
	}
	start := 0
	if lines[0] == "" {
		start = 1
	}
	minIndent := -1
	for i := start; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if minIndent < 0 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent <= 0 {
		if start > 0 {
			return strings.Join(lines[start:], "\n")
		}
		return s
	}
	result := make([]string, 0, len(lines)-start)
	for i := start; i < len(lines); i++ {
		line := lines[i]
		if len(line) >= minIndent {
			line = line[minIndent:]
		}
		result = append(result, line)
	}
	if len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
		result = result[:len(result)-1]
	}
	return strings.Join(result, "\n")
}

// Tokenize scans the entire source and returns all tokens up to (and including) EOF.
// Lexer errors are returned separately.
func Tokenize(src string) (tokens []Token, errs []string) {
	l := newLexer(src)
	for {
		tok := l.NextToken()
		tokens = append(tokens, tok)
		if tok.Type == EOF {
			break
		}
	}
	return tokens, l.errors
}
