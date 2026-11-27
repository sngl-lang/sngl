package bubbletea

import (
	"testing"

	"duckfam.us/sngl/pkg/go/tui"
)

func TestTokenColorRuntimeMatchesLiteral(t *testing.T) {
	for _, kind := range []string{"plain", "keyword", "type", "function", "variable", "constant", "string", "number", "comment", "operator", "punctuation"} {
		want, ok := tokenColors[kind]
		if !ok {
			want = "fallback"
		}
		if got := tui.TokenColor(kind, "fallback"); got != want {
			t.Errorf("tui.TokenColor(%q) = %q, tokenColors says %q", kind, got, want)
		}
	}
}
