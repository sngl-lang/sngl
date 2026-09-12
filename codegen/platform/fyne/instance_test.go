package fyne

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/internal/lower"
)

// loopComponentSrc is testdata/test_html_loop_component.sngl without the test
// func: two rows of a component that holds its own `var clicks`, under a
// dynamic `for`.
const loopComponentSrc = `
import . "sngl:ui"

struct Item {
    name string = ""
}

component card(name = "") node {
    var clicks = 0
    button(text="{name} ({clicks})", @click { clicks += 1 })
}

window {
    var items list<Item> = [
        {name="a"},
        {name="b"},
    ]
    for var item = items {
        card(name=item.name)
    }
}
`

// instanceDriver drives the emitted program: it taps one row and not the
// other, and reads back what each row's own state became.
//
// It reads the record's fields directly because that IS the claim under test:
// two rows are two records, and `clicks` is a field of one of them rather than
// a cell they share. Asserting on the generated Go's shape is the point --
// nothing about the SNGL source can distinguish the two outcomes, which is
// why every fixture-level harness passed while this was broken.
const instanceDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestEachRowHoldsItsOwnState(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	if got := len(m.__inst0_live); got != 2 {
		t.Fatalf("expected 2 live instances, got %d", got)
	}
	first, second := m.__inst0_live[0], m.__inst0_live[1]
	if first == second {
		t.Fatal("both rows are the same instance")
	}

	second.__n0.OnTapped()
	second.__n0.OnTapped()
	first.__n0.OnTapped()

	if first.clicks != 1 {
		t.Errorf("first row clicks = %d, want 1", first.clicks)
	}
	if second.clicks != 2 {
		t.Errorf("second row clicks = %d, want 2", second.clicks)
	}
	if got := first.__n0.Text; got != "a (1)" {
		t.Errorf("first row text = %q, want %q", got, "a (1)")
	}
	if got := second.__n0.Text; got != "b (2)" {
		t.Errorf("second row text = %q, want %q", got, "b (2)")
	}
}
`

// TestInstanceRecordsRunIndependently generates the program, builds it and
// RUNS it.
//
// A platform harness only asks whether a fixture generates, and the checker
// and interpreter harnesses never see what lowering produced -- so a component
// in a list row could share one state cell with every other row and no test in
// this repository would say so. This one runs the output and reads the two
// counters.
func TestInstanceRecordsRunIndependently(t *testing.T) {
	pkg := checkForFyne(t, loopComponentSrc)
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "fyne", Language: "go"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	modelSrc, ok := mem.Files()["model.go"]
	if !ok {
		t.Fatal("model.go not found in generated files")
	}

	// Under the main module, so the fyne imports resolve through the
	// project's own go.mod -- the same trick the intrinsic integration test
	// uses to compile emitted Go.
	tmp, err := os.MkdirTemp(".", "fyne-run-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "model.go"), modelSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "driver_test.go"), []byte(instanceDriver), 0o644); err != nil {
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
