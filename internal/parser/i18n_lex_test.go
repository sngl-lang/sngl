package parser

import "testing"

func TestLexI18nFullString(t *testing.T) {
	l := newLexer(`$"Login"`)
	tok := l.NextToken()
	if tok.Type != I18N_STR_FULL {
		t.Errorf("Type = %v, want I18N_STR_FULL", tok.Type)
	}
	if tok.Literal != "Login" {
		t.Errorf("Literal = %q, want Login", tok.Literal)
	}
}

func TestLexI18nInterpolation(t *testing.T) {
	l := newLexer(`$"Hello {name}!"`)
	var types []TokenType
	for {
		tok := l.NextToken()
		if tok.Type == EOF || tok.Type == SEMICOLON {
			break
		}
		types = append(types, tok.Type)
	}
	// Expect: I18N_STR_START "Hello " then IDENT "name" then I18N_STR_END "!"
	want := []TokenType{I18N_STR_START, IDENT, I18N_STR_END}
	if !sameTokenTypes(types, want) {
		t.Errorf("got %v, want %v", types, want)
	}
}

func TestLexI18nInterpolationLiterals(t *testing.T) {
	l := newLexer(`$"Hello {name}!"`)
	var toks []Token
	for {
		tok := l.NextToken()
		if tok.Type == EOF || tok.Type == SEMICOLON {
			break
		}
		toks = append(toks, tok)
	}
	if len(toks) != 3 {
		t.Fatalf("got %d tokens, want 3", len(toks))
	}
	if toks[0].Literal != "Hello " {
		t.Errorf("toks[0].Literal = %q, want \"Hello \"", toks[0].Literal)
	}
	if toks[1].Literal != "name" {
		t.Errorf("toks[1].Literal = %q, want \"name\"", toks[1].Literal)
	}
	if toks[2].Literal != "!" {
		t.Errorf("toks[2].Literal = %q, want \"!\"", toks[2].Literal)
	}
}

func TestLexI18nTriple(t *testing.T) {
	l := newLexer(`$"""hello"""`)
	tok := l.NextToken()
	if tok.Type != I18N_TRIPLE_FULL {
		t.Errorf("Type = %v, want I18N_TRIPLE_FULL", tok.Type)
	}
	if tok.Literal != "hello" {
		t.Errorf("Literal = %q, want \"hello\"", tok.Literal)
	}
}

func TestLexI18nTripleInterp(t *testing.T) {
	l := newLexer(`$"""x{name}y"""`)
	var types []TokenType
	for {
		tok := l.NextToken()
		if tok.Type == EOF || tok.Type == SEMICOLON {
			break
		}
		types = append(types, tok.Type)
	}
	want := []TokenType{I18N_TRIPLE_START, IDENT, I18N_TRIPLE_END}
	if !sameTokenTypes(types, want) {
		t.Errorf("got %v, want %v", types, want)
	}
}

func TestLexI18nMultipleInterpolations(t *testing.T) {
	// $"Hello {first} {last}!" → START "Hello " IDENT "first" RESUME " " IDENT "last" END "!"
	l := newLexer(`$"Hello {first} {last}!"`)
	var types []TokenType
	for {
		tok := l.NextToken()
		if tok.Type == EOF || tok.Type == SEMICOLON {
			break
		}
		types = append(types, tok.Type)
	}
	want := []TokenType{I18N_STR_START, IDENT, I18N_STR_RESUME, IDENT, I18N_STR_END}
	if !sameTokenTypes(types, want) {
		t.Errorf("got %v, want %v", types, want)
	}
}

func TestLexI18nNoInterpolation(t *testing.T) {
	// $"just text" with no {} should produce I18N_STR_FULL
	l := newLexer(`$"just text"`)
	tok := l.NextToken()
	if tok.Type != I18N_STR_FULL {
		t.Errorf("Type = %v, want I18N_STR_FULL", tok.Type)
	}
	if tok.Literal != "just text" {
		t.Errorf("Literal = %q, want \"just text\"", tok.Literal)
	}
}

func TestLexI18nEscapeSequences(t *testing.T) {
	l := newLexer(`$"line\nnewline"`)
	tok := l.NextToken()
	if tok.Type != I18N_STR_FULL {
		t.Errorf("Type = %v, want I18N_STR_FULL", tok.Type)
	}
	if tok.Literal != "line\nnewline" {
		t.Errorf("Literal = %q, want \"line\\nnewline\"", tok.Literal)
	}
}

