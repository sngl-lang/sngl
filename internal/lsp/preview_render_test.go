package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
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
	pkg, err := checkPreviewDoc(doc, dir)
	if err != nil {
		t.Fatalf("check: %v", err)
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
	pkg, _ := checkPreviewDoc(doc, dir)
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
	pkg, _ := checkPreviewDoc(doc, dir)
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
