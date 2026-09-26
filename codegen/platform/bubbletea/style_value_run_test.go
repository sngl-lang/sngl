package bubbletea

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// styleValueRunSrc hands a node a whole Style held in state, replaced by one
// handler and written a field at a time by another.
const styleValueRunSrc = `
import . "sngl:ui"

window {
    var s Style = {color=#ff0000}

    button #swap(text="swap", @click { s = {color=#0000ff, fontWeight=bold} })
    button #tint(text="tint", @click { s.color = #00ff00 })
    text(value="styled", style=s)
}
`

// The driver reads the SGR sequence lipgloss wraps the text in: a truecolor
// foreground is 38;2;r;g;b, and bold is 1.
const styleValueRunDriver = `package ui

import (
	"fmt"
	"regexp"
	"testing"
)

func styled(t *testing.T, m Model) string {
	t.Helper()
	got := regexp.MustCompile("\x1b\\[([0-9;]*)m[^\x1b]*styled").FindStringSubmatch(fmt.Sprint(m.View().Content))
	if got == nil {
		t.Fatalf("no styled text in %q", fmt.Sprint(m.View().Content))
	}
	return got[1]
}

func TestStyleValue(t *testing.T) {
	m := New()
	if got := styled(t, m); got != "38;2;255;0;0" {
		t.Errorf("initially %q, want the red foreground alone", got)
	}
	m.swapClick()
	if got := styled(t, m); got != "1;38;2;0;0;255" {
		t.Errorf("after the swap %q, want bold and blue", got)
	}
	m.tintClick()
	if got := styled(t, m); got != "1;38;2;0;255;0" {
		t.Errorf("after the tint %q, want bold and green", got)
	}
}
`

// TestStyleValueRunsInTheEmittedProgram renders a Style value held in state,
// and renders it again when a handler replaces it or writes one of its fields.
func TestStyleValueRunsInTheEmittedProgram(t *testing.T) {
	model := generateBubbleteaModel(t, styleValueRunSrc)

	tmp, err := os.MkdirTemp(".", "_bt-style-value-run-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "model.go"), []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "driver_test.go"), []byte(styleValueRunDriver), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "test", "-count=1", ".")
	cmd.Dir = tmp
	combined, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Errorf("running the emitted program failed: %v\n--- output ---\n%s\n--- model.go ---\n%s",
			runErr, combined, model)
	}
}
