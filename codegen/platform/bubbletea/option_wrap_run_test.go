package bubbletea

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// optionWrapRunSrc exercises both halves of the option conversion pair in one
// program: the promotion of a bare T into an option<T>, and the unwrap a null
// test earns on the way back out.
//
// `h` promotes an addressable operand and `fresh` promotes one that is not --
// the result of an impure call, which no optimizer folds away. `&` is legal on
// the first and does not compile on the second, so a program holding only the
// first would not say which spelling the emitter picked.
//
// Both narrowed reads are field reads rather than whole-value ones: `*` binds
// looser than a selector in Go, so a narrowed path with a `.` after it is the
// case the unwrap's parenthesisation decides.
const optionWrapRunSrc = `
import . "sngl:ui"

struct Box {
    value int = 0
}

struct Holder {
    inner option<Box> = null
}

var made = 0

func nextBox() Box {
    made = made + 1
    return Box{value=made * 10}
}

component main ui {
    var b = Box{value=3}
    var h = Holder{inner=b}
    var fresh = Holder{inner=null}
    var seen = 0
    var copied = 0

    button #read(text="read", @click {
        if h.inner != null {
            seen = h.inner.value
        }
        if fresh.inner != null {
            copied = fresh.inner.value
        }
    })
    button #fill(text="fill", @click {
        fresh = Holder{inner=nextBox()}
    })
    text #out(value=string(seen) + "/" + string(copied))
}
`

const optionWrapRunDriver = `package ui

import "testing"

func TestNarrowedFieldReadsThroughTheOption(t *testing.T) {
	m := New()
	m.readClick()
	if m.seen != 3 {
		t.Errorf("seen = %d, want 3 -- the narrowed read of h.inner.value", m.seen)
	}
	if m.copied != 0 {
		t.Errorf("copied = %d, want 0 -- fresh.inner is still null", m.copied)
	}
}

func TestAValueFromACallReachesTheOption(t *testing.T) {
	m := New()
	m.fillClick()
	m.readClick()
	if m.copied != 10 {
		t.Errorf("copied = %d, want 10", m.copied)
	}
}
`

// TestOptionWrapAndUnwrapRunInTheEmittedProgram generates the program, builds
// it and RUNS it.
//
// Building it is most of the claim and running it is the rest. Go's option<T>
// is *T, so the promotion is a box the checker inserts and the Go emitter
// spells: `(*Box)(b)` -- what the generic conversion path emits -- does not
// compile, and neither does `&nextBox()`. What only running catches is the
// value that came back: an unwrap that compiles can still read the wrong one.
func TestOptionWrapAndUnwrapRunInTheEmittedProgram(t *testing.T) {
	model := generateBubbleteaModel(t, optionWrapRunSrc)

	// Under the main module, so charm.land/bubbletea resolves through the
	// project's own go.mod.
	tmp, err := os.MkdirTemp(".", "bt-option-wrap-run-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "model.go"), []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "driver_test.go"), []byte(optionWrapRunDriver), 0o644); err != nil {
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
