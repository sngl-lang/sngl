package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestLSPFixturesTypeCheck(t *testing.T) {
	dirs := []string{
		"../../testdata/lsp",
		"../../testdata/lsp_hover",
	}
	for _, dir := range dirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*.sngl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range matches {
			t.Run(filepath.Base(path), func(t *testing.T) {
				src, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				doc, perr := parser.Parse(filepath.Base(path), src)
				if perr != nil {
					t.Fatalf("parse: %v", perr)
				}
				_, diags := checker.Check(doc, &checker.Config{IsMain: true})
				for _, d := range diags {
					if d.Severity == ir.Error {
						t.Errorf("check error: %s", d.Error())
					}
				}
			})
		}
	}
}
