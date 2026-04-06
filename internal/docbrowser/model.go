package docbrowser

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var _ = fmt.Sprint
var _ = strings.Join
var _ = lipgloss.NewStyle

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

// Model is the Bubble Tea model for this SNGL UI.
type Model struct {
	selectedIndex int

	focus         int
	width, height int
}

// New creates a Model with default bind values.
func New() Model {
	m := Model{
		selectedIndex: 0,
	}
	return m
}

func (m Model) SelectedIndex() int {
	return m.selectedIndex
}

func (m Model) SetSelectedIndex(v int) Model {
	m.selectedIndex = v
	return m
}

type setSelectedIndexMsg struct{ value int }

func SetSelectedIndexCmd(v int) tea.Cmd {
	return func() tea.Msg { return setSelectedIndexMsg{value: v} }
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case setSelectedIndexMsg:
		m = m.SetSelectedIndex(msg.value)
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
	return tea.NewView("")
}
