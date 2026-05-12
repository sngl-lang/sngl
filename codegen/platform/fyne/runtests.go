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
func (g *Generator) RunTests(pkg *ir.Package, lang codegen.LangTranslator, opts *ir.StructLit) ([]*codegen.TestResult, error) {
	if pkg == nil {
		return nil, nil
	}
	var cfg Config
	if err := codegen.ApplyOptions(&cfg, opts); err != nil {
		return nil, fmt.Errorf("fyne RunTests options: %w", err)
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
		grpResults, err := runFyneTestGroup(pkg, group, resp.Files, cfg.GoModExtra)
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
func runFyneTestGroup(origPkg *ir.Package, group testharness.TestGroup, files []*codegen.OutputFile, goModExtra string) ([]*codegen.TestResult, error) {
	dir, err := os.MkdirTemp("", "sngl-fyne-test-")
	if err != nil {
		return nil, fmt.Errorf("mktemp: %w", err)
	}
	if os.Getenv("SNGL_KEEP_TEST_DIR") == "" {
		defer os.RemoveAll(dir)
	} else {
		fmt.Fprintln(os.Stderr, "sngl-fyne-test temp dir:", dir)
	}

	if err := writeGoMod(dir, goModExtra); err != nil {
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
	_ = cmd.Run() // read JSON regardless of exit code

	return parseGoTestJSON(stdout.Bytes(), stderr.Bytes(), group), nil
}

// writeGoMod writes a minimal go.mod that pulls in fyne v2. The Go
// toolchain resolves fyne from the module cache. If `goModExtra` is
// non-empty (sourced from the lang option of the same name) it is
// appended verbatim — typically a `replace` directive pointing SNGL
// runtime imports at a local checkout.
func writeGoMod(dir, goModExtra string) error {
	mod := `module sngltest

go 1.23

require fyne.io/fyne/v2 v2.7.3
`
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

// writeTestFile lowers each SNGL test in the group to a Go *testing.T
// function and writes them into a `_test.go` alongside the generated
// component code.
func writeTestFile(dir string, origPkg *ir.Package, group testharness.TestGroup) (string, error) {
	var b bytes.Buffer
	b.WriteString("package ui\n\n")
	b.WriteString("import (\n\t\"testing\"\n\t\"time\"\n)\n\n")
	// Reference `time` so the import isn't flagged unused when a given
	// test happens not to compare time.Duration values directly.
	b.WriteString("var _ = time.Duration(0)\n\n")
	b.WriteString("func newTestComponent() *Model { return New() }\n\n")

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
		b.WriteString(golang.LowerTestFunc(fn, suffix, nil))
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
