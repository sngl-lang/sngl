//go:build !js

package html

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/html/internal/webtest"
	"git.duckfam.us/jonathan/sngl/codegen/testharness"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/go-rod/rod/lib/launcher"
)

// ProbeTest reports whether a Chrome/Chromium binary is on PATH. Returns
// false rather than triggering go-rod's auto-download — keeping `go tool
// verify` predictable on a clean host.
func (g *Generator) ProbeTest() (bool, string) {
	if _, found := launcher.LookPath(); found {
		return true, ""
	}
	return false, "no chrome/chromium found on PATH"
}

func (g *Generator) RunTests(pkg *ir.Package, lang codegen.LangTranslator) ([]*codegen.TestResult, error) {
	if pkg == nil {
		return nil, nil
	}

	// Collect test FuncDef AST nodes — the test runner still walks AST
	// test-body statements (this is the one place the cdprunner still
	// needs AST; the codegen side is pure IR).
	doc := ir.Convert(pkg)
	testFuncs := doc.TestFuncs()
	if len(testFuncs) == 0 {
		return nil, nil
	}

	// Group tests by component (from second param type)
	groups := testharness.Group(testFuncs)

	var results []*codegen.TestResult
	for _, group := range groups {
		compName := group.Component
		if compName == "" {
			// Standalone tests — skip browser for now, use headless
			continue
		}
		compDoc := PromoteComponent(doc, compName)
		if compDoc == nil {
			continue
		}

		// Re-check the promoted doc so we get an ir.Package for codegen.
		// The promoted doc restricts the surface to one component so the
		// test harness can render it in isolation.
		compPkg := checkPromoted(compDoc)
		if compPkg == nil {
			continue
		}
		resp, err := g.Generate(&codegen.Request{
			Pkg:     compPkg,
			Lang:    lang,
			Options: codegen.OptionsFromMap(map[string]any{"test": true}),
		})
		if err != nil {
			return nil, fmt.Errorf("generate %q: %w", compName, err)
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("generate %q: %s", compName, resp.Error)
		}
		var htmlBuf bytes.Buffer
		resp.Files[0].WriteTo(&htmlBuf)
		htmlStr := htmlBuf.String()

		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(htmlStr))
		})
		engine := webtest.New(mux)

		for _, tf := range group.Funcs {
			irFn := lookupIRFunc(compPkg, tf.Name)
			result := g.runSingleTestFunc(engine, compPkg, lang, compName, tf.Name, irFn)
			results = append(results, result)
		}

		engine.Close()
	}
	return results, nil
}

// lookupIRFunc finds a checked function by name in the package's top-level
// function set, or nil if absent.
func lookupIRFunc(pkg *ir.Package, name string) *ir.Func {
	if pkg == nil {
		return nil
	}
	for _, f := range pkg.Funcs {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// checkPromoted re-checks a promoted component doc through the normal
// checker so CDP test harness codegen gets a full ir.Package.
func checkPromoted(doc *ast.Document) *ir.Package {
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			return nil
		}
	}
	return pkg
}

func (g *Generator) runSingleTestFunc(engine *webtest.Engine, pkg *ir.Package, lang codegen.LangTranslator, compName, fnName string, fn *ir.Func) *codegen.TestResult {
	start := time.Now()
	result := &codegen.TestResult{
		Component: compName,
		Desc:      fnName,
		Passed:    true,
	}

	browser, err := engine.StartHeadless(1280, 720)
	if err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("browser start: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	defer browser.Close()

	if err := browser.NavigateRaw(engine.BaseURL() + "/"); err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("navigate: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	runner := NewCDPRunner(browser.Page(), pkg, lang)
	if err := runner.InjectHelpers(); err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("inject helpers: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	if fn != nil && len(fn.Block) > 0 {
		if err := runner.ExecTest(fn.Block); err != nil {
			result.Passed = false
			result.Error = err.Error()
		}
	}

	result.Duration = time.Since(start)
	return result
}

// Snapshot captures a browser screenshot of the generated HTML for the given package.
func (g *Generator) Snapshot(pkg *ir.Package, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	resp, err := g.Generate(&codegen.Request{
		Pkg:     pkg,
		Lang:    lang,
		Options: codegen.OptionsFromMap(map[string]any{"preview": true}),
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var buf bytes.Buffer
	resp.Files[0].WriteTo(&buf)
	return g.SnapshotHTML(buf.Bytes(), width, height)
}

// SnapshotHTML captures a browser screenshot of pre-compiled HTML bytes.
func (g *Generator) SnapshotHTML(html []byte, width, height int) ([]byte, error) {
	mux := http.NewServeMux()
	content := html
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(content)
	})
	engine := webtest.New(mux)
	defer engine.Close()

	browser, err := engine.StartHeadless(width, height)
	if err != nil {
		return nil, err
	}
	defer browser.Close()

	if err := browser.NavigateRaw(engine.BaseURL() + "/"); err != nil {
		return nil, err
	}
	_ = browser.WaitStable(300 * time.Millisecond)

	return browser.ScreenshotRaw()
}
