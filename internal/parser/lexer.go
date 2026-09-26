package parser

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// interpFrame tracks one level of string interpolation nesting.
type interpFrame struct {
	triple   bool // true for triple-quoted string
	i18n     bool // true for $"..." / $"""...""" frames
	depth    int  // brace nesting within this interpolation expression
	caseBody bool // true when this frame is inside an i18n case body (plural/select selector)
}

// lexer scans SNGL v2 source text into tokens.
// v2 changes vs v1:
//   - Non-base-10 integer literals (0x…, 0o…, 0b…) are rejected (ILLEGAL).
//   - true, false, null are keyword tokens (KW_TRUE/KW_FALSE/KW_NULL).
type lexer struct {
	// src is the original source; input is it decoded to runes, which the
	// scanning below indexes directly. wideAt/wideCum record where the two
	// indexings diverge, so text can slice src directly — see text.
	src              string
	wideAt           []int32
	wideCum          []int32
	input            []rune
	pos              int
	line             int
	col              int
	prevTok          TokenType
	prevPrevTok      TokenType
	errors           []string
	interpStack      []interpFrame // active string interpolation nesting
	macroAttrDepth   int           // incremented by #[, decremented by matching ]
	macroInnerBracks int           // [ inside macro attr args, to skip inner ]
}

func newLexer(src string) *lexer {
	runes := []rune(src)
	l := &lexer{src: src, input: runes, line: 1, col: 1}
	if len(runes) != len(src) {
		// Record only the runes that are wider than a byte, and the running
		// total of the extra bytes they contribute. Ranging a string yields
		// each rune's byte index, so this costs one pass and no decoding of
		// its own. A dense rune-to-byte table would be four bytes per rune of
		// source; this is four per *multi-byte* rune, and real documents have
		// a handful.
		extra, n := int32(0), int32(0)
		for _, r := range src {
			if w := int32(utf8.RuneLen(r)); w > 1 {
				extra += w - 1
				l.wideAt = append(l.wideAt, n)
				l.wideCum = append(l.wideCum, extra)
			}
			n++
		}
	}
	return l
}

// byteOff maps a rune offset in input to its byte offset in src.
func (l *lexer) byteOff(runeIdx int) int {
	if len(l.wideAt) == 0 {
		return runeIdx
	}
	// Number of multi-byte runes strictly before runeIdx, and with them the
	// extra bytes they occupy.
	k := sort.Search(len(l.wideAt), func(i int) bool { return int(l.wideAt[i]) >= runeIdx })
	if k == 0 {
		return runeIdx
	}
	return runeIdx + int(l.wideCum[k-1])
}

// text returns the source between two rune offsets, as a substring of the
// original — no allocation, no UTF-8 re-encoding.
//
// Every identifier, keyword and comment in a document goes through here, and
// string(l.input[a:b]) copied and re-encoded each one.
func (l *lexer) text(start, end int) string {
	return l.src[l.byteOff(start):l.byteOff(end)]
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
	if typ == ILLEGAL {
		// scanEncoded drops ILLEGAL from the stream the parser sees, so an
		// ILLEGAL token with no lex error beside it is a failure nobody
		// reports: the parse continues past the bad input and fails somewhere
		// else, or succeeds. illegal() is the only way to build one.
		panic("sngl/parser: ILLEGAL token built outside illegal(); the lex error would go unreported")
	}
	if typ != LINE_COMMENT && typ != BLOCK_COMMENT {
		l.prevPrevTok = l.prevTok
		l.prevTok = typ
	}
	return Token{Type: typ, Literal: lit, Line: line, Column: col}
}

