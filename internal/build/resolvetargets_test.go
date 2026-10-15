package build

import (
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// Selecting a declared target by flag must inherit that target's options: both
// flags together once built an empty one, dropping the declared stylesheet and
// with it the site's CSS. --opt still overrides.
func TestResolveTargets_CLIFlagsPreserveDeclaredOptions(t *testing.T) {
	pkg := &ir.Package{
		Outputs: []*ir.Output{{
			Lang:     "none",
			Platform: "html",
			Options:  &ir.StructLit{},
		}},
	}
	codegen.SetOptionField(pkg.Outputs[0].Options, "stylesheet", "public/style.css")

	targets, err := ResolveTargets(pkg, "none", "html", nil)
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

func TestResolveTargets_CLIFlagsUndeclaredTarget(t *testing.T) {
	pkg := &ir.Package{} // no output decls
	targets, err := ResolveTargets(pkg, "go", "bubbletea", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Platform != "bubbletea" || targets[0].Lang != "go" {
		t.Fatalf("unexpected targets: %+v", targets)
	}
}
