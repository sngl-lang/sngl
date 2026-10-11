package checker_test

import (
	"maps"
	"slices"
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
// the earlier one did not, whose overrides the library has not seen yet. The
// target package owns them: the declaration they override is not written, and
// the earlier check does not see them.
func TestFrozenLibraryTakesANewTarget(t *testing.T) {
	shared := checker.NewSharedLibrary()
	langs, plats := testtargets.Targets()
	var checked []*ir.Package
	for _, plat := range []string{"html", "bubbletea"} {
		doc, err := parser.Parse("main.sngl", []byte("import ui \"sngl:ui\"\n\nui.window(title=\"t\") {\n    ui.text(value=\"hi\")\n}\n"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := &checker.Config{IsMain: true, Languages: langs, Platforms: plats, Targets: []ir.StaticTarget{{Platform: plat}}}
		shared.Use(cfg)
		pkg, diags := checker.Check(doc, cfg)
		if len(diags) > 0 {
			t.Fatalf("%s: %s", plat, diags[0].Error())
		}
		checked = append(checked, pkg)
	}
	text := uiComponent(t, checked[1], "text")
	if len(text.PlatformOverrides) > 0 {
		t.Errorf("a target package wrote its override onto sngl:ui's text: %v", slices.Sorted(maps.Keys(text.PlatformOverrides)))
	}
	for i, want := range [][]string{{"html"}, {"bubbletea", "html"}} {
		got := slices.Sorted(maps.Keys(checked[i].OverridesOf(text).Platform))
		if !slices.Equal(got, want) {
			t.Errorf("check %d sees overrides of text for %v, want %v", i, got, want)
		}
	}
}

// A target package an output block names is loaded while that block is
// checked, in pass2, though the build is for another target. The package is
// the one a later check building for it merges, so it is held to every import
// it bound: loaded with the program's file current, html lost all but the one
// alias the program's file shared with it, and its overrides no longer
// resolved `markup.monospace`.
func TestTargetLoadedByAnOutputBlockKeepsItsImports(t *testing.T) {
	shared := checker.NewSharedLibrary()
	langs, plats := testtargets.Targets()
	for _, run := range []struct {
		src    string
		target ir.StaticTarget
	}{
		{"import ui \"sngl:ui\"\n\noutput {\n    go { bubbletea }\n    none { html }\n}\n\nui.window(title=\"t\") {\n    ui.text(value=\"hi\")\n}\n", ir.StaticTarget{Platform: "bubbletea", Language: "go"}},
		{"import ui \"sngl:ui\"\n\nui.window(title=\"t\") {\n    ui.text(value=\"hi\")\n}\n", ir.StaticTarget{Platform: "html", Language: "none"}},
	} {
		doc, err := parser.Parse("main.sngl", []byte(run.src))
		if err != nil {
			t.Fatal(err)
		}
		cfg := &checker.Config{IsMain: true, Languages: langs, Platforms: plats, Targets: []ir.StaticTarget{run.target}}
		shared.Use(cfg)
		if _, diags := checker.Check(doc, cfg); len(diags) > 0 {
			t.Fatalf("%s: %s", run.target.Platform, diags[0].Error())
		}
	}
}

func uiComponent(t *testing.T, pkg *ir.Package, name string) *ir.Component {
	t.Helper()
	sym, ok := pkg.Symbols.Root.Lookup("ui")
	ns, isNS := sym.(*ir.Namespace)
	if !ok || !isNS {
		t.Fatal("sngl:ui is not imported as ui")
	}
	member, _ := ns.Pkg.Symbols.LookupRootComponent(name)
	comp, isComp := member.(*ir.Component)
	if !isComp {
		t.Fatalf("sngl:ui declares no component %s", name)
	}
	return comp
}

// A check's library is frozen when the check returns, though no other check
// shares it: everything after the check reads it through a copy.
func TestLibraryFrozenWhenItsCheckReturns(t *testing.T) {
	doc, err := parser.Parse("main.sngl", []byte("import ui \"sngl:ui\"\n\nui.window(title=\"t\") {\n    ui.text(value=\"hi\")\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	if len(diags) > 0 {
		t.Fatalf("check: %v", diags[0].Error())
	}
	var ui *ir.Package
	for _, imp := range pkg.Imports {
		if imp.Path == "sngl:ui" {
			ui = imp.Pkg
		}
	}
	if ui == nil {
		t.Fatal("sngl:ui not imported")
	}
	if !ir.IsFrozen(ui) {
		t.Error("sngl:ui is not frozen after the check that loaded it returned")
	}
}
