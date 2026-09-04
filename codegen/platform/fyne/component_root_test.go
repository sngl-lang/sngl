package fyne

import (
	"strings"
	"testing"
)

// TestAPlainInstanceHandleIsItsOwnRoot reads
// testdata/test_html_recursive_component.sngl, whose `tree` the build renders
// as a method of the Model rather than as an instance record.
//
// OnComponentRoot selected `.Root` from whatever handle it was given. Only a
// record has that field; a method's handle is the fyne.CanvasObject the render
// returned, so `__n1.Root` named a field the emitted file does not have.
// OnCreateComponent already decided which of the two the handle is -- that is
// what types the field -- and now says so where the root is bound.
//
// The claim is made against what the compiler still reports rather than
// against a whole-file compile: this fixture holds a second, separate defect
// -- `__nN__el` is declared inside the `else` block that binds it and read
// after it, so the local is both unused and undefined. That is passNodeEscape's
// and out of this change's reach; what must be gone is every complaint about
// Root. codegen/platform/gtk4/instance_handle_type_test.go carries the
// whole-file compile, over a fixture whose component IS a record and so keeps
// its `.Root`.
func TestAPlainInstanceHandleIsItsOwnRoot(t *testing.T) {
	model := generateFyneModelBuilt(t, fixtureSource(t, "test_html_recursive_component.sngl"))

	for _, want := range []string{
		"__n1 := m.renderTree((label + \".a\"), (depth - 1), (__depth + 1))",
		"__n1__el := __n1\n",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("emitted Go missing %q\n--- model.go ---\n%s", want, model)
		}
	}
	if strings.Contains(model, ".Root") {
		t.Errorf("a method-rendered component's handle is still asked for a Root field\n--- model.go ---\n%s", model)
	}
	errs := compileErrors(t, "fyne-component-root-", model)
	if strings.Contains(errs, "Root") {
		t.Errorf("go build still reports a Root field on a plain handle\n--- go build ---\n%s\n--- model.go ---\n%s", errs, model)
	}
}
