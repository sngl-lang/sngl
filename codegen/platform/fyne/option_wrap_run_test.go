package fyne

import "testing"

// optionWrapCopySrc promotes a struct into an option and then writes to the
// struct it was promoted from.
//
// Fyne is where that question can be asked: its Model is a *Model all the way
// through, so a pointer taken in a handler still points at live state when the
// next handler runs. Bubbletea's Update takes the model by value and returns a
// copy, so a `&` there addresses a local nobody writes to again and the two
// spellings are indistinguishable.
const optionWrapCopySrc = `
import . "sngl:ui"

struct Box {
    value int = 0
}

struct Holder {
    inner option<Box> = null
}

window {
    var b = Box{value=3}
    var h = Holder{inner=null}
    var seen = 0

    button(text="wrap", @click {
        h = Holder{inner=b}
    })
    button(text="mutate", @click {
        b.value = 99
    })
    button(text="read", @click {
        if h.inner != null {
            seen = h.inner.value
        }
    })
    text(value=string(seen))
}
`

// A SNGL struct is a value: `h = Holder{inner=b}` stores what b was, and a
// later write to b is not a write to h. Go's option<T> is *T, so honouring
// that means the promotion boxes a COPY -- `&b` would hand the option a
// window onto b and the read below would come back 99.
const optionWrapCopyDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestPromotingIntoAnOptionStoresACopy(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	m.__n2.OnTapped() // wrap
	m.__n3.OnTapped() // mutate
	m.__n4.OnTapped() // read

	if m.b.Value != 99 {
		t.Fatalf("b.Value = %d, want 99 -- the mutation did not happen", m.b.Value)
	}
	if m.seen != 3 {
		t.Errorf("seen = %d, want 3 -- the option must hold a copy of b, not a window onto it", m.seen)
	}
}
`

// TestPromotingIntoAnOptionStoresACopy runs the emitted program.
//
// The claim is not about whether the code compiles -- `&b` compiles here,
// because a model field is addressable. It is about what the program then
// does, which is the half a build test cannot reach.
func TestPromotingIntoAnOptionStoresACopy(t *testing.T) {
	model := generateFyneModelBuilt(t, optionWrapCopySrc)
	runEmitted(t, "fyne-option-wrap-copy-", []byte(model), optionWrapCopyDriver)
}
