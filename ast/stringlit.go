package ast

import "strings"

// A string literal's Raw holds the source spelling between the delimiters:
// escapes are unprocessed, and an i18n literal still carries ICU's apostrophe
// quoting. The value it stands for is StringValue(); the inverse is
// EscapeString. Keeping the spelling is what lets the formatter reprint a
// string it did not write — decoding in the lexer meant `"a\nb"` was reprinted
// with a raw newline in it.

// StringStyleOf reports the quoting style a string literal kind is written in.
func StringStyleOf(kind LiteralKind) (StringStyle, bool) {
	switch kind {
	case LiteralStringQuoted:
		return StyleDouble, true
	case LiteralStringTrippleQuoted:
		return StyleTriple, true
	case LiteralStringBackticked:
		return StyleRaw, true
	}
	return StyleDouble, false
}

// StringValue decodes a string literal's source spelling. The second result is
// false when the literal is not a string.
func (l *LiteralExpr) StringValue() (string, bool) {
	style, ok := StringStyleOf(l.Kind)
	if !ok {
		return "", false
	}
	return UnescapeString(l.Raw, style), true
}

// NewStringLiteral builds a double-quoted literal standing for value. Use it
// rather than assigning Raw: a synthesized literal carries a spelling like any
// other, and a value holding a quote or a brace has to say so.
func NewStringLiteral(value string) *LiteralExpr {
	return &LiteralExpr{Kind: LiteralStringQuoted, Raw: EscapeString(value, StyleDouble)}
}

// UnescapeString decodes the escape sequences a string literal's spelling may
// carry. A backticked string carries none. Malformed escapes are left as
// written — the lexer has already reported them.
func UnescapeString(raw string, style StringStyle) string {
	if style == StyleRaw || !strings.Contains(raw, `\`) {
		return raw
	}
	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' || i+1 >= len(raw) {
			b.WriteByte(raw[i])
			continue
		}
		i++
		switch raw[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '0':
			b.WriteByte(0)
		case '"', '\\', '{', '}':
			b.WriteByte(raw[i])
		case 'x':
			if i+2 < len(raw) {
				hi, lo := hexDigit(raw[i+1]), hexDigit(raw[i+2])
				if hi >= 0 && lo >= 0 {
					b.WriteRune(rune(hi*16 + lo))
					i += 2
					continue
				}
			}
			b.WriteString(`\x`)
		default:
			b.WriteByte('\\')
			b.WriteByte(raw[i])
		}
	}
	return b.String()
}

// EscapeString spells value as the content of a string literal in the given
// style. Braces are escaped because a bare `{` opens an interpolation, and the
// control characters because a literal newline would end the logical line.
func EscapeString(value string, style StringStyle) string {
	if style == StyleRaw {
		return value
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '{':
			b.WriteString(`\{`)
		case '}':
			b.WriteString(`\}`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		case 0:
			b.WriteString(`\0`)
		case '"':
			if style == StyleTriple {
				b.WriteRune(r)
			} else {
				b.WriteString(`\"`)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
