//go:build !js

package gtk4

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/testharness"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// RunTests is the entry point for `sngl test --platform=gtk4`. Mirrors
// the fyne / bubbletea runners: temp Go module + go test -json. The
// generated code uses cgo against `pkg-config: gtk4`, so ProbeTest
// checks for the system gtk4 dev libs at probe time.
func (g *Generator) RunTests(pkg *ir.Package, lang codegen.LangTranslator, opts *ir.StructLit) ([]*codegen.TestResult, error) {
	if pkg == nil {
		return nil, nil
	}
	var cfg Config
	if err := codegen.ApplyOptions(&cfg, opts); err != nil {
		return nil, fmt.Errorf("gtk4 RunTests options: %w", err)
	}
	doc := ir.Convert(pkg)
	astTestFuncs := doc.TestFuncs()
	if len(astTestFuncs) == 0 {
		return nil, nil
	}
	groups := testharness.Group(astTestFuncs)

	var compGroups []testharness.TestGroup
	for _, g := range groups {
		if g.Component != "" {
			compGroups = append(compGroups, g)
		}
	}
	if len(compGroups) == 0 {
		return nil, nil
	}

	var results []*codegen.TestResult
	for _, group := range compGroups {
		// Promote the component under test into a synthetic window so
		// BuildUI actually renders its widgets — Windows() needs either
		// an explicit window or a `main` component.
		compDoc := testharness.Promote(doc, group.Component)
		if compDoc == nil {
			continue
		}
		compPkg, diags := checker.Check(compDoc, &checker.Config{IsMain: true})
		hasErr := false
		for _, d := range diags {
			if d.Severity == ir.Error {
				hasErr = true
				break
			}
		}
		if hasErr || compPkg == nil {
			continue
		}
		caps := g.Capabilities().Merge(lang.Capabilities())
		if err := lower.Lower(compPkg, caps, lower.Options{Platform: g.PlatformIdentifier()}); err != nil {
			return nil, fmt.Errorf("gtk4 lower %q: %w", group.Component, err)
		}
		resp, err := g.Generate(&codegen.Request{
			Pkg:  compPkg,
			Lang: lang,
		})
		if err != nil {
			return nil, fmt.Errorf("gtk4 generate %q: %w", group.Component, err)
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("gtk4 generate %q: %s", group.Component, resp.Error)
		}

		// IR func lookup uses the *original* pkg because Promote drops
		// *ast.FuncDef decls; the test funcs live on pkg.Funcs only.
		methodFields := testharness.CollectConditionalIDs(compPkg)
		grpResults, err := runGtk4TestGroup(pkg, group, resp.Files, cfg.GoModExtra, methodFields)
		if err != nil {
			return nil, err
		}
		results = append(results, grpResults...)
	}
	return results, nil
}

func runGtk4TestGroup(origPkg *ir.Package, group testharness.TestGroup, files []*codegen.OutputFile, goModExtra string, methodFields map[string]bool) ([]*codegen.TestResult, error) {
	dir, err := os.MkdirTemp("", "sngl-gtk4-test-")
	if err != nil {
		return nil, fmt.Errorf("mktemp: %w", err)
	}
	if os.Getenv("SNGL_KEEP_TEST_DIR") == "" {
		defer os.RemoveAll(dir)
	} else {
		fmt.Fprintln(os.Stderr, "sngl-gtk4-test temp dir:", dir)
	}

	if err := writeGtk4GoMod(dir, goModExtra); err != nil {
		return nil, err
	}
	for _, f := range files {
		var buf bytes.Buffer
		if _, err := f.WriteTo(&buf); err != nil {
			return nil, fmt.Errorf("buffer file %s: %w", f.Name, err)
		}
		out := filepath.Join(dir, filepath.Base(f.Name))
		if err := os.WriteFile(out, buf.Bytes(), 0644); err != nil {
			return nil, fmt.Errorf("write %s: %w", out, err)
		}
	}
	if _, err := writeGtk4TestFile(dir, origPkg, group, methodFields); err != nil {
		return nil, err
	}

	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	var tidyOut bytes.Buffer
	tidy.Stdout = &tidyOut
	tidy.Stderr = &tidyOut
	if err := tidy.Run(); err != nil {
		return nil, fmt.Errorf("go mod tidy: %w\n%s", err, tidyOut.String())
	}

	cmd := exec.Command("go", "test", "-json", "-count=1", "./...")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()

	return parseGtk4TestJSON(stdout.Bytes(), stderr.Bytes(), group), nil
}

