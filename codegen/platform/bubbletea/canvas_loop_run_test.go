package bubbletea

import "testing"

// canvasLoopRunSrc draws a canvas once per element, the radius read off the
// element and a spliced stateful component's own cell.
const canvasLoopRunSrc = `
import . "sngl:ui"
import draw "sngl:ui/draw"

component Dot(r float) node {
    var big = false

    vbox {
        draw.canvas(width=16px, height=16px) {
            draw.circle(cx=8, cy=8, r=big ? r * 2 : r, style=draw.CanvasStyle{fill=color{r=255, a=255}})
        }
        button(text="grow", @click { big = !big })
    }
}

window {
    var sizes list<float> = [1.0, 6.0]

    vbox {
        for var s = sizes {
            Dot(r=s)
        }
    }
}
`

const canvasLoopRunDriver = `package ui

import (
	"fmt"
	"testing"

	"charm.land/bubbletea/v2"
)

func view(m Model) string { return fmt.Sprint(m.View().Content) }

// Each copy draws with its own element: two different radii render
// differently from two equal ones, and each copy has a surface of its own.
func TestEachCopyDrawsItsOwnElement(t *testing.T) {
	m := New()
	mixed := view(m)
	if n := len(_canvasSurface0); n != 2 {
		t.Fatalf("want a surface per copy, have %d", n)
	}
	m.sizes = []float64{6.0, 6.0}
	same := view(m)
	if mixed == same {
		t.Fatal("the first copy drew the second copy's radius")
	}
	if m.__canvasTransmit() != nil {
		t.Fatal("a terminal without kitty graphics transmitted pixels")
	}
}

// A copy's own state reaches its drawing.
func TestACopysStateReachesItsDrawing(t *testing.T) {
	m := New()
	before := view(m)
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if !m.big__inst0["0"] {
		t.Fatalf("Enter did not reach the first copy: %v", m.big__inst0)
	}
	if view(m) == before {
		t.Fatal("the first copy's canvas did not redraw from its own cell")
	}
}
`

func TestALoopCanvasDrawsEachCopy(t *testing.T) {
	runEmittedProgram(t, canvasLoopRunSrc, canvasLoopRunDriver)
}
