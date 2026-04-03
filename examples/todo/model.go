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

type Color struct {
	R int
	G int
	B int
	A int
}

type Todo struct {
	Text string
	Done bool
}

// Model is the Bubble Tea model for this SNGL UI.
type Model struct {
	newTodo string
	todos   []Todo

	input0 textinput.Model

	todosCursor int

	focus         int
	width, height int
}

// New creates a Model with default bind values.
func New() Model {
	m := Model{
		newTodo: "",
		todos:   nil,
	}
	m.input0 = textinput.New()
	m.input0.Placeholder = "Buy eggs"
	m.input0.SetValue(m.newTodo)
	m.input0.Focus()
	return m
}

func (m Model) status() string {
	return (("Todo List (" + fmt.Sprint(len(m.todos))) + " items)")
}

func (m Model) NewTodo() string {
	return m.newTodo
}

func (m Model) SetNewTodo(v string) Model {
	m.newTodo = v
	m.input0.SetValue(m.newTodo)
	return m
}

type setNewTodoMsg struct{ value string }

func SetNewTodoCmd(v string) tea.Cmd {
	return func() tea.Msg { return setNewTodoMsg{value: v} }
}

func (m Model) Todos() []Todo {
	return m.todos
}

func (m Model) SetTodos(v []Todo) Model {
	m.todos = v
	return m
}

type setTodosMsg struct{ value []Todo }

func SetTodosCmd(v []Todo) tea.Cmd {
	return func() tea.Msg { return setTodosMsg{value: v} }
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case setNewTodoMsg:
		m = m.SetNewTodo(msg.value)
	case setTodosMsg:
		m = m.SetTodos(msg.value)
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
			m.todos = push(m.todos, Todo{Text: m.newTodo, Done: false})
			m.newTodo = ""
			m.input0.SetValue(m.newTodo)
		case msg.Code == tea.KeyEnter && m.focus == 2:
			if m.todosCursor < len(m.todos) {
				index := m.todosCursor
				m.todos[index].Done = !m.todos[index].Done
			}
		case msg.Code == tea.KeyUp && m.focus == 2:
			if m.todosCursor > 0 {
				m.todosCursor--
			}
		case msg.Code == tea.KeyDown && m.focus == 2:
			if m.todosCursor < len(m.todos)-1 {
				m.todosCursor++
			}
		case msg.Code == tea.KeyEnter && m.focus == 3:
			m.todos = remove(m.todos, (len(m.todos) - 1))
		}
	}
	if m.focus == 0 {
		m.input0, cmd = m.input0.Update(msg)
		m.newTodo = m.input0.Value()
	}
	return m, cmd
}

func (m Model) View() tea.View {
	var content string
	var contentChildren []string
	var content_0 string
	content_0 = lipgloss.NewStyle().
		Bold(true).Render(m.status())
	contentChildren = append(contentChildren, content_0)
	var content_1 string
	var content_1Children []string
	var content_1_0 string
	content_1_0 = m.input0.View()
	content_1Children = append(content_1Children, content_1_0)
	var content_1_1 string
	content_1_1Focused := m.focus == 1
	content_1_1Prefix := " "
	if content_1_1Focused {
		content_1_1Prefix = ">"
	}
	content_1_1 = lipgloss.NewStyle().Render(content_1_1Prefix + " " + "Add")
	content_1Children = append(content_1Children, content_1_1)
	content_1Gap := strings.Repeat(" ", 1)
	content_1Joined := strings.Join(content_1Children, content_1Gap)
	content_1 = lipgloss.NewStyle().
		AlignHorizontal(lipgloss.Center).Render(content_1Joined)
	contentChildren = append(contentChildren, content_1)
	var content_2 string
	var content_2Children []string
	var content_2_0 string
	var content_2_0Items []string
	for index, item := range m.todos {
		_ = index
		var content_2_0Item string
		content_2_0ItemFocused := m.focus == 2 && m.todosCursor == index
		content_2_0ItemPrefix := " "
		if content_2_0ItemFocused {
			content_2_0ItemPrefix = ">"
		}
		content_2_0Item = lipgloss.NewStyle().Render(content_2_0ItemPrefix + " " + ternary(item.Done, "[x] ", "[ ] ") + item.Text)
		content_2_0Items = append(content_2_0Items, content_2_0Item)
	}
	content_2_0 = strings.Join(content_2_0Items, "\n")
	content_2Children = append(content_2Children, content_2_0)
	content_2Gap := strings.Repeat("\n", 1)
	content_2Joined := strings.Join(content_2Children, content_2Gap)
	content_2 = content_2Joined
	contentChildren = append(contentChildren, content_2)
	var content_3 string
	content_3Focused := m.focus == 3
	content_3Prefix := " "
	if content_3Focused {
		content_3Prefix = ">"
	}
	content_3 = lipgloss.NewStyle().Render(content_3Prefix + " " + "Remove")
	contentChildren = append(contentChildren, content_3)
	contentGap := strings.Repeat("\n", 1)
	contentJoined := strings.Join(contentChildren, contentGap)
	content = lipgloss.NewStyle().
		Padding(2).Render(contentJoined)
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}
