package bubbletea

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// nameCollisionSrc is testdata/func_nested_name_taken.txtar's program: a func
// nested in a *top-level* func, whose hoisted `step__mark` is the name a struct
// beside it already holds.
const nameCollisionSrc = `
import . "sngl:ui"

struct step__mark {
    a int
}

func step(a int) int {
    func mark(k int) int {
        if k <= 0 {
            return 0
        }
        return mark(k - 1) + k
    }
    return mark(a)
}

component main node {
    var n int = 0
    var keep step__mark = step__mark{a=1}

    text(value="{n}{keep.a}")
    button(text="go", @click {
        n = step(n) + keep.a
    })
}

window {
    main
}
`

// TestGeneratedGoWithACollidingNestedFuncCompiles builds the emitted program.
//
// TestFixtures parses the generated Go and stops there, which is exactly what
// let this through: `type Step__mark` and `func Step__mark` in one file is a
// redeclaration, and a redeclaration parses. The golden shows the two names are
// distinct; only a compiler says they have to be.
func TestGeneratedGoWithACollidingNestedFuncCompiles(t *testing.T) {
	doc, err := parser.Parse("t.sngl", []byte(nameCollisionSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	var plats []ir.Platform
	if p := codegen.LookupPlatform("bubbletea"); p != nil {
		plats = append(plats, p)
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: plats,
		Languages: []ir.Language{lang},
		Targets:   []ir.StaticTarget{{Platform: "bubbletea", Language: "go"}},
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s: %s", d.Pos, d.Msg)
		}
	}
	g := &Generator{}
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "bubbletea", Language: "go"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	modelSrc, ok := mem.Files()["model.go"]
	if !ok {
		t.Fatalf("model.go not among generated files %v", mem.Files())
	}

	// Under the main module, so charm.land/bubbletea resolves through the
	// project's own go.mod.
	tmp, err := os.MkdirTemp(".", "bt-name-collision-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "model.go"), modelSrc, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "build", ".")
	cmd.Dir = tmp
	out, buildErr := cmd.CombinedOutput()
	if buildErr != nil {
		t.Errorf("the emitted program does not compile: %v\n--- output ---\n%s\n--- model.go ---\n%s",
			buildErr, out, modelSrc)
	}
}
