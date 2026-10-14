package fixtures

import (
	"bytes"
	"fmt"
	"reflect"
	"regexp"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/testutil"
	"duckfam.us/sngl/ir"
)

var dynKeyword = regexp.MustCompile(`\bdyn\b`)

// assertNoSilentDyn guards strict typing: a fixture that checks clean holds no
// TypeDyn anywhere -- not on a declaration, and not on any expression, a type
// argument or element type included. A dyn the checker reached without
// reporting is a type it gave up on, which nothing downstream can tell from a
// type the program wrote.
//
// The package is the one the runner already checked.
func assertNoSilentDyn(t *testing.T, s testutil.Sample, pkg *ir.Package) {
	if pkg == nil {
		return
	}
	// Strip comments before scanning so a directive mentioning `dyn` in its
	// expected message does not count as explicit use.
	if dynKeyword.Match(stripLineComments([]byte(s.Source))) {
		return
	}
	if s.ExpectsError("parse") || s.ExpectsError("check") {
		return // TypDyn during error recovery is expected
	}
	hasDyn := func(t *ir.Type) bool {
		return t != nil && t.Any(func(t *ir.Type) bool { return t.Kind == ir.TypeDyn })
	}
	report := func(where string, pos ast.Pos) {
		t.Errorf("TypeDyn at %s (%s:%d:%d) -- expected a type or an error",
			where, s.Filename, pos.Line, pos.Column)
	}
	visit := func(label string, pos ast.Pos, typ *ir.Type) {
		if hasDyn(typ) {
			report(label, pos)
		}
	}
	for _, v := range pkg.Vars {
		visit("package var "+v.Name, stmtPos(v.AST), v.Type)
	}
	for _, v := range pkg.Consts {
		visit("package const "+v.Name, stmtPos(v.AST), v.Type)
	}
	funcs := func(owner string, fns []*ir.Func, fallback ast.Pos) {
		for _, fn := range fns {
			pos := fallback
			if fn.AST != nil {
				pos = fn.AST.Pos
			}
			for _, p := range fn.Params {
				visit(owner+"func "+fn.Name+" param "+p.Name, pos, p.Type)
			}
			visit(owner+"func "+fn.Name+" return", pos, fn.Return)
		}
	}
	funcs("", pkg.Funcs, ast.Pos{})
	for _, comp := range pkg.Components {
		pos := ast.Pos{}
		if comp.AST != nil {
			pos = comp.AST.Pos
		}
		for _, p := range comp.Props {
			visit("component "+comp.Name+" prop "+p.Name, pos, p.Type)
		}
		for _, v := range comp.Vars {
			vpos := pos
			if p := stmtPos(v.AST); p.Line != 0 {
				vpos = p
			}
			visit("component "+comp.Name+" var "+v.Name, vpos, v.Type)
		}
		funcs("component "+comp.Name+" ", comp.Funcs, pos)
	}
	for _, sd := range pkg.Structs {
		pos := ast.Pos{}
		if sd.AST != nil {
			pos = sd.AST.Pos
		}
		for _, f := range sd.Fields {
			visit("struct "+sd.Name+" field "+f.Name, pos, f.Type)
		}
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		e, ok := n.(ir.Expr)
		if !ok || !hasDyn(e.ExprType()) {
			return nil
		}
		if id, ok := e.(*ir.Ident); ok {
			if _, ns := id.Sym.(*ir.Namespace); ns {
				return nil // a package, not a value
			}
		}
		report(fmt.Sprintf("%T", e), exprPos(e))
		return nil
	})
}

// exprPos is where the source wrote e, read off the AST node it carries.
func exprPos(e ir.Expr) ast.Pos {
	if v := reflect.ValueOf(e).Elem().FieldByName("AST"); v.IsValid() && !v.IsNil() {
		if ep, ok := v.Interface().(interface{ ExprPos() *ast.Pos }); ok && ep.ExprPos() != nil {
			return *ep.ExprPos()
		}
	}
	return ast.Pos{}
}

// stmtPos extracts the Pos from an ast.Stmt that wraps a decl with a Pos field.
func stmtPos(s ast.Stmt) ast.Pos {
	switch x := s.(type) {
	case *ast.VarDecl:
		return x.Pos
	case *ast.ConstDecl:
		return x.Pos
	}
	return ast.Pos{}
}

// stripLineComments removes // line comments from source so they don't pollute
// the backstop's `dyn` keyword scan.
func stripLineComments(src []byte) []byte {
	var out []byte
	for len(src) > 0 {
		i := bytes.Index(src, []byte("//"))
		if i < 0 {
			out = append(out, src...)
			break
		}
		out = append(out, src[:i]...)
		eol := bytes.IndexByte(src[i:], '\n')
		if eol < 0 {
			break
		}
		src = src[i+eol:]
	}
	return out
}
