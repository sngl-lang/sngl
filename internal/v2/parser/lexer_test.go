package parser

import "testing"

type tokExpect struct {
	typ TokenType
	lit string
}

func tokenize(src string) []Token {
	toks, _ := Tokenize(src)
	// Strip EOF and trailing ASI semicolon
	var out []Token
	for _, t := range toks {
		if t.Type == EOF {
			break
		}
		out = append(out, t)
	}
	// Remove trailing semicolon (ASI at EOF)
	if len(out) > 0 && out[len(out)-1].Type == SEMICOLON {
		out = out[:len(out)-1]
	}
	return out
}

func assertTokens(t *testing.T, src string, want []tokExpect) {
	t.Helper()
	got := tokenize(src)
	if len(got) != len(want) {
		t.Errorf("Tokenize(%q): got %d tokens, want %d", src, len(got), len(want))
		for i, g := range got {
			t.Logf("  [%d] type=0x%02x lit=%q", i, g.Type, g.Literal)
		}
		return
	}
	for i := range want {
		if got[i].Type != want[i].typ || got[i].Literal != want[i].lit {
			t.Errorf("Tokenize(%q)[%d]: got (0x%02x, %q), want (0x%02x, %q)",
				src, i, got[i].Type, got[i].Literal, want[i].typ, want[i].lit)
		}
	}
}

func TestStringFull(t *testing.T) {
	assertTokens(t, `"hello"`, []tokExpect{
		{STR_FULL, "hello"},
	})
	assertTokens(t, `""`, []tokExpect{
		{STR_FULL, ""},
	})
	assertTokens(t, `"hello world"`, []tokExpect{
		{STR_FULL, "hello world"},
	})
}

func TestStringEscapedBrace(t *testing.T) {
	assertTokens(t, `"price \{100}"`, []tokExpect{
		{STR_FULL, "price {100}"},
	})
	assertTokens(t, `"\{}\{}"`, []tokExpect{
		{STR_FULL, "{}{}"},
	})
}

func TestStringSimpleInterpolation(t *testing.T) {
	assertTokens(t, `"hello {name}"`, []tokExpect{
		{STR_START, "hello "},
		{IDENT, "name"},
		{STR_END, ""},
	})
	assertTokens(t, `"{name} world"`, []tokExpect{
		{STR_START, ""},
		{IDENT, "name"},
		{STR_END, " world"},
	})
	assertTokens(t, `"{x}"`, []tokExpect{
		{STR_START, ""},
		{IDENT, "x"},
		{STR_END, ""},
	})
	assertTokens(t, `"a {x} b"`, []tokExpect{
		{STR_START, "a "},
		{IDENT, "x"},
		{STR_END, " b"},
	})
}

func TestStringMultipleInterpolations(t *testing.T) {
	assertTokens(t, `"{a} and {b}"`, []tokExpect{
		{STR_START, ""},
		{IDENT, "a"},
		{STR_RESUME, " and "},
		{IDENT, "b"},
		{STR_END, ""},
	})
	assertTokens(t, `"{a}{b}"`, []tokExpect{
		{STR_START, ""},
		{IDENT, "a"},
		{STR_RESUME, ""},
		{IDENT, "b"},
		{STR_END, ""},
	})
}

func TestStringComplexExpressions(t *testing.T) {
	assertTokens(t, `"sum is {a + b}"`, []tokExpect{
		{STR_START, "sum is "},
		{IDENT, "a"},
		{PLUS, "+"},
		{IDENT, "b"},
		{STR_END, ""},
	})
	assertTokens(t, `"{obj.field}"`, []tokExpect{
		{STR_START, ""},
		{IDENT, "obj"},
		{DOT, "."},
		{IDENT, "field"},
		{STR_END, ""},
	})
}

func TestStringNestedBraces(t *testing.T) {
	// Struct literal inside interpolation
	assertTokens(t, `"{Foo{x: 1}}"`, []tokExpect{
		{STR_START, ""},
		{IDENT, "Foo"},
		{LBRACE, "{"},
		{IDENT, "x"},
		{COLON, ":"},
		{INT, "1"},
		{RBRACE, "}"},
		{STR_END, ""},
	})
}

func TestStringNestedStringInterpolation(t *testing.T) {
	assertTokens(t, `"outer {f("inner")} end"`, []tokExpect{
		{STR_START, "outer "},
		{IDENT, "f"},
		{LPAREN, "("},
		{STR_FULL, "inner"},
		{RPAREN, ")"},
		{STR_END, " end"},
	})
	// Nested interpolation within nested string
	assertTokens(t, `"a {f("b {x} c")} d"`, []tokExpect{
		{STR_START, "a "},
		{IDENT, "f"},
		{LPAREN, "("},
		{STR_START, "b "},
		{IDENT, "x"},
		{STR_END, " c"},
		{RPAREN, ")"},
		{STR_END, " d"},
	})
}

func TestTripleFull(t *testing.T) {
	assertTokens(t, `"""hello"""`, []tokExpect{
		{TRIPLE_FULL, "hello"},
	})
}

func TestTripleInterpolation(t *testing.T) {
	assertTokens(t, `"""hello {x}"""`, []tokExpect{
		{TRIPLE_START, "hello "},
		{IDENT, "x"},
		{TRIPLE_END, ""},
	})
	assertTokens(t, `"""{a} and {b}"""`, []tokExpect{
		{TRIPLE_START, ""},
		{IDENT, "a"},
		{STR_RESUME, " and "},
		{IDENT, "b"},
		{TRIPLE_END, ""},
	})
}

func TestRawStringNoInterpolation(t *testing.T) {
	assertTokens(t, "`hello {x}`", []tokExpect{
		{RAW_STRING, "hello {x}"},
	})
}

func TestASI(t *testing.T) {
	// STR_FULL triggers ASI
	toks := tokenize("\"hello\"\n\"world\"")
	if len(toks) != 3 {
		t.Fatalf("expected 3 tokens, got %d", len(toks))
	}
	if toks[1].Type != SEMICOLON {
		t.Errorf("expected SEMICOLON between strings, got 0x%02x", toks[1].Type)
	}

	// STR_END triggers ASI
	toks = tokenize("\"{x}\"\n\"y\"")
	found := false
	for _, tok := range toks {
		if tok.Type == SEMICOLON {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected SEMICOLON after interpolated string")
	}
}
