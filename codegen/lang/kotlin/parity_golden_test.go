package kotlin_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestParityGolden pins the byte-identical output of kotlin's LEGACY
// test-lowering path (testlower.go, whose call/i18n fallbacks route through
// translateIRExpr/translateIRCall/translateIRNamespaceCall) before the
// KtIRContext unification retires it. Kotlin has no route mode, so test-lower
// is the only producer.
//
// Each testdata/parity/*.sngl component fixture is parsed, checked, and run
// through codegen.CollectTestFuncs + kotlin.LowerTestFile in both Native
// (robolectric) and Agent modes — the same calls the android platform makes.
// Output is diffed against <name>.golden. A later-phase diff means a real
// behavioral change.
//
// Regenerate goldens with:
//
//	SNGL_UPDATE_GOLDEN=1 go test ./codegen/lang/kotlin/ -run TestParityGolden
func TestParityGolden(t *testing.T) {
	update := os.Getenv("SNGL_UPDATE_GOLDEN") == "1"

	srcs, err := filepath.Glob("testdata/parity/*.sngl")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(srcs)
	if len(srcs) == 0 {
		t.Fatal("no parity fixtures found")
	}
	for _, src := range srcs {
		t.Run(filepath.Base(src), func(t *testing.T) {
			got := lowerTestFileFixture(t, src)
			golden := strings.TrimSuffix(src, ".sngl") + ".golden"
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
		})
	}
}

// lowerTestFileFixture parses+checks a component fixture and renders its test
// functions in both Native (robolectric) and Agent modes via the exact calls
// the android platform makes (codegen.CollectTestFuncs + kotlin.LowerTestFile).
func lowerTestFileFixture(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc, err := parser.Parse(path, b)
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
	const pkgName = "us.duckfam.sngl.app"
	var out strings.Builder
	out.WriteString("// ==== TestEmitNative (robolectric) ====\n")
	out.WriteString(kotlin.LowerTestFile(pkgName, fns, suffixes, methodFields, kotlin.TestEmitNative, "robolectric"))
	out.WriteString("\n// ==== TestEmitAgent ====\n")
	out.WriteString(kotlin.LowerTestFile(pkgName, fns, suffixes, methodFields, kotlin.TestEmitAgent, ""))
	return out.String()
}
