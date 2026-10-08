package imports_test

import (
	"testing"

	"duckfam.us/sngl/internal/imports"
)

func TestParseScheme(t *testing.T) {
	scheme, uri := imports.ParseScheme("go:pkg/path")
	if scheme != "go" || uri != "pkg/path" {
		t.Errorf("got scheme=%q uri=%q", scheme, uri)
	}
	scheme, uri = imports.ParseScheme("relative/path")
	if scheme != "" || uri != "relative/path" {
		t.Errorf("got scheme=%q uri=%q", scheme, uri)
	}
}

func TestNamespaceFromPath(t *testing.T) {
	if got := imports.NamespaceFromPath("sngl:internal/draw"); got != "draw" {
		t.Errorf("got %q", got)
	}
	if got := imports.NamespaceFromPath("widgets/counter"); got != "counter" {
		t.Errorf("got %q", got)
	}
}
