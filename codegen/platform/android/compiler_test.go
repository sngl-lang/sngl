package android

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func compileAndVerify(t *testing.T, doc *ast.Document) []byte {
	t.Helper()
	src, err := Compile(doc, Config{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Verify basic structural elements of generated Kotlin
	code := string(src)
	if !strings.Contains(code, "package test.sngl.app") {
		t.Errorf("missing package declaration")
	}
	if !strings.Contains(code, "@Composable") {
		t.Errorf("missing @Composable annotation")
	}
	if !strings.Contains(code, "fun MainScreen()") {
		t.Errorf("missing MainScreen function")
	}
	// Check balanced braces
	opens := strings.Count(code, "{")
	closes := strings.Count(code, "}")
	if opens != closes {
		t.Errorf("unbalanced braces: %d opens, %d closes", opens, closes)
	}
	return src
}

func TestFixtures(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		if len(s.Errors) > 0 {
			continue
		}
		t.Run(s.Name, func(t *testing.T) {
			doc, err := parser.Parse(s.Filename, strings.NewReader(s.Source))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := checker.Check(doc, s.FS, s.Dir, checker.DefaultResolver(), nil, nil, nil, true); err != nil {
				t.Fatalf("check: %v", err)
			}
			compileAndVerify(t, doc)
		})
	}
}
