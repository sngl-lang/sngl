package fyne

import "testing"

// propInitComponentSrc is a component whose own `var` is initialised from a
// prop -- the one ordering a promoted prop can get wrong.
const propInitComponentSrc = `
import . "sngl:ui"

component greeter(name = "") ui {
    var greeting = "hi " + name
    text(value=greeting)
}

component main ui {
    var names list<string> = ["ann", "bob"]
    for var n = names {
        greeter(name=n)
    }
}
`

// propInitDriver reads what each row's own var was initialised to.
//
// A `var` that reads a prop is the assertion, and it is one only the running
// program can make: an initializer placed before the prop's cell is assigned
// compiles fine on Go and reads the zero value, so every row renders "hi "
// and no fixture-level harness has anything to compare.
const propInitDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestAVarReadsThePropItWasInitialisedFrom(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	if got := len(m.__inst0_live); got != 2 {
		t.Fatalf("expected 2 live instances, got %d", got)
	}
	for i, want := range []string{"hi ann", "hi bob"} {
		if got := m.__inst0_live[i].greeting; got != want {
			t.Errorf("row %d greeting = %q, want %q", i, got, want)
		}
	}
}
`

// TestAPromotedPropIsAssignedBeforeTheVarsThatReadIt generates the program,
// builds it and RUNS it.
func TestAPromotedPropIsAssignedBeforeTheVarsThatReadIt(t *testing.T) {
	runEmitted(t, "fyne-propinit-", generateForFyne(t, propInitComponentSrc), propInitDriver)
}
