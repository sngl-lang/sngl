package main

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestResolveTargets_CLIFlagsPreserveDeclaredOptions guards a regression where
// passing both --platform and --lang on the CLI built the target with empty
// options, silently discarding the options declared in the source's
// `output { ... }` block (e.g. stylesheet="..."). That dropped the site's CSS.
// Selecting a declared target by flag must inherit that target's options; CLI
// -o/--opt values still override.
func TestResolveTargets_CLIFlagsPreserveDeclaredOptions(t *testing.T) {
	pkg := &ir.Package{
		Outputs: []*ir.Output{{
			Lang:     "none",
			Platform: "html",
			Options:  &ir.StructLit{},
		}},
	}
	codegen.SetOptionField(pkg.Outputs[0].Options, "stylesheet", "public/style.css")

	targets, err := resolveTargets(pkg, "none", "html", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("want 1 target, got %d", len(targets))
	}
	if got := codegen.OptionString(targets[0].Options, "stylesheet"); got != "public/style.css" {
		t.Errorf("declared stylesheet option lost when CLI flags select the target: got %q", got)
	}
}

// TestResolveTargets_CLIFlagsUndeclaredTarget confirms selecting a target NOT
// present in any output block still works (empty base options).
func TestResolveTargets_CLIFlagsUndeclaredTarget(t *testing.T) {
	pkg := &ir.Package{} // no output decls
	targets, err := resolveTargets(pkg, "go", "bubbletea", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Platform != "bubbletea" || targets[0].Lang != "go" {
		t.Fatalf("unexpected targets: %+v", targets)
	}
}
