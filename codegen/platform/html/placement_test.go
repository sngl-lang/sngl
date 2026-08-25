package html

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// pkgWithImport builds a minimal *ir.Package carrying a single scheme import
// (e.g. scheme="go", path="example.com/api") so funcImportScheme can map a
// func's Foreign.Path back to its originating import scheme.
func pkgWithImport(scheme, importPath string) *ir.Package {
	return &ir.Package{
		Imports: []*ir.Import{{
			Alias:  "api",
			AST:    &ast.Import{Path: scheme + "://" + importPath},
			Native: &ir.NativeImport{ImportPath: importPath},
		}},
	}
}

// goCall builds a *ir.Call to a native func originating from the given import.
func goCall(importPath string) *ir.Call {
	return &ir.Call{Func: &ir.Func{Foreign: ir.Foreign{Path: importPath}}}
}

// jsCall builds a *ir.Call to a native js:// func.
func jsCall(importPath string) *ir.Call {
	return &ir.Call{Func: &ir.Func{Foreign: ir.Foreign{Path: importPath}}}
}

// intrinsicCall wraps an argument expression in a call to a func carrying the
// given intrinsic id (e.g. "html.frontend"/"html.backend").
func intrinsicCall(id string, arg ir.Expr) *ir.Call {
	return &ir.Call{
		Func: &ir.Func{Intrinsic: id},
		Args: []ir.CallArg{{Value: arg}},
	}
}

func TestFuncImportScheme(t *testing.T) {
	t.Run("go native func", func(t *testing.T) {
		pkg := pkgWithImport("go", "example.com/api")
		fn := &ir.Func{Foreign: ir.Foreign{Path: "example.com/api"}}
		if s := funcImportScheme(pkg, fn); s != "go" {
			t.Fatalf("got %q want go", s)
		}
	})
	t.Run("js native func", func(t *testing.T) {
		pkg := pkgWithImport("js", "some-pkg")
		fn := &ir.Func{Foreign: ir.Foreign{Path: "some-pkg"}}
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

func TestExprPlacement(t *testing.T) {
	t.Run("bare go call -> backend", func(t *testing.T) {
		pkg := pkgWithImport("go", "example.com/api")
		if p := exprPlacement(pkg, goCall("example.com/api")); p != Backend {
			t.Fatalf("got %v want Backend", p)
		}
	})
	t.Run("bare js call -> frontend", func(t *testing.T) {
		pkg := pkgWithImport("js", "some-pkg")
		if p := exprPlacement(pkg, jsCall("some-pkg")); p != Frontend {
			t.Fatalf("got %v want Frontend", p)
		}
	})
	t.Run("sngl-only expr -> frontend", func(t *testing.T) {
		pkg := pkgWithImport("go", "example.com/api")
		e := &ir.Binary{Left: &ir.Ident{Name: "n"}, Right: &ir.Literal{}}
		if p := exprPlacement(pkg, e); p != Frontend {
			t.Fatalf("got %v want Frontend", p)
		}
	})
	t.Run("go call nested in expr -> backend", func(t *testing.T) {
		pkg := pkgWithImport("go", "example.com/api")
		e := &ir.Binary{Left: goCall("example.com/api"), Right: &ir.Literal{}}
		if p := exprPlacement(pkg, e); p != Backend {
			t.Fatalf("got %v want Backend", p)
		}
	})
	t.Run("html.frontend(go_call) -> frontend", func(t *testing.T) {
		pkg := pkgWithImport("go", "example.com/api")
		e := intrinsicCall("html.frontend", goCall("example.com/api"))
		if p := exprPlacement(pkg, e); p != Frontend {
			t.Fatalf("got %v want Frontend", p)
		}
	})
	t.Run("html.backend(sngl_expr) -> backend", func(t *testing.T) {
		pkg := pkgWithImport("go", "example.com/api")
		e := intrinsicCall("html.backend", &ir.Ident{Name: "n"})
		if p := exprPlacement(pkg, e); p != Backend {
			t.Fatalf("got %v want Backend", p)
		}
	})
}

func TestHandlerPlacement(t *testing.T) {
	t.Run("handler with go call -> backend", func(t *testing.T) {
		pkg := pkgWithImport("go", "example.com/api")
		fn := &ir.Func{Block: []ir.Stmt{
			&ir.Assign{
				Target: &ir.Ident{Name: "count"},
				Value:  goCall("example.com/api"),
			},
		}}
		if p := handlerPlacement(pkg, fn); p != Backend {
			t.Fatalf("got %v want Backend", p)
		}
	})
	t.Run("handler with only local mutation -> frontend", func(t *testing.T) {
		pkg := pkgWithImport("go", "example.com/api")
		fn := &ir.Func{Block: []ir.Stmt{
			&ir.Assign{
				Target: &ir.Ident{Name: "count"},
				Value:  &ir.Binary{Left: &ir.Ident{Name: "count"}, Right: &ir.Literal{}},
			},
		}}
		if p := handlerPlacement(pkg, fn); p != Frontend {
			t.Fatalf("got %v want Frontend", p)
		}
	})
	t.Run("handler with html.frontend(go_call) -> frontend", func(t *testing.T) {
		pkg := pkgWithImport("go", "example.com/api")
		fn := &ir.Func{Block: []ir.Stmt{
			&ir.Assign{
				Target: &ir.Ident{Name: "count"},
				Value:  intrinsicCall("html.frontend", goCall("example.com/api")),
			},
		}}
		if p := handlerPlacement(pkg, fn); p != Frontend {
			t.Fatalf("got %v want Frontend", p)
		}
	})
}
