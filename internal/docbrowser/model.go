package docbrowser

import (
	"fmt"
	"git.duckfam.us/jonathan/sngl/docs"
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
	components    []docs.Component
	selectedIndex int

	focus         int
	width, height int
}

// New creates a Model with default bind values.
func New() Model {
	m := Model{
		components:    nil,
		selectedIndex: 0,
	}
	return m
}

func (m Model) currentName() string {
	return m.components[m.selectedIndex].Name
}

func (m Model) currentDoc() string {
	return m.components[m.selectedIndex].Doc
}

func (m Model) currentTier() string {
	return m.components[m.selectedIndex].Tier
}

func (m Model) currentChildren() string {
	return m.components[m.selectedIndex].Children
}

func (m Model) Components() []docs.Component {
	return m.components
}

func (m Model) SetComponents(v []docs.Component) Model {
	m.components = v
	return m
}

type setComponentsMsg struct{ value []docs.Component }

func SetComponentsCmd(v []docs.Component) tea.Cmd {
	return func() tea.Msg { return setComponentsMsg{value: v} }
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
	case setComponentsMsg:
		m = m.SetComponents(msg.value)
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
	var content string
	var contentChildren []string
	var content_0 string
	var content_0Children []string
	var content_0_0 string
	content_0_0 = lipgloss.NewStyle().Render(fmt.Sprint("SNGL Components"))
	content_0Children = append(content_0Children, content_0_0)
	var content_0_1 string
	content_0_1 = lipgloss.NewStyle().Render(fmt.Sprint(""))
	content_0Children = append(content_0Children, content_0_1)
	var content_0_2 string
	var content_0_2Items []string
	for index, comp := range m.components {
		_ = index
		var content_0_2Item string
		content_0_2Item = lipgloss.NewStyle().Render(fmt.Sprint(ternary((index == m.selectedIndex), ("> " + comp.Name), ("  " + comp.Name))))
		content_0_2Items = append(content_0_2Items, content_0_2Item)
	}
	content_0_2 = strings.Join(content_0_2Items, "\n")
	content_0Children = append(content_0Children, content_0_2)
	content_0 = lipgloss.JoinVertical(lipgloss.Left, content_0Children...)
	contentChildren = append(contentChildren, content_0)
	var content_1 string
	var content_1Children []string
	var content_1_0 string
	content_1_0 = lipgloss.NewStyle().Render(fmt.Sprint(m.currentName()))
	content_1Children = append(content_1Children, content_1_0)
	var content_1_1 string
	content_1_1 = lipgloss.NewStyle().Render(fmt.Sprint((("[" + m.currentTier()) + "]")))
	content_1Children = append(content_1Children, content_1_1)
	var content_1_2 string
	content_1_2 = lipgloss.NewStyle().Render(fmt.Sprint(""))
	content_1Children = append(content_1Children, content_1_2)
	var content_1_3 string
	content_1_3 = lipgloss.NewStyle().Render(fmt.Sprint(m.currentDoc()))
	content_1Children = append(content_1Children, content_1_3)
	var content_1_4 string
	content_1_4 = lipgloss.NewStyle().Render(fmt.Sprint(""))
	content_1Children = append(content_1Children, content_1_4)
	var content_1_5 string
	content_1_5 = lipgloss.NewStyle().Render(fmt.Sprint(("Children: " + m.currentChildren())))
	content_1Children = append(content_1Children, content_1_5)
	content_1 = lipgloss.JoinVertical(lipgloss.Left, content_1Children...)
	contentChildren = append(contentChildren, content_1)
	content = lipgloss.JoinHorizontal(lipgloss.Top, contentChildren...)
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}
