package bubbletea

import "testing"

// Content handed to a recursive component under a loop: bare children that
// each level forwards, and a population by name that each level re-wraps.
// Both are rendered once per level.
const focusRecursionRunSrc = `
import . "sngl:ui"

component frame(n int, children ...component) node {
    vbox {
        text(value="level {n}")
        children
        if n > 0 {
            frame(n=n - 1) {
                children
            }
        }
    }
}

component tower(n int, label component(depth int) node) node {
    vbox {
        label(n)
        if n > 0 {
            tower(n=n - 1) {
                component label(d) {
                    label(d * 10)
                }
            }
        }
    }
}

var hits = 0
var xs = [1, 2]

window {
    vbox {
        text(value="hits {hits}")
        for var x = xs {
            frame(n=x) {
                button(text="b{x}", @click { hits += x })
            }
            tower(n=x) {
                component label(d) {
                    button(text="t{x}/{d}", @click { hits += x * 100 })
                }
            }
        }
    }
}
`

const focusRecursionRunDriver = `package ui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
)

func key(m Model, code rune) Model {
	next, _ := m.Update(tea.KeyPressMsg{Code: code})
	return next.(Model)
}

func marked(m Model) []string {
	var out []string
	for _, l := range strings.Split(m.View().Content, "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == ">" {
			out = append(out, f[1])
		}
	}
	return out
}

// Each loop copy's content is one stop however many levels render it: every
// copy it renders is marked while it is focused, and Enter runs its handler.
func TestContentInARecursionIsOneStop(t *testing.T) {
	stops := []struct {
		marks []string
		hits  int
	}{
		{[]string{"b1", "b1"}, 1},
		{[]string{"t1/1", "t1/0"}, 100},
		{[]string{"b2", "b2", "b2"}, 2},
		{[]string{"t2/2", "t2/10", "t2/0"}, 200},
	}
	m := New()
	for i, s := range stops {
		if got := strings.Join(marked(m), " "); got != strings.Join(s.marks, " ") {
			t.Fatalf("stop %d marks %q, want %q", i, got, s.marks)
		}
		m.hits = 0
		m = key(m, tea.KeyEnter)
		if m.hits != s.hits {
			t.Fatalf("stop %d ran a handler adding %d, want %d", i, m.hits, s.hits)
		}
		m = key(m, tea.KeyTab)
	}
	if got := strings.Join(marked(m), " "); got != "b1 b1" {
		t.Fatalf("focus did not wrap to the first stop: %q", got)
	}
}
`

func TestFocusCountsContentInARecursionOnce(t *testing.T) {
	runEmittedProgram(t, focusRecursionRunSrc, focusRecursionRunDriver)
}
