package fyne

import (
	"strings"
	"testing"
)

// The optimizer unrolls test_html_recursive_component.sngl's recursion, so
// nothing the program renders instantiates `tree`.
func TestAnUnrenderedRecursionIsNotEmitted(t *testing.T) {
	model := generateFyneModelBuilt(t, fixtureSource(t, "test_html_recursive_component.sngl"))
	if strings.Contains(model, "renderTree") {
		t.Errorf("the unrolled recursion is still emitted\n--- model.go ---\n%s", model)
	}
	if errs := compileErrors(t, "fyne-unrendered-recursion-", model); errs != "" {
		t.Errorf("go build:\n%s\n--- model.go ---\n%s", errs, model)
	}
}
