package bubbletea

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const reactiveStyleRunSrc = `
import ui "sngl:ui"
import markup "sngl:ui/markup"

ui.window {
    var on = false

    ui.button #flip(text="flip", @click { on!! })
    ui.text(value="weight", style={fontWeight=on ? bold : normal})
    ui.text(value="slant", style={fontStyle=on ? italic : normal, textAlign=on ? center : right, width=160px})
    ui.text(value="dim", style={opacity=on ? 0.5 : 1.0, fontSize=on ? 20px : 10px})
    markup.richText(style={fontWeight=on ? bold : normal}) {
        markup.text("flow ")
        markup.run(style={fontWeight=on ? markup.Weight.inherit : markup.Weight.lighter, underline=on}) {
            markup.text("run")
        }
    }
}
`

const reactiveStyleRunDriver = `package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func expect(t *testing.T, phase, view string, style lipgloss.Style, text string, want bool) {
	t.Helper()
	rendered := style.Render(text)
	if strings.Contains(view, rendered) != want {
		t.Errorf("%s: view contains %q = %v, want %v\n--- view ---\n%q", phase, rendered, !want, want, view)
	}
}

func TestStyleFieldsFollowState(t *testing.T) {
	m := New()
	before := m.View().Content
	plain := lipgloss.NewStyle()
	expect(t, "before", before, plain.Bold(true), "weight", false)
	expect(t, "before", before, plain.Width(20).Align(lipgloss.Right), "slant", true)
	expect(t, "before", before, plain.Faint(true), "dim", false)
	expect(t, "before", before, plain.Bold(true), "flow ", false)
	expect(t, "before", before, plain.Faint(true), "run", true)

	m.flipClick()
	after := m.View().Content
	expect(t, "after", after, plain.Bold(true), "weight", true)
	expect(t, "after", after, plain.Italic(true).Width(20).Align(lipgloss.Center), "slant", true)
	expect(t, "after", after, plain.Faint(true), "dim", true)
	expect(t, "after", after, plain.Bold(true), "flow ", true)
	expect(t, "after", after, plain.Bold(true).Underline(true), "run", true)
}
`

// TestReactiveStyleRunsInTheEmittedProgram builds the model and drives it: a
// style field reading state has to compile, which a golden cannot say, and has
// to change what View renders when the state does, which only running says.
func TestReactiveStyleRunsInTheEmittedProgram(t *testing.T) {
	model := generateBubbleteaModel(t, reactiveStyleRunSrc)

	tmp, err := os.MkdirTemp(".", "_bt-reactive-style-run-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "model.go"), []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "driver_test.go"), []byte(reactiveStyleRunDriver), 0o644); err != nil {
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
