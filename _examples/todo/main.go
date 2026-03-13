package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
)

//go:generate go tool sngl compile todo.sngl.kdl

func main() {
	m := New()
	path := savePath()
	m.Todos = loadTodos(path)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	saveTodos(path, m.Todos)
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
