package bubbletea

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// compileBubbletea drives a SNGL source string through
// parse → check → optimize → lower(Platform:"bubbletea") → CompileIR and
// returns the generated Go source. It reuses compileAndVerify (which runs the
// optimize/lower/codegen pipeline and asserts the output is valid Go).
//
// Crucially it passes the registered Platforms/Languages to the checker (as
// the CLI does), so the stdlib platform extensions in bubbletea.sngl — i.e.
// the new-form `component sngl.text { platform bubbletea { ... } }` bodies —
// are merged via mergePlatformExtensions. Without this the checker silently
// falls back to the language-agnostic lib/ stdlib component and never
// exercises the platform body at all.
func compileBubbletea(t *testing.T, src string) string {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var plats []ir.Platform
	if p := codegen.LookupPlatform("bubbletea"); p != nil {
		plats = append(plats, p)
	}
	var langs []ir.Language
	if l := codegen.LookupLang("go"); l != nil {
		langs = append(langs, l)
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: plats,
		Languages: langs,
	})
	if hasErrors(diags) {
		t.Fatalf("check: %s", firstError(diags))
	}
	return string(compileAndVerify(t, doc, pkg))
}

// TestNewFormTextBindsValue guards the new-form platform-body binding
// mechanism: a stdlib component declared as
//
//	component sngl.text { platform bubbletea { Styled(content=value) {} } }
//
// called as text(value="HELLO") must render the caller's argument, not "".
func TestNewFormTextBindsValue(t *testing.T) {
	src := `output { go { bubbletea } }
component main {
    text(value="HELLO")
}`
	out := compileBubbletea(t, src)
	if !strings.Contains(out, `"HELLO"`) {
		t.Fatalf("generated source missing bound value; got:\n%s", out)
	}
}
