package javascript

import (
	"encoding/json"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

func TestRenderJSSourceMap_Basic(t *testing.T) {
	body := []byte("first;\nsecond;\nthird;\n")
	positions := []codegen.PosEntry{
		{ByteOffset: 0, Pos: ast.Pos{File: "f.sngl", Line: 10, Column: 1}},
		{ByteOffset: 7, Pos: ast.Pos{File: "f.sngl", Line: 11, Column: 1}},
		{ByteOffset: 15, Pos: ast.Pos{File: "f.sngl", Line: 12, Column: 1}},
	}
	res := renderJSSourceMap("model.js", positions, body)
	if res.SidecarName != "model.js.map" {
		t.Errorf("SidecarName=%q want model.js.map", res.SidecarName)
	}
	if res.Sidecar == nil {
		t.Fatal("Sidecar must be set")
	}
	if !strings.HasSuffix(string(res.InlineBody), "//# sourceMappingURL=model.js.map\n") {
		t.Errorf("missing sourceMappingURL footer:\n%s", res.InlineBody)
	}
	var m struct {
		Version  int      `json:"version"`
		File     string   `json:"file"`
		Sources  []string `json:"sources"`
		Names    []string `json:"names"`
		Mappings string   `json:"mappings"`
	}
	if err := json.Unmarshal(res.Sidecar, &m); err != nil {
		t.Fatalf("sidecar not valid JSON: %v\n%s", err, res.Sidecar)
	}
	if m.Version != 3 {
		t.Errorf("version=%d want 3", m.Version)
	}
	if m.File != "model.js" {
		t.Errorf("file=%q want model.js", m.File)
	}
	if len(m.Sources) != 1 || m.Sources[0] != "f.sngl" {
		t.Errorf("sources=%v want [f.sngl]", m.Sources)
	}
	if m.Mappings == "" {
		t.Errorf("empty mappings")
	}
}

func TestRenderJSSourceMap_NoPositionsReturnsZero(t *testing.T) {
	res := renderJSSourceMap("x.js", nil, []byte("foo"))
	if res.InlineBody != nil || res.Sidecar != nil {
		t.Fatalf("expected zero, got %+v", res)
	}
}

func TestRenderJSSourceMap_SinglePosition(t *testing.T) {
	body := []byte("console.log(1);\n")
	positions := []codegen.PosEntry{
		{ByteOffset: 0, Pos: ast.Pos{File: "f.sngl", Line: 1, Column: 1}},
	}
	res := renderJSSourceMap("out.js", positions, body)
	if res.Sidecar == nil {
		t.Fatal("Sidecar must be set for a single position")
	}
	var m struct {
		Mappings string `json:"mappings"`
	}
	if err := json.Unmarshal(res.Sidecar, &m); err != nil {
		t.Fatalf("sidecar JSON: %v", err)
	}
	// One position at gen line 0 col 0 → src 0 line 0 col 0; all deltas zero.
	// VLQ of 0 is "A" → "AAAA".
	if !strings.HasPrefix(m.Mappings, "AAAA") {
		t.Errorf("mappings should start with AAAA, got %q", m.Mappings)
	}
}

func TestRenderJSSourceMap_ExtractsInlineMarkers(t *testing.T) {
	body := []byte("/*@SNGL:foo.sngl:5@*/\nconsole.log(1);\n/*@SNGL:foo.sngl:7@*/\nconsole.log(2);\n")
	// Pass empty positions: marker extraction is the source.
	res := renderJSSourceMap("out.js", nil, body)
	if res.Sidecar == nil {
		t.Fatal("Sidecar must be set when markers are present")
	}
	if res.SidecarName != "out.js.map" {
		t.Errorf("SidecarName=%q want out.js.map", res.SidecarName)
	}
	// Inline body must have markers stripped.
	got := string(res.InlineBody)
	if strings.Contains(got, "@SNGL:") {
		t.Errorf("markers not stripped:\n%s", got)
	}
	// Must contain the original code.
	if !strings.Contains(got, "console.log(1);") || !strings.Contains(got, "console.log(2);") {
		t.Errorf("body content lost:\n%s", got)
	}
	// Footer present.
	if !strings.HasSuffix(got, "//# sourceMappingURL=out.js.map\n") {
		t.Errorf("missing sourceMappingURL footer:\n%s", got)
	}
}

func TestRenderJSSourceMap_NoMarkersNoPositionsReturnsZero(t *testing.T) {
	res := renderJSSourceMap("x.js", nil, []byte("plain;"))
	if res.InlineBody != nil || res.Sidecar != nil {
		t.Fatalf("expected zero, got %+v", res)
	}
}
