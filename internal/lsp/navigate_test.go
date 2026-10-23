package lsp

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestDefinitionAcrossSiblings(t *testing.T) {
	s := newSiblingSession(t, map[string]string{"lib.sngl": declaresGreet, "main.sngl": usesGreet})
	s.open("main.sngl", usesGreet)
	s.diagnostics("main.sngl")

	var loc Location
	s.call("textDocument/definition", s.at("main.sngl", 0, 19), &loc)
	want := Location{URI: s.uri("lib.sngl"), Range: Range{Start: Position{0, 5}, End: Position{0, 10}}}
	if loc != want {
		t.Errorf("definition of greet = %+v, want %+v", loc, want)
	}
}

func TestReferencesAcrossSiblings(t *testing.T) {
	main := usesGreet + "func again() => greet(2)\n"
	s := newSiblingSession(t, map[string]string{"lib.sngl": declaresGreet, "main.sngl": main})
	s.open("main.sngl", main)
	s.diagnostics("main.sngl")

	params := s.at("main.sngl", 1, 17)
	params["context"] = map[string]any{"includeDeclaration": true}
	var locs []Location
	s.call("textDocument/references", params, &locs)
	var got []string
	for _, l := range locs {
		got = append(got, fmt.Sprintf("%s:%d:%d", l.URI[strings.LastIndex(l.URI, "/")+1:], l.Range.Start.Line, l.Range.Start.Character))
	}
	if want := "lib.sngl:0:5 main.sngl:0:18 main.sngl:1:16"; strings.Join(got, " ") != want {
		t.Errorf("references to greet = %v, want %s", got, want)
	}

	params["context"] = map[string]any{"includeDeclaration": false}
	s.call("textDocument/references", params, &locs)
	if len(locs) != 2 {
		t.Errorf("references without the declaration = %+v, want the two uses", locs)
	}
}

// A library declaration is embedded in the binary, so its definition is a
// read-only copy written where the editor can open it.
func TestDefinitionInTheLibrary(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	src := "import math \"sngl:math\"\n\nfunc tau() => math.pi * 2\n"
	s := newSiblingSession(t, map[string]string{"main.sngl": src})
	s.open("main.sngl", src)
	s.diagnostics("main.sngl")

	var loc Location
	s.call("textDocument/definition", s.at("main.sngl", 2, 20), &loc)
	if !strings.HasSuffix(loc.URI, "/sngl/lib/math/math.sngl") {
		t.Fatalf("definition of math.pi = %+v, want the library's math.sngl", loc)
	}
	path := uriToPath(loc.URI)
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Split(string(text), "\n")[loc.Range.Start.Line]
	if got := line[loc.Range.Start.Character:loc.Range.End.Character]; got != "pi" {
		t.Errorf("definition range covers %q in %q, want pi", got, line)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Errorf("library copy is writable: %v", info.Mode())
	}
}
