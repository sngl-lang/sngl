package checker_test

import (
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

func TestColorStructLitAssignableToColor(t *testing.T) {
	src := `const X color = color{r=255, g=255, b=255, a=255}`
	doc, err := parser.Parse("t.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

func TestColorHexAssignableToColor(t *testing.T) {
	// Baseline: the literal form already works. Pin it so the fix
	// doesn't accidentally break it.
	src := `const Y color = #ffffff`
	doc, err := parser.Parse("t.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}
