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

// constructComponentSrc is loopComponentSrc with a #[construct] prop the row's
// own `var` is initialised from -- the shape where a setter would reach
// nothing, because the initializer has already run.
const constructComponentSrc = `
import . "sngl:ui"
import . "sngl:macro"

component card(#[construct] seed int, name = "") node {
    var clicks = seed
    button(text="{name} ({clicks})", @click { clicks += 1 })
}

window {
    var (
        names list<string> = ["a", "b"]
        base = 0
    )
    for var nm = names {
        card(seed=base, name=nm)
    }
    button(text="rebase", @click { base += 10 })
}
`

// constructDriver drives the emitted program across two re-renders: one that
// describes the same seed and one that describes a new one.
//
// Identity is the assertion, and it is one only the generated Go can make: a
// kept row is the same *CardInstance pointer with its own counter still on it,
// and a rebuilt row is a different pointer whose counter came from the new
// seed. Nothing about the SNGL source distinguishes the two, which is how the
// silent drop survived every fixture-level harness.
const constructDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestAConstructPropRebuildsTheRowItBuilt(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	if got := len(m.__inst0_live); got != 2 {
		t.Fatalf("expected 2 live instances, got %d", got)
	}
	first := m.__inst0_live[0]
	first.__n0.OnTapped()
	if first.clicks != 1 {
		t.Fatalf("first row clicks = %d, want 1", first.clicks)
	}

	// A render describing the same seed keeps the row, counter and all.
	m.__renderSlot0(m.__root)
	if m.__inst0_live[0] != first {
		t.Error("an unchanged construct prop should keep the instance")
	}
	if got := m.__inst0_live[0].clicks; got != 1 {
		t.Errorf("kept row clicks = %d, want 1", got)
	}

	// Changing it rebuilds: the initializer runs again, from the new value.
	m.__n3_click_handler()
	if m.__inst0_live[0] == first {
		t.Fatal("a changed construct prop should rebuild the instance")
	}
	if got := m.__inst0_live[0].clicks; got != 10 {
		t.Errorf("rebuilt row clicks = %d, want 10 -- the new seed", got)
	}
	if got := len(m.__inst0_live); got != 2 {
		t.Errorf("live instances after the rebuild = %d, want 2", got)
	}
}
`

// TestConstructPropRebuildsTheInstance generates the program, builds it and
// RUNS it. The fyne half of the html browser tests, for the same reason: a
// platform harness only asks whether a fixture generates.
func TestConstructPropRebuildsTheInstance(t *testing.T) {
	pkg := checkForFyne(t, constructComponentSrc)
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	if err := lower.Lower(pkg, codegen.CapsOrNone(lang.LanguageIdentifier(), g.PlatformIdentifier()), lower.Options{Platform: "fyne", Language: "go"}); err != nil {
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

	// Under the main module, so the fyne imports resolve through the project's
	// own go.mod.
	tmp, err := os.MkdirTemp(".", "_fyne-construct-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "model.go"), modelSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "driver_test.go"), []byte(constructDriver), 0o644); err != nil {
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
