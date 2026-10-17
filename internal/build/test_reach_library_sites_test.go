package build_test

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/txtar"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/interp/testrunner"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/optimize"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testlaunch"
	"duckfam.us/sngl/internal/testutil"
	"duckfam.us/sngl/ir"
)

var (
	updateRuns = flag.Bool("update", false, "rerun the test launches whose inputs moved, and rewrite their testrun/ records")
	verifyRuns = flag.Bool("verify", false, "rerun every test launch, whatever its testrun/ record says")
)

const librarySites = "testdata/test_reach_library_sites.txtar"

// librarySitesCheck checks the archive's program against its sngl:blip,
// selecting the target a build is for.
func librarySitesCheck(t *testing.T, files []txtar.File, lang, plat string) *ir.Package {
	t.Helper()
	docs := map[string]*ast.Document{}
	for _, f := range files {
		doc, err := parser.Parse(f.Name, f.Data)
		if err != nil {
			t.Fatal(err)
		}
		docs[f.Name] = doc
	}
	langs, plats := build.RegisteredTargets()
	pkg, diags := checker.Check(docs["main.sngl"], &checker.Config{
		IsMain:          true,
		Languages:       langs,
		Platforms:       plats,
		Targets:         build.SelectedTargets(lang, plat),
		TargetsComplete: true,
		LibSources:      map[string][]*ast.Document{"blip": {docs["blip.sngl"]}},
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	return pkg
}

func librarySitesArchive(t *testing.T) (*txtar.Archive, map[string]testlaunch.Record, []txtar.File) {
	t.Helper()
	a, err := txtar.ParseFile(librarySites)
	if err != nil {
		t.Fatal(err)
	}
	records, rest, err := testlaunch.SplitArchive(a)
	if err != nil {
		t.Fatal(err)
	}
	return a, records, rest
}

var librarySitesTargets = [][2]string{{"none", "none"}, {"none", "html"}, {"go", "bubbletea"}, {"go", "fyne"}, {"go", "gtk4"}, {"kotlin", "android"}}

// A test reaching through a library component the program renders at several
// sites reaches that site's copy, made on the build's copy of the package: the
// library's own declaration is left as every other check reads it.
func TestTestReachCopiesALibraryComponentPerSite(t *testing.T) {
	_, _, files := librarySitesArchive(t)
	pkg := librarySitesCheck(t, files, "", "")
	var badge *ir.Component
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if ni, ok := n.(*ir.NodeInst); ok && ni.Component != nil && ni.Component.Name == "badge" {
			badge = ni.Component
		}
		return nil
	})
	if badge == nil || badge.PkgPath() == "" {
		t.Fatal("found no library badge")
	}
	before := testutil.GraphOf(badge)
	for _, target := range librarySitesTargets[1:] {
		lang, plat := target[0], target[1]
		t.Run(plat, func(t *testing.T) {
			b := ir.ClonePackageFor(pkg, plat, lang)
			if err := optimize.Optimize(b, &optimize.Config{Platform: plat, Language: lang}); err != nil {
				t.Fatal(err)
			}
			feats, err := codegen.CapsFor(lang, plat)
			if err != nil {
				t.Fatal(err)
			}
			if err := lower.Lower(b, feats, lower.Options{Platform: plat, Language: lang, RootComponent: "main", Tests: true}); err != nil {
				t.Fatalf("lower: %v", err)
			}
		})
	}
	if changed := before.Changed(testutil.GraphOf(badge)); len(changed) > 0 {
		t.Errorf("a test build wrote the library's badge:\n%v", changed)
	}
}

// TestTestReachLibrarySitesRun runs the archive's tests on every target, each
// launch answered from the archive's testrun/ records as a script's are.
func TestTestReachLibrarySitesRun(t *testing.T) {
	a, records, files := librarySitesArchive(t)
	rr := testlaunch.NewRecords(records, *updateRuns, *verifyRuns)
	dir := t.TempDir()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.Name), f.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range librarySitesTargets {
		lang, plat := target[0], target[1]
		t.Run(plat, func(t *testing.T) {
			pkg := librarySitesCheck(t, files, lang, plat)
			pkg.SourcePath = filepath.Join(dir, "main.sngl")
			var results []*codegen.TestResult
			var err error
			if plat == "none" {
				results, err = testrunner.Run(pkg)
			} else {
				results, err = testlaunch.Run(context.Background(), pkg, testlaunch.Config{
					Platform: codegen.LookupPlatform(plat),
					Lang:     codegen.LookupLang(lang),
					Dir:      dir,
					File:     "main.sngl",
					Records:  rr,
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			var walk func(rs []*codegen.TestResult)
			walk = func(rs []*codegen.TestResult) {
				for _, r := range rs {
					if len(r.Children) > 0 {
						walk(r.Children)
						continue
					}
					n++
					if !r.Passed {
						t.Errorf("%s failed: %s", r.Desc, r.Error)
					}
				}
			}
			walk(results)
			if n == 0 {
				t.Error("ran no test")
			}
		})
	}
	if *updateRuns && !t.Failed() {
		if err := testlaunch.WriteArchive(librarySites, a, files, rr.Written()); err != nil {
			t.Fatal(err)
		}
	}
}
