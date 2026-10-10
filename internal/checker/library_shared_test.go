package checker_test

import (
	"flag"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/fixtures"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testtargets"
	"duckfam.us/sngl/internal/testutil"
	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/lib"
)

var updateLibraryShared = flag.Bool("update", false, "rewrite testdata/library_shared.txt")

const librarySharedRatchet = "testdata/library_shared.txt"

// Every fixture checks against a library an earlier check loaded as it does
// against a fresh one, and leaves that library as it found it. That is what
// checking each package once per process needs. The fixtures that do not yet
// are listed in testdata/library_shared.txt, which may only shrink: a fixture
// newly writing the library fails, and so does one listed that no longer
// does.
func TestLibraryShared(t *testing.T) {
	var library *checker.SharedLibrary
	load := func() {
		library = checker.NewSharedLibrary()
		loadEveryLibraryPackage(t, library)
	}
	load()
	var f testutil.Fingerprinter
	var pkgs []*ir.Package
	var before uint64
	got := map[string]string{}
	for s := range testutil.TestdataSamples(t) {
		doc, err := parser.Parse(s.Filename, []byte(s.Source))
		if err != nil {
			continue
		}
		_, fresh := checker.Check(doc, fixtures.CheckConfig(s, doc))

		if now := library.Packages(); pkgs == nil || len(now) != len(pkgs) {
			pkgs, f.Skip = now, notTheLibrary(now)
			before = f.Of(pkgs)
		}
		cfg := fixtures.CheckConfig(s, doc)
		library.Use(cfg)
		_, shared := checker.Check(doc, cfg)
		after := f.Of(pkgs)

		var why []string
		if after != before {
			why = append(why, "writes the library")
			logLibraryWrites(t, s, doc)
		}
		if a, b := diagnosticLines(fresh), diagnosticLines(shared); !slices.Equal(a, b) {
			why = append(why, "diagnostics differ")
			t.Logf("%s against a fresh library:\n%s\nagainst the shared one:\n%s", s.Name, strings.Join(a, "\n"), strings.Join(b, "\n"))
		}
		before = after
		if len(why) > 0 {
			got[s.Name] = strings.Join(why, ", ")
			// The next fixture is asked about a library no fixture has
			// written, or each offender would be blamed for the ones before.
			load()
			pkgs = nil
		}
	}

	var lines []string
	for _, name := range slices.Sorted(maps.Keys(got)) {
		lines = append(lines, name+": "+got[name])
	}
	want := readRatchet(t)
	if *updateLibraryShared {
		header := "# Fixtures whose check writes a library an earlier check loaded, or reports\n# what a check against a fresh library does not. TestLibraryShared keeps it\n# exact; rewrite it with -update.\n"
		if err := os.WriteFile(librarySharedRatchet, []byte(header+strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Logf("%d fixtures do not share the library", len(lines))
	for _, line := range lines {
		if !slices.Contains(want, line) {
			t.Errorf("not in %s: %s", librarySharedRatchet, line)
		}
	}
	for _, line := range want {
		if !slices.Contains(lines, line) {
			t.Errorf("%s lists %q, which no longer holds; rewrite it with -update", librarySharedRatchet, line)
		}
	}
}

func readRatchet(t *testing.T) []string {
	data, err := os.ReadFile(librarySharedRatchet)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var lines []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines
}

// loadEveryLibraryPackage checks a program importing every package a program
// may, so that a fixture is asked about the library rather than about the
// packages it happens to load first.
func loadEveryLibraryPackage(t *testing.T, shared *checker.SharedLibrary) {
	var src strings.Builder
	for _, path := range lib.Packages() {
		if path == "builtin" || strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "x/scheme/") {
			continue
		}
		fmt.Fprintf(&src, "import _ %q\n", "sngl:"+path)
	}
	doc, err := parser.Parse("library.sngl", []byte(src.String()))
	if err != nil {
		t.Fatal(err)
	}
	langs, plats := testtargets.Targets()
	cfg := &checker.Config{IsMain: true, Languages: langs, Platforms: plats, TargetsComplete: true}
	shared.Use(cfg)
	if _, diags := checker.Check(doc, cfg); len(diags) > 0 {
		t.Fatalf("loading the library:\n%s", strings.Join(diagnosticLines(diags), "\n"))
	}
}

func diagnosticLines(diags []ir.Diagnostic) []string {
	lines := make([]string, len(diags))
	for i, d := range diags {
		lines[i] = fmt.Sprintf("%v %s", d.Severity, d.Error())
	}
	slices.Sort(lines)
	return lines
}

// logLibraryWrites checks s again against a library no fixture has checked
// against, and logs what that check writes in it.
func logLibraryWrites(t *testing.T, s testutil.Sample, doc *ast.Document) {
	library := checker.NewSharedLibrary()
	loadEveryLibraryPackage(t, library)
	pkgs := library.Packages()
	f := testutil.Fingerprinter{Skip: notTheLibrary(pkgs)}
	before := f.Graph(pkgs)
	cfg := fixtures.CheckConfig(s, doc)
	library.Use(cfg)
	checker.Check(doc, cfg)
	changed := before.Changed(f.Graph(pkgs))
	t.Logf("%s writes %d library objects:\n%s", s.Name, len(changed), strings.Join(changed[:min(len(changed), 8)], "\n"))
}

// notTheLibrary stops a walk at what does not belong to pkgs: the AST, which
// TestProvidedDocsSurviveBuilds holds immutable, and another package or a
// declaration of one. A write linking the library to one is still seen, in the
// library object it was written into. Without it the walk reaches every
// library loaded before, through the list and map declarations ListOf and
// MapOf hand out from a process global until a load publishes its own.
func notTheLibrary(pkgs []*ir.Package) func(reflect.Value) bool {
	in := map[uintptr]bool{}
	for _, p := range pkgs {
		in[reflect.ValueOf(p).Pointer()] = true
	}
	pkgType := reflect.TypeFor[*ir.Package]()
	declPkg := map[reflect.Type][]int{}
	return func(v reflect.Value) bool {
		if v.Type() == pkgType {
			return !in[v.Pointer()]
		}
		elem := v.Type().Elem()
		if elem.PkgPath() == "duckfam.us/sngl/ast" {
			return true
		}
		index, ok := declPkg[elem]
		if !ok {
			if elem.Kind() == reflect.Struct {
				if field, found := elem.FieldByName("Pkg"); found && field.Type == pkgType {
					index = field.Index
				}
			}
			declPkg[elem] = index
		}
		if index == nil {
			return false
		}
		p := v.Elem().FieldByIndex(index)
		return !p.IsNil() && !in[p.Pointer()]
	}
}
