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
				if typ, ok := ir.ScalarType(writtenBuiltinKind(doc, "sngl:builtin", sd.Attrs)); ok {
					universe[sd.Name] = typ
				}
			}
		}
	})
	return universe
}

// writtenBuiltinKind is the kind a declaration's #[builtin] mark names, read
// off the source rather than by running the mark: `#[_builtin(int)]` in
// sngl:builtin, or `#[marks.builtin("list")]` under whatever alias doc
// imports sngl:internal/marks as. BuiltinNone when it carries neither.
//
// A kind is needed before marks run. A type is resolved as its declaration
// registers -- `func list<T>.filter(…) list<T>` in the package declaring
// `list`, `func makeChan<T>(zero T) chan<T>` in the one declaring `chan` --
// and what `list<T>` resolves to is the kind's constructor, and what a
// family's members are measured against is the family of families'. So the
// kind is stamped as the declaration registers (stampBuiltinKind) and the
// mark, when it runs, binds the role and checks it read the same.
func writtenBuiltinKind(doc *ast.Document, pkg string, attrs []ast.MacroAttr) ir.BuiltinKind {
	if len(attrs) == 0 {
		return ir.BuiltinNone
	}
	marksAlias, marksImported := "", false
	if doc != nil {
		for _, stmt := range doc.Stmts {
			imp, ok := stmt.(*ast.Import)
			if !ok || imp.Path != "sngl:"+marksPkg {
				continue
			}
			marksImported = true
			switch {
			case imp.IsDot():
				marksAlias = ""
			case imp.Alias != "":
				marksAlias = imp.Alias
			default:
				marksAlias = NamespaceFromPath(imp.Path)
			}
		}
	}
	for _, a := range attrs {
		if len(a.Args) != 1 {
			continue
		}
		switch {
		case pkg == "sngl:builtin" && a.Alias == "" && a.Name == builtinMark:
			if id, ok := a.Args[0].(*ast.IdentExpr); ok {
				return ir.BuiltinKind(id.Name)
			}
		case marksImported && a.Alias == marksAlias && a.Name == "builtin":
			if lit, ok := a.Args[0].(*ast.LiteralExpr); ok {
				if v, ok := lit.StringValue(); ok {
					return ir.BuiltinKind(v)
				}
			}
		}
	}
	return ir.BuiltinNone
}

// marksPkg is the package declaring the compiler's own marks.
const marksPkg = "internal/marks"

// stampBuiltinKind gives a declaration the kind its mark names, as it
// registers. See writtenBuiltinKind; an invalid kind is left for the mark to
// report.
func (c *checker) stampBuiltinKind(attrs []ast.MacroAttr) ir.BuiltinKind {
	if kind := writtenBuiltinKind(c.doc, c.libPkgName, attrs); kind.Valid() {
		return kind
	}
	return ir.BuiltinNone
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
