package parser

import (
	"strings"
	"testing"
)

// Every ILLEGAL token comes with a recorded lex error. encode() drops ILLEGAL
// from the stream the parser sees, so one built without an error is input the
// compiler rejected and then said nothing about -- the parse carries on past it
// and fails somewhere else, or succeeds.
//
// tok() panics on ILLEGAL to keep illegal() the only builder. This asserts the
// invariant from the outside: every input that produces an ILLEGAL token
// produces an error naming its position.
func TestEveryIllegalTokenIsReported(t *testing.T) {
	for _, src := range []string{
		`var x string = "hello`,
		"var x string = \"\"\"hello",
		"var x = `raw",
		"/* unclosed",
		"var x = §",
		"var x = #",
		`var x = $"{n, plural, one{`,
	} {
		t.Run(strings.TrimSpace(src), func(t *testing.T) {
			toks, errs := Tokenize(src)
			var illegal []Token
			for _, tk := range toks {
				if tk.Type == ILLEGAL {
					illegal = append(illegal, tk)
				}
			}
			if len(illegal) == 0 {
				return // lexed cleanly; the parser's problem, not this test's
			}
			if len(errs) == 0 {
				t.Fatalf("%d ILLEGAL token(s) and no lex error: %q", len(illegal), src)
			}
			for _, tk := range illegal {
				want := "" +
					itoa(tk.Line) + ":" + itoa(tk.Column) + ":"
				if !strings.Contains(strings.Join(errs, "\n"), want) {
					t.Errorf("no error at %s for ILLEGAL %q; errors: %v", want, tk.Literal, errs)
				}
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
