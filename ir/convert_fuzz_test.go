package ir_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func FuzzConvertRoundTrip(f *testing.F) {
	// Seed corpus from testdata .sngl files.
	_, thisFile, _, _ := runtime.Caller(0)
	testdataDir := filepath.Join(filepath.Dir(thisFile), "..", "testdata")
	entries, err := os.ReadDir(testdataDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(testdataDir, e.Name()))
			if err == nil {
				f.Add(string(data))
			}
		}
	}

	f.Fuzz(func(t *testing.T, src string) {
		// Phase 1: parse and type-check the original source.
		doc1, ok := safeParse(t, src)
		if !ok {
			t.Skip("parse failed")
		}
		pkg1, diags1 := checker.Check(doc1, &checker.Config{IsMain: true})
		for _, d := range diags1 {
			if d.Severity == ir.Error {
				t.Skip("check failed")
			}
		}

		// Skip inputs with imports — resolver not available in fuzz context.
		if len(pkg1.Imports) > 0 {
			t.Skip("imports require resolver")
		}

		// Phase 2: convert IR back to AST and type-check again.
		doc2 := ir.Convert(pkg1)
		pkg2, diags2 := checker.Check(doc2, &checker.Config{IsMain: true})
		for _, d := range diags2 {
			if d.Severity == ir.Error {
				t.Fatalf("round-trip check error: %s", d.Error())
			}
		}

		// Phase 3: strip AST/cross-refs and deep compare.
		ir.StripForCompare(pkg1)
		ir.StripForCompare(pkg2)
		if !reflect.DeepEqual(pkg1, pkg2) {
			t.Fatalf("round-trip IR mismatch:\npkg1: %+v\npkg2: %+v", summarizePkg(pkg1), summarizePkg(pkg2))
		}
	})
}

func summarizePkg(pkg *ir.Package) string {
	return fmt.Sprintf("imports=%d structs=%d enums=%d units=%d consts=%d vars=%d funcs=%d comps=%d windows=%d timers=%d outputs=%d",
		len(pkg.Imports), len(pkg.Structs), len(pkg.Enums), len(pkg.Units),
		len(pkg.Consts), len(pkg.Vars), len(pkg.Funcs), len(pkg.Components),
		len(pkg.Windows), len(pkg.Timers), len(pkg.Outputs))
}

func safeParse(_ *testing.T, src string) (doc *ast.Document, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	d, err := parser.Parse("fuzz.sngl", []byte(src))
	if err != nil {
		return nil, false
	}
	return d, true
}
