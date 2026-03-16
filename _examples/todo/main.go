package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
)

//go:generate go tool sngl compile todo.sngl

func main() {
	m := New()
	path := savePath()
	m = m.SetTodos(loadTodos(path))
	p := tea.NewProgram(m)
	final, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	saveTodos(path, final.(Model).Todos())
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
