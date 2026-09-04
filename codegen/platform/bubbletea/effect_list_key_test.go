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

// `effect<T = struct {}>` constrains T to nothing, so `on` may be a list, a map
// or a struct -- and `==` on one of those does not mean the same thing on every
// target. Go compares a struct field by field and will not compile `!=` on a
// slice at all; JS compares two objects by identity, so a freshly built one is
// never the one already running.
const effectListKeySrc = `
import . "sngl:ui"

component main {
    var (
        tags list<string> = ["a"]
        log list<string> = []
    )

    effect(
        on=tags,
        @mount {
            log.push("m")
        },
        @unmount {
            log.push("u")
        },
    )

    button #bump(text="+", @click {
        tags = ["a"]
    })
    text #out(value=log.join(","))
}
`

// A key the targets do not compare alike is rebuilt on every settle, which is
// the answer passSlotInstances already gives a prop of such a type: wrong the
// same way everywhere beats right on one target and wrong on another.
const effectListKeyDriver = `package ui

import (
	"strings"
	"testing"
)

func TestAListKeyRebuildsOnEverySettle(t *testing.T) {
	m := New()
	if got := strings.Join(m.log, ","); got != "m" {
		t.Fatalf("log after New() = %q, want %q", got, "m")
	}
	// The same elements in a fresh list: equal by content, a different object
	// by identity. Every target ends the lifetime and begins another.
	m.bumpClick()
	if got := strings.Join(m.log, ","); got != "m,u,m" {
		t.Fatalf("log after one settle = %q, want %q", got, "m,u,m")
	}
}
`

// TestAListKeyedEffectCompilesAndRuns generates, builds and RUNS the program.
//
// Before the lowering asked whether the key type compares alike everywhere,
// this emitted `__effect0_live[i] != __effect0_k` over a []string and the Go
// build failed with "slice can only be compared to nil" -- so the failure was
// not a wrong answer but no program at all, and nothing in the suite compiled
// one.
func TestAListKeyedEffectCompilesAndRuns(t *testing.T) {
	doc, err := parser.Parse("t.sngl", []byte(effectListKeySrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var plats []ir.Platform
	if p := codegen.LookupPlatform("bubbletea"); p != nil {
		plats = append(plats, p)
	}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
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
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "bubbletea"}); err != nil {
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

	tmp, err := os.MkdirTemp(".", "bt-effect-listkey-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "model.go"), modelSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "driver_test.go"), []byte(effectListKeyDriver), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "test", "-count=1", ".")
	cmd.Dir = tmp
	combined, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Errorf("running the emitted program failed: %v\n--- output ---\n%s\n--- model.go ---\n%s",
			runErr, combined, modelSrc)
	}
}
