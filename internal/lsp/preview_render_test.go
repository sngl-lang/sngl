package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestRenderDocAsHTML_SimpleWindow(t *testing.T) {
	src := `
window #home(title="Home", href="/") {
    text(value="hello world")
}
`
	doc, err := parser.Parse("t.sngl", []byte(withStdSrc(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dir, _ := filepath.Abs("../../testdata/lsp")
	pkg, diags := sngl.Check(doc, dir)
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	html, err := renderDocAsHTML(pkg, "home")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(html)
	if !strings.Contains(body, "hello world") {
		t.Errorf("body missing %q; got:\n%s", "hello world", body)
	}
	if !strings.Contains(body, "<!doctype") && !strings.Contains(body, "<!DOCTYPE") {
		t.Errorf("body missing doctype; got:\n%s", body)
	}
}

func TestRenderDocAsHTML_UnknownWindow(t *testing.T) {
	src := `window #home(title="x", href="/") { text(value="hi") }`
	doc, _ := parser.Parse("t.sngl", []byte(withStdSrc(src)))
	dir, _ := filepath.Abs("../../testdata/lsp")
	pkg, _ := sngl.Check(doc, dir)
	_, err := renderDocAsHTML(pkg, "notreal")
	if err == nil {
		t.Fatal("expected error for unknown window")
	}
	if !strings.Contains(err.Error(), "notreal") {
		t.Errorf("error %q should mention the window name", err)
	}
}

func TestRenderDocAsHTML_InjectsReloadScript(t *testing.T) {
	src := `window #home(title="x", href="/") { text(value="hi") }`
	doc, _ := parser.Parse("t.sngl", []byte(withStdSrc(src)))
	dir, _ := filepath.Abs("../../testdata/lsp")
	pkg, _ := sngl.Check(doc, dir)
	html, err := renderDocAsHTML(pkg, "home")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(html)
	if !strings.Contains(body, "WebSocket") {
		t.Errorf("body missing reload script (WebSocket): %s", body)
	}
	if !strings.Contains(body, "reload") {
		t.Errorf("body missing reload handler: %s", body)
	}
}
