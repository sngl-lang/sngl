package checker_test

import (
	"fmt"
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

// Every fixture checks against a library an earlier check loaded as it does
// against a fresh one, and leaves that library as it found it. That is what
// checking each package once per process needs. A write the library makes of
// itself the first time something uses it is blamed on the first fixture after
// a load to use it.
func TestLibraryShared(t *testing.T) {
	var library *checker.SharedLibrary
	load := func() {
		library = checker.NewSharedLibrary()
		loadEveryLibraryPackage(t, library)
	}
	load()
	var f testutil.Fingerprinter
	var pkgs []*ir.Package
	var tables map[string]string
	var before uint64
	for s := range testutil.TestdataSamples(t) {
		doc, err := parser.Parse(s.Filename, []byte(s.Source))
		if err != nil {
			continue
		}
		_, fresh := checker.Check(doc, fixtures.CheckConfig(s, doc))

		// A load adds packages and table entries, and what it added is held
		// from the next fixture on.
		if now, held := library.Packages(), library.Tables(); pkgs == nil || len(now) != len(pkgs) || len(held) != len(tables) {
			pkgs, tables, f.Skip = now, held, notTheLibrary(now)
			before = f.Of(pkgs)
		}
		cfg := fixtures.CheckConfig(s, doc)
		library.Use(cfg)
		_, shared := checker.Check(doc, cfg)
		after := f.Of(pkgs)

		var why []string
		if rewritten := rewrittenEntries(tables, library.Tables()); after != before || len(rewritten) > 0 {
			if len(rewritten) > 0 {
				t.Logf("%s rewrites library cache entries:\n%s", s.Name, strings.Join(rewritten, "\n"))
			}
			why = append(why, "writes the library")
			logLibraryWrites(t, s, doc)
		}
		if a, b := diagnosticLines(fresh), diagnosticLines(shared); !slices.Equal(a, b) {
			why = append(why, "diagnostics differ")
			t.Logf("%s against a fresh library:\n%s\nagainst the shared one:\n%s", s.Name, strings.Join(a, "\n"), strings.Join(b, "\n"))
		}
		before = after
		if len(why) > 0 {
			t.Errorf("%s %s", s.Name, strings.Join(why, ", "))
			// The next fixture is asked about a library no fixture has
			// written, or each offender would be blamed for the ones before.
			load()
			pkgs = nil
		}
	}
}

// A write to any package the library holds is one the fingerprint sees,
// including the gir: widgets, which no libCache table names.
func TestLibraryFingerprintSeesEveryPackage(t *testing.T) {
	library := checker.NewSharedLibrary()
	loadEveryLibraryPackage(t, library)
	pkgs := library.Packages()
	skip := notTheLibrary(pkgs)
	var outside []string
	f := testutil.Fingerprinter{Skip: func(v reflect.Value) bool {
		stop := skip(v)
		if stop && v.Type().Elem().PkgPath() != "duckfam.us/sngl/ast" {
			outside = append(outside, fmt.Sprintf("%s %v", v.Type(), v.Elem().FieldByName("Name")))
		}
		return stop
	}}
	if f.Of(pkgs); len(outside) > 0 {
		t.Fatalf("the library reaches %d objects of packages Packages does not name, such as a %s", len(outside), outside[0])
	}
	var written int
	for _, p := range pkgs {
		var doc *string
		switch {
		case len(p.Components) > 0:
			doc = &p.Components[0].Doc
		case len(p.Funcs) > 0:
			doc = &p.Funcs[0].Doc
		case len(p.Structs) > 0:
			doc = &p.Structs[0].Doc
		default:
			continue
		}
		before := f.Of(pkgs)
		was := *doc
		*doc = was + "written"
		if f.Of(pkgs) == before {
			t.Errorf("a write to %q is not seen", p.Path)
		}
		*doc = was
		written++
	}
	if written < 30 {
		t.Fatalf("wrote to %d of %d packages", written, len(pkgs))
	}
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
// library object it was written into.
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

func rewrittenEntries(before, after map[string]string) []string {
	var out []string
	for k, v := range before {
		if now, ok := after[k]; !ok || now != v {
			out = append(out, fmt.Sprintf("%s: %s, now %q", k, v, now))
		}
	}
	slices.Sort(out)
	return out
}
