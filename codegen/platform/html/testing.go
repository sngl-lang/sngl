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
)

func (g *Generator) RunTests(doc *ast.Document, lang codegen.LangTranslator, tests []*ast.TestDef) ([]*codegen.TestResult, error) {
	// Group tests by component
	compTests := make(map[string][]*ast.TestDef)
	for _, td := range tests {
		compTests[td.Component] = append(compTests[td.Component], td)
	}

	var results []*codegen.TestResult
	for compName, tests := range compTests {
		compDoc := PromoteComponent(doc, compName)
		if compDoc == nil {
			continue
		}

		resp, err := g.Generate(&codegen.Request{
			Doc:     compDoc,
			Lang:    lang,
			Options: map[string]string{"test": "true"},
		})
		if err != nil {
			return nil, fmt.Errorf("generate %q: %w", compName, err)
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("generate %q: %s", compName, resp.Error)
		}
		var htmlBuf bytes.Buffer
		resp.Files[0].WriteTo(&htmlBuf)
		html := htmlBuf.String()

		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(html))
		})
		engine := webtest.New(mux)

		for _, td := range tests {
			result := g.runSingleTest(engine, compDoc, lang, compName, td)
			results = append(results, result)
		}

		engine.Close()
	}
	return results, nil
}

func (g *Generator) runSingleTest(engine *webtest.Engine, doc *ast.Document, lang codegen.LangTranslator, compName string, td *ast.TestDef) *codegen.TestResult {
	start := time.Now()
	desc := td.Desc
	if desc == "" {
		desc = "unnamed"
	}

	result := &codegen.TestResult{
		Component: compName,
		Desc:      desc,
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

	runner := NewCDPRunner(browser.Page(), doc, lang)
	if err := runner.InjectHelpers(); err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("inject helpers: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	if err := runner.ExecTest(td.Body); err != nil {
		result.Passed = false
		result.Error = err.Error()
		result.Duration = time.Since(start)
		return result
	}

	for _, sub := range td.Subtests {
		child := g.runSubtest(runner, compName, sub)
		result.Children = append(result.Children, child)
		if !child.Passed {
			result.Passed = false
		}
	}

	result.Duration = time.Since(start)
	return result
}

func (g *Generator) runSubtest(runner *CDPRunner, compName string, td *ast.TestDef) *codegen.TestResult {
	start := time.Now()
	desc := td.Desc
	if desc == "" {
		desc = "unnamed"
	}

	result := &codegen.TestResult{
		Component: compName,
		Desc:      desc,
		Passed:    true,
	}

	if err := runner.SaveState(); err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("save state: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	defer runner.RestoreState()

	if err := runner.ExecTest(td.Body); err != nil {
		result.Passed = false
		result.Error = err.Error()
		result.Duration = time.Since(start)
		return result
	}

	for _, sub := range td.Subtests {
		child := g.runSubtest(runner, compName, sub)
		result.Children = append(result.Children, child)
		if !child.Passed {
			result.Passed = false
		}
	}

	result.Duration = time.Since(start)
	return result
}

// Snapshot captures a browser screenshot of the generated HTML for the given document.
func (g *Generator) Snapshot(doc *ast.Document, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	resp, err := g.Generate(&codegen.Request{
		Doc:     doc,
		Lang:    lang,
		Options: map[string]string{"preview": "true"},
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
