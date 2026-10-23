package lsp

import (
	"slices"
	"strings"
	"testing"

	"duckfam.us/sngl/internal/lspcore"
	"duckfam.us/sngl/internal/parser"
)

// apply makes LSP edits in text, as an editor would.
func apply(text string, edits []TextEdit) string {
	edits = slices.Clone(edits)
	slices.SortFunc(edits, func(a, b TextEdit) int {
		if a.Range.Start.Line != b.Range.Start.Line {
			return b.Range.Start.Line - a.Range.Start.Line
		}
		return b.Range.Start.Character - a.Range.Start.Character
	})
	lines := strings.SplitAfter(text, "\n")
	offset := func(p Position) int {
		n := 0
		for i := 0; i < p.Line && i < len(lines); i++ {
			n += len(lines[i])
		}
		if p.Line < len(lines) {
			l := strings.TrimSuffix(lines[p.Line], "\n")
			n += len(string([]rune(l)[:lspcore.RuneColumn(l, p.Character)]))
		}
		return n
	}
	for _, e := range edits {
		text = text[:offset(e.Range.Start)] + e.NewText + text[offset(e.Range.End):]
		lines = strings.SplitAfter(text, "\n")
	}
	return text
}

func TestFormatting(t *testing.T) {
	src := "const a=1\n\nfunc f( x int )=>x+a\n"
	s := newSiblingSession(t, map[string]string{"main.sngl": src})
	s.open("main.sngl", src)
	s.diagnostics("main.sngl")
	doc := map[string]any{"textDocument": map[string]any{"uri": s.uri("main.sngl")}}

	var edits []TextEdit
	s.call("textDocument/formatting", doc, &edits)
	parsed, err := parser.Parse("main.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := parser.Format(parsed)
	if got := apply(src, edits); got != want {
		t.Errorf("formatted:\n%s\nwant:\n%s", got, want)
	}
	if len(edits) != 1 || edits[0].Range.Start.Line != 0 {
		t.Errorf("edits = %+v, want one from the first changed line", edits)
	}

	s.change("main.sngl", want)
	s.diagnostics("main.sngl")
	s.call("textDocument/formatting", doc, &edits)
	if len(edits) != 0 {
		t.Errorf("a formatted file gets edits %+v", edits)
	}

	s.change("main.sngl", "func (\n")
	s.diagnostics("main.sngl")
	if msg := s.callErr("textDocument/formatting", doc, &edits); !strings.Contains(msg, "does not parse") {
		t.Errorf("formatting a file that does not parse answered %q", msg)
	}
}

func TestLineEditKeepsWhatIsShared(t *testing.T) {
	for _, c := range []struct{ old, new string }{
		{"a\nb\nc\n", "a\nB\nc\n"},
		{"a\nb", "a\nb\n"},
		{"a\nb\n", "a\n"},
		{"x\n", "x\ny\nz\n"},
		{"😀 a\nb\n", "😀 a\nc\n"},
	} {
		edits := lineEdit(c.old, c.new)
		if got := apply(c.old, edits); got != c.new {
			t.Errorf("lineEdit(%q, %q) makes %q", c.old, c.new, got)
		}
	}
}

// renamed applies a rename's edits to the files they name, read from files.
func renamed(t *testing.T, s *siblingSession, files map[string]string, we WorkspaceEdit) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name, text := range files {
		out[name] = apply(text, we.Changes[s.uri(name)])
	}
	return out
}

func renameAt(name string, line, char int, newName string, s *siblingSession) map[string]any {
	p := s.at(name, line, char)
	p["newName"] = newName
	return p
}

func TestRenameAcrossSiblings(t *testing.T) {
	main := usesGreet + "func again() => greet(2)\n"
	files := map[string]string{"lib.sngl": declaresGreet, "main.sngl": main}
	s := newSiblingSession(t, files)
	s.open("main.sngl", main)
	s.diagnostics("main.sngl")

	var we WorkspaceEdit
	s.call("textDocument/rename", renameAt("main.sngl", 1, 17, "hello", s), &we)
	got := renamed(t, s, files, we)
	if want := "func hello(n int) => n + 1\n"; got["lib.sngl"] != want {
		t.Errorf("lib.sngl:\n%s\nwant:\n%s", got["lib.sngl"], want)
	}
	if want := "func welcome() => hello(1)\nfunc again() => hello(2)\n"; got["main.sngl"] != want {
		t.Errorf("main.sngl:\n%s\nwant:\n%s", got["main.sngl"], want)
	}
}

func TestRenameWritesOutAShorthandField(t *testing.T) {
	src := `struct P {
    a int
}

func make(a int) => P{a}
`
	files := map[string]string{"main.sngl": src}
	s := newSiblingSession(t, files)
	s.open("main.sngl", src)
	s.diagnostics("main.sngl")

	var we WorkspaceEdit
	s.call("textDocument/rename", renameAt("main.sngl", 4, 10, "b", s), &we)
	if got, want := renamed(t, s, files, we)["main.sngl"], strings.Replace(src, "make(a int) => P{a}", "make(b int) => P{a=b}", 1); got != want {
		t.Errorf("renaming the parameter:\n%s\nwant:\n%s", got, want)
	}

	s.call("textDocument/rename", renameAt("main.sngl", 1, 4, "c", s), &we)
	want := strings.Replace(strings.Replace(src, "    a int", "    c int", 1), "P{a}", "P{c=a}", 1)
	if got := renamed(t, s, files, we)["main.sngl"]; got != want {
		t.Errorf("renaming the field:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenameRefuses(t *testing.T) {
	src := "import math \"sngl:math\"\n\nfunc tau() => math.pi * 2\n\nfunc other() => 1\n"
	s := newSiblingSession(t, map[string]string{"main.sngl": src})
	s.open("main.sngl", src)
	s.diagnostics("main.sngl")

	var we WorkspaceEdit
	for _, c := range []struct {
		params map[string]any
		want   string
	}{
		{renameAt("main.sngl", 2, 20, "e", s), "declared in the library"},
		{renameAt("main.sngl", 2, 6, "other", s), "would leave an error"},
		{renameAt("main.sngl", 2, 6, "func", s), "is not a name"},
		{renameAt("main.sngl", 2, 6, "2x", s), "is not a name"},
	} {
		if msg := s.callErr("textDocument/rename", c.params, &we); !strings.Contains(msg, c.want) {
			t.Errorf("rename to %v answered %q, want %q", c.params["newName"], msg, c.want)
		}
	}

	var r Range
	if msg := s.callErr("textDocument/prepareRename", s.at("main.sngl", 2, 20), &r); !strings.Contains(msg, "declared in the library") {
		t.Errorf("prepareRename on math.pi answered %q", msg)
	}
	s.call("textDocument/prepareRename", s.at("main.sngl", 2, 6), &r)
	if r != (Range{Start: Position{2, 5}, End: Position{2, 8}}) {
		t.Errorf("prepareRename on tau = %+v", r)
	}
}
