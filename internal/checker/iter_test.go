package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestIterTypeResolves(t *testing.T) {
	src := `func count(xs iter<int>) int { return 0 }`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
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

func TestIterWrongArity(t *testing.T) {
	src := `func count(xs iter<int, string>) int { return 0 }`
	doc, _ := parser.Parse("test.sngl", []byte(withStd(src)))
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error {
			found = true
		}
	}
	if !found {
		t.Error("expected error for iter<int, string>")
	}
}

func TestListAssignableToIter(t *testing.T) {
	src := `func count(xs iter<int>) int { return 0 }
var lst list<int> = [1, 2, 3]
var n = count(lst)`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
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

func TestForLoopOnIter(t *testing.T) {
	src := `func count(xs iter<int>) int {
    var n = 0
    for x = xs { n = n + x }
    return n
}`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
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

func TestForLoopOnMap(t *testing.T) {
	src := `var m map<string, int> = {a = 1, b = 2}
func test() int {
    var total = 0
    for k, v = m {
        total = total + v
    }
    return total
}`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
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

func TestForLoopOnMapSingleVarErrors(t *testing.T) {
	src := `var m map<string, int> = {a = 1}
func test() {
    for k = m {}
}`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	found := false
	for _, d := range diags {
		if d.Severity == ir.Error {
			found = true
		}
	}
	if !found {
		t.Error("expected error for single-var map iteration")
	}
}
