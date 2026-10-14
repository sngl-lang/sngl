package fixtures

import (
	"fmt"
	"reflect"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/testutil"
	"duckfam.us/sngl/ir"
)

// assertNoSilentInvalid guards strict typing: a fixture that checks clean
// holds no TypeInvalid anywhere -- not on a declaration, and not on any
// expression, a type argument or element type included. An invalid type is a
// mistake the checker reports where it is made, so one in a clean program is a
// type the checker gave up on without saying so.
//
// The package is the one the runner already checked.
func assertNoSilentInvalid(t *testing.T, s testutil.Sample, pkg *ir.Package) {
	if pkg == nil {
		return
	}
	if s.ExpectsError("parse") || s.ExpectsError("check") {
		return // invalid during error recovery is expected
	}
	hasInvalid := func(t *ir.Type) bool {
		return t != nil && t.Any(func(t *ir.Type) bool { return t.Kind == ir.TypeInvalid })
	}
	report := func(where string, pos ast.Pos) {
		t.Errorf("TypeInvalid at %s (%s:%d:%d) -- expected a type or an error",
			where, s.Filename, pos.Line, pos.Column)
	}
	visit := func(label string, pos ast.Pos, typ *ir.Type) {
		if hasInvalid(typ) {
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
		if !ok || !hasInvalid(e.ExprType()) {
			return nil
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
