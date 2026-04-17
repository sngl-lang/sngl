package ir_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func FuzzConvertRoundTrip(f *testing.F) {
	// Seed corpus from testdata .sngl files.
	_, thisFile, _, _ := runtime.Caller(0)
	testdataDir := filepath.Join(filepath.Dir(thisFile), "..", "testdata")
	entries, err := os.ReadDir(testdataDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(testdataDir, e.Name()))
			if err == nil {
				f.Add(string(data))
			}
		}
	}

	f.Fuzz(func(t *testing.T, src string) {
		// Phase 1: parse and type-check the original source.
		// Recover from parser panics on malformed input.
		doc1, ok := safeParse(t, src)
		if !ok {
			t.Skip("parse failed")
		}
		pkg1, diags1 := checker.Check(doc1, &checker.Config{IsMain: true})
		for _, d := range diags1 {
			if d.Severity == ir.Error {
				t.Skip("check failed")
			}
		}

		// Phase 2: convert IR back to AST and type-check again.
		doc2 := ir.Convert(pkg1)
		_, diags2 := checker.Check(doc2, &checker.Config{IsMain: true})
		for _, d := range diags2 {
			if d.Severity == ir.Error {
				t.Fatalf("round-trip check error: %s", d.Error())
			}
		}
	})
}

func safeParse(t *testing.T, src string) (doc *ast.Document, ok bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	d, err := parser.Parse("fuzz.sngl", []byte(src))
	if err != nil {
		return nil, false
	}
	return d, true
}
