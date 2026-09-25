package gtk4

import (
	"regexp"
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

	// The field declaration is matched as a pattern because gofmt aligns a
	// struct's types to its widest field name: a literal run of spaces asserts
	// what else is in the Model rather than what this field's type is.
	for _, want := range []string{
		`__n0\s+\*Tree_viewInstance`,
		`m\.__n0 = newTree_viewInstance\(`,
		// A record does carry a Root, so this is also the positive half of
		// what OnComponentRoot decides.
		`m\.__n0__el = m\.__n0\.Root`,
	} {
		if !regexp.MustCompile(want).MatchString(model) {
			t.Errorf("emitted Go missing %s\n--- model.go ---\n%s", want, model)
		}
	}
	buildGeneratedGo(t, "gtk4-handle-type-", model)
}

// TestAnUnrenderedRecursionIsNotEmitted is over
// testdata/test_html_recursive_component.sngl, whose recursion the optimizer
// unrolls: nothing the program renders instantiates `tree` afterwards. It was
// emitted anyway, as a Model method whose handle was the widget it returned,
// with `__nN__el` declared inside the `else` that bound it and read after it.
func TestAnUnrenderedRecursionIsNotEmitted(t *testing.T) {
	model := generateGTK4ModelBuilt(t, fixtureSource(t, "test_html_recursive_component.sngl"))
	if strings.Contains(model, "renderTree") || strings.Contains(model, "TreeInstance") {
		t.Errorf("the unrolled recursion is still emitted\n--- model.go ---\n%s", model)
	}
	buildGeneratedGo(t, "gtk4-unrendered-recursion-", model)
}
