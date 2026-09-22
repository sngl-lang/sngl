package lower

import (
	"fmt"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

var passEnum = pass{
	name:    "NoEnum",
	enabled: func(c Features) bool { return !c.Enum },
	apply:   lowerEnum,
}

// lowerEnum rewrites enum-member references to int literals (ordinal in
// declaration order). Does not delete enum declarations from pkg.Enums —
// that is DCE's job.
func lowerEnum(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}

	ordinals := make(map[*ir.EnumDef]map[string]int)
	for _, e := range pkg.Enums {
		m := make(map[string]int, len(e.Members))
		for i, mem := range e.Members {
			m[mem.Name] = i
		}
		ordinals[e] = m
	}

	memberOrdinal := func(decl *ir.EnumDef, name string) (int, bool) {
		if m, ok := ordinals[decl]; ok {
			if ord, ok := m[name]; ok {
				return ord, true
			}
		}
		m := make(map[string]int, len(decl.Members))
		for i, mem := range decl.Members {
			m[mem.Name] = i
		}
		ordinals[decl] = m
		ord, ok := m[name]
		return ord, ok
	}

	rewrite := func(e ir.Expr) ir.Expr {
		return rewriteEnumExpr(e, memberOrdinal)
	}
	walkPackage(pkg, walkFuncs{
		expr:  rewrite,
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return rewriteStmtExprs(stmts, rewrite) },
	})
	return nil
}

func rewriteEnumExpr(e ir.Expr, memberOrdinal func(*ir.EnumDef, string) (int, bool)) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Ident:
		// Bare-member shorthand resolved against context enum.
		if x.Member != "" && x.Type != nil && x.Type.Kind == ir.TypeEnum && x.Type.Decl != nil {
			if decl, ok := x.Type.Decl.(*ir.EnumDef); ok {
				if ord, ok := memberOrdinal(decl, x.Member); ok {
					return intLiteral(ord)
				}
			}
		}
	case *ir.Select:
		// Qualified access: Status.active. Operand is the enum decl Ident.
		if id, ok := x.Operand.(*ir.Ident); ok {
			if decl, ok := id.Sym.(*ir.EnumDef); ok {
				if ord, ok := memberOrdinal(decl, x.Field); ok {
					return intLiteral(ord)
				}
			}
		}
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
	case *ir.Binary:
		x.Left = rewriteEnumExpr(x.Left, memberOrdinal)
		x.Right = rewriteEnumExpr(x.Right, memberOrdinal)
	case *ir.Unary:
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
	case *ir.Ternary:
		x.Cond = rewriteEnumExpr(x.Cond, memberOrdinal)
		x.Then = rewriteEnumExpr(x.Then, memberOrdinal)
		x.Else = rewriteEnumExpr(x.Else, memberOrdinal)
	case *ir.Call:
		if x.Receiver != nil {
			x.Receiver = rewriteEnumExpr(x.Receiver, memberOrdinal)
		}
		for i := range x.Args {
			x.Args[i].Value = rewriteEnumExpr(x.Args[i].Value, memberOrdinal)
		}
	case *ir.Conversion:
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
	case *ir.Index:
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
		x.Idx = rewriteEnumExpr(x.Idx, memberOrdinal)
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = rewriteEnumExpr(x.Elems[i], memberOrdinal)
		}
	case *ir.StructLit:
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				x.Fields[i].Value = rewriteEnumExpr(x.Fields[i].Value, memberOrdinal)
			}
		}
	case *ir.Spread:
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = rewriteEnumExpr(x.Entries[i].Key, memberOrdinal)
			x.Entries[i].Value = rewriteEnumExpr(x.Entries[i].Value, memberOrdinal)
		}
	case *ir.Lambda:
		if x.Func != nil {
			x.Func.Block = rewriteStmtExprs(x.Func.Block, func(e ir.Expr) ir.Expr {
				return rewriteEnumExpr(e, memberOrdinal)
			})
		}
	case *ir.Closure:
		if x.State != nil {
			for i := range x.State.Fields {
				if x.State.Fields[i].Value != nil {
					x.State.Fields[i].Value = rewriteEnumExpr(x.State.Fields[i].Value, memberOrdinal)
				}
			}
		}
		if x.Func != nil {
			x.Func.Block = rewriteStmtExprs(x.Func.Block, func(e ir.Expr) ir.Expr {
				return rewriteEnumExpr(e, memberOrdinal)
			})
		}
	case *ir.Literal, *ir.ContextRead:
		// Terminal — no enum member to rewrite.
	default:
		panic(fmt.Sprintf("rewriteEnumExpr: unhandled %T", x))
	}
	return e
}

func intLiteral(n int) *ir.Literal {
	return &ir.Literal{
		Type:  ir.TypInt,
		Value: strconv.Itoa(n),
	}
}
