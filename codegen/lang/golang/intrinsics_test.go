package golang

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestI18nEmittersNameRealRuntimeFuncs emits every i18n intrinsic and checks
// that each `i18n.X(` it produces is a function pkg/go/i18n actually exports.
// The primitives are unreachable today — the entry point that wraps one is
// itself an intrinsic, so its emitter wins before the body is consulted — so
// nothing else notices when a rename walks over the runtime names.
func TestI18nEmittersNameRealRuntimeFuncs(t *testing.T) {
	exported := goI18nExportedFuncs(t)

	args := []ir.Expr{
		&ir.Ident{Name: "a0"}, &ir.Ident{Name: "a1"},
		&ir.Ident{Name: "a2"}, &ir.Ident{Name: "a3"},
	}
	translate := func(e ir.Expr) string { return e.(*ir.Ident).Name }
	call := regexp.MustCompile(`\bi18n\.([A-Za-z_][A-Za-z0-9_]*)\s*\(`)

	for _, decl := range append(append([]ir.IntrinsicDef{}, ir.Intrinsics...), ir.I18nIntrinsics...) {
		if !strings.HasPrefix(decl.Name, "i18n.") {
			continue
		}
		emit := codegen.LookupIntrinsic(langGo, decl.Name)
		if emit == nil {
			t.Errorf("%s: no Go emitter", decl.Name)
			continue
		}
		code, _ := emit(args, translate)
		for _, m := range call.FindAllStringSubmatch(code, -1) {
			if !exported[m[1]] {
				t.Errorf("%s emits %q, but pkg/go/i18n has no exported func %s", decl.Name, code, m[1])
			}
		}
	}
}

func goI18nExportedFuncs(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, "../../../pkg/go/i18n", nil, 0)
	if err != nil {
		t.Fatalf("parse pkg/go/i18n: %v", err)
	}
	out := map[string]bool{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if ok && fd.Recv == nil && fd.Name.IsExported() {
					out[fd.Name.Name] = true
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("found no exported funcs in pkg/go/i18n")
	}
	return out
}
