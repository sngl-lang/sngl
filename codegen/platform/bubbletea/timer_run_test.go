package bubbletea

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// timerRunSrc has a timer in `main` and one in a component, so the run asserts
// both that a tick reaches Update and that a timer written somewhere other than
// the root is emitted at all. The second was collected by nobody: the analysis
// read `pkg.Timers` plus `main`'s.
const timerRunSrc = `
import . "sngl:ui"
import . "sngl:time"

component beat(label = "") node {
    var beats = 0
    timer(interval=10ms, enabled=true, @tick { beats += 1 })
    text(value="{label} {beats}")
}

window {
    var (
        seconds = 0
        running = true
    )
    timer(interval=10ms, enabled=running, @tick { seconds += 1 })
    beat(label="b")
    text(value=string(seconds))
}
`

// The driver sends each tick message through Update, which is the only place a
// bubbletea model changes: a value receiver means anything a callback did to it
// would be thrown away, and that is why a timer here is a Msg and not a
// scheduled func.
const timerRunDriver = `package ui

import "testing"

func TestATickReachesUpdate(t *testing.T) {
	m := New()
	if m.Seconds() != 0 || m.Beats__inst0() != 0 {
		t.Fatalf("a fresh model already ticked: %d %d", m.Seconds(), m.Beats__inst0())
	}
	next, _ := m.Update(timerTickMsg0{})
	m = next.(Model)
	if m.Seconds() != 1 {
		t.Errorf("the root component's tick left seconds at %d, want 1", m.Seconds())
	}
	next, _ = m.Update(timerTickMsg1{})
	m = next.(Model)
	if m.Beats__inst0() != 1 {
		t.Errorf("the child component's tick left beats at %d, want 1", m.Beats__inst0())
	}
}

// A tick re-arms itself, or the schedule fires once and stops.
func TestATickRearms(t *testing.T) {
	m := New()
	_, cmd := m.Update(timerTickMsg0{})
	if cmd == nil {
		t.Fatal("a tick returned no command, so nothing re-armed the timer")
	}
}

// The gate is read on the tick: a disabled timer neither runs its body nor
// re-arms, which is what stops it.
func TestAGatedTickDoesNothingWhenTheGateIsFalse(t *testing.T) {
	m := New().SetRunning(false)
	next, _ := m.Update(timerTickMsg0{})
	m = next.(Model)
	if m.Seconds() != 0 {
		t.Errorf("a disabled timer fired: seconds = %d", m.Seconds())
	}
}
`

// TestATimerTicksInTheEmittedProgram generates the program, builds it and RUNS
// it. The platform harness otherwise only asks whether the emitted Go compiles,
// and a timer that is emitted but never wired to Update compiles perfectly.
func TestATimerTicksInTheEmittedProgram(t *testing.T) {
	doc, err := parser.Parse("t.sngl", []byte(timerRunSrc))
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
	// optimize -> lower -> optimize, which is what internal/build runs. The
	// second pass is not decoration: it folds a const-valued gate such as
	// `enabled=true` from the identifier it still is after lowering into a
	// literal. Lowering alone leaves it an Ident named "true", and the emitter
	// reads a bare Ident gate as a model field -- `m.true`, which does not
	// compile. A harness that skips it tests a program `sngl generate` never
	// produces.
	optCfg := &optimize.Config{Platform: "bubbletea", Language: "go"}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	g := &Generator{}
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "bubbletea", Language: "go"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize2: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	modelSrc, ok := mem.Files()["model.go"]
	if !ok {
		t.Fatalf("model.go not among generated files %v", mem.Files())
	}

	tmp, err := os.MkdirTemp(".", "bt-timer-run-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "model.go"), modelSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "driver_test.go"), []byte(timerRunDriver), 0o644); err != nil {
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
