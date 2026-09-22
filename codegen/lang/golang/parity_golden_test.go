package golang_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
	"git.duckfam.us/jonathan/sngl/ir"

	// Register the html platform and the go language so the route-mode
	// fixture can be driven end-to-end through the same pipeline `sngl
	// generate --platform=html --lang=go` uses. The legacy CompileHTTP
	// path (golang/http.go) is what produces server.go's POST handler.
	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
	htmlplat "git.duckfam.us/jonathan/sngl/codegen/platform/html"
)

// TestParityGolden pins the byte-identical output of golang's LEGACY
// translation paths (testlower.go and http.go CompileHTTP) before the
// translator-unification refactor moves emission onto GoIRContext. A diff
// after a later phase means a real behavioral change — fix the emission,
// don't regenerate blindly.
//
// Two producers are exercised:
//
//   - test-lower: every testdata/parity/*.sngl component fixture is parsed,
//     checked, and run through codegen.CollectTestFuncs + golang.LowerTestFile
//     in both Native and Agent modes (the same calls fyne/bubbletea/gtk4 make).
//     Output diffed against <name>.golden.
//
//   - route mode: the testdata/parity/route_*/ directory (an app.sngl plus a
//     go.mod and a go: native package) is compiled through the html
//     Generator with --lang go, and the emitted server.go is diffed against
//     route_<name>.golden. server.go's POST action handler is lowered by the
//     legacy ContextVar path in http.go.
//
// Regenerate goldens with:
//
//	SNGL_UPDATE_GOLDEN=1 go test ./codegen/lang/golang/ -run TestParityGolden
func TestParityGolden(t *testing.T) {
	update := os.Getenv("SNGL_UPDATE_GOLDEN") == "1"

	srcs, err := filepath.Glob("testdata/parity/*.sngl")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(srcs)
	if len(srcs) == 0 {
		t.Fatal("no test-lower parity fixtures found")
	}
	for _, src := range srcs {
		t.Run(filepath.Base(src), func(t *testing.T) {
			got := lowerTestFileFixture(t, src)
			golden := strings.TrimSuffix(src, ".sngl") + ".golden"
			checkGolden(t, golden, got, update)
		})
	}

	routes, err := filepath.Glob("testdata/parity/route_*/app.sngl")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(routes)
	if len(routes) == 0 {
		t.Fatal("no route-mode parity fixtures found")
	}
	for _, src := range routes {
		dir := filepath.Dir(src)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			got := generateRouteServer(t, src)
			golden := filepath.Join(dir, "server.golden")
			checkGolden(t, golden, got, update)
		})
	}
}

func checkGolden(t *testing.T, golden, got string, update bool) {
	t.Helper()
	if update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with SNGL_UPDATE_GOLDEN=1 to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("output differs from golden %s\n--- got ---\n%s", golden, got)
	}
}

// lowerTestFileFixture parses+checks a component fixture and renders its test
// functions in both Native and Agent modes via the exact calls the platforms
// make (codegen.CollectTestFuncs + golang.LowerTestFile). The two blocks are
// concatenated so one golden pins both emission modes.
func lowerTestFileFixture(t *testing.T, path string) string {
	t.Helper()
	doc, err := parser.Parse(path, readFile(t, path))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Msg)
		}
	}
	if pkg == nil {
		t.Fatal("nil pkg")
	}
	fns, suffixes, methodFields := codegen.CollectTestFuncs(pkg)
	if len(fns) == 0 {
		t.Fatal("fixture has no test funcs")
	}
	var b strings.Builder
	b.WriteString("// ==== TestEmitNative ====\n")
	b.WriteString(golang.LowerTestFile("ui", nil, fns, suffixes, methodFields, golang.TestEmitNative))
	b.WriteString("\n// ==== TestEmitAgent ====\n")
	b.WriteString(golang.LowerTestFile("ui", nil, fns, suffixes, methodFields, golang.TestEmitAgent))
	return b.String()
}

// generateRouteServer drives the html Generator route-mode path for a fixture
// directory, returning the emitted server.go. Mirrors the html package's own
// generateHTML helper but captures server.go instead of index.html.
func generateRouteServer(t *testing.T, path string) string {
	t.Helper()
	doc, err := testutil.ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dir := filepath.Dir(path)
	// Configured with the platform and language this then generates for.
	// Without them html's package never loads, no stdlib component is
	// overridden, and the fixture renders `<vbox>`/`<text>` -- output no build
	// produces, pinned in a golden.
	pkg, diags := checker.Check(doc, &checker.Config{
		FS: os.DirFS(dir), Dir: dir, IsMain: true,
		Platforms: []ir.Platform{&htmlplat.Generator{}},
		// And the JavaScript translator, whatever the target language: the
		// page html writes carries script either way, and html.sngl declares
		// its setInterval with #[js.native] off sngl:language/js.
		Languages: []ir.Language{&golang.Translator{}, &javascript.Translator{}},
		Targets:   []ir.StaticTarget{{Platform: "html", Language: "go"}},
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Msg)
		}
	}
	if pkg == nil {
		t.Fatal("nil pkg")
	}

	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go language translator not registered")
	}
	gen := &htmlplat.Generator{}
	caps := codegen.CapsOrNone(lang.LanguageIdentifier(), gen.PlatformIdentifier()).ToLowerCaps()
	if err := optimize.Optimize(pkg, &optimize.Config{
		Platform: gen.PlatformIdentifier(),
		Language: lang.LanguageIdentifier(),
	}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	if err := lower.Lower(pkg, caps, lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	opts := &ir.StructLit{}
	codegen.SetOptionField(opts, "main", true)

	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{
		Doc:     doc,
		Pkg:     pkg,
		Lang:    lang,
		Options: opts,
	}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	for name, content := range mem.Files() {
		if strings.HasSuffix(name, "server.go") {
			return string(content)
		}
	}
	t.Fatal("expected a server.go output file")
	return ""
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
