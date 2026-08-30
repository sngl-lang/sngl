//go:build !js

package optimize

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A pure go: call inside a component body only becomes const when the call
// site's prop value is bound into it, which happens in a child context. That
// child has to carry the round's request collector: without it the call is
// neither folded nor requested, and the value silently comes out empty (the
// docs site lost every highlighted code block that way).
func TestInlinedNativeCallReachesTheBatch(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}

	// component wrapped(name string) { text(value = purepkg.Greet(name)) }
	sym := &ir.Param{Name: "name", Type: ir.TypString}
	body := &ir.NodeInst{
		Name: "text",
		Props: []ir.Arg{{Name: "value", Value: &ir.Call{
			AST: &ast.CallExpr{Func: &ast.SelectExpr{
				Operand: &ast.IdentExpr{Name: "purepkg"},
				Field:   "Greet",
			}},
			Type: ir.TypString,
			Args: []ir.CallArg{{Value: &ir.Ident{Name: "name", Sym: sym, Type: ir.TypString}}},
		}}},
	}
	comp := &ir.Component{
		Name:  "wrapped",
		Props: []*ir.Prop{{Name: "name", Type: ir.TypString, Sym: sym}},
		Body:  []ir.Stmt{body},
	}
	// wrapped(name="inlined")
	site := &ir.NodeInst{
		Name:      "wrapped",
		Component: comp,
		Props:     []ir.Arg{{Name: "name", Value: &ir.Literal{Type: ir.TypString, Value: "inlined"}}},
	}
	pkg := &ir.Package{
		Components: []*ir.Component{comp},
		Imports: []*ir.Import{{
			Path:  "go:" + purepkgPath,
			Alias: "purepkg",
			Native: &ir.NativeImport{
				ImportPath: purepkgPath,
				Funcs:      []*ir.Func{fnGreet},
			},
		}},
	}

	ctx := &evalCtx{
		platform: "html",
		language: "none",
		dir:      dir,
		pkg:      pkg,
		native:   &nativeEval{},
		values:   map[ir.Symbol]any{},
		inlining: map[*ir.Component]int{},
	}
	if got := inlineComponentCall(site, ctx); got == nil {
		t.Fatal("component with a native call on a prop was not inlined")
	}
	if len(ctx.native.order) != 1 {
		t.Fatalf("the inlined native call requested %d evaluations, want 1", len(ctx.native.order))
	}
	if want := "purepkg.Greet"; ctx.native.order[0].nativeType != want {
		t.Errorf("requested %q, want %q", ctx.native.order[0].nativeType, want)
	}
}

// A for-loop over a const list folds its body against a child context too, so
// a native call whose argument is the loop variable has the same requirement:
// the child must carry the round's collector.
func TestExpandedNativeCallReachesTheBatch(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}

	// for name = ["a", "b"] { text(value = purepkg.Greet(name)) }
	loop := &ir.LoopVar{Name: "name", Type: ir.TypString}
	fs := &ir.For{
		Key:      "name",
		ElemType: ir.TypString,
		Iter: &ir.ListLit{
			Type:  &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{ir.TypString}},
			Elems: []ir.Expr{&ir.Literal{Type: ir.TypString, Value: "a"}, &ir.Literal{Type: ir.TypString, Value: "b"}},
		},
		KeySym: loop,
		Body: []ir.Stmt{&ir.NodeInst{
			Name: "text",
			Props: []ir.Arg{{Name: "value", Value: &ir.Call{
				AST: &ast.CallExpr{Func: &ast.SelectExpr{
					Operand: &ast.IdentExpr{Name: "purepkg"},
					Field:   "Greet",
				}},
				Type: ir.TypString,
				Args: []ir.CallArg{{Value: &ir.Ident{Name: "name", Sym: loop, Type: ir.TypString}}},
			}}},
		}},
	}
	pkg := &ir.Package{Imports: []*ir.Import{{
		Path:  "go:" + purepkgPath,
		Alias: "purepkg",
		Native: &ir.NativeImport{
			ImportPath: purepkgPath,
			Funcs:      []*ir.Func{fnGreet},
		},
	}}}
	ctx := &evalCtx{
		platform: "html",
		language: "none",
		dir:      dir,
		pkg:      pkg,
		native:   &nativeEval{},
		values:   map[ir.Symbol]any{},
	}
	if got := expandForStmt(fs, ctx); got == nil {
		t.Fatal("for-loop over a const list was not expanded")
	}
	// One request per iteration: the argument differs.
	if len(ctx.native.order) != 2 {
		t.Fatalf("the expanded body requested %d evaluations, want 2", len(ctx.native.order))
	}
}
