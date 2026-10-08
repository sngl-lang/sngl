package playground

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCompileTxtarJSImport drives Compile() with a txtar archive that
// includes a sibling `lib/index.ts` and a `js:` import. The bundled
// output must include the imported function body, proving the FS
// plumbing reaches both the checker (resolution) and the html platform
// (esbuild plugin via fsys).
func TestCompileTxtarJSImport(t *testing.T) {
	const source = `output { js { html } }

import lib "js:./lib"

import . "sngl:ui"

window {
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
	const source = `import . "sngl:ui"
import . "sngl:ui"

window {
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

// TestCompileRootSiblings holds the archive's root .sngl sections to being
// files of the editor's package, as a directory's files are of the CLI's.
func TestCompileRootSiblings(t *testing.T) {
	const source = `import ui "sngl:ui"

ui.window {
    ui.text(value=greeting)
}
-- strings.sngl --
const greeting = "hello from a sibling"
`
	var result struct {
		HTML  string `json:"html"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(Compile(source)), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatalf("Compile error: %s", result.Error)
	}
	if !strings.Contains(result.HTML, "hello from a sibling") {
		t.Errorf("expected the sibling's const in HTML; got: %s", result.HTML)
	}

	var diags []map[string]any
	if err := json.Unmarshal([]byte(Diagnostics(source)), &diags); err != nil {
		t.Fatal(err)
	}
	if len(diags) != 0 {
		t.Errorf("diagnostics: %v", diags)
	}
}

func TestFormatKeepsSections(t *testing.T) {
	const source = "const a   = 1\n-- b.sngl --\nconst b   = 2\n-- notes.txt --\nkept  as is\n"
	var result struct {
		Source string `json:"source"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(Format(source)), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	const want = "const a = 1\n-- b.sngl --\nconst b = 2\n-- notes.txt --\nkept  as is\n"
	if result.Source != want {
		t.Errorf("got:\n%q\nwant:\n%q", result.Source, want)
	}
}

// TestCompileIgnoresUnlinkedOutputTargets: the preview is html whatever the
// archive's output block names, and a target the playground does not link
// has none of its overrides loaded.
func TestCompileIgnoresUnlinkedOutputTargets(t *testing.T) {
	const source = `import ui "sngl:ui"

output {
    go {
        fyne
    }
}

ui.window {
    ui.text(value="hello")
}
`
	var result struct {
		HTML  string `json:"html"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(Compile(source)), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatalf("Compile error: %s", result.Error)
	}
	var diags []map[string]any
	if err := json.Unmarshal([]byte(Diagnostics(source)), &diags); err != nil {
		t.Fatal(err)
	}
	if len(diags) != 0 {
		t.Errorf("diagnostics: %v", diags)
	}
}

func diagnosticMessages(t *testing.T, source string) []string {
	t.Helper()
	var diags []struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(Diagnostics(source)), &diags); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range diags {
		out = append(out, d.Message)
	}
	return out
}

// TestDiagnosticsReportOtherFiles: the editor shows the comment alone, so a
// problem another section holds is reported there with its own position
// rather than dropped.
func TestDiagnosticsReportOtherFiles(t *testing.T) {
	broken := diagnosticMessages(t, "const a = b\n-- b.sngl --\nconst b = 1\n-- c.sngl --\nconst c = (\n")
	if len(broken) != 1 || !strings.Contains(broken[0], "c.sngl") {
		t.Errorf("a section that does not parse: %q", broken)
	}
	redeclared := diagnosticMessages(t, "const a = 1\n-- b.sngl --\nconst a = 2\n")
	if len(redeclared) != 1 || !strings.Contains(redeclared[0], "redeclared") {
		t.Errorf("a declaration two files make: %q", redeclared)
	}
}

func TestCompileMainSection(t *testing.T) {
	const source = `-- playground.sngl --
import ui "sngl:ui"

ui.window {
    ui.text(value="from the section")
}
`
	var result struct {
		HTML  string `json:"html"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(Compile(source)), &result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.HTML, "from the section") {
		t.Errorf("html %q, error %q", result.HTML, result.Error)
	}
}
