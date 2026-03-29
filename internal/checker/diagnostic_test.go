package checker

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
)

func TestCheckDiagnostics_Valid(t *testing.T) {
	src := `component main {
	var count = 0
	text(value="hello")
}
`
	doc, err := snglparser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	_, diags := CheckDiagnostics(doc, ".", nil, nil)
	// text is a stdlib component so should be valid
	for _, d := range diags {
		if strings.Contains(d.Msg, "unexpected") {
			t.Errorf("unexpected diagnostic: %s", d.Msg)
		}
	}
}

func TestCheckDiagnostics_UnknownComponent(t *testing.T) {
	src := `component main {
	nonexistent_widget(value="hi")
}
`
	doc, err := snglparser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	_, diags := CheckDiagnostics(doc, ".", nil, nil)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Msg, "unknown component") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'unknown component' diagnostic")
	}
}

func TestCheckDiagnostics_NoApp(t *testing.T) {
	doc := &ast.Document{}
	_, diags := CheckDiagnostics(doc, ".", nil, nil)
	if len(diags) == 0 {
		t.Error("expected diagnostic for missing app")
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Msg, "missing app") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'missing app' diagnostic")
	}
}

func TestParseDiagnostic(t *testing.T) {
	tests := []struct {
		input   string
		wantPos ast.Pos
		wantMsg string
	}{
		{"5:3: bad syntax", ast.Pos{Line: 5, Column: 3}, "bad syntax"},
		{"10:1: unexpected EOF", ast.Pos{Line: 10, Column: 1}, "unexpected EOF"},
		{"no position here", ast.Pos{}, "no position here"},
	}
	for _, tt := range tests {
		d := parseDiagnostic(tt.input)
		if d.Pos != tt.wantPos {
			t.Errorf("parseDiagnostic(%q) pos = %v, want %v", tt.input, d.Pos, tt.wantPos)
		}
		if d.Msg != tt.wantMsg {
			t.Errorf("parseDiagnostic(%q) msg = %q, want %q", tt.input, d.Msg, tt.wantMsg)
		}
	}
}
