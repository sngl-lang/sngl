package playground

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCompileTxtarJSImport drives Compile() with a txtar archive that
// includes a sibling `lib/index.ts` and a `js://` import. The bundled
// output must include the imported function body, proving the FS
// plumbing reaches both the checker (resolution) and the html platform
// (esbuild plugin via fsys).
func TestCompileTxtarJSImport(t *testing.T) {
	const source = `output { js { html } }

import lib "js://./lib"

import . "sngl://std"

component main {
    var n = 0.0
    button(text="add", @click { n = lib.add(2.0, 3.0) })
    text(value=string(n))
}
-- lib/index.ts --
export function add(a: number, b: number): number {
    return a + b;
}
`
	out := Compile(source)
	var result struct {
		HTML  string `json:"html"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, out)
	}
	if result.Error != "" {
		t.Fatalf("Compile error: %s", result.Error)
	}
	if !strings.Contains(result.HTML, "function add") {
		t.Errorf("expected bundled `function add` in HTML; got: %s", result.HTML)
	}
	if !strings.Contains(result.HTML, "a + b") {
		t.Errorf("expected `a + b` body in HTML; got: %s", result.HTML)
	}
}

// TestCompileBareSource is the degenerate case: no `-- name --` markers,
// the editor buffer is one SNGL file. Must still compile.
func TestCompileBareSource(t *testing.T) {
	const source = `import . "sngl://std"
component main {
    text(value="hello")
}
`
	out := Compile(source)
	var result struct {
		HTML  string `json:"html"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, out)
	}
	if result.Error != "" {
		t.Fatalf("Compile error: %s", result.Error)
	}
	if !strings.Contains(result.HTML, "hello") {
		t.Errorf("expected 'hello' in HTML; got: %s", result.HTML)
	}
}
