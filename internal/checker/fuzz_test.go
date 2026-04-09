package checker

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func FuzzCheckResolved(f *testing.F) {
	f.Skip()
	f.Add(`component main { text(value="hello") }`)
	f.Add(`component main { var count = 0; button(text="inc", @click { count += 1 }) }`)
	f.Add(`struct User { name string; age int }`)
	f.Add(`component main { var x = 1 + 2; text(value=string(x)) }`)

	f.Fuzz(func(t *testing.T, src string) {
		doc, err := parser.Parse("fuzz.sngl", strings.NewReader(src))
		if err != nil {
			t.Skip()
		}
		// Record user-authored counts before Check prepends stdlib.
		userFuncCount := len(doc.Functions)
		userStructCount := len(doc.Structs)
		userDataCount := len(doc.Data)

		err = Check(doc, os.DirFS("."), ".", nil, nil, nil, nil, true)
		if err != nil {
			t.Skip()
		}

		// Stdlib is prepended; user items are at the tail.
		userFuncs := doc.Functions[len(doc.Functions)-userFuncCount:]
		userStructs := doc.Structs[len(doc.Structs)-userStructCount:]
		userData := doc.Data[len(doc.Data)-userDataCount:]

		assertAllResolved(t, doc, userStructs, userData, userFuncs)
	})
}

// assertAllResolved walks user-authored AST nodes and checks that every
// Resolved *TypeInfo field populated by the checker is non-nil.
func assertAllResolved(t *testing.T, doc *ast.Document, structs []*ast.StructDef, data []*ast.Data, funcs []*ast.FuncDef) {
	t.Helper()

	checkExpr := func(ctx string, e ast.Expr) {
		if e.Literal == nil && e.SNGL == nil {
			return // zero-value Expr, nothing to resolve
		}
		if e.Resolved == nil {
			t.Errorf("%s: Expr.Resolved is nil (TypeHint=%q)", ctx, e.TypeHint)
		}
	}

	for _, s := range structs {
		for _, f := range s.Fields {
			if f.Resolved == nil {
				t.Errorf("struct %s field %s: Resolved is nil", s.Name, f.Name)
			}
			if f.Default.Literal != nil || f.Default.SNGL != nil {
				checkExpr(fmt.Sprintf("struct %s field %s default", s.Name, f.Name), f.Default)
			}
		}
	}

	for _, d := range data {
		if d.Resolved == nil {
			t.Errorf("var %s: Resolved is nil", d.Name)
		}
		checkExpr(fmt.Sprintf("var %s init", d.Name), d.Init)
	}

	for _, c := range doc.Consts {
		checkExpr(fmt.Sprintf("const %s init", c.Name), c.Init)
	}

	for _, fn := range funcs {
		checkFuncDef(t, checkExpr, "func "+fn.Name, fn)
	}

	for _, comp := range doc.Components {
		prefix := "component " + comp.Name
		for _, p := range comp.Params {
			if p.Resolved == nil {
				t.Errorf("%s param %s: Resolved is nil", prefix, p.Name)
			}
			if p.Default.Literal != nil || p.Default.SNGL != nil {
				checkExpr(fmt.Sprintf("%s param %s default", prefix, p.Name), p.Default)
			}
		}
		for _, d := range comp.Data {
			if d.Resolved == nil {
				t.Errorf("%s var %s: Resolved is nil", prefix, d.Name)
			}
			checkExpr(fmt.Sprintf("%s var %s init", prefix, d.Name), d.Init)
		}
		for _, c := range comp.Consts {
			checkExpr(fmt.Sprintf("%s const %s init", prefix, c.Name), c.Init)
		}
		for _, fn := range comp.Functions {
			checkFuncDef(t, checkExpr, fmt.Sprintf("%s func %s", prefix, fn.Name), fn)
		}
		checkVisualNodes(t, checkExpr, prefix, comp.Body)
		for plat, body := range comp.PlatformBodies {
			checkVisualNodes(t, checkExpr, fmt.Sprintf("%s[%s]", prefix, plat), body)
		}
	}

	if doc.App != nil {
		for _, w := range doc.App.EffectiveWindows() {
			checkWindow(t, checkExpr, "app window "+w.Name, w)
		}
	}
	for _, w := range doc.Windows {
		checkWindow(t, checkExpr, "window "+w.Name, w)
	}
}

func checkFuncDef(t *testing.T, checkExpr func(string, ast.Expr), prefix string, fn *ast.FuncDef) {
	t.Helper()
	for _, p := range fn.Params {
		if p.Resolved == nil {
			t.Errorf("%s param %s: Resolved is nil", prefix, p.Name)
		}
	}
	checkExpr(prefix+" body", fn.Body)
}

func checkWindow(t *testing.T, checkExpr func(string, ast.Expr), prefix string, w *ast.Window) {
	t.Helper()
	for k, v := range w.Props {
		checkExpr(fmt.Sprintf("%s prop %s", prefix, k), v)
	}
	for _, d := range w.Data {
		if d.Resolved == nil {
			t.Errorf("%s var %s: Resolved is nil", prefix, d.Name)
		}
		checkExpr(fmt.Sprintf("%s var %s init", prefix, d.Name), d.Init)
	}
	for _, c := range w.Consts {
		checkExpr(fmt.Sprintf("%s const %s init", prefix, c.Name), c.Init)
	}
	for _, fn := range w.Functions {
		checkFuncDef(t, checkExpr, fmt.Sprintf("%s func %s", prefix, fn.Name), fn)
	}
	checkVisualNodes(t, checkExpr, prefix, w.Children)
	if w.For != nil {
		checkExpr(prefix+" for iterable", w.For.Iterable)
	}
}

func checkVisualNodes(t *testing.T, checkExpr func(string, ast.Expr), prefix string, nodes []*ast.VisualNode) {
	t.Helper()
	for _, vn := range nodes {
		ctx := fmt.Sprintf("%s > %s", prefix, vn.Component)
		if vn.Key != nil {
			checkExpr(ctx+" key", *vn.Key)
		}
		if vn.If != nil {
			checkExpr(ctx+" if", *vn.If)
		}
		if vn.Class != nil {
			checkExpr(ctx+" class", *vn.Class)
		}
		if vn.Ref != nil {
			checkExpr(ctx+" ref", *vn.Ref)
		}
		for k, v := range vn.Props {
			checkExpr(fmt.Sprintf("%s prop %s", ctx, k), v)
		}
		for k, v := range vn.Bindings {
			checkExpr(fmt.Sprintf("%s binding %s", ctx, k), v)
		}
		for k, eh := range vn.Events {
			checkExpr(fmt.Sprintf("%s event %s", ctx, k), eh.Body)
		}
		if vn.For != nil {
			checkExpr(ctx+" for iterable", vn.For.Iterable)
			checkVisualNodes(t, checkExpr, ctx+" for else", vn.For.Else)
		}
		checkVisualNodes(t, checkExpr, ctx, vn.Children)
	}
}
