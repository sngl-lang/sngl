package optimize

import (
	"os"
	"path/filepath"
	"testing"
)

func projectDir() string {
	// Walk up from the test file to find go.mod
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func TestExecPureGoFunc_Double(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}

	result, err := execPureGoFunc(
		dir,
		"git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg",
		"purepkg.Double",
		[]string{"int"},
		"int",
		[]any{5},
	)
	if err != nil {
		t.Fatalf("execPureGoFunc: %v", err)
	}
	if result != 10 {
		t.Errorf("expected 10, got %v (%T)", result, result)
	}
}

func TestExecPureGoFunc_Greet(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}

	result, err := execPureGoFunc(
		dir,
		"git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg",
		"purepkg.Greet",
		[]string{"string"},
		"string",
		[]any{"world"},
	)
	if err != nil {
		t.Fatalf("execPureGoFunc: %v", err)
	}
	if result != "Hello, world!" {
		t.Errorf("expected %q, got %v", "Hello, world!", result)
	}
}

func TestExecPureGoFunc_GetItems(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}

	result, err := execPureGoFunc(
		dir,
		"git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg",
		"purepkg.GetItems",
		nil,
		"list:item",
		nil,
	)
	if err != nil {
		t.Fatalf("execPureGoFunc: %v", err)
	}

	items, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", items[0])
	}
	if first["name"] != "alpha" {
		t.Errorf("expected name=alpha, got %v", first["name"])
	}
	if first["value"] != 1 {
		t.Errorf("expected value=1, got %v (%T)", first["value"], first["value"])
	}
}
