package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var _ = fmt.Sprint
var _ = strings.Join

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

// Model is the Bubble Tea model for this SNGL UI.
type Model struct {
	Count int

	focus         int
	width, height int
}

// New creates a Model with default bind values.
func New() Model {
	m := Model{
		Count: 0,
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyPressMsg:
		switch {
		case msg.Code == 'c' && msg.Mod == tea.ModCtrl:
			return m, tea.Quit
		}
	}
	return m, cmd
}

func (m Model) View() tea.View {
	var content string
	var contentChildren []string
	var content0 string
	content0 = lipgloss.NewStyle().Render("Hello")
	contentChildren = append(contentChildren, content0)
	content = lipgloss.JoinVertical(lipgloss.Left, contentChildren...)
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}
