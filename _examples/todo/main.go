package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Todo represents a single todo item.
type Todo struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type model struct {
	todos    []Todo
	input    textinput.Model
	cursor   int // -1 = input focused, 0..n = todo items
	savePath string
}

func savePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sngl-todo.json")
}

func loadTodos(path string) []Todo {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var todos []Todo
	if json.Unmarshal(data, &todos) != nil {
		return nil
	}
	return todos
}

func saveTodos(path string, todos []Todo) {
	data, _ := json.MarshalIndent(todos, "", "  ")
	os.WriteFile(path, data, 0644)
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "What needs to be done?"
	ti.Focus()
	ti.SetWidth(40)

	sp := savePath()
	return model{
		todos:    loadTodos(sp),
		input:    ti,
		cursor:   -1,
		savePath: sp,
	}
}

func (m model) Init() tea.Cmd {
	return m.input.Focus()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "tab":
			if m.cursor < len(m.todos)-1 {
				m.cursor++
			} else {
				m.cursor = -1
			}
			return m, m.updateFocus()
		case "shift+tab":
			if m.cursor > -1 {
				m.cursor--
			} else if len(m.todos) > 0 {
				m.cursor = len(m.todos) - 1
			}
			return m, m.updateFocus()
		case "enter":
			if m.cursor == -1 {
				text := strings.TrimSpace(m.input.Value())
				if text != "" {
					m.todos = append(m.todos, Todo{Text: text})
					m.input.SetValue("")
					saveTodos(m.savePath, m.todos)
				}
				return m, nil
			}
			m.todos[m.cursor].Done = !m.todos[m.cursor].Done
			saveTodos(m.savePath, m.todos)
			return m, nil
		case "d":
			if m.cursor >= 0 && m.cursor < len(m.todos) {
				m.todos = append(m.todos[:m.cursor], m.todos[m.cursor+1:]...)
				if m.cursor >= len(m.todos) {
					m.cursor = len(m.todos) - 1
				}
				saveTodos(m.savePath, m.todos)
				return m, nil
			}
		}
	}

	if m.cursor == -1 {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *model) updateFocus() tea.Cmd {
	if m.cursor == -1 {
		return m.input.Focus()
	}
	m.input.Blur()
	return nil
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7D56F4"))
	itemStyle   = lipgloss.NewStyle()
	doneStyle   = lipgloss.NewStyle().Faint(true)
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4"))
	helpStyle   = lipgloss.NewStyle().Faint(true)
)

func (m model) View() tea.View {
	var b strings.Builder

	b.WriteString(headerStyle.Render(fmt.Sprintf("Todo List (%d items)", len(m.todos))))
	b.WriteString("\n\n")

	if m.cursor == -1 {
		b.WriteString(cursorStyle.Render("> "))
	} else {
		b.WriteString("  ")
	}
	b.WriteString(m.input.View())
	b.WriteString("\n\n")

	for i, todo := range m.todos {
		prefix := "  "
		if i == m.cursor {
			prefix = cursorStyle.Render("> ")
		}

		check := "[ ]"
		style := itemStyle
		if todo.Done {
			check = "[x]"
			style = doneStyle
		}

		b.WriteString(fmt.Sprintf("%s%s\n", prefix, style.Render(check+" "+todo.Text)))
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("tab/shift-tab: navigate • enter: add/toggle • d: delete • ctrl+c: quit"))
	b.WriteString("\n")

	return tea.NewView(b.String())
}

func main() {
	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
