package main

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// A source that does not check is reported per example as a warning and the
// command still exits 0, so the render path is asked here rather than through
// the CLI.
func TestStdlibExampleSourcesCheck(t *testing.T) {
	sources := stdlibExampleSources([]string{"bubbletea"})
	if len(sources) == 0 {
		t.Fatal("no stdlib component carries an example")
	}
	for name, src := range sources {
		assertExampleChecks(t, name, src)
	}
}

func TestFileExampleSourceChecks(t *testing.T) {
	doc, err := parser.Parse("widgets.sngl", []byte(`import ui "sngl:ui"

component _example_label ui.node {
    ui.vbox {
        ui.text(value="hello")
    }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	imports := checker.DocumentExampleImports(doc)
	for name, src := range checker.PrefixedExamples(doc) {
		assertExampleChecks(t, name, exampleSource(imports, src, []string{"bubbletea"}))
	}
}

func assertExampleChecks(t *testing.T, name, src string) {
	t.Helper()
	doc, err := parser.Parse(name+".sngl", []byte(src))
	if err != nil {
		t.Errorf("%s: %v\n%s", name, err, src)
		return
	}
	pkg, err := checkDoc(doc, t.TempDir(), true)
	if err != nil {
		t.Errorf("%s: %v\n%s", name, err, src)
		return
	}
	if !pkg.IsProgram() {
		t.Errorf("%s: example source renders no window\n%s", name, src)
	}
}
