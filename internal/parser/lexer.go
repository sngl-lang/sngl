package parser

import (
	"strings"
)

// lexer scans SNGL source text into tokens.
type lexer struct {
	input   []rune
	pos     int       // current position
	line    int
	col     int
	prevTok TokenType // for semicolon insertion
}

func newLexer(src string) *lexer {
	return &lexer{
		input: []rune(src),
		line:  1,
		col:   1,
	}
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

func (l *lexer) token(typ TokenType, lit string, line, col int) Token {
	l.prevTok = typ
	return Token{Type: typ, Literal: lit, Line: line, Column: col}
}

// NextToken returns the next token from the input.
func (l *lexer) NextToken() Token {
	for {
		// Skip whitespace, inserting semicolons at newlines
		for l.pos < len(l.input) {
			ch := l.input[l.pos]
			if ch == '\n' {
				if insertsSemicolon(l.prevTok) {
					semi := l.token(SEMICOLON, ";", l.line, l.col)
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
				return l.token(SEMICOLON, ";", l.line, l.col)
			}
			return l.token(EOF, "", l.line, l.col)
		}

		ch := l.input[l.pos]
		startLine, startCol := l.line, l.col

		// Slashdash (must come before // and /* checks)
		if ch == '/' && l.peekAt(1) == '-' {
			l.advance()
			l.advance()
			return l.token(SLASHDASH, "/-", startLine, startCol)
		}

		// Line comment
		if ch == '/' && l.peekAt(1) == '/' {
			// Check for semicolon insertion before consuming comment
			if insertsSemicolon(l.prevTok) {
				return l.token(SEMICOLON, ";", l.line, l.col)
			}
			start := l.pos
			for l.pos < len(l.input) && l.input[l.pos] != '\n' {
				l.advance()
			}
			return l.token(LINE_COMMENT, string(l.input[start:l.pos]), startLine, startCol)
		}

		// Block comment
		if ch == '/' && l.peekAt(1) == '*' {
			// Check for semicolon insertion before consuming comment
			if insertsSemicolon(l.prevTok) {
				return l.token(SEMICOLON, ";", l.line, l.col)
			}
			start := l.pos
			l.advance()
			l.advance()
			closed := false
			for l.pos < len(l.input) {
				if l.input[l.pos] == '*' && l.peekAt(1) == '/' {
					l.advance()
					l.advance()
					closed = true
					break
				}
				l.advance()
			}
			if !closed {
				return l.token(ILLEGAL, "unterminated block comment", startLine, startCol)
			}
			return l.token(BLOCK_COMMENT, string(l.input[start:l.pos]), startLine, startCol)
		}

		// Color literal (#hex) or element reference (#id)
		if ch == '#' && l.pos+1 < len(l.input) {
			next := l.peekAt(1)
			if isHexDigit(next) || isIdentStart(next) {
				return l.scanHashToken(startLine, startCol)
			}
		}

		// Number literal (may be duration)
		if isDigit(ch) {
			return l.scanNumber(startLine, startCol)
		}

		// String literal
		if ch == '"' {
			return l.scanString(startLine, startCol)
		}

		// Identifier or keyword (may include hyphens in style contexts)
		if isIdentStart(ch) {
			return l.scanIdent(startLine, startCol)
		}

		// Punctuation and operators
		l.advance()
		switch ch {
		case '(':
			return l.token(LPAREN, "(", startLine, startCol)
		case ')':
			return l.token(RPAREN, ")", startLine, startCol)
		case '{':
			return l.token(LBRACE, "{", startLine, startCol)
		case '}':
			return l.token(RBRACE, "}", startLine, startCol)
		case '[':
			return l.token(LBRACKET, "[", startLine, startCol)
		case ']':
			return l.token(RBRACKET, "]", startLine, startCol)
		case ',':
			return l.token(COMMA, ",", startLine, startCol)
		case '.':
			if l.peek() == '.' && l.peekAt(1) == '.' {
				l.advance()
				l.advance()
				return l.token(ELLIPSIS, "...", startLine, startCol)
			}
			return l.token(DOT, ".", startLine, startCol)
		case ':':
			return l.token(COLON, ":", startLine, startCol)
		case '@':
			return l.token(AT, "@", startLine, startCol)
		case '?':
			return l.token(QUESTION, "?", startLine, startCol)
		case ';':
			return l.token(SEMICOLON, ";", startLine, startCol)
		case '+':
			if l.peek() == '=' {
				l.advance()
				return l.token(PLUS_ASSIGN, "+=", startLine, startCol)
			}
			return l.token(PLUS, "+", startLine, startCol)
		case '-':
			if l.peek() == '=' {
				l.advance()
				return l.token(MINUS_ASSIGN, "-=", startLine, startCol)
			}
			if l.peek() == '>' {
				l.advance()
				return l.token(ARROW, "->", startLine, startCol)
			}
			return l.token(MINUS, "-", startLine, startCol)
		case '*':
			if l.peek() == '=' {
				l.advance()
				return l.token(STAR_ASSIGN, "*=", startLine, startCol)
			}
			return l.token(STAR, "*", startLine, startCol)
		case '/':
			if l.peek() == '=' {
				l.advance()
				return l.token(SLASH_ASSIGN, "/=", startLine, startCol)
			}
			return l.token(SLASH, "/", startLine, startCol)
		case '%':
			if l.peek() == '=' {
				l.advance()
				return l.token(PERCENT_ASSIGN, "%=", startLine, startCol)
			}
			return l.token(PERCENT, "%", startLine, startCol)
		case '!':
			if l.peek() == '=' {
				l.advance()
				return l.token(NEQ, "!=", startLine, startCol)
			}
			if l.peek() == '!' {
				l.advance()
				return l.token(BANGBANG, "!!", startLine, startCol)
			}
			return l.token(BANG, "!", startLine, startCol)
		case '=':
			if l.peek() == '=' {
				l.advance()
				return l.token(EQ, "==", startLine, startCol)
			}
			if l.peek() == '>' {
				l.advance()
				return l.token(FAT_ARROW, "=>", startLine, startCol)
			}
			return l.token(ASSIGN, "=", startLine, startCol)
		case '<':
			if l.peek() == '=' {
				l.advance()
				return l.token(LTE, "<=", startLine, startCol)
			}
			return l.token(LT, "<", startLine, startCol)
		case '>':
			if l.peek() == '=' {
				l.advance()
				return l.token(GTE, ">=", startLine, startCol)
			}
			return l.token(GT, ">", startLine, startCol)
		case '&':
			if l.peek() == '&' {
				l.advance()
				return l.token(AND, "&&", startLine, startCol)
			}
			return l.token(ILLEGAL, "&", startLine, startCol)
		case '|':
			if l.peek() == '|' {
				l.advance()
				return l.token(OR, "||", startLine, startCol)
			}
			return l.token(PIPE, "|", startLine, startCol)
		default:
			return l.token(ILLEGAL, string(ch), startLine, startCol)
		}
	}
}

func (l *lexer) scanIdent(startLine, startCol int) Token {
	start := l.pos
	for l.pos < len(l.input) && isIdentContinue(l.input[l.pos]) {
		l.pos++
		l.col++
	}
	lit := string(l.input[start:l.pos])
	typ := LookupIdent(lit)
	return l.token(typ, lit, startLine, startCol)
}

func (l *lexer) scanNumber(startLine, startCol int) Token {
	var sb strings.Builder
	// Optional leading minus
	if l.peek() == '-' {
		sb.WriteRune(l.advance())
	}

	// Integer part
	for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
		sb.WriteRune(l.advance())
	}

	isFloat := false
	// Decimal part
	if l.peek() == '.' && l.pos+1 < len(l.input) && isDigit(l.peekAt(1)) {
		isFloat = true
		sb.WriteRune(l.advance()) // .
		for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
			sb.WriteRune(l.advance())
		}
	}

	// Check for unit suffix (letter immediately after number, e.g. 5s, 12px, 1.5em)
	if l.pos < len(l.input) && isLetter(l.input[l.pos]) {
		var suffix strings.Builder
		for l.pos < len(l.input) && isLetter(l.input[l.pos]) {
			suffix.WriteRune(l.advance())
		}
		return l.token(UNIT_LITERAL, sb.String()+suffix.String(), startLine, startCol)
	}

	if isFloat {
		return l.token(FLOAT, sb.String(), startLine, startCol)
	}
	return l.token(INT, sb.String(), startLine, startCol)
}

