package gtk4

import (
	"strings"
	"testing"
)

// TestAnInstanceHandleFieldIsTypedAsTheRecord compiles the Go emitted for
// testdata/test_recursive_component.sngl, whose recursive `tree_view` the
// build renders as an instance record.
//
// The ctor and the record were right; the Model field holding one was not.
// compilation.widgetFieldSink built the Go type itself -- gtk4rt.Handle in
// wrapped mode, *C.<class> otherwise -- where the other three sinks go through
// widgetFieldGoType, which is what knows that a component instance's handle
// arrives already spelled as Go and is not a widget in either mode. The two
// disagreed and the field lost, so `m.__n0 gtk4rt.Handle` was assigned a
// *Tree_viewInstance.
func TestAnInstanceHandleFieldIsTypedAsTheRecord(t *testing.T) {
	model := generateGTK4ModelBuilt(t, fixtureSource(t, "test_recursive_component.sngl"))

	for _, want := range []string{
		"__n0     *Tree_viewInstance",
		"m.__n0 = newTree_viewInstance(",
		// A record does carry a Root, so this is also the positive half of
		// what OnComponentRoot decides.
		"m.__n0__el = m.__n0.Root",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("emitted Go missing %q\n--- model.go ---\n%s", want, model)
		}
	}
	buildGeneratedGo(t, "gtk4-handle-type-", model)
}

// TestAPlainInstanceHandleIsItsOwnRoot is the other half of the same decision,
// over testdata/test_html_recursive_component.sngl -- whose `tree` the build
// renders as a method of the Model, so the handle is the gtk4rt.Handle the
// render returned and has no Root field to select.
//
// Asserted against what the compiler still reports rather than a whole-file
// compile: this fixture holds a second, separate defect -- `__nN__el` is
// declared inside the `else` block that binds it and read after it, which is
// passNodeEscape's and out of this change's reach.
func TestAPlainInstanceHandleIsItsOwnRoot(t *testing.T) {
	model := generateGTK4ModelBuilt(t, fixtureSource(t, "test_html_recursive_component.sngl"))

	if !strings.Contains(model, "__n1__el := __n1\n") {
		t.Errorf("a method-rendered component's handle is not bound as its own root\n--- model.go ---\n%s", model)
	}
	if strings.Contains(model, ".Root") {
		t.Errorf("a method-rendered component's handle is still asked for a Root field\n--- model.go ---\n%s", model)
	}
	if errs := compileErrors(t, "gtk4-component-root-", model); strings.Contains(errs, "Root") {
		t.Errorf("go build still reports a Root field on a plain handle\n--- go build ---\n%s\n--- model.go ---\n%s", errs, model)
	}
}
