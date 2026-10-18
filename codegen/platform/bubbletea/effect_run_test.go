package bubbletea

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "duckfam.us/sngl/codegen/lang"
)

// effectRunSrc is testdata/effect_on_recreates.sngl with the test funcs
// replaced by a button, so the key changes the way a program changes it: from
// a handler, through Update.
const effectRunSrc = `
import . "sngl:ui"

window {
    var (
        n = 0
        log list[string] = []
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
	modelSrc := []byte(generateBubbleteaModel(t, effectRunSrc))

	// Under the main module, so charm.land/bubbletea resolves through the
	// project's own go.mod.
	tmp, err := os.MkdirTemp(".", "_bt-effect-run-")
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
