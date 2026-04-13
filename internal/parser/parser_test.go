package parser_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

// TestParseFixtures parses each .sngl fixture file and verifies it round-trips
// through Format without error.
func TestParseFixtures(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		if strings.HasPrefix(s.Name, "error_") || s.ExpectsError("parse") {
			continue
		}
		t.Run(s.Name, func(t *testing.T) {
			doc, err := parser.Parse(s.Filename, strings.NewReader(s.Source))
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			// Round-trip: format and re-parse
			formatted := parser.Format(doc)
			doc2, err := parser.Parse(s.Filename, strings.NewReader(formatted))
			if err != nil {
				t.Fatalf("re-parse error after format: %v\nformatted:\n%s", err, formatted)
			}

			// Format from re-parsed doc should be idempotent (map order is
			// now deterministic since it came from a sequential parse).
			formatted2 := parser.Format(doc2)
			doc3, err := parser.Parse(s.Filename, strings.NewReader(formatted2))
			if err != nil {
				t.Fatalf("second re-parse error: %v\nformatted:\n%s", err, formatted2)
			}
			formatted3 := parser.Format(doc3)
			if formatted2 != formatted3 {
				t.Errorf("format not idempotent after stabilization:\nsecond:\n%s\nthird:\n%s", formatted2, formatted3)
			}
		})
	}
}
