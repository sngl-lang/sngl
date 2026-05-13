package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
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

func (extStubPlatform) PlatformIdentifier() string           { return "extstub" }
func (extStubPlatform) Description() string                  { return "extension-merge test stub" }
func (extStubPlatform) IsLanguageSupported(ir.Language) bool { return true }
func (extStubPlatform) Resolve(string) ir.Symbol             { return nil }

func (p extStubPlatform) Package() []*ast.Document {
	doc, err := parser.Parse("extstub.sngl", []byte(p.source))
	if err != nil {
		panic("extstub parse: " + err.Error())
	}
	return []*ast.Document{doc}
}

// TestExtensionMergeBasic exercises mergePlatformExtensions: an extension
// platform ships a new-form `component sngl.text { platform extstub { ... } }`
// declaration whose body should be spliced into the stdlib's `text` component
// at check time. User code uses bare `text(...)` and must type-check cleanly.
func TestExtensionMergeBasic(t *testing.T) {
	const extSource = `
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
	doc, err := parser.Parse("main.sngl", []byte(userSource))
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

	// Assert the stdlib `text` component now has a body (the spliced platform
	// block contained a single visual node).
	stdText, ok := pkg.Symbols.Comps["text"].(*ir.Component)
	if !ok || stdText == nil {
		t.Fatal("stdlib text component missing from symbol table")
	}
	if stdText.AST == nil || len(stdText.AST.Body.Stmts) == 0 {
		t.Errorf("expected stdlib text AST body to be spliced, got %d stmts", len(stdText.AST.Body.Stmts))
	}
}
