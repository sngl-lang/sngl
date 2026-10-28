package tui

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestTabLook(t *testing.T) {
	tab := func(selected, first bool) string {
		return ansi.Strip(TabLook(selected, first, false, "underline").Render("One"))
	}
	bar := lipgloss.JoinHorizontal(lipgloss.Top, tab(false, true), tab(true, false), tab(false, false),
		ansi.Strip(Sized(TabGap(), true, 4).Render("\n")))
	want := "" +
		"╭─────╮╭─────╮╭─────╮    \n" +
		"│ One ││ One ││ One │    \n" +
		"├─────┴┘     └┴─────┴───╮"
	if bar != want {
		t.Errorf("bar:\n%s\nwant:\n%s", bar, want)
	}
	if got := tab(true, true); got != "╭─────╮\n│ One │\n│     └" {
		t.Errorf("first selected:\n%s", got)
	}
	if got := ansi.Strip(Sized(TabPanel("underline"), true, 8).Render("x")); got != "│x     │\n╰──────╯" {
		t.Errorf("panel:\n%s", got)
	}
}

func TestTabLookPills(t *testing.T) {
	on, off := TabLook(true, true, false, "pills"), TabLook(false, false, false, "pills")
	if !on.GetReverse() || off.GetReverse() {
		t.Errorf("reverse: selected %v, other %v", on.GetReverse(), off.GetReverse())
	}
	if got := ansi.Strip(off.Render("Two")); got != " Two " {
		t.Errorf("pill: %q", got)
	}
	if TabPanel("pills").GetBorderStyle() != (lipgloss.Border{}) {
		t.Error("a pills panel has a border")
	}
}

func TestTabLookDisabledIsFaint(t *testing.T) {
	if !TabLook(false, false, true, "underline").GetFaint() || TabLook(false, false, false, "underline").GetFaint() {
		t.Error("faint is not the disabled tab's alone")
	}
}
