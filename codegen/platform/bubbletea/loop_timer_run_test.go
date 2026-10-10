package bubbletea

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/optimize"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// loopTimerRunSrc is a component with state of its own and a timer, once per
// element of a list. This platform keeps no state of an instance's own, so the
// component is spliced into the loop with a cell per copy, and the timer is a
// schedule per copy that Update routes by key.
const loopTimerRunSrc = `
import . "sngl:ui"
import . "sngl:time"

component ticker(period duration = 10ms) node {
    var ticks = 0
    timer(interval=period, enabled=true, @tick { ticks += 1 })
    text(value="{period}: {ticks}")
}

window {
    var periods list<duration> = [10ms, 20ms]
    for var p = periods {
        ticker(period=p)
    }
}
`

const loopTimerRunDriver = `package ui

import (
	"testing"
	"time"
)

func started() Model {
	m := New()
	m.__timer0Sync()
	return m
}

func TestEveryCopyIsScheduled(t *testing.T) {
	m := started()
	if len(m.__timer0) != 2 {
		t.Fatalf("want a schedule per element, have %v", m.__timer0)
	}
}

// A tick runs its own copy's handler, against its own copy's cell.
func TestATickRunsOnlyItsCopy(t *testing.T) {
	m := started()
	next, cmd := m.Update(timerTickMsg0{key: "1", gen: m.__timer0["1"]})
	m = next.(Model)
	if m.ticks__inst0["1"] != 1 || m.ticks__inst0["0"] != 0 {
		t.Fatalf("a tick for the second copy left the cells at %v", m.ticks__inst0)
	}
	if cmd == nil {
		t.Fatal("a tick returned no command, so nothing re-armed its copy")
	}
}

// One armed for a generation the copy is no longer running is not a second
// schedule for it.
func TestAStaleTickIsIgnored(t *testing.T) {
	m := started()
	next, _ := m.Update(timerTickMsg0{key: "0", gen: m.__timer0["0"] + 1})
	m = next.(Model)
	if m.ticks__inst0["0"] != 0 {
		t.Fatalf("a stale tick ran: %v", m.ticks__inst0)
	}
}

// A copy the loop stops producing stops ticking.
func TestARemovedCopyStops(t *testing.T) {
	m := started()
	gen := m.__timer0["1"]
	m = m.SetPeriods([]time.Duration{10 * time.Millisecond})
	next, _ := m.Update(struct{}{})
	m = next.(Model)
	if _, live := m.__timer0["1"]; live {
		t.Fatalf("the removed copy is still scheduled: %v", m.__timer0)
	}
	next, _ = m.Update(timerTickMsg0{key: "1", gen: gen})
	m = next.(Model)
	if m.ticks__inst0["1"] != 0 {
		t.Fatalf("the removed copy ticked: %v", m.ticks__inst0)
	}
}
`

// TestALoopTimerTicksPerCopyInTheEmittedProgram builds the emitted program and
// runs the driver against it. A golden only says the Go compiles, and a
// schedule nothing arms compiles perfectly: before this, a timer under a loop
// was collected by nobody and never ran.
func TestALoopTimerTicksPerCopyInTheEmittedProgram(t *testing.T) {
	runEmittedProgram(t, loopTimerRunSrc, loopTimerRunDriver)
}

// runEmittedProgram checks src for bubbletea, runs the pipeline internal/build
// runs, and `go test`s the model it emits beside driver.
func runEmittedProgram(t *testing.T, src, driver string) {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
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
	optCfg := &optimize.Config{Platform: "bubbletea", Language: "go"}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	g := &Generator{}
	if err := lower.Lower(pkg, codegen.CapsOrNone(lang.LanguageIdentifier(), g.PlatformIdentifier()), lower.Options{Platform: "bubbletea", Language: "go"}); err != nil {
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
	tmp, err := os.MkdirTemp(".", "_bt-run-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "model.go"), modelSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "driver_test.go"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "-count=1", ".")
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("running the emitted program failed: %v\n--- output ---\n%s\n--- model.go ---\n%s", err, out, modelSrc)
	}
}
