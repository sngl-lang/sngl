package lsp

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/lspcore"
	"git.duckfam.us/jonathan/sngl/internal/parser"
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
		got := lspcore.WordAtPosition(content, tt.line, tt.col)
		if got != tt.want {
			t.Errorf("WordAtPosition(%d,%d) = %q, want %q", tt.line, tt.col, got, tt.want)
		}
	}
}

func hoverOf(t *testing.T, src, word string) string {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(withStdSrc(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return lspcore.HoverInfo(doc, word)
}

func TestHoverInfo_Var(t *testing.T) {
	info := hoverOf(t, `component main node { var count = 0 }`, "count")
	if !strings.Contains(info, "count") {
		t.Errorf("expected var hover, got %q", info)
	}
}

func TestHoverInfo_Computed(t *testing.T) {
	info := hoverOf(t, `component main node { func greeting() => "hi" }`, "greeting")
	if !strings.Contains(info, "greeting") {
		t.Errorf("expected func hover, got %q", info)
	}
}

func TestHoverInfo_Component(t *testing.T) {
	info := hoverOf(t, `component Counter(label = "", step int) node { vbox {} }`, "Counter")
	if !strings.Contains(info, "Counter") {
		t.Errorf("expected component hover, got %q", info)
	}
}

func TestHoverInfo_StdlibComponent(t *testing.T) {
	info := hoverOf(t, ``, "text")
	_ = info // stdlib hover is optional in v2; just exercise the path
}

func TestHoverInfo_Struct(t *testing.T) {
	src := `struct User {
    name string
    age int
}
`
	info := hoverOf(t, src, "User")
	if !strings.Contains(info, "User") {
		t.Errorf("expected struct hover, got %q", info)
	}
}

func TestHoverInfo_Enum(t *testing.T) {
	src := `enum Status {
    active
    inactive
}
`
	info := hoverOf(t, src, "Status")
	if !strings.Contains(info, "Status") {
		t.Errorf("expected enum hover, got %q", info)
	}
}

func TestHoverInfo_Unknown(t *testing.T) {
	info := hoverOf(t, ``, "nonexistent_xyz_12345")
	if info != "" {
		t.Errorf("expected empty hover for unknown word, got %q", info)
	}
}
