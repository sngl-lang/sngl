package lsp

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

func TestWordAtPosition(t *testing.T) {
	content := "var count = 0\ncomputed greeting = \"hello\""
	tests := []struct {
		line, col int
		want      string
	}{
		{1, 1, "var"},
		{1, 5, "count"},
		{1, 7, "count"},
		{1, 13, "0"},
		{2, 5, "computed"},
		{2, 14, "greeting"},
		{2, 10, "greeting"},
		// out of bounds
		{0, 1, ""},
		{99, 1, ""},
	}
	for _, tt := range tests {
		got := wordAtPosition(content, tt.line, tt.col)
		if got != tt.want {
			t.Errorf("wordAtPosition(%d,%d) = %q, want %q", tt.line, tt.col, got, tt.want)
		}
	}
}

func TestHoverInfo_Var(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{
			{Name: "count", Init: ast.Expr{TypeHint: "int"}},
			{Name: "user", Init: ast.Expr{TypeHint: "User"}, Extern: true},
		},
	}

	info := hoverInfo(doc, "count")
	if !strings.Contains(info, "var count int") {
		t.Errorf("expected var hover, got %q", info)
	}

	info = hoverInfo(doc, "user")
	if !strings.Contains(info, "extern") {
		t.Errorf("expected extern hover, got %q", info)
	}
}

func TestHoverInfo_Computed(t *testing.T) {
	doc := &ast.Document{
		Computeds: []*ast.Computed{
			{Name: "greeting"},
		},
	}
	info := hoverInfo(doc, "greeting")
	if !strings.Contains(info, "computed greeting") {
		t.Errorf("expected computed hover, got %q", info)
	}
}

func TestHoverInfo_Component(t *testing.T) {
	doc := &ast.Document{
		Components: []*ast.Component{
			{
				Name: "Counter",
				Params: []*ast.Param{
					{Name: "label", Default: ast.Expr{TypeHint: "string"}},
					{Name: "step", Required: true, Default: ast.Expr{TypeHint: "int"}},
				},
			},
		},
	}
	info := hoverInfo(doc, "Counter")
	if !strings.Contains(info, "component Counter") {
		t.Errorf("expected component hover, got %q", info)
	}
	if !strings.Contains(info, "label") || !strings.Contains(info, "step") {
		t.Errorf("expected params in hover, got %q", info)
	}
	if !strings.Contains(info, "required") {
		t.Errorf("expected required annotation, got %q", info)
	}
}

func TestHoverInfo_StdlibComponent(t *testing.T) {
	doc := &ast.Document{}
	info := hoverInfo(doc, "text")
	if info == "" {
		t.Error("expected hover info for stdlib component 'text'")
	}
	if !strings.Contains(info, "stdlib") {
		t.Errorf("expected stdlib annotation, got %q", info)
	}
}

func TestHoverInfo_Struct(t *testing.T) {
	doc := &ast.Document{
		Structs: []*ast.StructDef{
			{
				Name: "User",
				Fields: []*ast.StructField{
					{Name: "name", Type: "string"},
					{Name: "age", Type: "int"},
				},
			},
		},
	}
	info := hoverInfo(doc, "User")
	if !strings.Contains(info, "struct User") {
		t.Errorf("expected struct hover, got %q", info)
	}
	if !strings.Contains(info, "name string") {
		t.Errorf("expected field info, got %q", info)
	}
}

func TestHoverInfo_Enum(t *testing.T) {
	doc := &ast.Document{
		Enums: []*ast.EnumDef{
			{Name: "Status", Values: []string{"active", "inactive"}},
		},
	}
	info := hoverInfo(doc, "Status")
	if !strings.Contains(info, "enum Status") {
		t.Errorf("expected enum hover, got %q", info)
	}
	if !strings.Contains(info, "active") {
		t.Errorf("expected values in hover, got %q", info)
	}
}

func TestHoverInfo_Unknown(t *testing.T) {
	doc := &ast.Document{}
	info := hoverInfo(doc, "nonexistent_xyz_12345")
	if info != "" {
		t.Errorf("expected empty hover for unknown word, got %q", info)
	}
}
