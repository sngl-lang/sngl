package bubbletea

import "testing"

// wrapRunSrc is the Pokédex's About and Moves tabs: a paragraph in a padded
// column, and a table whose name column grows, in a scroll moving across.
const wrapRunSrc = `
import ui "sngl:ui"

component moveLine(level string, name string, kind string) ui.node {
    ui.hbox(style={gap=8px}) {
        ui.text(value=level, style={width=48px})
        ui.text(value=name, style={flex=1})
        ui.text(value=kind, style={width=88px})
    }
}

ui.window {
    var tab = 0
    ui.tabs(:selected=tab) {
        ui.tab(label="About") {
            ui.vbox(style={gap=6px, padding=8px}) {
                ui.text(value="What appears to be drool is actually sweet honey. It is very sticky and clings stubbornly if touched.")
            }
        }
        ui.tab(label="Moves") {
            ui.scroll(direction=horizontal) {
                ui.vbox {
                    moveLine(level="1", name="Acid", kind="Poison")
                    moveLine(level="14", name="Poison Powder", kind="Poison")
                }
            }
        }
    }
}
`

const wrapRunDriver = `package ui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func lineWith(t *testing.T, view, s string) string {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, s) {
			return line
		}
	}
	t.Fatalf("no %q in the view:\n%s", s, view)
	return ""
}

func col(line, s string) int { return ansi.StringWidth(line[:strings.LastIndex(line, s)]) }

// start is the column a line's text starts at, inside the panel's border.
func start(line string) int {
	inside := line[strings.Index(line, "│")+len("│"):]
	return ansi.StringWidth(line) - ansi.StringWidth(strings.TrimLeft(inside, " "))
}

// A paragraph breaks its lines inside its column's padding, and a column
// that grows is as wide in every line of a scroll moving across.
func TestTextFitsItsBox(t *testing.T) {
	next, _ := New().Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m := next.(Model)
	view := ansi.Strip(m.View().Content)
	if start(lineWith(t, view, "What")) != start(lineWith(t, view, "touched")) {
		t.Fatalf("a broken line does not start where the paragraph does:\n%s", view)
	}
	m.tab = 1
	view = ansi.Strip(m.View().Content)
	if a, b := col(lineWith(t, view, "Acid"), "Poison"), col(lineWith(t, view, "Powder"), "Poison"); a != b {
		t.Fatalf("the type column is at %d in one line and %d in the next:\n%s", a, b, view)
	}
}
`

func TestTextFitsItsBox(t *testing.T) {
	runEmittedProgram(t, wrapRunSrc, wrapRunDriver)
}
