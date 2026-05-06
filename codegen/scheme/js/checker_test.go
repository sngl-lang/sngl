package js_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen/scheme/js"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// jsResolver delegates ResolveScheme to the real JSImporter, anchoring
// imports against a fixed dir so testdata fixtures resolve.
type jsResolver struct {
	dir      string
	importer js.JSImporter
}

func (r *jsResolver) Resolve(srcFS fs.FS, path string) ([]*ast.Document, error) {
	return nil, fmt.Errorf("not used in js tests: %s", path)
}

func (r *jsResolver) ResolveScheme(scheme, uri, _ string) (*ir.NativeImport, error) {
	if scheme != "js" {
		return nil, fmt.Errorf("unknown scheme %q", scheme)
	}
	return r.importer.Resolve(scheme+"://"+uri, r.dir)
}

func (r *jsResolver) ResolveSchemeFS(scheme, uri, dir string) ([]*ast.Document, fs.FS, error) {
	return nil, nil, nil
}

func TestCheckerAcceptsJSImport(t *testing.T) {
	dir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	src := `
import lib "js://./simple"

func wrap(a int, b int) => lib.add(a, b)
`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r := &jsResolver{dir: dir}
	pkg, diags := checker.Check(doc, &checker.Config{Resolver: r, Dir: dir})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("unexpected diag: %s", d.Error())
		}
	}
	var wrap *ir.Func
	for _, f := range pkg.Funcs {
		if f.Name == "wrap" {
			wrap = f
			break
		}
	}
	if wrap == nil {
		t.Fatal("wrap not found")
	}
	if wrap.Return == nil || wrap.Return.Kind != ir.TypeFloat {
		// add returns number → TypeFloat in our v1 mapping
		t.Errorf("wrap return: %v, want float", wrap.Return)
	}
}

func TestAsyncPropagatesThroughCall(t *testing.T) {
	dir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	// fetchTitle is async (returns a Promise in the TS fixture). A SNGL wrapper
	// that calls it — even with a param — must propagate IsAsync. Rule 2 only
	// fires when the func is called from a reactive prop expression; a bare
	// top-level func declaration that is never referenced from a visual node is
	// not a reactive context, so no error is expected here.
	src := `
import lib "js://./simple"

func getTitle(url string) => lib.fetchTitle(url)
`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r := &jsResolver{dir: dir}
	pkg, diags := checker.Check(doc, &checker.Config{Resolver: r, Dir: dir})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("unexpected diag: %s", d.Error())
		}
	}
	var getTitle *ir.Func
	for _, f := range pkg.Funcs {
		if f.Name == "getTitle" {
			getTitle = f
			break
		}
	}
	if getTitle == nil {
		t.Fatal("getTitle not found")
	}
	if !getTitle.IsAsync {
		t.Error("getTitle.IsAsync = false; expected propagation from lib.fetchTitle")
	}
}

func TestUnusableSymbolRejected(t *testing.T) {
	dir := t.TempDir()
	tsPath := filepath.Join(dir, "weird.ts")
	if err := os.WriteFile(tsPath, []byte(`export function tup(): [string, number] { return ["x", 1]; }`), 0o644); err != nil {
		t.Fatal(err)
	}
	imp, err := (&js.JSImporter{}).Resolve("js://./weird.ts", dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(imp.Funcs) != 1 {
		t.Fatalf("Funcs: %d, want 1", len(imp.Funcs))
	}
	if imp.Funcs[0].Unusable == "" {
		t.Errorf("tup.Unusable empty; expected tuple-not-supported message")
	}
	if !strings.Contains(imp.Funcs[0].Unusable, "tuple") {
		t.Errorf("tup.Unusable = %q, want tuple mention", imp.Funcs[0].Unusable)
	}
}
