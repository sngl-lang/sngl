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

func (g *Generator) RunTests(doc *ast.Document, lang codegen.LangTranslator) ([]*codegen.TestResult, error) {
	testFuncs := doc.TestFuncs()
	if len(testFuncs) == 0 {
		return nil, nil
	}

	// Group tests by component (from second param type)
	type testGroup struct {
		compName string
		funcs    []*ast.FuncDef
	}
	groups := map[string]*testGroup{}
	for _, fn := range testFuncs {
		compName := ""
		if len(fn.Params.Params) >= 2 {
			if nt, ok := fn.Params.Params[1].Type.(*ast.NamedType); ok {
				compName = nt.Name
			}
		}
		g, ok := groups[compName]
		if !ok {
			g = &testGroup{compName: compName}
			groups[compName] = g
		}
		g.funcs = append(g.funcs, fn)
	}

	var results []*codegen.TestResult
	for compName, group := range groups {
		if compName == "" {
			// Standalone tests — skip browser for now, use headless
			continue
		}
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
		htmlStr := htmlBuf.String()

		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(htmlStr))
		})
		engine := webtest.New(mux)

		for _, fn := range group.funcs {
			result := g.runSingleTestFunc(engine, compDoc, lang, compName, fn)
			results = append(results, result)
		}

		engine.Close()
	}
	return results, nil
}

func (g *Generator) runSingleTestFunc(engine *webtest.Engine, doc *ast.Document, lang codegen.LangTranslator, compName string, fn *ast.FuncDef) *codegen.TestResult {
	start := time.Now()
	result := &codegen.TestResult{
		Component: compName,
		Desc:      fn.Name,
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

	if fn.Block.IsDefined() {
		if err := runner.ExecTest(fn.Block.Stmts); err != nil {
			result.Passed = false
			result.Error = err.Error()
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
