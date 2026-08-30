package javascript

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	res := renderJSSourceMap("model.js", "", positions, body)
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
	res := renderJSSourceMap("x.js", "", nil, []byte("foo"))
	if res.InlineBody != nil || res.Sidecar != nil {
		t.Fatalf("expected zero, got %+v", res)
	}
}

func TestRenderJSSourceMap_SinglePosition(t *testing.T) {
	body := []byte("console.log(1);\n")
	positions := []codegen.PosEntry{
		{ByteOffset: 0, Pos: ast.Pos{File: "f.sngl", Line: 1, Column: 1}},
	}
	res := renderJSSourceMap("out.js", "", positions, body)
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
	res := renderJSSourceMap("out.js", "", nil, body)
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
	res := renderJSSourceMap("x.js", "", nil, []byte("plain;"))
	if res.InlineBody != nil || res.Sidecar != nil {
		t.Fatalf("expected zero, got %+v", res)
	}
}

// A `sources` entry is resolved against the map's own location, not the
// directory the compiler ran in. Both spellings an invocation can produce.
func TestRenderJSSourceMap_SourcesResolveFromMapDir(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	outDir := filepath.Join(root, "out")
	for _, d := range []string{srcDir, outDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	srcPath := filepath.Join(srcDir, "app.sngl")
	if err := os.WriteFile(srcPath, []byte("component main {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rel, err := filepath.Rel(outDir, srcPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		file string
	}{
		{"absolute", srcPath},
		{"relative-to-cwd", relToCwd(t, srcPath)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			positions := []codegen.PosEntry{
				{ByteOffset: 0, Pos: ast.Pos{File: tc.file, Line: 1, Column: 1}},
			}
			res := renderJSSourceMap("model.js", outDir, positions, []byte("first;\n"))
			var m struct {
				Sources []string `json:"sources"`
			}
			if err := json.Unmarshal(res.Sidecar, &m); err != nil {
				t.Fatalf("sidecar not valid JSON: %v", err)
			}
			if len(m.Sources) != 1 {
				t.Fatalf("sources=%v want one entry", m.Sources)
			}
			if m.Sources[0] != filepath.ToSlash(rel) {
				t.Errorf("sources[0]=%q want %q", m.Sources[0], filepath.ToSlash(rel))
			}
			resolved := filepath.Join(outDir, filepath.FromSlash(m.Sources[0]))
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("sources[0] does not resolve from the map's directory: %v", err)
			}
		})
	}
}

// relToCwd spells path the way a CLI invocation from the working directory
// would.
func relToCwd(t *testing.T, path string) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

// With no output directory known (playground, LSP preview) the path stays
// as the compiler saw it.
func TestRenderJSSourceMap_NoMapDirLeavesSourcesAlone(t *testing.T) {
	positions := []codegen.PosEntry{
		{ByteOffset: 0, Pos: ast.Pos{File: "f.sngl", Line: 1, Column: 1}},
	}
	res := renderJSSourceMap("model.js", "", positions, []byte("first;\n"))
	var m struct {
		Sources []string `json:"sources"`
	}
	if err := json.Unmarshal(res.Sidecar, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Sources) != 1 || m.Sources[0] != "f.sngl" {
		t.Errorf("sources=%v want [f.sngl]", m.Sources)
	}
}

// Generated line numbers count the file as written, header included.
func TestFileEmitter_MapAccountsForGeneratedByHeader(t *testing.T) {
	sink := codegen.NewMemSink()
	fe := newFileEmitter(sink, codegen.FileOptions{
		Name:     "model.js",
		Source:   "app.sngl",
		Platform: "html",
		Maps:     true,
	})
	if _, err := fe.Write([]byte("/*@SNGL:app.sngl:7@*/\nconst a = 1;\n")); err != nil {
		t.Fatal(err)
	}
	if err := fe.Close(); err != nil {
		t.Fatal(err)
	}

	files := sink.Files()
	out, ok := files["model.js"]
	if !ok {
		t.Fatal("model.js not written")
	}
	sidecar, ok := files["model.js.map"]
	if !ok {
		t.Fatal("model.js.map not written")
	}

	wantLine := -1
	for i, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "const a = 1;") {
			wantLine = i
			break
		}
	}
	if wantLine < 0 {
		t.Fatalf("statement missing from output:\n%s", out)
	}
	if wantLine == 0 {
		t.Fatalf("header not emitted, so this test would pass vacuously:\n%s", out)
	}

	var m struct {
		Mappings string `json:"mappings"`
	}
	if err := json.Unmarshal(sidecar, &m); err != nil {
		t.Fatal(err)
	}
	// One ';'-separated group per generated line; the leading empty ones
	// precede the first mapped statement.
	gotLine := strings.Index(m.Mappings, "A")
	if gotLine < 0 {
		t.Fatalf("no segment in mappings %q", m.Mappings)
	}
	if gotLine != wantLine {
		t.Errorf("first mapping is on generated line %d, statement is on line %d (mappings %q)\n%s",
			gotLine, wantLine, m.Mappings, out)
	}
}
