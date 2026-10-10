package checker_test

import (
	"strings"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testtargets"
	"duckfam.us/sngl/ir"
)

// What a library's load records about its declarations is the library's: a
// check that finds it loaded refuses a call site setting a prop a library
// member inherited, as the check that loaded it does.
func TestLoadedLibraryKeepsInheritedProps(t *testing.T) {
	libDoc, err := parser.Parse("suits.sngl", []byte(`import build "sngl:build"

component suit(name string, color string = "black") build.family

component plain(name string) suit {}
`))
	if err != nil {
		t.Fatal(err)
	}
	shared := checker.NewSharedLibrary()
	for _, run := range []string{"loads the library", "finds it loaded"} {
		doc, err := parser.Parse("main.sngl", []byte(`import suits "sngl:suits"

component table(name string) suits.suit {
    suits.plain(name="a", color="red")
}
`))
		if err != nil {
			t.Fatal(err)
		}
		cfg := &checker.Config{LibSources: map[string][]*ast.Document{"suits": {libDoc}}}
		shared.Use(cfg)
		_, diags := checker.Check(doc, cfg)
		var refused bool
		for _, d := range diags {
			if d.Severity == ir.Error && strings.Contains(d.Msg, `takes "color" from its family suit`) {
				refused = true
			}
		}
		if !refused {
			t.Errorf("the check that %s accepted setting an inherited prop: %v", run, diags)
		}
	}
}

// A check sharing a library that an earlier one froze may build for a target
// the earlier one did not, whose overrides the library has not seen yet.
func TestFrozenLibraryTakesANewTarget(t *testing.T) {
	shared := checker.NewSharedLibrary()
	langs, plats := testtargets.Targets()
	for _, plat := range []string{"html", "bubbletea"} {
		doc, err := parser.Parse("main.sngl", []byte("import ui \"sngl:ui\"\n\nui.window(title=\"t\") {\n    ui.text(value=\"hi\")\n}\n"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := &checker.Config{IsMain: true, Languages: langs, Platforms: plats, Targets: []ir.StaticTarget{{Platform: plat}}}
		shared.Use(cfg)
		if _, diags := checker.Check(doc, cfg); len(diags) > 0 {
			t.Fatalf("%s: %s", plat, diags[0].Error())
		}
	}
}
