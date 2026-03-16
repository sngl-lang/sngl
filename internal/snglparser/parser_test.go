package snglparser_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/snglparser"
)

// TestParseFixtures parses each .sngl fixture file and verifies it round-trips
// through Format without error.
func TestParseFixtures(t *testing.T) {
	dir := filepath.Join("..", "testdata")
	matches, err := filepath.Glob(filepath.Join(dir, "*.sngl"))
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range matches {
		base := filepath.Base(path)
		// Skip error fixtures — they intentionally fail parsing/checking.
		if strings.HasPrefix(base, "error_") {
			continue
		}
		name := strings.TrimSuffix(base, ".sngl")

		t.Run(name, func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			doc, err := snglparser.Parse(name+".sngl", f)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			// Round-trip: format and re-parse
			formatted := snglparser.Format(doc)
			doc2, err := snglparser.Parse(name+".sngl", strings.NewReader(formatted))
			if err != nil {
				t.Fatalf("re-parse error after format: %v\nformatted:\n%s", err, formatted)
			}

			// Format from re-parsed doc should be idempotent (map order is
			// now deterministic since it came from a sequential parse).
			formatted2 := snglparser.Format(doc2)
			doc3, err := snglparser.Parse(name+".sngl", strings.NewReader(formatted2))
			if err != nil {
				t.Fatalf("second re-parse error: %v\nformatted:\n%s", err, formatted2)
			}
			formatted3 := snglparser.Format(doc3)
			if formatted2 != formatted3 {
				t.Errorf("format not idempotent after stabilization:\nsecond:\n%s\nthird:\n%s", formatted2, formatted3)
			}
		})
	}
}