// illegal records a positioned lex error and returns the ILLEGAL token for it.
//
// Both halves matter: scanEncoded drops ILLEGAL tokens from the stream the parser
// sees, so a token returned without the error is a failure nobody reports at
// the place it happened -- an unterminated string was blamed on the first
// string in the file, because the parse then ran to EOF.
func (l *lexer) illegal(msg string, line, col int) Token {
	l.errors = append(l.errors, fmt.Sprintf("%d:%d: %s", line, col, msg))
	l.prevPrevTok, l.prevTok = l.prevTok, ILLEGAL
	return Token{Type: ILLEGAL, Literal: msg, Line: line, Column: col}
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
			return l.tok(LINE_COMMENT, l.text(start, l.pos), startLine, startCol)
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
				return l.illegal("unterminated block comment", startLine, startCol)
			}
			return l.tok(BLOCK_COMMENT, l.text(start, l.pos), startLine, startCol)
		}

		// #[ — macro attribute open; #hex — color; #id — element reference
		if ch == '#' && l.pos+1 < len(l.input) {
			next := l.peekAt(1)
			if next == '[' {
				l.advance() // #
				l.advance() // [
				l.macroAttrDepth++
				return l.tok(ATTR_OPEN, "#[", startLine, startCol)
			}
			if isHexDigit(next) || isIdentStart(next) {
				return l.scanHashToken(startLine, startCol)
			}
		}

		// Number
		if isDigit(ch) {
			return l.scanNumber(startLine, startCol)
		}

		// Translatable string literal: $"..." or $"""..."""
		if ch == '$' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '"' {
			l.advance() // consume $
			if l.pos+2 < len(l.input) && l.input[l.pos] == '"' && l.input[l.pos+1] == '"' && l.input[l.pos+2] == '"' {
				return l.scanTripleString(startLine, startCol, true)
			}
			return l.scanString(startLine, startCol, true)
		}

		// Strings
		if ch == '"' {
			if l.pos+2 < len(l.input) && l.input[l.pos+1] == '"' && l.input[l.pos+2] == '"' {
				return l.scanTripleString(startLine, startCol, false)
			}
			return l.scanString(startLine, startCol, false)
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
			// Check if this opens an i18n case body (plural/select selector pattern).
			// Inside an i18n placeholder body, COMMA IDENT { or ASSIGN INT { signals a case body.
			if len(l.interpStack) > 0 {
				top := &l.interpStack[len(l.interpStack)-1]
				if top.i18n && top.depth == 0 {
					// Detect case body selectors in ICU plural/select/selectordinal messages.
					// Pattern 1: COMMA IDENT { — first case selector after the keyword args
					// Pattern 2: I18N_CASE_{FULL,END} IDENT { — subsequent case selectors
					// Pattern 3: ASSIGN INT { — numeric case selector (=0, =1, etc.)
					afterCase := l.prevPrevTok == I18N_CASE_FULL || l.prevPrevTok == I18N_CASE_END
					isCaseSelector := (l.prevTok == IDENT && (l.prevPrevTok == COMMA || afterCase)) ||
						(l.prevTok == INT && l.prevPrevTok == ASSIGN)
					if isCaseSelector {
						return l.scanCaseBodyContent(startLine, startCol)
					}
				}
				top.depth++
			}
			return l.tok(LBRACE, "{", startLine, startCol)
		case '}':
			if len(l.interpStack) > 0 {
				top := &l.interpStack[len(l.interpStack)-1]
				if top.depth == 0 {
					triple := top.triple
					i18n := top.i18n
					caseBody := top.caseBody
					l.interpStack = l.interpStack[:len(l.interpStack)-1]
					if caseBody {
						// Resuming inside a case body after a nested placeholder.
						return l.scanCaseBodyContentResume(true, startLine, startCol)
					}
					return l.scanStringContent(true, triple, i18n, startLine, startCol)
				}
				top.depth--
			}
			return l.tok(RBRACE, "}", startLine, startCol)
		case '[':
			if l.macroAttrDepth > 0 {
				l.macroInnerBracks++
			}
			return l.tok(LBRACKET, "[", startLine, startCol)
		case ']':
			if l.macroAttrDepth > 0 {
				if l.macroInnerBracks > 0 {
					l.macroInnerBracks-- // inner ] — don't decrement depth, don't suppress ASI
				} else {
					l.macroAttrDepth--
					tok := l.tok(RBRACKET, "]", startLine, startCol)
					// Suppress ASI: the closing ] of a macro attribute is not a
					// statement terminator — the next line holds the decorated decl.
					l.prevTok = COMMA // something that does not trigger insertsSemicolon
					return tok
				}
			}
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
			return l.tok(AMP, "&", startLine, startCol)
		case '|':
			if l.peek() == '|' {
				l.advance()
				return l.tok(OR, "||", startLine, startCol)
			}
			return l.tok(PIPE, "|", startLine, startCol)
		default:
			return l.illegal(fmt.Sprintf("unexpected character %q", string(ch)), startLine, startCol)
		}
	}
}