// writeGtk4GoMod writes a minimal go.mod. gtk4 codegen has no Go-module
// deps (it's pure cgo against the system gtk4 lib), so this is just
// `module sngltest` plus the optional `goModExtra` injection.
func writeGtk4GoMod(dir, goModExtra string) error {
	if goModExtra == "" {
		_, goModExtra = codegen.DetectHostGoMod()
	}
	mod := "module sngltest\n\ngo 1.23\n"
	if goModExtra != "" {
		mod += "\n" + goModExtra
		if !strings.HasSuffix(mod, "\n") {
			mod += "\n"
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0644); err != nil {
		return fmt.Errorf("go.mod: %w", err)
	}
	return nil
}

func writeGtk4TestFile(dir string, origPkg *ir.Package, group testharness.TestGroup, methodFields map[string]bool) (string, error) {
	// Go forbids cgo in *_test.go files, so the GTK bootstrap lives
	// in a sibling test_helpers.go that's compiled into the package
	// proper. The _test.go file only contains pure-Go test funcs.
	helpers := `package main

/*
#include <gtk/gtk.h>
*/
import "C"

import (
	"sync"
	"unsafe"
)

var gtkInit sync.Once

// newTestComponent boots GTK in headless mode (gtk_init pulls the
// display from $DISPLAY / $WAYLAND_DISPLAY), constructs a non-unique
// GtkApplication, registers it without running the main loop, and
// invokes BuildUI to materialize the widget tree. Tests then drive
// the model via direct field access and fire UI events through the
// per-id invoker methods emitted by emitEventInvokers.
func newTestComponent() *Model {
	gtkInit.Do(func() { C.gtk_init() })
	app := C.gtk_application_new(C.CString("dev.sngl.test"), C.G_APPLICATION_NON_UNIQUE)
	C.g_application_register((*C.GApplication)(unsafe.Pointer(app)), nil, nil)
	m := New()
	m.BuildUI(app)
	return m
}

// Stdlib event payload structs — surfaced for test bodies that
// construct InputEvent{...} / ChangeEvent{...} / SubmitEvent{...}.
// The main codegen never references them so they don't appear in
// model.go; declaring them here keeps the test source self-contained.
type InputEvent struct{ Value string }
type ChangeEvent struct{ Value string }
type SubmitEvent struct{ Value string }
`
	if err := os.WriteFile(filepath.Join(dir, "test_helpers.go"), []byte(helpers), 0644); err != nil {
		return "", fmt.Errorf("write test helpers: %w", err)
	}

	var b bytes.Buffer
	b.WriteString("package main\n\n")
	b.WriteString("import (\n\t\"testing\"\n\t\"time\"\n)\n\n")
	b.WriteString("var _ = time.Duration(0)\n\n")

	for _, tf := range group.Funcs {
		var fn *ir.Func
		for _, f := range origPkg.Funcs {
			if f.Name == tf.Name {
				fn = f
				break
			}
		}
		if fn == nil {
			continue
		}
		suffix := strings.TrimPrefix(fn.Name, "test")
		b.WriteString(golang.LowerTestFunc(fn, suffix, methodFields))
		b.WriteString("\n")
	}

	out := filepath.Join(dir, "component_test.go")
	if err := os.WriteFile(out, b.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("write test file: %w", err)
	}
	return out, nil
}

func parseGtk4TestJSON(stdoutRaw, stderrRaw []byte, group testharness.TestGroup) []*codegen.TestResult {
	type event struct {
		Action  string  `json:"Action"`
		Test    string  `json:"Test"`
		Output  string  `json:"Output"`
		Elapsed float64 `json:"Elapsed"`
	}
	var out []*codegen.TestResult
	logs := map[string][]string{}
	sawTestEvent := false
	for line := range bytes.SplitSeq(stdoutRaw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e event
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		switch e.Action {
		case "output":
			if e.Test != "" {
				logs[e.Test] = append(logs[e.Test], e.Output)
			}
		case "pass", "fail":
			if e.Test == "" {
				continue
			}
			sawTestEvent = true
			r := &codegen.TestResult{
				Component: group.Component,
				Desc:      e.Test,
				Passed:    e.Action == "pass",
				Log:       logs[e.Test],
			}
			if !r.Passed {
				r.Error = strings.Join(logs[e.Test], "")
			}
			out = append(out, r)
		}
	}
	if !sawTestEvent {
		errMsg := strings.TrimSpace(string(stderrRaw))
		if errMsg == "" {
			errMsg = strings.TrimSpace(string(stdoutRaw))
		}
		if errMsg == "" {
			errMsg = "go test produced no events"
		}
		for _, tf := range group.Funcs {
			out = append(out, &codegen.TestResult{
				Component: group.Component,
				Desc:      tf.Name,
				Passed:    false,
				Error:     errMsg,
			})
		}
	}
	return out
}

// ProbeTest reports whether the gtk4 dev libraries are available via
// pkg-config. gtk4 codegen uses cgo against system gtk4, so without
// it `go test` would fail at the cgo preamble.
func (g *Generator) ProbeTest() (bool, string) {
	if _, err := exec.LookPath("go"); err != nil {
		return false, "go toolchain not on PATH"
	}
	if _, err := exec.LookPath("pkg-config"); err != nil {
		return false, "pkg-config not on PATH (gtk4 cgo build requires it)"
	}
	if err := exec.Command("pkg-config", "--exists", "gtk4").Run(); err != nil {
		return false, "gtk4 dev libraries not installed (pkg-config --exists gtk4 failed)"
	}
	return true, ""
}
