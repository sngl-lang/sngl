package lsp

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/lspcore"
)

func TestAnalyze_ValidFile(t *testing.T) {
	srv := New()
	fs := srv.ws.open("file:///test.sngl", `component main ui {
	var count = 0
	text(value="hello")
}
`, 1)
	diags := srv.analyze(fs)
	// Should produce no parse errors (checker errors about unknown components are ok)
	for _, d := range diags {
		if strings.Contains(d.Message, "unexpected token") {
			t.Errorf("unexpected parse error: %s", d.Message)
		}
	}
}

func TestAnalyze_ParseError(t *testing.T) {
	srv := New()
	fs := srv.ws.open("file:///test.sngl", `component main ui {
	var =
}
`, 1)
	diags := srv.analyze(fs)
	if len(diags) == 0 {
		t.Error("expected diagnostics for parse error")
	}
}

func TestAnalyze_CheckerError(t *testing.T) {
	srv := New()
	// Unknown component reference should trigger checker error
	fs := srv.ws.open("file:///test.sngl", `component main ui {
	nonexistent_widget(value="hi")
}
`, 1)
	diags := srv.analyze(fs)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "unknown component") || strings.Contains(d.Message, "undefined") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'unknown component' or 'undefined' diagnostic, got %v", diags)
	}
}

func TestURIToPath(t *testing.T) {
	tests := []struct {
		uri, want string
	}{
		{"file:///home/user/test.sngl", "/home/user/test.sngl"},
		{"file:///tmp/foo.sngl", "/tmp/foo.sngl"},
		{"/plain/path.sngl", "/plain/path.sngl"},
	}
	for _, tt := range tests {
		got := uriToPath(tt.uri)
		if got != tt.want {
			t.Errorf("uriToPath(%q) = %q, want %q", tt.uri, got, tt.want)
		}
	}
}

func TestParseOneDiagnostic(t *testing.T) {
	tests := []struct {
		filename string
		input    string
		wantLine int
		wantCol  int
		wantMsg  string
	}{
		{"test.sngl", "test.sngl:5:3: unexpected token", 5, 3, "unexpected token"},
		{"test.sngl", "5:3: bad syntax", 5, 3, "bad syntax"},
		{"test.sngl", "some random error", 0, 0, "some random error"},
	}
	for _, tt := range tests {
		d := lspcore.ParseOneDiagnostic(tt.filename, tt.input)
		if tt.wantLine > 0 {
			gotLine := d.Range.Start.Line + 1
			gotCol := d.Range.Start.Character + 1
			if gotLine != tt.wantLine || gotCol != tt.wantCol {
				t.Errorf("ParseOneDiagnostic(%q) pos = %d:%d, want %d:%d", tt.input, gotLine, gotCol, tt.wantLine, tt.wantCol)
			}
		}
		if d.Message != tt.wantMsg {
			t.Errorf("ParseOneDiagnostic(%q) msg = %q, want %q", tt.input, d.Message, tt.wantMsg)
		}
	}
}
