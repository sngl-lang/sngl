package compiler_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
	"git.duckfam.us/jonathan/sngl/internal/testrunner"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func TestWebTests(t *testing.T) {
	matches, err := filepath.Glob("../../testdata/test_*.sngl")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no test_*.sngl files found")
	}

	lang := codegen.LookupLang("js")
	if lang == nil {
		t.Fatal("js language translator not registered")
	}
	htmlPlat := codegen.LookupPlatform("html")
	if htmlPlat == nil {
		t.Fatal("html platform not registered")
	}
	runner, ok := htmlPlat.(codegen.TestRunner)
	if !ok {
		t.Fatal("html platform does not implement TestRunner")
	}

	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")
		t.Run(name, func(t *testing.T) {
			// Skip files with parse/check error directives
			dirs, err := testutil.ParseDirectives(path)
			if err != nil {
				t.Fatalf("parse directives: %v", err)
			}
			if hasPhase(dirs, "parse") || hasPhase(dirs, "check") {
				t.Skip("error directive fixture")
			}

			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			doc, err := snglparser.Parse(path, f)
			f.Close()
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			// Get interpreter results for comparison
			interpResults, err := testrunner.Run(doc)
			if err != nil {
				t.Fatalf("interpreter: %v", err)
			}
			interpMap := buildInterpMap(interpResults)

			// Run browser tests via platform interface
			browserResults, err := runner.RunTests(doc, lang, doc.Tests)
			if err != nil {
				t.Fatalf("browser: %v", err)
			}

			// Compare browser results against interpreter
			for _, br := range browserResults {
				testKey := br.Component + "/" + br.Desc
				t.Run(br.Component+"/"+br.Desc, func(t *testing.T) {
					if hasTestErrorByKey(dirs, doc, testKey) {
						t.Skip("ERROR(test) directive")
					}
					if !br.Passed {
						if ir, ok := interpMap[testKey]; ok && !ir.Passed {
							t.Skipf("both interpreter and browser failed: %s", br.Error)
						}
						t.Fatalf("browser test failed: %s", br.Error)
					}
					compareChildren(t, br.Children, interpMap, testKey)
				})
			}
		})
	}
}

func compareChildren(t *testing.T, children []*codegen.TestResult, interpMap map[string]*codegen.TestResult, parentKey string) {
	for _, child := range children {
		childKey := parentKey + "/" + child.Desc
		t.Run(child.Desc, func(t *testing.T) {
			if !child.Passed {
				if ir, ok := interpMap[childKey]; ok && !ir.Passed {
					t.Skipf("both interpreter and browser failed: %s", child.Error)
				}
				t.Fatalf("browser subtest failed: %s", child.Error)
			}
			compareChildren(t, child.Children, interpMap, childKey)
		})
	}
}

func buildInterpMap(results []*codegen.TestResult) map[string]*codegen.TestResult {
	m := make(map[string]*codegen.TestResult)
	for _, r := range results {
		key := r.Component + "/" + r.Desc
		m[key] = r
		for _, child := range r.Children {
			childKey := key + "/" + child.Desc
			m[childKey] = child
			addChildResults(m, childKey, child.Children)
		}
	}
	return m
}

func addChildResults(m map[string]*codegen.TestResult, parentKey string, children []*codegen.TestResult) {
	for _, child := range children {
		key := parentKey + "/" + child.Desc
		m[key] = child
		addChildResults(m, key, child.Children)
	}
}

func hasPhase(dirs []testutil.ErrorDirective, phase string) bool {
	for _, d := range dirs {
		if d.Phase == phase {
			return true
		}
	}
	return false
}

func hasTestErrorByKey(dirs []testutil.ErrorDirective, doc *ast.Document, testKey string) bool {
	for _, td := range doc.Tests {
		key := td.Component + "/" + td.Desc
		if key == testKey && td.Pos.IsValid() {
			for _, d := range dirs {
				if d.Phase == "test" && d.Line == td.Pos.Line {
					return true
				}
			}
		}
	}
	return false
}
