package codegen

import (
	"errors"
	"fmt"
	"testing"
)

// The matrix in `sngl test --platform=all` tells "this target does not support
// this component" from "this target is broken" by the error's type, so it has
// to survive the wrapping a platform generator puts around it. A skip that
// stops matching becomes a failure; one that matches too much hides a real
// break -- both directions are asserted.
func TestUnimplementedComponentIsRecognisableWhenWrapped(t *testing.T) {
	missing := &UnimplementedComponent{Component: "badge", Platform: "gtk4"}

	if got, want := missing.Error(), `component "badge" has no gtk4 implementation`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	wrapped := []error{
		missing,
		fmt.Errorf("emit: %w", missing),
		fmt.Errorf("generate: %w", fmt.Errorf("emit: %w", missing)),
		errors.Join(errors.New("unrelated"), missing),
	}
	for i, err := range wrapped {
		got, ok := errors.AsType[*UnimplementedComponent](err)
		if !ok {
			t.Errorf("case %d: not recognised through the wrapping: %v", i, err)
			continue
		}
		if got.Component != "badge" || got.Platform != "gtk4" {
			t.Errorf("case %d: got %+v, want badge/gtk4", i, got)
		}
	}

	// An ordinary failure must not be read as an unsupported component, or a
	// genuinely broken target would skip instead of failing.
	for _, err := range []error{
		errors.New("gtk4: no widget for node \"frobnicate\""),
		fmt.Errorf("gtk4: component %q has a gtk4 implementation that did not lower to a widget", "text"),
		errors.New("exit status 1"),
	} {
		if _, ok := errors.AsType[*UnimplementedComponent](err); ok {
			t.Errorf("%q was read as an unsupported component", err)
		}
	}
}
