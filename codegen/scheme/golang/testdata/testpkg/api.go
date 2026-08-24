// Package testpkg provides test types for the Go scheme importer.
package testpkg

import (
	"context"
	"time"
)

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

// WithCtx has a leading context.Context; importer strips it and records
// HasContextArg on the SNGL-visible signature.
func WithCtx(ctx context.Context, msg string) string { return msg }

// MaybeFail returns (T, error); importer unwraps to T and records
// HasErrorReturn.
func MaybeFail(name string) (string, error) { return name, nil }

// WithCtxAndErr combines both adaptations.
func WithCtxAndErr(ctx context.Context, q string) (Todo, error) { return Todo{}, nil }

// MultiReturn has multiple non-error returns; importer marks it unusable.
func MultiReturn() (string, int) { return "", 0 }

// ReturnsMap returns a Go type not representable in SNGL; importer marks
// the function unusable.
func ReturnsMap() map[string]int { return nil }

// Bag exercises per-field unusability: only Bad is unusable, OK is fine.
type Bag struct {
	OK  string
	Bad map[string]int
}

// Stringer is an interface; fields/params/returns of interface type are
// exposed to SNGL as explicit dyn (still usable, just opaque).
type Stringer interface {
	String() string
}

// Carrier has an interface-typed field; the field stays usable as dyn.
type Carrier struct {
	Handler Stringer
}

// WithIface takes an interface parameter; usable via dyn.
func WithIface(h Stringer) string { return "" }

// Count is an exported variable.
var Count int

// unexported should not appear in imports.
var hidden string

func privateFn() {}

// Priority is a named type whose underlying type is a basic int, so the
// importer types it as int unless a mapping is registered for it. That makes it
// the case that pins the registry lookup ahead of the Underlying() dispatch.
type Priority int

// Rank returns a Priority.
func Rank() Priority { return 0 }

// Stamped returns a time.Time. Without a registered mapping the importer takes
// it for an opaque struct from another package and marks the function unusable,
// so usability is what shows the mapping firing here — the resolved SNGL type
// needs a loaded standard library, which this package has no way to reach.
func Stamped() time.Time { return time.Time{} }
