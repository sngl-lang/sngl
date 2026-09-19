package fixtures

// FOLD assertion helpers, moved from internal/optimize's test package.

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

func collectVarsByLine(pkg *ir.Package) map[int]*ir.Var {
	byLine := map[int]*ir.Var{}
	walk := func(v *ir.Var) {
		if v == nil || v.AST == nil {
			return
		}
		// VarDecl/ConstDecl positions: each spec sits on the parent decl's
		// line for single-line decls; grouped (parenthesized) decls share
		// the parent decl's line. The fold matcher uses the spec line, so
		// we record the per-spec position when available.
		// VarSpec has no Pos, so non-grouped var/const decls (one spec
		// per line) are matched by the parent decl's line. Grouped decls
		// would land all specs on the same line, which is good enough for
		// the existing FOLD fixtures (none use grouped form for FOLDed
		// expressions).
		switch d := v.AST.(type) {
		case *ast.VarDecl:
			byLine[d.Pos.Line] = v
		case *ast.ConstDecl:
			byLine[d.Pos.Line] = v
		}
	}
	for _, v := range pkg.Vars {
		walk(v)
	}
	for _, v := range pkg.Consts {
		walk(v)
	}
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			walk(v)
		}
	}
	return byLine
}

func litValue(lit *ir.Literal) any {
	if lit == nil || lit.Type == nil {
		return lit.Value
	}
	switch lit.Type.Kind {
	case ir.TypeInt:
		var i int
		_, _ = fmt.Sscanf(lit.Value, "%d", &i)
		return i
	case ir.TypeFloat:
		var f float64
		_, _ = fmt.Sscanf(lit.Value, "%g", &f)
		return f
	case ir.TypeBool:
		return lit.Value == "true"
	case ir.TypeString:
		return lit.Value
	}
	return lit.Value
}

func irExprDescr(e ir.Expr) string {
	if e == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", e)
}
