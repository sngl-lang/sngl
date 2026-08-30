package javascript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// The map carries the SNGL text, so a consumer needs no path to resolve.
func TestRenderJSSourceMap_EmbedsSourcesContent(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "app.sngl")
	const text = "component main {\n    text(value=\"hi\")\n}\n"
	if err := os.WriteFile(srcPath, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	positions := []codegen.PosEntry{
		{ByteOffset: 0, Pos: ast.Pos{File: srcPath, Line: 2, Column: 5}},
	}
	res := renderJSSourceMap("model.js", dir, positions, []byte("first;\n"))

	var m struct {
		Sources        []string  `json:"sources"`
		SourcesContent []*string `json:"sourcesContent"`
	}
	if err := json.Unmarshal(res.Sidecar, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.SourcesContent) != len(m.Sources) {
		t.Fatalf("sourcesContent has %d entries for %d sources; it is positional against sources",
			len(m.SourcesContent), len(m.Sources))
	}
	if m.SourcesContent[0] == nil {
		t.Fatal("sourcesContent[0] is null, want the source text")
	}
	if *m.SourcesContent[0] != text {
		t.Errorf("sourcesContent[0] = %q, want %q", *m.SourcesContent[0], text)
	}
}

// Nothing readable leaves the field off rather than emitting a shorter
// array, which would misalign every later entry against sources.
func TestRenderJSSourceMap_UnreadableSourceOmitsContent(t *testing.T) {
	positions := []codegen.PosEntry{
		{ByteOffset: 0, Pos: ast.Pos{File: "no/such/app.sngl", Line: 1, Column: 1}},
	}
	res := renderJSSourceMap("model.js", "", positions, []byte("first;\n"))

	var m struct {
		SourcesContent []*string `json:"sourcesContent"`
	}
	if err := json.Unmarshal(res.Sidecar, &m); err != nil {
		t.Fatal(err)
	}
	if m.SourcesContent != nil {
		t.Errorf("sourcesContent=%v, want it absent when nothing could be read", m.SourcesContent)
	}
}

// One readable source among several still yields a full-length array, with
// the unreadable one an explicit null holding its position.
func TestRenderJSSourceMap_PartialContentKeepsAlignment(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "app.sngl")
	if err := os.WriteFile(srcPath, []byte("component main {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	positions := []codegen.PosEntry{
		{ByteOffset: 0, Pos: ast.Pos{File: "no/such/gone.sngl", Line: 1, Column: 1}},
		{ByteOffset: 7, Pos: ast.Pos{File: srcPath, Line: 1, Column: 1}},
	}
	res := renderJSSourceMap("model.js", dir, positions, []byte("first;\nsecond;\n"))

	var m struct {
		Sources        []string  `json:"sources"`
		SourcesContent []*string `json:"sourcesContent"`
	}
	if err := json.Unmarshal(res.Sidecar, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Sources) != 2 {
		t.Fatalf("sources=%v, want two", m.Sources)
	}
	if len(m.SourcesContent) != 2 {
		t.Fatalf("sourcesContent has %d entries, want 2 so index 1 still names the file it belongs to",
			len(m.SourcesContent))
	}
	if m.SourcesContent[0] != nil {
		t.Errorf("sourcesContent[0]=%q, want null for the unreadable source", *m.SourcesContent[0])
	}
	if m.SourcesContent[1] == nil {
		t.Error("sourcesContent[1] is null, want the readable source's text")
	}
}
