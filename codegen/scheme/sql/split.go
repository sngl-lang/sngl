package sql

import (
	"strings"
	"unicode"
)

// split is a schema file's statements, each as written without its closing
// semicolon. A semicolon ends a statement only outside a string, a quoted
// name, a comment, PostgreSQL's dollar quoting and a `BEGIN … END` or
// `CASE … END` block -- a trigger's body holds statements of its own. A
// statement of comments alone is no statement, and the comments before one
// are not part of it.
//
// sqlc's own parsers are not asked: SQLite's leaves out what it does not
// model, a trigger or an index, which the database still needs.
func split(text string) []string {
	var out []string
	start, depth := 0, 0
	emit := func(end int) {
		if s := strings.TrimSpace(skipComments(text[start:end])); s != "" {
			out = append(out, s)
		}
	}
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = skipQuoted(text, i, c)
		case c == '[':
			if j := strings.IndexByte(text[i:], ']'); j >= 0 {
				i += j + 1
			} else {
				i = len(text)
			}
		case c == '-' && strings.HasPrefix(text[i:], "--"), c == '#' && (i == 0 || text[i-1] == '\n'):
			if j := strings.IndexByte(text[i:], '\n'); j >= 0 {
				i += j + 1
			} else {
				i = len(text)
			}
		case c == '/' && strings.HasPrefix(text[i:], "/*"):
			if j := strings.Index(text[i+2:], "*/"); j >= 0 {
				i += j + 4
			} else {
				i = len(text)
			}
		case c == '$':
			i = skipDollar(text, i)
		case c == ';':
			if depth == 0 {
				emit(i)
				start = i + 1
			}
			i++
		case isWordStart(c) && (i == 0 || !isWord(text[i-1])):
			j := i
			for j < len(text) && isWord(text[j]) {
				j++
			}
			switch strings.ToUpper(text[i:j]) {
			case "BEGIN":
				if !transaction(text[j:]) {
					depth++
				}
			case "CASE":
				depth++
			case "END":
				if depth > 0 {
					depth--
				}
			}
			i = j
		default:
			i++
		}
	}
	emit(len(text))
	return out
}

// transaction reports whether the text after a BEGIN makes it a
// transaction's start rather than a block's.
func transaction(rest string) bool {
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	if rest == "" || rest[0] == ';' {
		return true
	}
	j := 0
	for j < len(rest) && isWord(rest[j]) {
		j++
	}
	switch strings.ToUpper(rest[:j]) {
	case "TRANSACTION", "WORK", "DEFERRED", "IMMEDIATE", "EXCLUSIVE", "ISOLATION", "READ":
		return true
	}
	return false
}

func skipQuoted(text string, i int, q byte) int {
	for j := i + 1; j < len(text); j++ {
		switch {
		case text[j] == '\\' && q != '`':
			j++
		case text[j] == q:
			if j+1 < len(text) && text[j+1] == q {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(text)
}

// skipDollar passes PostgreSQL's dollar quoting, `$$ … $$` or `$tag$ … $tag$`;
// a `$1` placeholder or a lone `$` is passed as itself.
func skipDollar(text string, i int) int {
	j := i + 1
	for j < len(text) && (isWord(text[j]) && !(j == i+1 && text[j] >= '0' && text[j] <= '9')) {
		j++
	}
	if j >= len(text) || text[j] != '$' {
		return i + 1
	}
	tag := text[i : j+1]
	if k := strings.Index(text[j+1:], tag); k >= 0 {
		return j + 1 + k + len(tag)
	}
	return len(text)
}

func isWordStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isWord(c byte) bool { return isWordStart(c) || c >= '0' && c <= '9' }

// skipComments is s after the comments and the space it starts with.
func skipComments(s string) string {
	for {
		s = strings.TrimLeftFunc(s, unicode.IsSpace)
		switch {
		case strings.HasPrefix(s, "--"), strings.HasPrefix(s, "#"):
			j := strings.IndexByte(s, '\n')
			if j < 0 {
				return ""
			}
			s = s[j+1:]
		case strings.HasPrefix(s, "/*"):
			j := strings.Index(s, "*/")
			if j < 0 {
				return ""
			}
			s = s[j+2:]
		default:
			return s
		}
	}
}
