package optimize

import (
	"os"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
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

// purepkgCtx is an evalCtx whose go:// imports are the purepkg test functions,
// so execPureGoFunc builds its evaluator from exactly those.
func purepkgCtx(dir string) *evalCtx {
	const path = "git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg"
	fn := func(name string, params []*ir.Param, ret *ir.Type) *ir.Func {
		return &ir.Func{
			Name:       name,
			NativeName: "purepkg." + name,
			NativePkg:  "purepkg",
			Purity:     ir.PurityPure,
			Params:     params,
			Return:     ret,
		}
	}
	return &evalCtx{
		dir: dir,
		nativeImports: map[string]*ir.NativeImport{"purepkg": {
			ImportPath: path,
			Funcs: []*ir.Func{
				fn("Double", []*ir.Param{{Name: "x", Type: ir.TypInt}}, ir.TypInt),
				fn("Greet", []*ir.Param{{Name: "name", Type: ir.TypString}}, ir.TypString),
				fn("GetItems", nil, nil),
				fn("Boom", nil, ir.TypString),
			},
		}},
		nativeSchemes: map[string]string{"purepkg": "go"},
	}
}

func TestExecPureGoFunc_Double(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}

	result, err := execPureGoFunc(
		purepkgCtx(dir),
		"git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg",
		"purepkg.Double",
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
		purepkgCtx(dir),
		"git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg",
		"purepkg.Greet",
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
		purepkgCtx(dir),
		"git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg",
		"purepkg.GetItems",
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

// A panic in one function must not take the evaluator down: the next call
// still has to be answered by the same resident process.
func TestExecPureGoFunc_PanicIsContained(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	ctx := purepkgCtx(dir)

	if _, err := execPureGoFunc(ctx, "git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg",
		"purepkg.Boom", nil); err == nil {
		t.Fatal("expected a panicking function to fail")
	}

	got, err := execPureGoFunc(ctx, "git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg",
		"purepkg.Greet", []any{"again"})
	if err != nil {
		t.Fatalf("evaluator unusable after a panic: %v", err)
	}
	if got != "Hello, again!" {
		t.Errorf("got %v, want %q", got, "Hello, again!")
	}
}

// One evaluator answers every call: a second request must not rebuild.
func TestEvaluatorIsReused(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}
	ctx := purepkgCtx(dir)

	first, err := evaluatorFor(ctx)
	if err != nil {
		t.Fatalf("evaluatorFor: %v", err)
	}
	second, err := evaluatorFor(ctx)
	if err != nil {
		t.Fatalf("evaluatorFor: %v", err)
	}
	if first != second {
		t.Error("a second call built a second evaluator")
	}
}
