package lsp

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lspcore"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func hasErrors(diags []ir.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == ir.Error {
			return true
		}
	}
	return false
}

func TestCompletionContext(t *testing.T) {
	tests := []struct {
		name    string
		content string
		line    int
		col     int
		want    lspcore.CompletionCtx
	}{
		{
			"top level",
			"import \"foo\"\n\n",
			3, 1,
			lspcore.CtxTopLevel,
		},
		{
			"inside component",
			"component main {\n    \n}",
			2, 5,
			lspcore.CtxComponent,
		},
		{
			"inside visual node",
			"component main {\n    vbox {\n        \n    }\n}",
			3, 9,
			lspcore.CtxVisualNode,
		},
		{
			"event handler",
			"component main {\n    vbox {\n        @click\n    }\n}",
			3, 9,
			lspcore.CtxEventHandler,
		},
		{
			"style context",
			"component main {\n    vbox {\n        style={gap\n    }\n}",
			3, 9,
			lspcore.CtxStyleProp,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lspcore.CompletionContext(tt.content, tt.line, tt.col)
			if got != tt.want {
				t.Errorf("CompletionContext() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTopLevelKeywords(t *testing.T) {
	items := lspcore.TopLevelKeywords()
	if len(items) == 0 {
		t.Fatal("expected top level keywords")
	}
	found := false
	for _, item := range items {
		if item.Label == "component" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'component' in top level keywords")
	}
}

func TestComponentKeywords(t *testing.T) {
	items := lspcore.ComponentKeywords("", nil)
	labels := map[string]bool{}
	for _, item := range items {
		labels[item.Label] = true
	}
	for _, kw := range []string{"var", "const", "if", "for"} {
		if !labels[kw] {
			t.Errorf("expected %q in component keywords", kw)
		}
	}
}

func TestExpressionCompletionsFromScope(t *testing.T) {
	src := `const MAX = 10
var count = 0
func greeting() => "hi"
struct User {
    name string
}
enum Status {
    active
    inactive
}

component main ui {
    vbox {
    }
}
`
	doc, err := parser.Parse("t.sngl", []byte(withStdSrc(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Run the checker to exercise the real pipeline; completion itself walks
	// the AST, so diagnostics being absent is enough.
	if _, diags := checker.Check(doc, &checker.Config{IsMain: true}); hasErrors(diags) {
		t.Fatalf("check: %v", diags)
	}
	items := lspcore.ExpressionCompletions(doc)
	labels := map[string]bool{}
	for _, item := range items {
		labels[item.Label] = true
	}
	for _, name := range []string{"true", "false", "null", "count", "greeting", "MAX", "User", "Status"} {
		if !labels[name] {
			t.Errorf("expected %q in expression completions", name)
		}
	}
}

// A `style=` prop takes sngl:ui's Style, so its fields are the vocabulary,
// with the type each one takes.
func TestStylePropCompletions(t *testing.T) {
	items := lspcore.StylePropCompletions()
	if len(items) == 0 {
		t.Fatal("no style props offered")
	}
	byLabel := map[string]lspcore.CompletionItem{}
	for _, it := range items {
		byLabel[it.Label] = it
	}
	for _, name := range []string{"padding", "color", "fontSize"} {
		it, ok := byLabel[name]
		if !ok {
			t.Errorf("style prop %q not offered", name)
			continue
		}
		if it.Detail == "" {
			t.Errorf("style prop %q offered with no type", name)
		}
	}
	if _, ok := byLabel["nosuchprop"]; ok {
		t.Error("a name Style does not declare was offered")
	}
}
