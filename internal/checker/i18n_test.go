package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestI18nValidNoWarning(t *testing.T) {
	src := `var name = "world"; var x = $"Hello {name}!"`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Warning && contains(d.Msg, "no static text") {
			t.Errorf("unexpected no-static-text warning: %s", d.Error())
		}
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

func TestI18nNoStaticTextWarn(t *testing.T) {
	src := `var n = 5; var x = $"{n}"`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	found := false
	for _, d := range diags {
		if d.Severity == ir.Warning && contains(d.Msg, "no static text") {
			found = true
		}
	}
	if !found {
		t.Error("expected no-static-text warning")
	}
}
