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

// effectRunSrc is testdata/effect_on_recreates.sngl with the test funcs
// replaced by a button, so the key changes the way a program changes it: from
// a handler, through Update.
const effectRunSrc = `
import . "sngl:ui"

window {
    var (
        n = 0
        log list<string> = []
    )

    effect(
        on=n,
        @mount {
            log.push("mount")
        },
        @unmount {
            log.push("unmount")
        },
    )

    button #bump(text="+", @click {
        n += 1
    })
    text #out(value=log.join(","))
}
`

// effectRunDriver drives the emitted Model directly rather than through
// tea.NewProgram: the claim is about what the Model does, and a program needs a
// TTY.
//
// The log is read from the field the mount handler writes AND from View(), so
// neither a mount that ran without reaching the render nor a render that
// happens to say "mount" for some other reason passes alone.
const effectRunDriver = `package ui

import (
	"fmt"
	"strings"
	"testing"
)

func logOf(m Model) string { return strings.Join(m.log, ",") }

func TestEffectMountsWhenTheModelIsBuilt(t *testing.T) {
	m := New()
	if got := logOf(m); got != "mount" {
		t.Fatalf("log after New() = %q, want %q", got, "mount")
	}
	if got := fmt.Sprint(m.View().Content); !strings.Contains(got, "mount") {
		t.Errorf("View() = %q, want it to contain the mount the handler logged", got)
	}
}

// A changed key ends the old lifetime before beginning the new one: a pair
// logged mount-then-unmount would leave the program holding what the effect
// was supposed to give back, and a count alone cannot tell the two orders
// apart.
func TestChangingTheKeyRerunsTheBracketInOrder(t *testing.T) {
	m := New()
	m.bumpClick()
	if got := logOf(m); got != "mount,unmount,mount" {
		t.Fatalf("log after one bump = %q, want %q", got, "mount,unmount,mount")
	}
	m.bumpClick()
	if got := logOf(m); got != "mount,unmount,mount,unmount,mount" {
		t.Fatalf("log after two bumps = %q, want %q", got, "mount,unmount,mount,unmount,mount")
	}
}

// The exit handler releases what is still running, so the last bracket the
// program held gets its @unmount.
func TestTeardownEndsTheRunningBracket(t *testing.T) {
	m := New()
	m.__snglTeardown()
	if got := logOf(m); got != "mount,unmount" {
		t.Fatalf("log after teardown = %q, want %q", got, "mount,unmount")
	}
	if got := len(m.__effect0_live); got != 0 {
		t.Errorf("%d brackets still live after teardown, want 0", got)
	}
}
`

// TestEffectMountsAndRekeysInTheEmittedProgram generates the program, builds it
// and RUNS it.
//
// The bubbletea harness otherwise only asks whether the emitted Go compiles,
// which is exactly how this went unnoticed: the lowering appends the initial
// `__effect0_settle()` to the component body, that body becomes View(), and a
// View skips imperative statements -- so the call was emitted, dropped, and no
// effect on this platform ever mounted. The testdata fixtures say nothing about
// it either: the interpreter evaluates their assertions and the interpreter
// does not lower.
func TestEffectMountsAndRekeysInTheEmittedProgram(t *testing.T) {
	doc, err := parser.Parse("t.sngl", []byte(effectRunSrc))
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
	tmp, err := os.MkdirTemp(".", "bt-effect-run-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "model.go"), modelSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "driver_test.go"), []byte(effectRunDriver), 0o644); err != nil {
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
