package parser

import "testing"

// Token literals are substrings of the source addressed by rune offsets, so
// every multi-byte rune before a token shifts its bytes. A document with any
// non-ASCII in it — an em dash in a comment is enough — exercises that
// mapping for every identifier after it.
func TestLexer_LiteralsAfterMultiByteRunes(t *testing.T) {
	src := "// — an em dash, then → an arrow\n" +
		"component widget {\n" +
		"    var label = \"café naïve\"\n" +
		"    // ← another\n" +
		"    var plain = 2\n" +
		"}\n"

	toks, errs := Tokenize(src)
	if len(errs) != 0 {
		t.Fatalf("lex errors: %v", errs)
	}

	var idents, comments, strs []string
	for _, tok := range toks {
		switch tok.Type {
		case IDENT:
			idents = append(idents, tok.Literal)
		case LINE_COMMENT:
			comments = append(comments, tok.Literal)
		case STR_FULL:
			strs = append(strs, tok.Literal)
		}
	}

	// Identifiers are ASCII by construction, but they sit after multi-byte
	// runes here, so their offsets are what the mapping has to get right.
	wantIdents := []string{"widget", "label", "plain"}
	if len(idents) != len(wantIdents) {
		t.Fatalf("idents = %q, want %q", idents, wantIdents)
	}
	for i, want := range wantIdents {
		if idents[i] != want {
			t.Errorf("ident %d = %q, want %q", i, idents[i], want)
		}
	}

	wantComments := []string{
		"// — an em dash, then → an arrow",
		"// ← another",
	}
	if len(comments) != len(wantComments) {
		t.Fatalf("comments = %q, want %q", comments, wantComments)
	}
	for i, want := range wantComments {
		if comments[i] != want {
			t.Errorf("comment %d = %q, want %q", i, comments[i], want)
		}
	}

	if len(strs) != 1 || strs[0] != "café naïve" {
		t.Errorf("strings = %q, want [%q]", strs, "café naïve")
	}
}

// byteOff must agree with a plain rune-slice walk at every offset, including
// the one past the end that text() uses as an exclusive bound.
func TestLexer_ByteOffMatchesRuneWalk(t *testing.T) {
	for _, src := range []string{
		"plain ascii only",
		"— leading wide",
		"trailing wide —",
		"a—b—c",
		"",
		"🙂 astral plane 🙂",
	} {
		l := newLexer(src)
		want := 0
		for i, r := range src {
			if got := l.byteOff(want); got != i {
				t.Errorf("%q: byteOff(%d) = %d, want %d", src, want, got, i)
			}
			want++
			_ = r
		}
		if got := l.byteOff(want); got != len(src) {
			t.Errorf("%q: byteOff(%d) = %d, want %d (end)", src, want, got, len(src))
		}
		// And the whole-source slice must round-trip.
		if got := l.text(0, len(l.input)); got != src {
			t.Errorf("%q: text(0, len) = %q", src, got)
		}
	}
}
