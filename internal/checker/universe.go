package checker

import (
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// scalarUniverse is every scalar sngl:builtin declares, by the name it is
// declared under: `#[_builtin(int)] struct int {}` puts `int` here, as the
// type its kind stands for (ir.ScalarType).
//
// It is read off the parsed source rather than the checked package because it
// is what the check of that package, and of the packages it imports, resolves
// `string` and `int` against: sngl:builtin imports sngl:build, which imports
// sngl:internal/marks, and all of them are checked before sngl:builtin is in
// scope. Once it is, a scalar's name finds its declaration first, which says
// the same thing; a program's own declaration of the name shadows both.
func scalarUniverse() map[string]*ir.Type {
	universeOnce.Do(func() {
		universe = map[string]*ir.Type{}
		for _, doc := range PackageDocsFor("builtin") {
			for _, stmt := range doc.Stmts {
				sd, ok := stmt.(*ast.StructDef)
				if !ok {
					continue
				}
				for _, a := range sd.Attrs {
					if a.Alias != "" || a.Name != builtinMark || len(a.Args) != 1 {
						continue
					}
					id, ok := a.Args[0].(*ast.IdentExpr)
					if !ok {
						continue
					}
					if typ, ok := ir.ScalarType(ir.BuiltinKind(id.Name)); ok {
						universe[sd.Name] = typ
					}
				}
			}
		}
	})
	return universe
}

// builtinMark is sngl:builtin's own mark, declared privately in the package
// it marks: it is that package's, so a program cannot write one.
const builtinMark = "_builtin"

var (
	universeOnce sync.Once
	universe     map[string]*ir.Type
)

// scalarOf is the scalar type a symbol stands for: a scalar's declaration in
// sngl:builtin, or its name in the universe before that package is in scope.
func scalarOf(sym ir.Symbol) (*ir.Type, ir.BuiltinKind, bool) {
	switch s := sym.(type) {
	case *ir.StructDef:
		if typ, ok := ir.ScalarType(s.Builtin); ok {
			return typ, s.Builtin, true
		}
	case *ir.TypeSym:
		for _, kind := range ir.AllBuiltinKinds() {
			if typ, ok := ir.ScalarType(kind); ok && typ == s.Type {
				return typ, kind, true
			}
		}
	}
	return nil, ir.BuiltinNone, false
}
