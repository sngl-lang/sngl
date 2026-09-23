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

// A row that reports upward, under a dynamic `for` so the build cannot inline
// it. The parent's handler writes state that a widget of the Model displays,
// which is what makes the write cross a boundary.
const instanceEventSrc = `
import . "sngl:ui"

component row(label = "", @pick) node {
    var seen = 0
    button(text=label, @click { seen = seen + 1 pick() })
}

window {
    var items list<string> = ["a", "b"]
    var picked = ""

    vbox {
        for var it = items {
            row(label=it, @pick { picked = it })
        }
    }
    text(value=picked)
}
`

const instanceEventDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestTheParentHandlerRepaintsTheModelsWidget(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	if got := len(m.__inst0_live); got != 2 {
		t.Fatalf("expected 2 live instances, got %d", got)
	}
	m.__inst0_live[1].__n0.OnTapped()

	if m.picked != "b" {
		t.Errorf("picked = %q, want %q", m.picked, "b")
	}
	if got := m.__n1.Text; got != "b" {
		t.Errorf("the Model's label = %q, want %q", got, "b")
	}
}
`

// A handler an instantiation hands over is emitted through the platform's own
// translator.
//
// A prop is where a handler crosses into an instance -- an event the call site
// subscribed to is a func-typed prop, and its lambda is a field of the struct
// the create call carries. WalkLowered reached a handler attached with
// AttachHandler and no other, so this one went to the Go backend raw: the write
// to the Model's label kept its IR shape and named the field as a bare local,
// and the emitted package did not compile.
//
// Compiling it is most of the assertion, which is why this generates, writes
// and runs rather than matching on the source. The run then checks the write
// actually lands: `m.__n1.Text` is the widget the parent's handler repaints.
func TestInstanceEventReachesTheModelsWidget(t *testing.T) {
	pkg := checkForFyne(t, instanceEventSrc)
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

	tmp, err := os.MkdirTemp(".", "_fyne-event-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "model.go"), modelSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "driver_test.go"), []byte(instanceEventDriver), 0o644); err != nil {
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
