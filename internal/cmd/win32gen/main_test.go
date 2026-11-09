package main

import (
	"os"
	"testing"

	"duckfam.us/sngl"
	"duckfam.us/sngl/internal/parser"
)

// TestCommittedIsFormatted holds the committed api.sngl to what the tool
// writes it as, short of running the tool, which needs MinGW and the network:
// formatted, and so parsed. That every package of the library checks is
// internal/fixtures' TestLibraryHoldsNoInvalidType.
func TestCommittedIsFormatted(t *testing.T) {
	const path = "../../../lib/platform/win32/api/api.sngl"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parser.Parse("api.sngl", src)
	if err != nil {
		t.Fatal(err)
	}
	if got := sngl.Format(doc); got != string(src) {
		t.Errorf("%s is not as `sngl fmt` writes it; rerun `go tool win32gen`", path)
	}
}
