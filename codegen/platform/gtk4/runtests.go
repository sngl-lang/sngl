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

	// Generate once for the whole package; per-test temp module copies
	// the same files and writes its own _test.go.
	resp, err := g.Generate(&codegen.Request{
		Pkg:  pkg,
		Lang: lang,
	})
	if err != nil {
		return nil, fmt.Errorf("gtk4 generate: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("gtk4 generate: %s", resp.Error)
	}

	var results []*codegen.TestResult
	for _, group := range compGroups {
		grpResults, err := runGtk4TestGroup(pkg, group, resp.Files, cfg.GoModExtra)
		if err != nil {
			return nil, err
		}
		results = append(results, grpResults...)
	}
	return results, nil
}

func runGtk4TestGroup(origPkg *ir.Package, group testharness.TestGroup, files []*codegen.OutputFile, goModExtra string) ([]*codegen.TestResult, error) {
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
	if _, err := writeGtk4TestFile(dir, origPkg, group); err != nil {
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

func writeGtk4TestFile(dir string, origPkg *ir.Package, group testharness.TestGroup) (string, error) {
	var b bytes.Buffer
	b.WriteString("package main\n\n")
	b.WriteString("import (\n\t\"testing\"\n\t\"time\"\n)\n\n")
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
