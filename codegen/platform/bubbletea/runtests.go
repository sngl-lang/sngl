//go:build !js

package bubbletea

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

// RunTests is the entry point for `sngl test --platform=bubbletea`. It
// groups the package's test functions by their target component,
// generates a temp Go module containing the bubbletea-emitted Model and
// a `_test.go` per component group, shells `go test -json`, and parses
// results back. Mirrors the fyne runner; the only differences are the
// require directives and the constructor return type (bubbletea's Model
// is a value type, not a pointer).
func (g *Generator) RunTests(pkg *ir.Package, lang codegen.LangTranslator, opts *ir.StructLit) ([]*codegen.TestResult, error) {
	if pkg == nil {
		return nil, nil
	}
	var cfg Config
	if err := codegen.ApplyOptions(&cfg, opts); err != nil {
		return nil, fmt.Errorf("bubbletea RunTests options: %w", err)
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

	resp, err := g.Generate(&codegen.Request{
		Pkg:  pkg,
		Lang: lang,
	})
	if err != nil {
		return nil, fmt.Errorf("bubbletea generate: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("bubbletea generate: %s", resp.Error)
	}

	var results []*codegen.TestResult
	for _, group := range compGroups {
		grpResults, err := runBubbleteaTestGroup(pkg, group, resp.Files, cfg.GoModExtra)
		if err != nil {
			return nil, err
		}
		results = append(results, grpResults...)
	}
	return results, nil
}

func runBubbleteaTestGroup(origPkg *ir.Package, group testharness.TestGroup, files []*codegen.OutputFile, goModExtra string) ([]*codegen.TestResult, error) {
	dir, err := os.MkdirTemp("", "sngl-bubbletea-test-")
	if err != nil {
		return nil, fmt.Errorf("mktemp: %w", err)
	}
	if os.Getenv("SNGL_KEEP_TEST_DIR") == "" {
		defer os.RemoveAll(dir)
	} else {
		fmt.Fprintln(os.Stderr, "sngl-bubbletea-test temp dir:", dir)
	}

	if err := writeBubbleteaGoMod(dir, goModExtra); err != nil {
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

	if _, err := writeBubbleteaTestFile(dir, origPkg, group); err != nil {
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

	return parseBubbleteaTestJSON(stdout.Bytes(), stderr.Bytes(), group), nil
}

// writeBubbleteaGoMod pins the bubbletea + lipgloss versions that match
// what the emitted code imports. goModExtra (from the `goModExtra` Go
// build option) is appended verbatim — typically a replace directive
// pointing SNGL runtime imports at a local checkout.
func writeBubbleteaGoMod(dir, goModExtra string) error {
	mod := `module sngltest

go 1.23

require (
	charm.land/bubbletea/v2 v2.0.6
	charm.land/lipgloss/v2 v2.0.3
)
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

func writeBubbleteaTestFile(dir string, origPkg *ir.Package, group testharness.TestGroup) (string, error) {
	var b bytes.Buffer
	b.WriteString("package ui\n\n")
	b.WriteString("import (\n\t\"testing\"\n\t\"time\"\n)\n\n")
	b.WriteString("var _ = time.Duration(0)\n\n")

	// bubbletea's Model is a value type (not a pointer), so the helper
	// returns Model directly. Lowered tests bind `c` to this value and
	// rely on Go local-addressability for `c.field = …` writes.
	b.WriteString("func newTestComponent() Model { return New() }\n\n")

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

func parseBubbleteaTestJSON(stdoutRaw, stderrRaw []byte, group testharness.TestGroup) []*codegen.TestResult {
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

// ProbeTest reports whether a Go toolchain is available. Bubbletea's
// own runtime dependencies are resolved at RunTests time and surface as
// build failures through the test results rather than at probe time.
func (g *Generator) ProbeTest() (bool, string) {
	if _, err := exec.LookPath("go"); err != nil {
		return false, "go toolchain not on PATH"
	}
	return true, ""
}
