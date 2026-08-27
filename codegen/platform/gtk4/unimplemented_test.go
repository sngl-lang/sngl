package gtk4

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// buildForGtk4 runs the whole pipeline for one source string and returns the
// generated files, or the error the platform refused with.
func buildForGtk4(t *testing.T, src string) (map[string][]byte, error) {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms()})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s", d.Msg)
		}
	}
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "gtk4"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		return nil, err
	}
	return mem.Files(), nil
}

// A stdlib component with no `platform gtk4` body has nothing to emit. Before
// this was an error the node was dropped and the build succeeded, so the window
// came out missing widgets the source asked for with no diagnostic anywhere.
func TestUnimplementedStdlibComponentFailsBuild(t *testing.T) {
	skipWithoutGIR(t)
	files, err := buildForGtk4(t, `
import . "sngl://std"
component main {
    text(value="visible")
    progress(value=0.5)
}
`)
	if err == nil {
		t.Fatalf("expected the build to fail; it emitted %d files", len(files))
	}
	msg := err.Error()
	// The message has to name both halves: which component, and which target
	// does not have it.
	for _, want := range []string{`"progress"`, "gtk4"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic %q does not mention %s", msg, want)
		}
	}
}

// The same rule must not fire on a component the program declared itself. An
// empty body there is the program saying it draws nothing, which is a legal
// thing to say — the fixtures behind the LSP tests are written that way.
func TestUserComponentWithEmptyBodyStillBuilds(t *testing.T) {
	skipWithoutGIR(t)
	files, err := buildForGtk4(t, `
import . "sngl://std"
component label(value string) {
}
component main {
    label(value="hello")
    text(value="visible")
}
`)
	if err != nil {
		t.Fatalf("expected a clean build for a user component with an empty body: %v", err)
	}
	if _, ok := files["model.go"]; !ok {
		t.Fatal("model.go not emitted")
	}
}
