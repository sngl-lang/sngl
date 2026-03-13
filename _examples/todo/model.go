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
			m.focus = (m.focus + 1) % 3
			if m.focus == 0 {
				m.input0.Focus()
			} else {
				m.input0.Blur()
			}
		case msg.Code == tea.KeyTab && msg.Mod == tea.ModShift:
			m.focus = (m.focus - 1 + 3) % 3
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
	for _, item := range m.Todos {
		var content20Item string
		content20Item = m.renderTodoItem(item.Text, item.Done)
		content20Items = append(content20Items, content20Item)
	}
	content20 = strings.Join(content20Items, "\n")
	content2Children = append(content2Children, content20)
	content2Gap := strings.Repeat("\n", 1)
	content2Joined := strings.Join(content2Children, content2Gap)
	content2 = content2Joined
	contentChildren = append(contentChildren, content2)
	var content3 string
	content3Focused := m.focus == 2
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

func (m Model) renderTodoItem(text string, done bool) string {
	var result string
	var resultChildren []string
	var result0 string
	result0 = lipgloss.NewStyle().Render(ternary(done, "[x]", "[ ]"))
	resultChildren = append(resultChildren, result0)
	var result1 string
	result1 = lipgloss.NewStyle().Render(text)
	resultChildren = append(resultChildren, result1)
	resultGap := strings.Repeat(" ", 1)
	resultJoined := strings.Join(resultChildren, resultGap)
	result = resultJoined
	return result
}
