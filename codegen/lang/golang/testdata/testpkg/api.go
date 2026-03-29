// Package testpkg provides test types for the Go scheme importer.
package testpkg

// Todo is a sample struct for testing.
type Todo struct {
	ID    int
	Title string
	Done  bool
}

// SaveTodo persists a todo item.
func SaveTodo(todo Todo) {}

// FormatDate formats a date string.
func FormatDate(d string) string { return d }

// FetchAll returns all todos.
func FetchAll() []Todo { return nil }

// Count is an exported variable.
var Count int

// unexported should not appear in imports.
var hidden string

func privateFn() {}
