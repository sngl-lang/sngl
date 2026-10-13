package build_test

import (
	"strings"
	"testing"

	"duckfam.us/sngl/ast"
	_ "duckfam.us/sngl/codegen/lang"
	_ "duckfam.us/sngl/codegen/platform"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testtargets"
	"duckfam.us/sngl/ir"
)

// What a target package's override declares -- a struct in its body --
// belongs to that package, which is checked as part of loading it, and a
// build reaches it there.
func TestTargetOverrideDeclarationsAreGenerated(t *testing.T) {
	parse := func(name, src string) *ast.Document {
		doc, err := parser.Parse(name, []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	blip := parse("blip.sngl", `import ui "sngl:ui"

component chip(label string, @hit()) ui.node
`)
	override := parse("blipchip.sngl", `import ui "sngl:ui"
import blip "sngl:blip"

const component blip.chip[platform] {
    struct ChipTag {
        label string
    }
    ui.button(text=label, @click {
        for var t = [ChipTag{label=label}, ChipTag{label="x"}] {
            if t.label == "x" {
                hit()
            }
        }
    })
}
`)
	prog := parse("main.sngl", `import ui "sngl:ui"
import blip "sngl:blip"

output {
    go {
        bubbletea()
    }
}

var name = "hi"

ui.window(title="t") {
    blip.chip(label=name)
}
`)
	langs, plats := testtargets.Targets()
	pkg, diags := checker.Check(prog, &checker.Config{
		IsMain:    true,
		Languages: langs,
		Platforms: plats,
		LibSources: map[string][]*ast.Document{
			"blip":               {blip},
			"platform/bubbletea": append(checker.PackageDocsFor("platform/bubbletea"), override),
		},
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	for _, s := range pkg.Structs {
		if s.Name == "ChipTag" {
			t.Errorf("the override's struct was declared in the program, not in sngl:platform/bubbletea")
		}
	}
	results, err := build.Emit(pkg, build.Options{Name: "main.sngl", Dir: t.TempDir(), OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, r := range results {
		for _, f := range r.Files {
			out.Write(f)
		}
	}
	for _, want := range []string{"type ChipTag struct"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the generated code has no %q:\n%s", want, out.String())
		}
	}
}