func (l *lexer) scanIdent(startLine, startCol int) Token {
	start := l.pos
	for l.pos < len(l.input) && isIdentCont(l.input[l.pos]) {
		l.pos++
		l.col++
	}
	lit := l.text(start, l.pos)
	return l.tok(LookupIdent(lit), lit, startLine, startCol)
}

// scanNumber scans integer and float literals.
// v2: decimal only. There are no non-decimal bases; a digit run followed by
// letters (including 0x/0o/0b) is a unit literal, not a based number.
func (l *lexer) scanNumber(startLine, startCol int) Token {
	var sb strings.Builder

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

func (l *lexer) scanString(startLine, startCol int, i18n bool) Token {
	l.advance() // opening "
	return l.scanStringContent(false, false, i18n, startLine, startCol)
}

// scanStringContent scans string text until a closing quote or interpolation {.
// resume: true when resuming after } closes an interpolation.
// triple: true for triple-quoted strings.
// i18n: true for $"..." / $"""...""" translatable strings.
func (l *lexer) scanStringContent(resume, triple, i18n bool, startLine, startCol int) Token {
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]

		// Closing quote
		if !triple && ch == '"' {
			l.advance()
			if resume {
				if i18n {
					return l.tok(I18N_STR_END, sb.String(), startLine, startCol)
				}
				return l.tok(STR_END, sb.String(), startLine, startCol)
			}
			if i18n {
				return l.tok(I18N_STR_FULL, sb.String(), startLine, startCol)
			}
			return l.tok(STR_FULL, sb.String(), startLine, startCol)
		}
		if triple && ch == '"' && l.pos+2 < len(l.input) && l.input[l.pos+1] == '"' && l.input[l.pos+2] == '"' {
			l.advance()
			l.advance()
			l.advance()
			text := sb.String()
			if resume {
				if i18n {
					return l.tok(I18N_TRIPLE_END, text, startLine, startCol)
				}
				return l.tok(TRIPLE_END, text, startLine, startCol)
			}
			if i18n {
				return l.tok(I18N_TRIPLE_FULL, text, startLine, startCol)
			}
			return l.tok(TRIPLE_FULL, text, startLine, startCol)
		}

		// Interpolation start
		if ch == '{' {
			l.advance()
			l.interpStack = append(l.interpStack, interpFrame{triple: triple, i18n: i18n})
			if resume {
				if i18n {
					return l.tok(I18N_STR_RESUME, sb.String(), startLine, startCol)
				}
				return l.tok(STR_RESUME, sb.String(), startLine, startCol)
			}
			if triple {
				if i18n {
					return l.tok(I18N_TRIPLE_START, sb.String(), startLine, startCol)
				}
				return l.tok(TRIPLE_START, sb.String(), startLine, startCol)
			}
			if i18n {
				return l.tok(I18N_STR_START, sb.String(), startLine, startCol)
			}
			return l.tok(STR_START, sb.String(), startLine, startCol)
		}

		// ICU apostrophe quoting (i18n mode only). It decides where a
		// placeholder starts — 'X' hides a metachar from the lexer — so it is
		// scanned here, but ICU is the runtime's syntax and the quoting is
		// copied through for the runtime to decode.
		// '' → literal '; 'X' where X starts with a metachar → literal run; bare ' → literal '.
		if i18n && ch == '\'' {
			if l.pos+1 < len(l.input) && l.input[l.pos+1] == '\'' {
				sb.WriteString("''")
				l.advance()
				l.advance()
				continue
			}
			var next rune
			if l.pos+1 < len(l.input) {
				next = l.input[l.pos+1]
			}
			if next == '{' || next == '}' || next == '#' || next == '|' {
				// 'X' quoted run — X starts with a metachar; consume interior literally.
				sb.WriteByte('\'')
				l.advance() // consume opening '
				for l.pos < len(l.input) {
					if l.input[l.pos] == '\'' {
						if l.pos+1 < len(l.input) && l.input[l.pos+1] == '\'' {
							sb.WriteString("''")
							l.advance()
							l.advance()
							continue
						}
						sb.WriteByte('\'')
						l.advance() // consume closing '
						break
					}
					sb.WriteRune(l.input[l.pos])
					l.advance()
				}
				continue
			}
			// Bare ' not followed by a metachar — literal apostrophe.
			sb.WriteByte('\'')
			l.advance()
			continue
		}

		// Escape sequences. The token carries the spelling, not the character
		// it stands for — ast.UnescapeString decodes it. The scan still has to
		// consume the escape, both to report a bad one and so that `\"` does
		// not end the string.
		if ch == '\\' {
			l.advance()
			if l.pos < len(l.input) {
				esc := l.input[l.pos]
				l.advance()
				switch esc {
				case 'n', 't', 'r', '"', '\\', '{', '}', '0':
					sb.WriteRune('\\')
					sb.WriteRune(esc)
				case 'x':
					if l.pos+1 < len(l.input) && hexVal(l.input[l.pos]) >= 0 && hexVal(l.input[l.pos+1]) >= 0 {
						sb.WriteString(`\x`)
						sb.WriteRune(l.input[l.pos])
						sb.WriteRune(l.input[l.pos+1])
						l.advance()
						l.advance()
					} else {
						l.errors = append(l.errors, fmt.Sprintf("%d:%d: invalid hex escape", l.line, l.col))
						sb.WriteString(`\x`)
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
		return l.illegal("unterminated triple-quoted string", startLine, startCol)
	}
	return l.illegal("unterminated string", startLine, startCol)
}

// scanCaseBodyContent scans a case body in an i18n plural/select placeholder.
// Called after the opening '{' of a case body has been consumed (initial call),
// or after a nested placeholder's closing '}' (resume call).
// The body is treated as literal text; inner '{...}' pairs open nested i18n
// placeholder expressions.
//
// Token sequence for a body with no nested placeholders:
//
//	I18N_CASE_FULL("body text")
//
// Token sequence for a body with nested placeholders:
//
//	I18N_CASE_START("prefix") … I18N_STR_RESUME("middle") … I18N_CASE_END("suffix")
func (l *lexer) scanCaseBodyContent(startLine, startCol int) Token {
	return l.scanCaseBodyContentResume(false, startLine, startCol)
}

func (l *lexer) scanCaseBodyContentResume(resume bool, startLine, startCol int) Token {
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == '}' {
			l.advance()
			if resume {
				return l.tok(I18N_CASE_END, sb.String(), startLine, startCol)
			}
			return l.tok(I18N_CASE_FULL, sb.String(), startLine, startCol)
		}
		if ch == '{' {
			l.advance()
			// Push a fresh i18n frame for the nested placeholder.
			l.interpStack = append(l.interpStack, interpFrame{i18n: true, caseBody: true})
			if resume {
				return l.tok(I18N_STR_RESUME, sb.String(), startLine, startCol)
			}
			return l.tok(I18N_CASE_START, sb.String(), startLine, startCol)
		}
		// ICU apostrophe quoting inside case bodies.
		if ch == '\'' {
			if l.pos+1 < len(l.input) && l.input[l.pos+1] == '\'' {
				sb.WriteString("''")
				l.advance()
				l.advance()
				continue
			}
			var next rune
			if l.pos+1 < len(l.input) {
				next = l.input[l.pos+1]
			}
			if next == '{' || next == '}' || next == '#' || next == '|' {
				// 'X' quoted run.
				sb.WriteByte('\'')
				l.advance() // consume opening '
				for l.pos < len(l.input) {
					if l.input[l.pos] == '\'' {
						if l.pos+1 < len(l.input) && l.input[l.pos+1] == '\'' {
							sb.WriteString("''")
							l.advance()
							l.advance()
							continue
						}
						sb.WriteByte('\'')
						l.advance() // consume closing '
						break
					}
					sb.WriteRune(l.input[l.pos])
					l.advance()
				}
				continue
			}
			// Bare ' — literal apostrophe.
			sb.WriteByte('\'')
			l.advance()
			continue
		}
		sb.WriteRune(ch)
		l.advance()
	}
	return l.illegal("unterminated i18n case body", startLine, startCol)
}

func (l *lexer) scanTripleString(startLine, startCol int, i18n bool) Token {
	l.advance() // "
	l.advance() // "
	l.advance() // "
	return l.scanStringContent(false, true, i18n, startLine, startCol)
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
	return l.illegal("unterminated raw string", startLine, startCol)
}

func (l *lexer) scanHashToken(startLine, startCol int) Token {
	l.advance() // #
	start := l.pos
	for l.pos < len(l.input) && isIdentCont(l.input[l.pos]) {
		l.pos++
		l.col++
	}
	name := l.text(start, l.pos)
	if len(name) == 0 {
		return l.illegal(`"#" names nothing: a color literal or an element reference follows it`, startLine, startCol)
	}
	// A single token for every `#…` form. Whether it is a color literal
	// (#rrggbb / #rrggbbaa) or an element reference is decided downstream by
	// position: the parser builds a color literal in value position and an
	// element reference in a naming-tag/selection position. The token value is
	// the text after `#`.
	return l.tok(HASH, name, startLine, startCol)
}

// Tokenize scans the entire source and returns all tokens up to (and including) EOF.
// Lexer errors are returned separately.
func Tokenize(src string) (tokens []Token, errs []string) {
	return scanAll(newLexer(src), nil)
}

// scanEncoded lexes src straight into the three things the parser needs: the
// byte stream the generated parser reads, the tokens that stream indexes, and
// the comments the AST builder reattaches.
//
// Producing them in the scan rather than walking a complete []Token afterwards
// removes a pass and a second full copy of the token slice — for a large
// document that copy alone was most of a megabyte of garbage, allocated only
// to drop the comments out of it.
func scanEncoded(l *lexer, seed []Token) (stream []byte, filtered, comments []Token, errs []string) {
	est := len(l.input)/5 + 16
	stream = make([]byte, 0, 2*est)
	filtered = make([]Token, 0, est)

	for _, tok := range seed {
		stream = append(stream, byte(tok.Type), streamSep)
		filtered = append(filtered, tok)
	}
	for {
		tok := l.NextToken()
		switch tok.Type {
		case EOF:
			// Not emitted: egg detects the end from stream exhaustion.
			return stream, filtered, comments, l.errors
		case ILLEGAL:
			// Skipped; the caller reports l.errors separately.
		case LINE_COMMENT, BLOCK_COMMENT:
			comments = append(comments, tok)
		default:
			stream = append(stream, byte(tok.Type), streamSep)
			filtered = append(filtered, tok)
		}
	}
}

func scanAll(l *lexer, tokens []Token) ([]Token, []string) {
	// Size the slice up front. SNGL averages a token every five or six
	// bytes, so growing from nil re-allocates and copies a dozen times for
	// a large document; that churn was the single largest source of garbage
	// in a parse — more than the AST it produces.
	if want := len(tokens) + len(l.input)/5 + 16; cap(tokens) < want {
		grown := make([]Token, len(tokens), want)
		copy(grown, tokens)
		tokens = grown
	}
	for {
		tok := l.NextToken()
		tokens = append(tokens, tok)
		if tok.Type == EOF {
			break
		}
	}
	return tokens, l.errors
}
