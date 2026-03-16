package lsp

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

func TestCompletionContext(t *testing.T) {
	tests := []struct {
		name    string
		content string
		line    int
		col     int
		want    completionCtx
	}{
		{
			"top level",
			"import \"foo\"\n\n",
			3, 1,
			ctxTopLevel,
		},
		{
			"inside component",
			"component main {\n    \n}",
			2, 5,
			ctxComponent,
		},
		{
			"inside visual node",
			"component main {\n    vbox {\n        \n    }\n}",
			3, 9,
			ctxVisualNode,
		},
		{
			"event handler",
			"component main {\n    vbox {\n        @click\n    }\n}",
			3, 9,
			ctxEventHandler,
		},
		{
			"style context",
			"component main {\n    vbox {\n        style={gap\n    }\n}",
			3, 9,
			ctxStyleProp,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := completionContext(tt.content, tt.line, tt.col)
			if got != tt.want {
				t.Errorf("completionContext() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTopLevelKeywords(t *testing.T) {
	items := topLevelKeywords()
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
	items := componentKeywords()
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
	fs := &fileState{
		Doc: &ast.Document{
			Data:      []*ast.Data{{Name: "count", Init: ast.Expr{TypeHint: "int"}}},
			Computeds: []*ast.Computed{{Name: "greeting"}},
			Consts:    []*ast.Const{{Name: "MAX"}},
			Structs:   []*ast.StructDef{{Name: "User"}},
			Enums:     []*ast.EnumDef{{Name: "Status"}},
		},
	}
	items := expressionCompletions(fs)
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
	items := stdlibComponentItems()
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
	items := stylePropCompletions()
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
