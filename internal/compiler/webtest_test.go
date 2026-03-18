package compiler_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"
	"git.duckfam.us/jonathan/sngl/internal/compiler"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
	"git.duckfam.us/jonathan/sngl/internal/testrunner"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
	"git.duckfam.us/jonathan/sngl/internal/testutil/webtest"
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

			// Group tests by component
			compTests := groupByComponent(doc.Tests)

			for compName, tests := range compTests {
				// Promote component to top-level document for HTML codegen
				compDoc := compiler.PromoteComponent(doc, compName)
				if compDoc == nil {
					t.Logf("skipping component %q: not found or no body", compName)
					continue
				}

				// Generate HTML
				resp, err := htmlPlat.Generate(&codegen.Request{
					Doc:     compDoc,
					Lang:    lang,
					Options: map[string]string{"test": "true"},
				})
				if err != nil {
					t.Logf("skipping component %q: generate error: %v", compName, err)
					continue
				}
				if resp.Error != "" {
					t.Logf("skipping component %q: %s", compName, resp.Error)
					continue
				}
				html := string(resp.Files[0].Content)

				// Serve the HTML
				mux := http.NewServeMux()
				mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/html")
					w.Write([]byte(html))
				})
				engine := webtest.New(mux)
				defer engine.Close()

				for _, td := range tests {
					desc := td.Desc
					if desc == "" {
						desc = "unnamed"
					}
					testKey := compName + "/" + td.Desc

					t.Run(compName+"/"+desc, func(t *testing.T) {
						// Skip tests with ERROR(test) directives
						if hasTestError(dirs, td) {
							t.Skip("ERROR(test) directive")
						}

						browser := engine.Start(t)
						browser.Navigate(t, "/")

						runner := compiler.NewCDPRunner(browser.Page(), compDoc, lang)
						if err := runner.InjectHelpers(); err != nil {
							t.Fatalf("inject helpers: %v", err)
						}

						// Execute test body
						if err := runner.ExecTest(td.Body); err != nil {
							// Check if interpreter also failed
							if ir, ok := interpMap[testKey]; ok && !ir.Passed {
								t.Skipf("both interpreter and browser failed: %v", err)
							}
							t.Fatalf("browser test failed: %v", err)
						}

						// Run subtests
						runWebSubtests(t, runner, td.Subtests, interpMap, testKey)
					})
				}
			}
		})
	}
}

func runWebSubtests(t *testing.T, runner *compiler.CDPRunner, subtests []*ast.TestDef, interpMap map[string]*testrunner.Result, parentKey string) {
	for _, sub := range subtests {
		desc := sub.Desc
		if desc == "" {
			desc = "unnamed"
		}
		subKey := parentKey + "/" + sub.Desc

		t.Run(desc, func(t *testing.T) {
			if err := runner.SaveState(); err != nil {
				t.Fatalf("save state: %v", err)
			}
			t.Cleanup(func() {
				runner.RestoreState()
			})

			if err := runner.ExecTest(sub.Body); err != nil {
				if ir, ok := interpMap[subKey]; ok && !ir.Passed {
					t.Skipf("both interpreter and browser failed: %v", err)
				}
				t.Fatalf("browser subtest failed: %v", err)
			}

			runWebSubtests(t, runner, sub.Subtests, interpMap, subKey)
		})
	}
}

func groupByComponent(tests []*ast.TestDef) map[string][]*ast.TestDef {
	groups := make(map[string][]*ast.TestDef)
	for _, td := range tests {
		groups[td.Component] = append(groups[td.Component], td)
	}
	return groups
}

func buildInterpMap(results []*testrunner.Result) map[string]*testrunner.Result {
	m := make(map[string]*testrunner.Result)
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

func addChildResults(m map[string]*testrunner.Result, parentKey string, children []*testrunner.Result) {
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

func hasTestError(dirs []testutil.ErrorDirective, td *ast.TestDef) bool {
	if !td.Pos.IsValid() {
		return false
	}
	for _, d := range dirs {
		if d.Phase == "test" && d.Line == td.Pos.Line {
			return true
		}
	}
	return false
}
