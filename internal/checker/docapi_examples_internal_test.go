package checker

import (
	"strings"
	"testing"

	"duckfam.us/sngl/internal/parser"
)

// PackageSource is parsed once per process and shared, so building the
// examples must copy what it rewrites rather than edit the library's AST.
func TestPackageExamplesLeaveSourceUnchanged(t *testing.T) {
	var before []string
	for _, doc := range PackageSource("ui") {
		before = append(before, parser.Format(doc))
	}
	first := PackageExamples("ui")
	if len(first) == 0 {
		t.Fatal("sngl:ui carries no examples")
	}
	for i, doc := range PackageSource("ui") {
		if got := parser.Format(doc); got != before[i] {
			t.Fatalf("PackageExamples edited the shared source of sngl:ui file %d", i)
		}
	}
	second := PackageExamples("ui")
	for name, srcs := range first {
		if second[name][0] != srcs[0] {
			t.Errorf("%s: a second build differs:\n%s\n---\n%s", name, srcs[0], second[name][0])
		}
	}
}

func TestPrefixedExamplesReachTheWindow(t *testing.T) {
	for _, tc := range []struct {
		name, imports, window string
	}{
		{"no import", "", `import ui "sngl:ui"`},
		{"qualified", `import ui "sngl:ui"`, "ui.window {"},
		{"aliased", `import u "sngl:ui"`, "u.window {"},
		{"dot", `import . "sngl:ui"`, "\nwindow {"},
	} {
		src := tc.imports + "\n\ncomponent _example_thing node {\n    text(value=\"x\")\n}\n"
		doc, err := parser.Parse("t.sngl", []byte(src))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := PrefixedExamples(doc)["thing"]
		if !strings.Contains(got, tc.window) {
			t.Errorf("%s: want %q in\n%s", tc.name, tc.window, got)
		}
		if _, err := parser.Parse("out.sngl", []byte(got)); err != nil {
			t.Errorf("%s: output does not parse: %v\n%s", tc.name, err, got)
		}
	}
}

func TestExampleProgramQualifiesNestedNodes(t *testing.T) {
	doc, err := parser.Parse("t.sngl", []byte(`component _example_card node {
    vbox {
        if true {
            card()
        } else {
            card {
                badge()
            }
        }
        for var x = [1, 2] {
            card()
        }
        vbox {
            badge()
        }
    }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	comp := prefixedExampleDecls(doc)["card"]
	imports, ui := withUI(nil)
	got := exampleProgram(imports, ui, comp, "p", map[string]bool{"card": true, "badge": true})
	for _, bare := range []string{" card(", " card {", " badge("} {
		if strings.Contains(got, bare) {
			t.Errorf("%q left unqualified:\n%s", strings.TrimSpace(bare), got)
		}
	}
	if n := strings.Count(got, "p.card"); n != 3 {
		t.Errorf("want 3 p.card, got %d:\n%s", n, got)
	}
	if n := strings.Count(got, "p.badge"); n != 2 {
		t.Errorf("want 2 p.badge, got %d:\n%s", n, got)
	}
	if strings.Contains(parser.Format(doc), "p.card") {
		t.Error("exampleProgram edited the document it was given")
	}
}
