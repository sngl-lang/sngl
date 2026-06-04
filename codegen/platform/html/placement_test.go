package html

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// pkgWithImport builds a minimal *ir.Package carrying a single scheme import
// (e.g. scheme="go", path="example.com/api") so funcImportScheme can map a
// func's NativePkg back to its originating import scheme.
func pkgWithImport(scheme, importPath string) *ir.Package {
	return &ir.Package{
		Imports: []*ir.Import{{
			Alias:  "api",
			AST:    &ast.Import{Path: scheme + "://" + importPath},
			Native: &ir.NativeImport{ImportPath: importPath},
		}},
	}
}

func TestFuncImportScheme(t *testing.T) {
	t.Run("go native func", func(t *testing.T) {
		pkg := pkgWithImport("go", "example.com/api")
		fn := &ir.Func{NativePkg: "example.com/api"}
		if s := funcImportScheme(pkg, fn); s != "go" {
			t.Fatalf("got %q want go", s)
		}
	})
	t.Run("js native func", func(t *testing.T) {
		pkg := pkgWithImport("js", "some-pkg")
		fn := &ir.Func{NativePkg: "some-pkg"}
		if s := funcImportScheme(pkg, fn); s != "js" {
			t.Fatalf("got %q want js", s)
		}
	})
	t.Run("non-native func", func(t *testing.T) {
		pkg := pkgWithImport("go", "example.com/api")
		fn := &ir.Func{Name: "local"}
		if s := funcImportScheme(pkg, fn); s != "" {
			t.Fatalf("got %q want empty", s)
		}
	})
	t.Run("nil func", func(t *testing.T) {
		pkg := pkgWithImport("go", "example.com/api")
		if s := funcImportScheme(pkg, nil); s != "" {
			t.Fatalf("got %q want empty", s)
		}
	})
}
