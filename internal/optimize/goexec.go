//go:build !js

package optimize

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var pureCache sync.Map // funcName+args → result

// execPureGoFunc runs a pure Go function at compile time via gen-and-run.
// It generates a temporary main.go, runs it with `go run`, and parses the JSON output.
func execPureGoFunc(dir, importPath, nativeType string, paramTypes []string, returnType string, args []any) (any, error) {
	// Split nativeType "pkgname.FuncName" into just the function name
	_, funcName, _ := strings.Cut(nativeType, ".")

	// Check cache
	cacheKey := fmt.Sprintf("%s:%v", nativeType, args)
	if cached, ok := pureCache.Load(cacheKey); ok {
		return cached, nil
	}

	// Build argument literals for Go source
	var goArgs []string
	for _, arg := range args {
		goArgs = append(goArgs, goLiteral(arg))
	}

	// Generate temporary Go source
	src := fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"

	pkg %q
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic: %%v\n", r)
			os.Exit(1)
		}
	}()
	result := pkg.%s(%s)
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
`, importPath, funcName, strings.Join(goArgs, ", "))

	// Write temp directory inside project
	tmpDir := filepath.Join(dir, ".sngl-goexec")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(tmpFile, []byte(src), 0o644); err != nil {
		return nil, fmt.Errorf("writing temp file: %w", err)
	}

	// Run with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "run", "./.sngl-goexec/")
	cmd.Dir = dir
	slog.Info("exec", "cmd", "go run ./.sngl-goexec/", "dir", dir, "func", nativeType)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("compile-time evaluation of %s failed: %s", nativeType, string(ee.Stderr))
		}
		return nil, fmt.Errorf("compile-time evaluation of %s failed: %w", nativeType, err)
	}

	// Parse JSON result
	var result any
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing result of %s: %w", nativeType, err)
	}

	// Normalize JSON numbers to int where possible
	result = normalizeJSON(result)

	// Cache and return
	pureCache.Store(cacheKey, result)
	return result, nil
}

// lowerFirst lowercases the first letter of a string, matching SNGL field naming convention.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	if strings.ToUpper(s) == s {
		return strings.ToLower(s)
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// goLiteral converts a SNGL value to a Go literal string for code generation.
func goLiteral(v any) string {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case int:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%g", val)
	case bool:
		return fmt.Sprintf("%t", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// normalizeJSON converts JSON float64 numbers to int where they are whole numbers,
// and recursively normalizes nested structures.
func normalizeJSON(v any) any {
	switch val := v.(type) {
	case float64:
		if val == float64(int(val)) {
			return int(val)
		}
		return val
	case []any:
		for i, el := range val {
			val[i] = normalizeJSON(el)
		}
		return val
	case map[string]any:
		normalized := make(map[string]any, len(val))
		for k, el := range val {
			normalized[lowerFirst(k)] = normalizeJSON(el)
		}
		return normalized
	default:
		return v
	}
}