func (l *lexer) scanString(startLine, startCol int) Token {
	l.advance() // consume opening "
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == '"' {
			l.advance()
			return l.token(STRING, sb.String(), startLine, startCol)
		}
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
					// \xHH hex escape
					if l.pos+1 < len(l.input) {
						hi := hexVal(l.input[l.pos])
						lo := hexVal(l.input[l.pos+1])
						if hi >= 0 && lo >= 0 {
							sb.WriteRune(rune(hi*16 + lo))
							l.advance()
							l.advance()
						} else {
							sb.WriteRune('\\')
							sb.WriteRune('x')
						}
					} else {
						sb.WriteRune('\\')
						sb.WriteRune('x')
					}
				default:
					sb.WriteRune('\\')
					sb.WriteRune(esc)
				}
			}
			continue
		}
		sb.WriteRune(ch)
		l.advance()
	}
	// Unterminated string
	return l.token(ILLEGAL, "unterminated string", startLine, startCol)
}

// scanHashToken handles # followed by hex digits or identifier chars.
// Produces COLOR for #rrggbb/#rrggbbaa, ELEMENT_REF for #identifier.
func (l *lexer) scanHashToken(startLine, startCol int) Token {
	l.advance() // consume #
	start := l.pos
	// Scan all ident-continue characters (letters, digits, _)
	for l.pos < len(l.input) && isIdentContinue(l.input[l.pos]) {
		l.pos++
		l.col++
	}
	name := string(l.input[start:l.pos])

	// Check if it's a valid color: exactly 6 or 8 hex-only chars
	if (len(name) == 6 || len(name) == 8) && isAllHex(name) {
		return l.token(COLOR, "#"+name, startLine, startCol)
	}

	// Otherwise it's an element reference — name must be a valid identifier
	if len(name) == 0 || !isIdentStart(rune(name[0])) {
		return l.token(ILLEGAL, "#"+name, startLine, startCol)
	}
	return l.token(ELEMENT_REF, name, startLine, startCol)
}

func isAllHex(s string) bool {
	for _, ch := range s {
		if !isHexDigit(ch) {
			return false
		}
	}
	return true
}

func isDigit(ch rune) bool { return ch >= '0' && ch <= '9' }
func isHexDigit(ch rune) bool {
	return isDigit(ch) || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}
func isLetter(ch rune) bool        { return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') }
func isIdentStart(ch rune) bool    { return isLetter(ch) || ch == '_' }
func isIdentContinue(ch rune) bool { return isLetter(ch) || isDigit(ch) || ch == '_' }

// hexVal returns the numeric value of a hex digit, or -1 if not hex.
func hexVal(r rune) int {
	if r >= '0' && r <= '9' {
		return int(r - '0')
	}
	if r >= 'a' && r <= 'f' {
		return int(r - 'a' + 10)
	}
	if r >= 'A' && r <= 'F' {
		return int(r - 'A' + 10)
	}
	return -1
}
