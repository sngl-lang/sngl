package fyne

import "testing"

// effectLoopElseSrc is testdata/effect_in_loop_else.sngl with the test funcs
// replaced by a button, so the list empties the way a program empties it.
//
// The claim is the compiled half of that fixture's: the interpreter reaches an
// `else` through the mounter and always got this right, so `sngl test` passed
// while every lowered target mounted the bracket once per element of the list
// it was placed to react to the ABSENCE of.
const effectLoopElseSrc = `
import . "sngl:ui"

component main {
    var (
        items list<string> = ["a", "b"]
        log list<string> = []
    )

    for var it = items {
        text(value=it)
    } else {
        effect(
            on="none",
            @mount(v) { log.push("+" + v) },
            @unmount(v) { log.push("-" + v) },
        )
    }

    button #clear(text="clear", @click { items = [] })
    button #fill(text="fill", @click { items = ["a"] })
    text #out(value=log.join(","))
}
`

const effectLoopElseDriver = `package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestTheLoopElseBracketsEmptinessNotElements(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	// Two elements, so the body ran and the else describes no bracket at all.
	// A position that inherited the loop's frame reads "+none,+none" here.
	if got := strings.Join(m.log, ","); got != "" {
		t.Fatalf("log with a full list = %q, want it empty", got)
	}

	m.clear.OnTapped()
	if got := strings.Join(m.log, ","); got != "+none" {
		t.Fatalf("log after emptying the list = %q, want %q", got, "+none")
	}

	m.fill.OnTapped()
	if got := strings.Join(m.log, ","); got != "+none,-none" {
		t.Fatalf("log after refilling the list = %q, want %q", got, "+none,-none")
	}
}
`

func TestAnEffectInALoopElseRuns(t *testing.T) {
	runEmitted(t, "fyne-effect-loop-else-", []byte(generateFyneModelBuilt(t, effectLoopElseSrc)), effectLoopElseDriver)
}