func TestLexNonTranslatableStillWorks(t *testing.T) {
	l := newLexer(`"Login"`)
	tok := l.NextToken()
	if tok.Type != STR_FULL {
		t.Errorf("Type = %v, want STR_FULL", tok.Type)
	}
}

func TestLexNonTranslatableInterpStillWorks(t *testing.T) {
	l := newLexer(`"Hello {name}!"`)
	var types []TokenType
	for {
		tok := l.NextToken()
		if tok.Type == EOF || tok.Type == SEMICOLON {
			break
		}
		types = append(types, tok.Type)
	}
	want := []TokenType{STR_START, IDENT, STR_END}
	if !sameTokenTypes(types, want) {
		t.Errorf("got %v, want %v", types, want)
	}
}

func TestLexNonTranslatableTripleStillWorks(t *testing.T) {
	l := newLexer(`"""hello"""`)
	tok := l.NextToken()
	if tok.Type != TRIPLE_FULL {
		t.Errorf("Type = %v, want TRIPLE_FULL", tok.Type)
	}
}

func TestLexI18nPluralLiteralCases(t *testing.T) {
	// $"You have {count, plural, one{message} other{messages}}"
	src := `$"You have {count, plural, one{message} other{messages}}"`
	l := newLexer(src)
	var types []TokenType
	var literals []string
	for {
		tok := l.NextToken()
		if tok.Type == EOF || tok.Type == SEMICOLON {
			break
		}
		types = append(types, tok.Type)
		literals = append(literals, tok.Literal)
	}
	want := []TokenType{
		I18N_STR_START,             // "You have "
		IDENT, COMMA, IDENT, COMMA, // count, plural,
		IDENT, I18N_CASE_FULL,      // one{message}
		IDENT, I18N_CASE_FULL,      // other{messages}
		I18N_STR_END,               // ""
	}
	if !sameTokenTypes(types, want) {
		t.Errorf("got types %v\nwant     %v", types, want)
	}
	// Check literals for the interesting tokens.
	if len(types) == len(want) {
		if literals[0] != "You have " {
			t.Errorf("I18N_STR_START literal = %q, want \"You have \"", literals[0])
		}
		if literals[5] != "one" {
			t.Errorf("first case ident = %q, want \"one\"", literals[5])
		}
		if literals[6] != "message" {
			t.Errorf("first I18N_CASE_FULL literal = %q, want \"message\"", literals[6])
		}
		if literals[7] != "other" {
			t.Errorf("second case ident = %q, want \"other\"", literals[7])
		}
		if literals[8] != "messages" {
			t.Errorf("second I18N_CASE_FULL literal = %q, want \"messages\"", literals[8])
		}
	}
}

func TestLexI18nPluralEqualNSelector(t *testing.T) {
	// $"{count, plural, =0{none} other{some}}"
	src := `$"{count, plural, =0{none} other{some}}"`
	l := newLexer(src)
	var types []TokenType
	for {
		tok := l.NextToken()
		if tok.Type == EOF || tok.Type == SEMICOLON {
			break
		}
		types = append(types, tok.Type)
	}
	want := []TokenType{
		I18N_STR_START,
		IDENT, COMMA, IDENT, COMMA,
		ASSIGN, INT, I18N_CASE_FULL, // =0{none}
		IDENT, I18N_CASE_FULL,       // other{some}
		I18N_STR_END,
	}
	if !sameTokenTypes(types, want) {
		t.Errorf("got  %v\nwant %v", types, want)
	}
}

func TestLexI18nNestedPlaceholderInCase(t *testing.T) {
	// $"{count, plural, one{1 file} other{# files}}"
	// '#' inside a case body is literal text (ICU number placeholder), not a SNGL token.
	src := `$"{count, plural, one{1 file} other{# files}}"`
	l := newLexer(src)
	var types []TokenType
	var literals []string
	for {
		tok := l.NextToken()
		if tok.Type == EOF || tok.Type == SEMICOLON {
			break
		}
		types = append(types, tok.Type)
		literals = append(literals, tok.Literal)
	}
	// Find an I18N_CASE_FULL whose literal is "# files".
	found := false
	for i, ty := range types {
		if ty == I18N_CASE_FULL && literals[i] == "# files" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected I18N_CASE_FULL with '# files'; got types=%v literals=%v", types, literals)
	}
}

func sameTokenTypes(a, b []TokenType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
