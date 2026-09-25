package fyne

import (
	"strings"
	"testing"
)

// TestAnUnrenderedRecursionIsNotEmitted reads
// testdata/test_html_recursive_component.sngl, whose recursion the optimizer
// unrolls: nothing the program renders instantiates `tree` afterwards. It was
// emitted anyway, as a Model method whose `__nN__el` was declared inside the
// `else` that bound it and read after it, so the file did not build.
func TestAnUnrenderedRecursionIsNotEmitted(t *testing.T) {
	model := generateFyneModelBuilt(t, fixtureSource(t, "test_html_recursive_component.sngl"))
	if strings.Contains(model, "renderTree") {
		t.Errorf("the unrolled recursion is still emitted\n--- model.go ---\n%s", model)
	}
	if errs := compileErrors(t, "fyne-unrendered-recursion-", model); errs != "" {
		t.Errorf("go build:\n%s\n--- model.go ---\n%s", errs, model)
	}
}
