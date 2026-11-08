package lower

import (
	"fmt"
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
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
		// Bare-member shorthand resolved against context enum. A value of an
		// enum carrying payloads is no ordinal.
		if x.Member != "" && ir.PayloadEnum(x.Type) == nil && x.Type != nil && x.Type.Kind == ir.TypeEnum && x.Type.Decl != nil {
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
	case *ir.MatchExpr:
		x.Subject = rewriteEnumExpr(x.Subject, memberOrdinal)
		for _, arm := range x.Arms {
			for i := range arm.Patterns {
				arm.Patterns[i] = rewriteEnumExpr(arm.Patterns[i], memberOrdinal)
			}
			arm.Value = rewriteEnumExpr(arm.Value, memberOrdinal)
		}
	case *ir.Call:
		if x.Receiver != nil {
			x.Receiver = rewriteEnumExpr(x.Receiver, memberOrdinal)
		}
		decl := enumFormatted(x)
		for i := range x.Args {
			x.Args[i].Value = rewriteEnumExpr(x.Args[i].Value, memberOrdinal)
		}
		if decl != nil {
			return memberName(decl, x.Args[0].Value)
		}
	case *ir.Conversion:
		var decl *ir.EnumDef
		if x.Type != nil && x.Type.Kind == ir.TypeString {
			decl = namedEnum(x.Operand.ExprType())
		}
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
		if decl != nil {
			return memberName(decl, x.Operand)
		}
	case *ir.CConvert:
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
		// Its function is one of the package's, which the walk reaches.
		rewriteEnumExpr(x.State, memberOrdinal)
	case *ir.Literal, *ir.ContextRead, *ir.SlotEmpty:
		// Terminal — no enum member to rewrite.
	default:
		panic(fmt.Sprintf("rewriteEnumExpr: unhandled %T", x))
	}
	return e
}

// enumFormatted is the enum whose value c formats, `"{direction}"`, or nil
// when c is no such call. The ordinal the value becomes would format as a
// number, so the call becomes the member's name (memberName), as a
// conversion to string does.
func enumFormatted(c *ir.Call) *ir.EnumDef {
	if c.Func == nil || c.Func.Name != "format" || !c.Func.IsMethod() || len(c.Args) != 1 {
		return nil
	}
	return namedEnum(c.Args[0].Value.ExprType())
}

// namedEnum is t's declaration where t is an enum NoEnum makes ordinals of.
func namedEnum(t *ir.Type) *ir.EnumDef {
	if t == nil || t.Kind != ir.TypeEnum || ir.PayloadEnum(t) != nil {
		return nil
	}
	decl, _ := t.Decl.(*ir.EnumDef)
	return decl
}

// memberName is the name of decl's member at the ordinal v: the name itself
// where v is a literal, and else a choice among them, from a list where v is
// not a name read twice for free.
func memberName(decl *ir.EnumDef, v ir.Expr) ir.Expr {
	names := make([]ir.Expr, len(decl.Members))
	for i, m := range decl.Members {
		names[i] = &ir.Literal{Type: ir.TypString, Value: m.Name}
	}
	if lit, ok := v.(*ir.Literal); ok {
		if i, err := strconv.Atoi(lit.Value); err == nil && i >= 0 && i < len(names) {
			return names[i]
		}
	}
	if _, ok := v.(*ir.Ident); !ok || len(names) == 0 {
		return &ir.Index{Operand: &ir.ListLit{Elems: names, Type: ir.ListOf(ir.TypString)}, Idx: v, Type: ir.TypString}
	}
	var out ir.Expr = names[len(names)-1]
	for i := len(names) - 2; i >= 0; i-- {
		out = &ir.Ternary{
			Cond: &ir.Binary{Op: ast.BinEq, Left: v, Right: intLiteral(i), Type: ir.TypBool},
			Then: names[i],
			Else: out,
			Type: ir.TypString,
		}
	}
	return out
}

func intLiteral(n int) *ir.Literal {
	return &ir.Literal{
		Type:  ir.TypInt,
		Value: strconv.Itoa(n),
	}
}
