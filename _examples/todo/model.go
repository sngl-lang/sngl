package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
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

type Todo struct {
	Text string
	Done bool
}

// Model is the Bubble Tea model for this SNGL UI.
type Model struct {
	NewTodo string
	Todos   []Todo

	input0 textinput.Model

	TodosCursor int

	focus         int
	width, height int
}

// New creates a Model with default bind values.
func New() Model {
	m := Model{
		NewTodo: "",
		Todos:   nil,
	}
	m.input0 = textinput.New()
	m.input0.Placeholder = "What needs to be done?"
	m.input0.SetValue(m.NewTodo)
	m.input0.Focus()
	return m
}

func (m Model) status() string {
	return (("Todo List (" + fmt.Sprint(len(m.Todos))) + " items)")
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
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
		case msg.Code == tea.KeyTab && msg.Mod == 0:
			m.focus = (m.focus + 1) % 4
			if m.focus == 0 {
				m.input0.Focus()
			} else {
				m.input0.Blur()
			}
		case msg.Code == tea.KeyTab && msg.Mod == tea.ModShift:
			m.focus = (m.focus - 1 + 4) % 4
			if m.focus == 0 {
				m.input0.Focus()
			} else {
				m.input0.Blur()
			}
		case msg.Code == tea.KeyEnter && m.focus == 1:
			m.Todos = append(m.Todos, Todo{Text: m.NewTodo, Done: false})
			m.NewTodo = ""
			m.input0.SetValue(m.NewTodo)
		case msg.Code == tea.KeyEnter && m.focus == 2:
			if m.TodosCursor < len(m.Todos) {
				index := m.TodosCursor
				m.Todos[index].Done = !m.Todos[index].Done
			}
		case msg.Code == tea.KeyUp && m.focus == 2:
			if m.TodosCursor > 0 {
				m.TodosCursor--
			}
		case msg.Code == tea.KeyDown && m.focus == 2:
			if m.TodosCursor < len(m.Todos)-1 {
				m.TodosCursor++
			}
		case msg.Code == tea.KeyEnter && m.focus == 3:
			m.Todos = append(m.Todos[:(len(m.Todos)-1)], m.Todos[(len(m.Todos)-1)+1:]...)
		}
	}
	if m.focus == 0 {
		m.input0, cmd = m.input0.Update(msg)
		m.NewTodo = m.input0.Value()
	}
	return m, cmd
}

func (m Model) View() tea.View {
	var content string
	var contentChildren []string
	var content0 string
	content0 = lipgloss.NewStyle().
		Bold(true).Render(m.status())
	contentChildren = append(contentChildren, content0)
	var content1 string
	var content1Children []string
	var content10 string
	content10 = m.input0.View()
	content1Children = append(content1Children, content10)
	var content11 string
	content11Focused := m.focus == 1
	content11Prefix := " "
	if content11Focused {
		content11Prefix = ">"
	}
	content11 = lipgloss.NewStyle().Render(content11Prefix + " " + "Add")
	content1Children = append(content1Children, content11)
	content1Gap := strings.Repeat(" ", 1)
	content1Joined := strings.Join(content1Children, content1Gap)
	content1 = content1Joined
	contentChildren = append(contentChildren, content1)
	var content2 string
	var content2Children []string
	var content20 string
	var content20Items []string
	for index, item := range m.Todos {
		_ = index
		var content20Item string
		content20ItemFocused := m.focus == 2 && m.TodosCursor == index
		content20ItemPrefix := " "
		if content20ItemFocused {
			content20ItemPrefix = ">"
		}
		content20Item = lipgloss.NewStyle().Render(content20ItemPrefix + " " + ternary(item.Done, "[x] ", "[ ] ") + item.Text)
		content20Items = append(content20Items, content20Item)
	}
	content20 = strings.Join(content20Items, "\n")
	content2Children = append(content2Children, content20)
	content2Gap := strings.Repeat("\n", 1)
	content2Joined := strings.Join(content2Children, content2Gap)
	content2 = content2Joined
	contentChildren = append(contentChildren, content2)
	var content3 string
	content3Focused := m.focus == 3
	content3Prefix := " "
	if content3Focused {
		content3Prefix = ">"
	}
	content3 = lipgloss.NewStyle().Render(content3Prefix + " " + "Remove Last")
	contentChildren = append(contentChildren, content3)
	contentGap := strings.Repeat("\n", 1)
	contentJoined := strings.Join(contentChildren, contentGap)
	content = lipgloss.NewStyle().
		Padding(2).Render(contentJoined)
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}
