//go:build !js

package fyne

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
	"git.duckfam.us/jonathan/sngl/ir"
)

// RunTests is the entry point for `sngl test --platform=fyne`. It groups
// the package's test functions by their target component, generates a
// temp Go module per component, shells `go test -json`, and parses
// results back.
//
// Unlike the HTML runner, fyne does not need to promote+re-check the
// component in isolation. The fyne codegen generates one Model type per
// component declared in the package; the lowered _test.go references that
// type directly. We therefore run Generate against the original (already-
// checked) pkg and collect all outputs into a single temp module, then
// write one _test.go per component group.
func (g *Generator) RunTests(pkg *ir.Package, lang codegen.LangTranslator) ([]*codegen.TestResult, error) {
	if pkg == nil {
		return nil, nil
	}
	doc := ir.Convert(pkg)
	astTestFuncs := doc.TestFuncs()
	if len(astTestFuncs) == 0 {
		return nil, nil
	}
	groups := testharness.Group(astTestFuncs)

	// Filter to component groups only.
	var compGroups []testharness.TestGroup
	for _, g := range groups {
		if g.Component != "" {
			compGroups = append(compGroups, g)
		}
	}
	if len(compGroups) == 0 {
		return nil, nil
	}

	// Generate fyne code once for the whole package (all components).
	resp, err := g.Generate(&codegen.Request{
		Pkg:  pkg,
		Lang: lang,
	})
	if err != nil {
		return nil, fmt.Errorf("fyne generate: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("fyne generate: %s", resp.Error)
	}

	var results []*codegen.TestResult
	for _, group := range compGroups {
		grpResults, err := runFyneTestGroup(pkg, group, resp.Files)
		if err != nil {
			return nil, err
		}
		results = append(results, grpResults...)
	}
	return results, nil
}

// runFyneTestGroup writes a temp Go module containing the generated fyne
// component plus a `_test.go` file with one Go test per SNGL test in the
// group, shells `go test -json`, and parses results.
//
// origPkg is the *un-promoted* IR package: the test funcs themselves
// live there (testharness.Promote drops *ast.FuncDef decls so the
// promoted pkg.Funcs is empty for our tests).
func runFyneTestGroup(origPkg *ir.Package, group testharness.TestGroup, files []*codegen.OutputFile) ([]*codegen.TestResult, error) {
	dir, err := os.MkdirTemp("", "sngl-fyne-test-")
	if err != nil {
		return nil, fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := writeGoMod(dir); err != nil {
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

	if _, err := writeTestFile(dir, origPkg, group); err != nil {
		return nil, err
	}

	cmd := exec.Command("go", "test", "-json", "-count=1", "./...")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run() // read JSON regardless of exit code

	return parseGoTestJSON(stdout.Bytes(), stderr.Bytes(), group), nil
}

// writeGoMod writes a minimal go.mod that pulls in fyne v2. The Go
// toolchain resolves fyne from the module cache.
func writeGoMod(dir string) error {
	mod := `module sngltest

go 1.23

require fyne.io/fyne/v2 v2.5.0
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0644); err != nil {
		return fmt.Errorf("go.mod: %w", err)
	}
	return nil
}

// writeTestFile lowers each SNGL test in the group to a Go *testing.T
// function and writes them into a `_test.go` alongside the generated
// component code.
func writeTestFile(dir string, origPkg *ir.Package, group testharness.TestGroup) (string, error) {
	var b bytes.Buffer
	b.WriteString("package ui\n\n")
	b.WriteString("import \"testing\"\n\n")

	// Helper used by every lowered test. Task 5 will wire a real
	// constructor; for now return nil so the file compiles.
	b.WriteString("func newTestComponent() *Model { return nil }\n\n")

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
		// Strip the leading "test" so the Go test name is `TestFooBar`.
		suffix := strings.TrimPrefix(fn.Name, "test")
		b.WriteString(golang.LowerTestFunc(fn, suffix))
		b.WriteString("\n")
	}

	out := filepath.Join(dir, "component_test.go")
	if err := os.WriteFile(out, b.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("write test file: %w", err)
	}
	return out, nil
}

// parseGoTestJSON reads `go test -json` event stream and converts each
// PASS/FAIL test event into a codegen.TestResult. Build failures surface
// as a single synthetic failed TestResult per group with stderr attached.
func parseGoTestJSON(stdoutRaw, stderrRaw []byte, group testharness.TestGroup) []*codegen.TestResult {
	type event struct {
		Action  string  `json:"Action"`
		Test    string  `json:"Test"`
		Output  string  `json:"Output"`
		Elapsed float64 `json:"Elapsed"`
	}
	var out []*codegen.TestResult
	logs := map[string][]string{}
	sawTestEvent := false
	for _, line := range bytes.Split(stdoutRaw, []byte("\n")) {
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
				continue // package-level summary event
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
		// Build failed or test binary couldn't start. Surface one failure
		// per SNGL test in the group with stderr attached.
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

// ProbeTest reports whether a Go toolchain is available. We don't probe
// for fyne build deps directly — that would require running a real
// `go build`, which is too expensive for a cheap probe. Actual fyne
// dependencies surface at RunTests time as a build failure reported
// through the test results.
func (g *Generator) ProbeTest() (bool, string) {
	if _, err := exec.LookPath("go"); err != nil {
		return false, "go toolchain not on PATH"
	}
	return true, ""
}
