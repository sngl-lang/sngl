package checker

import (
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/parser"
)

func parseFixture(t *testing.T, name string) *ast.Document {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	defer f.Close()
	doc, err := parser.Parse(name, f)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return doc
}

func checkFixture(t *testing.T, name string) {
	t.Helper()
	doc := parseFixture(t, name)
	if err := Check(doc); err != nil {
		t.Fatalf("check %s: %v", name, err)
	}
}

func checkFixtureExpectError(t *testing.T, name string, substr string) {
	t.Helper()
	doc := parseFixture(t, name)
	err := Check(doc)
	if err == nil {
		t.Fatalf("check %s: expected error containing %q, got nil", name, substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("check %s: error %q does not contain %q", name, err.Error(), substr)
	}
}

func TestValidMinimal(t *testing.T) {
	checkFixture(t, "valid_minimal.kdl")
}

func TestValidBinds(t *testing.T) {
	checkFixture(t, "valid_binds.kdl")
}

func TestValidComponent(t *testing.T) {
	checkFixture(t, "valid_component.kdl")
}

func TestValidFull(t *testing.T) {
	checkFixture(t, "valid_full.kdl")
}

func TestErrorUnknownProp(t *testing.T) {
	checkFixtureExpectError(t, "error_unknown_prop.kdl", "unknown property")
}

func TestErrorTypeMismatch(t *testing.T) {
	checkFixtureExpectError(t, "error_type_mismatch.kdl", "expected bool")
}

func TestErrorUnknownComponent(t *testing.T) {
	checkFixtureExpectError(t, "error_unknown_component.kdl", "unknown component")
}

func TestErrorInvalidEvent(t *testing.T) {
	checkFixtureExpectError(t, "error_invalid_event.kdl", "unknown event")
}

func TestErrorIfNotBool(t *testing.T) {
	checkFixtureExpectError(t, "error_if_not_bool.kdl", "expected bool")
}

func TestErrorChildrenPolicy(t *testing.T) {
	checkFixtureExpectError(t, "error_children_policy.kdl", "does not accept children")
}

func TestErrorBadStyle(t *testing.T) {
	checkFixtureExpectError(t, "error_bad_style.kdl", "unknown style property")
}

func TestErrorEventNotMutation(t *testing.T) {
	checkFixtureExpectError(t, "error_event_not_mutation.kdl", "must return Mutation")
}

func TestErrorMissingRequired(t *testing.T) {
	checkFixtureExpectError(t, "error_missing_required.kdl", "required")
}

func TestParserFullExample(t *testing.T) {
	// full_example.kdl uses User{} proto message literal which requires import
	// resolution (not yet implemented). Verify it fails with an expected CEL error
	// rather than a panic or unexpected failure.
	f, err := os.Open("../parser/testdata/full_example.kdl")
	if err != nil {
		t.Fatalf("open full_example.kdl: %v", err)
	}
	defer f.Close()
	doc, err := parser.Parse("full_example.kdl", f)
	if err != nil {
		t.Fatalf("parse full_example.kdl: %v", err)
	}
	err = Check(doc)
	if err == nil {
		t.Fatal("expected error due to unresolved User type")
	}
	if !strings.Contains(err.Error(), "undeclared reference to 'User'") {
		t.Fatalf("unexpected error: %v", err)
	}
}
