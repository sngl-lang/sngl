package lsp

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/lspcore"
)

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
	items := lspcore.ComponentKeywords()
	labels := map[string]bool{}
	for _, item := range items {
		labels[item.Label] = true
	}
	for _, kw := range []string{"param", "var", "const", "computed", "if", "for"} {
		if !labels[kw] {
			t.Errorf("expected %q in component keywords", kw)
		}
	}
}

func TestExpressionCompletions(t *testing.T) {
	doc := &ast.Document{
		Data:      []*ast.Data{{Name: "count", Init: ast.Expr{TypeHint: "int"}}},
		Functions: []*ast.FuncDef{{Name: "greeting", Body: ast.Expr{Literal: ""}}},
		Consts:    []*ast.Const{{Name: "MAX"}},
		Structs:   []*ast.StructDef{{Name: "User"}},
		Enums:     []*ast.EnumDef{{Name: "Status"}},
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

func TestStdlibComponentItems(t *testing.T) {
	items := lspcore.StdlibComponentItems()
	if len(items) == 0 {
		t.Fatal("expected stdlib component completions")
	}
	labels := map[string]bool{}
	for _, item := range items {
		labels[item.Label] = true
	}
	for _, name := range []string{"text", "button", "vbox", "hbox"} {
		if !labels[name] {
			t.Errorf("expected stdlib component %q in completions", name)
		}
	}
}

func TestStylePropCompletions(t *testing.T) {
	items := lspcore.StylePropCompletions()
	if len(items) == 0 {
		t.Fatal("expected style property completions")
	}
	labels := map[string]bool{}
	for _, item := range items {
		labels[item.Label] = true
	}
	for _, name := range []string{"width", "height", "color", "fontSize", "padding"} {
		if !labels[name] {
			t.Errorf("expected style property %q in completions", name)
		}
	}
}
