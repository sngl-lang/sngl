//go:build !js

package optimize

import (
	"git.duckfam.us/jonathan/sngl/internal/trust"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestOptimize_GoImportEvalFailureFatal asserts that when a go: import
// function fails to evaluate at build time (compile error / panic / timeout),
// the build aborts UNLESS the target language can call the function at runtime
// instead. Only go-language targets emit a real runtime go: call, so every
// other target (html static/none, kotlin, …) must hard-fail rather than
// silently drop the const — which previously rendered pages with empty
// content. A successful fold is unaffected (see
// TestOptimize_GoImportConstFoldsOnKotlin): the guard only fires on failure.
func TestOptimize_GoImportEvalFailureFatal(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}

	// `const x = purepkg.Boom()` where Boom panics at compile-time eval.
	mkPkg := func() *ir.Package {
		callAST := &ast.CallExpr{Func: &ast.SelectExpr{
			Operand: &ast.IdentExpr{Name: "purepkg"},
			Field:   "Boom",
		}}
		c := &ir.Var{
			Name:    "x",
			Type:    ir.TypString,
			IsConst: true,
			Init:    &ir.Call{AST: callAST, Type: ir.TypString},
		}
		return &ir.Package{
			Consts: []*ir.Var{c},
			Imports: []*ir.Import{{
				Path:  "go:git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg",
				Alias: "purepkg",
				Native: &ir.NativeImport{
					ImportPath: "git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg",
					Funcs: []*ir.Func{{
						Name:    "Boom",
						Foreign: ir.Foreign{Name: "purepkg.Boom", Path: "purepkg"},
						Purity:  ir.PurityPure,
						Return:  ir.TypString,
					}},
				},
			}},
		}
	}

	cases := []struct {
		name     string
		platform string
		lang     string
		wantErr  bool
	}{
		{"html/none aborts", "html", "none", true},
		{"android/kotlin aborts", "android", "kotlin", true},
		{"bubbletea/go soft fallback", "bubbletea", "go", false},
		{"fyne/go soft fallback", "fyne", "go", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Optimize(mkPkg(), &Config{Platform: tc.platform, Language: tc.lang, Dir: dir, Trust: trust.AllowAll()})
			if tc.wantErr && err == nil {
				t.Fatalf("expected Optimize to fail: a go: import that failed to evaluate cannot be run at runtime by the %q target", tc.lang)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("%q target can call go: at runtime; eval failure should be a soft fallback, got: %v", tc.lang, err)
			}
		})
	}
}
