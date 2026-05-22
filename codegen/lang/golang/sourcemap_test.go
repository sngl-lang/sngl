package golang

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

func TestRenderGoSourceMap_InsertsLineDirectives(t *testing.T) {
	body := []byte("first()\nsecond()\nthird()\n")
	// Mark byte 0 as f.sngl:10, byte 16 (start of "third()") as f.sngl:20.
	positions := []codegen.PosEntry{
		{ByteOffset: 0, Pos: ast.Pos{File: "f.sngl", Line: 10, Column: 1}},
		{ByteOffset: 16, Pos: ast.Pos{File: "f.sngl", Line: 20, Column: 1}},
	}
	out := renderGoSourceMap("model.go", positions, body)
	if out.Sidecar != nil {
		t.Errorf("Go should not emit a sidecar, got %d bytes", len(out.Sidecar))
	}
	if out.InlineBody == nil {
		t.Fatal("InlineBody must be set")
	}
	got := string(out.InlineBody)
	if !strings.Contains(got, "//line f.sngl:10\n") {
		t.Errorf("missing first //line directive: %q", got)
	}
	if !strings.Contains(got, "//line f.sngl:20\n") {
		t.Errorf("missing second //line directive: %q", got)
	}
	// Directive comes BEFORE the source bytes it covers.
	idx := strings.Index(got, "first()")
	dirIdx := strings.Index(got, "//line f.sngl:10")
	if dirIdx > idx {
		t.Errorf("first //line directive must precede its code; dirIdx=%d codeIdx=%d", dirIdx, idx)
	}
}

func TestRenderGoSourceMap_NoPositionsReturnsZero(t *testing.T) {
	out := renderGoSourceMap("model.go", nil, []byte("x"))
	if out.InlineBody != nil || out.Sidecar != nil {
		t.Fatalf("expected zero result, got %+v", out)
	}
}

func TestRenderGoSourceMap_CollapsesAdjacentSameLine(t *testing.T) {
	body := []byte("aaaabbbb")
	positions := []codegen.PosEntry{
		{ByteOffset: 0, Pos: ast.Pos{File: "f.sngl", Line: 5}},
		{ByteOffset: 4, Pos: ast.Pos{File: "f.sngl", Line: 5}}, // same line, no new directive
	}
	out := renderGoSourceMap("model.go", positions, body)
	count := strings.Count(string(out.InlineBody), "//line ")
	if count != 1 {
		t.Errorf("expected 1 //line directive, got %d: %s", count, out.InlineBody)
	}
}
