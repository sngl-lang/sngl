package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// extStubPlatform is a minimal ir.Platform used by extension-merge tests. It
// ships a single new-form `component sngl.text { platform extstub { ... } }`
// extension so the merge pass has something to splice into the stdlib's
// abstract `text` component.
type extStubPlatform struct {
	source string
}

func (extStubPlatform) PlatformIdentifier() string { return "extstub" }
func (extStubPlatform) Description() string        { return "extension-merge test stub" }
func (extStubPlatform) Resolve(string) ir.Symbol   { return nil }

func (p extStubPlatform) Package() []*ast.Document {
	doc, err := parser.Parse("extstub.sngl", []byte(withStd(p.source)))
	if err != nil {
		panic("extstub parse: " + err.Error())
	}
	return []*ast.Document{doc}
}

// TestExtensionMergeBasic exercises the platform-agnostic checker collection
// + lower-time swap. An extension platform ships a new-form
// `component sngl.text { platform extstub { ... } }` declaration. The checker
// stashes the checked IR body under stdText.PlatformBodies["extstub"]; the
// stdlib `text` component's own Body stays empty after Check (the checker
// does not know which platform is active). After running Lower with
// Options.Platform="extstub", the swap pass moves the platform body into
// stdText.Body so subsequent passes (and any inlining) see a body-bearing
// stdlib component. User code uses bare `text(...)` and must type-check.
func TestExtensionMergeBasic(t *testing.T) {
	const extSource = `
import sngl "sngl://std"

component sngl.text {
    platform extstub {
        image(src=value)
    }
}
`
	const userSource = `
component main {
    text(value="hi")
}
`
	doc, err := parser.Parse("main.sngl", []byte(withStd(userSource)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	plat := extStubPlatform{source: extSource}
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: []ir.Platform{plat},
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
	if pkg == nil {
		t.Fatal("nil pkg")
	}

	stdText, ok := pkg.Symbols.Comps["text"].(*ir.Component)
	if !ok || stdText == nil {
		t.Fatal("stdlib text component missing from symbol table")
	}

	// Checker contract: PlatformBodies has the extstub entry; the live
	// Component.Body stays empty until the lowering swap runs.
	if stdText.PlatformBodies == nil {
		t.Fatal("expected PlatformBodies populated, got nil")
	}
	body, ok := stdText.PlatformBodies["extstub"]
	if !ok {
		t.Fatalf("expected PlatformBodies[\"extstub\"], have keys %v", keys(stdText.PlatformBodies))
	}
	if len(body) == 0 {
		t.Errorf("expected stashed extstub body to be non-empty, got %d stmts", len(body))
	}
	if len(stdText.Body) != 0 {
		t.Errorf("expected stdlib text Body still empty pre-lower, got %d stmts", len(stdText.Body))
	}

	// Lower with the matching active platform — swap pass should move the
	// stashed body into Component.Body.
	if err := lower.Lower(pkg, lower.Caps{}, lower.Options{Platform: "extstub"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(stdText.Body) == 0 {
		t.Errorf("expected stdlib text Body to be swapped in by lower, got 0 stmts")
	}
}

func keys(m map[string][]ir.Stmt) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
