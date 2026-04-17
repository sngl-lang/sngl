package highlight

import (
	"bytes"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Terminal applies chroma syntax highlighting for terminal (true-colour)
// output. Returns the original text unchanged on any error.
//
// The special lexer name "spew" maps to the Go lexer (spew output is
// Go-like struct dumps).
func Terminal(text, lexerName, styleName string) string {
	name := lexerName
	if name == "spew" {
		name = "Go"
	}
	l := lexers.Get(name)
	if l == nil {
		return text
	}
	l = chroma.Coalesce(l)
	s := styles.Get(styleName)
	if s == nil {
		s = styles.Fallback
	}
	it, err := l.Tokenise(nil, text)
	if err != nil {
		return text
	}
	var buf bytes.Buffer
	if err := formatters.TTY16m.Format(&buf, s, it); err != nil {
		return text
	}
	return buf.String()
}
