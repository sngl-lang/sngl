package fixtures

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
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
	for _, at := range InvalidTypes(pkg) {
		t.Errorf("TypeInvalid at %s (%s:%d:%d) -- expected a type or an error",
			at.Where, s.Filename, at.Pos.Line, at.Pos.Column)
	}
}

// InvalidAt is one place a checked package holds a TypeInvalid.
type InvalidAt struct {
	Where string
	Pos   ast.Pos
}

// InvalidTypes is every place pkg holds a TypeInvalid: its declarations, every
// expression its bodies hold, and the bodies its overrides hold -- a
// program's of another package's declaration, and a target package's own. A
// declaration marked unusable is the one that may: its type is a host's SNGL
// has no name for, and every use of it is refused.
func InvalidTypes(pkg *ir.Package) []InvalidAt {
	var out []InvalidAt
	hasInvalid := func(t *ir.Type) bool {
		return t != nil && t.Any(func(t *ir.Type) bool { return t.Kind == ir.TypeInvalid })
	}
	visit := func(label string, pos ast.Pos, typ *ir.Type) {
		if hasInvalid(typ) {
			out = append(out, InvalidAt{label, pos})
		}
	}
	exprs := func(root any) {
		_ = ir.Walk(root, func(n ir.Node) error {
			if e, ok := n.(ir.Expr); ok && hasInvalid(e.ExprType()) {
				out = append(out, InvalidAt{fmt.Sprintf("%T", e), exprPos(e)})
			}
			return nil
		})
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
			if fn.Foreign.Unusable != "" {
				continue
			}
			for _, p := range fn.Params {
				visit(owner+"func "+fn.Name+" param "+p.Name, pos, p.Type)
			}
			visit(owner+"func "+fn.Name+" return", pos, fn.Return)
		}
	}
	vars := func(owner string, vs []*ir.Var, fallback ast.Pos) {
		for _, v := range vs {
			if v.Foreign.Unusable != "" {
				continue
			}
			pos := fallback
			if p := stmtPos(v.AST); p.Line != 0 {
				pos = p
			}
			visit(owner+"var "+v.Name, pos, v.Type)
		}
	}
	vars("package ", pkg.Vars, ast.Pos{})
	funcs("", pkg.Funcs, ast.Pos{})
	for _, comp := range pkg.Components {
		pos := ast.Pos{}
		if comp.AST != nil {
			pos = comp.AST.Pos
		}
		for _, p := range comp.Props {
			visit("component "+comp.Name+" prop "+p.Name, pos, p.Type)
		}
		vars("component "+comp.Name+" ", comp.Vars, pos)
		funcs("component "+comp.Name+" ", comp.Funcs, pos)
	}
	for _, sd := range pkg.Structs {
		pos := ast.Pos{}
		if sd.AST != nil {
			pos = sd.AST.Pos
		}
		for _, f := range sd.Fields {
			if f.Foreign.Unusable == "" {
				visit("struct "+sd.Name+" field "+f.Name, pos, f.Type)
			}
		}
	}
	exprs(pkg)
	for _, ovs := range pkg.Overlay.Overrides {
		for _, m := range []map[string]ir.Override{ovs.Platform, ovs.Language} {
			for _, target := range slices.Sorted(maps.Keys(m)) {
				o := m[target]
				owner := "override for " + target + " "
				vars(owner, o.Vars, ast.Pos{})
				funcs(owner, o.Funcs, ast.Pos{})
				exprs(o.Stmts)
				for _, v := range o.Vars {
					exprs(v.Init)
				}
				for _, fn := range o.Funcs {
					exprs(fn)
				}
			}
		}
	}
	return out
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
